package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// SyncFlows refreshes flows, revisions and triggers of a namespace from a snapshot
// (REQ-FLOW-001, REQ-FLOW-004, REQ-FLOW-005, REQ-GIT-006). files holds every path of
// the snapshot, with content for flow files and namespace.yaml.
func (s *Service) SyncFlows(ctx context.Context, tx pgx.Tx, nsID, snapshotID uuid.UUID, files map[string][]byte) error {
	q := dbq.New(tx)
	res := flow.ValidateNamespace(files)
	existing, err := q.ListNamespaceFlows(ctx, nsID)
	if err != nil {
		return err
	}
	byKey := map[string]dbq.Flow{}
	for _, f := range existing {
		byKey[f.FlowKey] = f
	}
	// One flow row per flow key. Duplicates are all invalid; the first path represents them.
	groups := map[string]*flow.ParsedFlow{}
	var keys []string
	for _, pf := range res.Flows {
		k := pf.Key()
		if _, ok := groups[k]; !ok {
			groups[k] = pf
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	now := s.Clock.Now()
	for _, key := range keys {
		pf := groups[key]
		fl, ok := byKey[key]
		if !ok {
			fl = dbq.Flow{ID: newID(), NamespaceID: nsID, FlowKey: key, Path: pf.Path}
			if err := q.InsertFlow(ctx, dbq.InsertFlowParams{ID: fl.ID, NamespaceID: nsID, FlowKey: key, Path: pf.Path, CreatedAt: now}); err != nil {
				return err
			}
		}
		delete(byKey, key)
		issues := pf.Issues
		if issues == nil {
			issues = []flow.Issue{}
		}
		errJSON, _ := json.Marshal(issues)
		var def json.RawMessage
		if pf.Flow != nil {
			def, _ = json.Marshal(pf.Flow)
		}
		revID := fl.CurrentRevisionID
		needRevision := revID == nil
		if !needRevision {
			cur, err := q.GetFlowRevision(ctx, *revID)
			if err != nil {
				return err
			}
			needRevision = cur.SourceHash != pf.SourceHash || !bytes.Equal(normJSON(cur.Errors), normJSON(errJSON)) || cur.Path != pf.Path
		}
		valid := pf.Valid()
		if needRevision {
			id := newID()
			if err := q.InsertFlowRevision(ctx, dbq.InsertFlowRevisionParams{ID: id, FlowID: fl.ID, SnapshotID: snapshotID,
				SourceHash: pf.SourceHash, Source: pf.Source, Path: pf.Path, Definition: def, Errors: errJSON, CreatedAt: now}); err != nil {
				return err
			}
			revID = &id
			if err := q.UpdateFlowRevision(ctx, dbq.UpdateFlowRevisionParams{ID: fl.ID, CurrentRevisionID: revID, Valid: valid, Path: pf.Path}); err != nil {
				return err
			}
		} else if err := q.UpdateFlowPathValid(ctx, dbq.UpdateFlowPathValidParams{ID: fl.ID, Valid: valid, Path: pf.Path}); err != nil {
			return err
		}
		if err := syncTriggers(ctx, q, fl.ID, *revID, pf, valid && !fl.Disabled); err != nil {
			return err
		}
	}
	// Flow files that no longer exist: mark deleted, deactivate triggers (REQ-GIT-006).
	for _, fl := range byKey {
		if err := q.MarkFlowDeleted(ctx, dbq.MarkFlowDeletedParams{ID: fl.ID, DeletedAt: &now}); err != nil {
			return err
		}
		if err := q.DeactivateFlowTriggers(ctx, fl.ID); err != nil {
			return err
		}
	}
	return nil
}

func normJSON(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	out, _ := json.Marshal(v)
	return out
}

// syncTriggers upserts the declared triggers. Invalid, disabled or deleted flows have no active triggers.
func syncTriggers(ctx context.Context, q *dbq.Queries, flowID, revID uuid.UUID, pf *flow.ParsedFlow, active bool) error {
	if pf.Flow == nil || !pf.Valid() {
		return q.DeactivateFlowTriggers(ctx, flowID)
	}
	keys := []string{}
	for _, tr := range pf.Flow.Triggers {
		cfg, _ := json.Marshal(tr)
		if err := q.UpsertTrigger(ctx, dbq.UpsertTriggerParams{ID: newID(), FlowID: flowID, RevisionID: revID, TriggerKey: tr.ID,
			Type: tr.Type, Config: cfg, Active: active}); err != nil {
			return err
		}
		keys = append(keys, tr.ID)
	}
	return q.DeactivateMissingTriggers(ctx, dbq.DeactivateMissingTriggersParams{FlowID: flowID, Keys: keys})
}

// FlowRow is a flow with its namespace name.
type FlowRow = dbq.GetFlowRow

// ErrFlowNotFound is returned for unknown flows.
var ErrFlowNotFound = httpx.Errorf(404, "flow_not_found", "flow not found")

// GetFlow returns a flow by namespace and flow ID.
func (s *Service) GetFlow(ctx context.Context, namespace, flowID string) (FlowRow, error) {
	f, err := dbq.New(s.Pool).GetFlow(ctx, dbq.GetFlowParams{Name: namespace, FlowKey: flowID})
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrFlowNotFound
	}
	return f, err
}

// SetDisabled enables or disables a flow (REQ-FLOW-006). Disabled flows keep no active triggers.
func (s *Service) SetDisabled(ctx context.Context, namespace, flowID string, disabled bool) error {
	f, err := s.GetFlow(ctx, namespace, flowID)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbq.New(tx)
		if err := q.SetFlowDisabled(ctx, dbq.SetFlowDisabledParams{ID: f.ID, Disabled: disabled}); err != nil {
			return err
		}
		if f.CurrentRevisionID != nil {
			rev, err := q.GetFlowRevision(ctx, *f.CurrentRevisionID)
			if err != nil {
				return err
			}
			pf := &flow.ParsedFlow{Path: rev.Path}
			var issues []flow.Issue
			_ = json.Unmarshal(rev.Errors, &issues)
			pf.Issues = issues
			if len(rev.Definition) > 0 && string(rev.Definition) != "null" {
				var fl flow.Flow
				if err := json.Unmarshal(rev.Definition, &fl); err == nil {
					pf.Flow = &fl
				}
			}
			if err := syncTriggers(ctx, q, f.ID, rev.ID, pf, f.Valid && !disabled); err != nil {
				return err
			}
		}
		action := "flow.enable"
		if disabled {
			action = "flow.disable"
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: action, TargetType: "flow", TargetID: namespace + "/" + flowID})
	})
}
