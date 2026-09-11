package namespace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/namespace/namespacedb"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/page"
)

// ExecutionRef names the last execution of a flow.
type ExecutionRef struct {
	ID        uuid.UUID `json:"id"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// FlowSummary is one flow with the state of its last execution.
type FlowSummary struct {
	ID            uuid.UUID          `json:"id"`
	Namespace     string             `json:"namespace"`
	FlowID        string             `json:"flow_id"`
	Path          string             `json:"path"`
	Valid         bool               `json:"valid"`
	Disabled      bool               `json:"disabled"`
	Description   string             `json:"description"`
	ErrorCount    int                `json:"error_count"`
	Labels        *map[string]string `json:"labels,omitempty"`
	LastExecution *ExecutionRef      `json:"last_execution,omitempty"`
}

// FlowList is one page of flows.
type FlowList struct {
	Items      []FlowSummary `json:"items"`
	NextCursor *string       `json:"next_cursor,omitempty"`
}

// TriggerInfo is one trigger of a flow.
type TriggerInfo struct {
	Key           string         `json:"key"`
	Type          string         `json:"type" enum:"schedule,webhook,flow"`
	Active        bool           `json:"active"`
	Config        map[string]any `json:"config"`
	NextFireAt    *time.Time     `json:"next_fire_at,omitempty" nullable:"true"`
	LastFiredAt   *time.Time     `json:"last_fired_at,omitempty" nullable:"true"`
	HasWebhookKey bool           `json:"has_webhook_key"`
}

// Revision is one revision of a flow with its source.
type Revision struct {
	ID              uuid.UUID       `json:"id"`
	CreatedAt       time.Time       `json:"created_at"`
	SnapshotVersion *int            `json:"snapshot_version,omitempty" nullable:"true"`
	GitSha          *string         `json:"git_sha,omitempty"`
	Message         *string         `json:"message,omitempty"`
	Path            string          `json:"path"`
	Source          string          `json:"source"`
	Valid           bool            `json:"valid"`
	Definition      *map[string]any `json:"definition,omitempty" nullable:"true"`
	Errors          []Issue         `json:"errors"`
}

// RevisionSummary is one revision without its source.
type RevisionSummary struct {
	ID              uuid.UUID `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	SnapshotVersion *int      `json:"snapshot_version,omitempty" nullable:"true"`
	GitSha          *string   `json:"git_sha,omitempty"`
	Message         *string   `json:"message,omitempty"`
	Valid           bool      `json:"valid"`
	ErrorCount      int       `json:"error_count"`
}

// RevisionList is a list of revisions, newest first.
type RevisionList struct {
	Items []RevisionSummary `json:"items"`
}

// FlowDetail is a flow with its current revision and triggers.
type FlowDetail struct {
	ID            uuid.UUID          `json:"id"`
	Namespace     string             `json:"namespace"`
	FlowID        string             `json:"flow_id"`
	Path          string             `json:"path"`
	Valid         bool               `json:"valid"`
	Disabled      bool               `json:"disabled"`
	Description   string             `json:"description"`
	ErrorCount    int                `json:"error_count"`
	Labels        *map[string]string `json:"labels,omitempty"`
	LastExecution *ExecutionRef      `json:"last_execution,omitempty"`
	Revision      *Revision          `json:"revision,omitempty"`
	Triggers      []TriggerInfo      `json:"triggers"`
}

// TextDiff is a unified diff.
type TextDiff struct {
	Diff string `json:"diff"`
}

type flowDetailOut struct{ Body FlowDetail }

func (r FlowListRow) summaryOp() FlowSummary {
	s := FlowSummary{ID: r.ID, Namespace: r.Namespace, FlowID: r.FlowID, Path: r.Path, Valid: r.Valid, Disabled: r.Disabled,
		Description: r.Description, ErrorCount: r.ErrorCount}
	if len(r.Labels) > 0 {
		l := r.Labels
		s.Labels = &l
	}
	if r.LastID != nil {
		s.LastExecution = &ExecutionRef{ID: *r.LastID, State: r.LastState, CreatedAt: *r.LastCreated}
	}
	return s
}

func detailOp(ctx context.Context, s *Service, namespace, flowID string) (FlowDetail, error) {
	f, err := s.GetFlow(ctx, namespace, flowID)
	if err != nil {
		return FlowDetail{}, err
	}
	rows, err := s.ListFlowsByID(ctx, f.ID)
	if err != nil {
		return FlowDetail{}, err
	}
	if len(rows) == 0 {
		return FlowDetail{}, ErrFlowNotFound
	}
	sum := rows[0].summaryOp()
	d := FlowDetail{ID: sum.ID, Namespace: sum.Namespace, FlowID: sum.FlowID, Path: sum.Path, Valid: sum.Valid, Disabled: sum.Disabled,
		Description: sum.Description, ErrorCount: sum.ErrorCount, Labels: sum.Labels, LastExecution: sum.LastExecution, Triggers: []TriggerInfo{}}
	q := namespacedb.New(s.Pool)
	if f.CurrentRevisionID != nil {
		rev, err := revisionOp(ctx, s, f.ID, *f.CurrentRevisionID)
		if err != nil {
			return d, err
		}
		d.Revision = &rev
	}
	triggers, err := q.ListFlowTriggers(ctx, f.ID)
	if err != nil {
		return d, err
	}
	for _, t := range triggers {
		cfg := map[string]any{}
		_ = json.Unmarshal(t.Config, &cfg)
		d.Triggers = append(d.Triggers, TriggerInfo{Key: t.TriggerKey, Type: t.Type, Active: t.Active, Config: cfg,
			NextFireAt: t.NextFireAt, LastFiredAt: t.LastFiredAt, HasWebhookKey: len(t.WebhookKeyHash) > 0})
	}
	return d, nil
}

func revisionOp(ctx context.Context, s *Service, flowID, revID uuid.UUID) (Revision, error) {
	q := namespacedb.New(s.Pool)
	r, err := q.GetFlowRevision(ctx, revID)
	if err != nil || r.FlowID != flowID {
		return Revision{}, httpx.Errorf(http.StatusNotFound, "revision_not_found", "revision not found")
	}
	var issues []flow.Issue
	_ = json.Unmarshal(r.Errors, &issues)
	out := Revision{ID: r.ID, CreatedAt: r.CreatedAt, Path: r.Path, Source: r.Source, Valid: len(issues) == 0, Errors: toIssuesOp(issues)}
	if len(r.Definition) > 0 && string(r.Definition) != "null" {
		def := map[string]any{}
		if json.Unmarshal(r.Definition, &def) == nil {
			out.Definition = &def
		}
	}
	if sn, err := q.GetSnapshot(ctx, r.SnapshotID); err == nil {
		out.SnapshotVersion = optInt32(sn.Version)
		out.GitSha = optStr(sn.GitSha)
		out.Message = optStr(sn.Message)
	}
	return out, nil
}

// flowIn holds the path parameters of one flow.
type flowIn struct {
	Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
	FlowID    string `path:"flowId" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]*$"`
}

func registerFlows(api huma.API, s *Service) {
	viewer := httpx.MinRole(kernel.Viewer)

	huma.Register(api, httpx.Op("listFlows", http.MethodGet, "/api/v1/flows", viewer),
		func(ctx context.Context, in *struct {
			Namespace string `query:"namespace" doc:"Namespace and its children."`
			Cursor    string `query:"cursor" doc:"Opaque cursor from next_cursor of the previous page."`
			Limit     int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
		}) (*struct{ Body FlowList }, error) {
			var after []string
			if in.Cursor != "" {
				var err error
				if after, err = page.DecodeStrings(in.Cursor, 2); err != nil {
					return nil, err
				}
			}
			limit := page.Limit(in.Limit)
			rows, err := s.ListFlows(ctx, in.Namespace, after, limit+1)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body FlowList }{Body: FlowList{Items: []FlowSummary{}}}
			if len(rows) > limit {
				rows = rows[:limit]
				last := rows[len(rows)-1]
				c := page.EncodeStrings(last.Namespace, last.FlowID)
				out.Body.NextCursor = &c
			}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, r.summaryOp())
			}
			return out, nil
		})

	huma.Register(api, httpx.Op("getFlow", http.MethodGet, "/api/v1/flows/{namespace}/{flowId}", viewer),
		func(ctx context.Context, in *flowIn) (*flowDetailOut, error) {
			d, err := detailOp(ctx, s, in.Namespace, in.FlowID)
			if err != nil {
				return nil, err
			}
			return &flowDetailOut{Body: d}, nil
		})

	huma.Register(api, httpx.Op("updateFlow", http.MethodPatch, "/api/v1/flows/{namespace}/{flowId}", httpx.MinRole(kernel.Editor)),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			FlowID    string `path:"flowId" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]*$"`
			Body      struct {
				Disabled bool `json:"disabled"`
			}
		}) (*flowDetailOut, error) {
			if err := s.SetDisabled(ctx, in.Namespace, in.FlowID, in.Body.Disabled); err != nil {
				return nil, err
			}
			d, err := detailOp(ctx, s, in.Namespace, in.FlowID)
			if err != nil {
				return nil, err
			}
			return &flowDetailOut{Body: d}, nil
		})

	huma.Register(api, httpx.Op("listFlowRevisions", http.MethodGet, "/api/v1/flows/{namespace}/{flowId}/revisions", viewer),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			FlowID    string `path:"flowId" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]*$"`
			Limit     int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
		}) (*struct{ Body RevisionList }, error) {
			f, err := s.GetFlow(ctx, in.Namespace, in.FlowID)
			if err != nil {
				return nil, err
			}
			rows, err := namespacedb.New(s.Pool).ListFlowRevisions(ctx, namespacedb.ListFlowRevisionsParams{FlowID: f.ID, Limit: int32(page.Limit(in.Limit))})
			if err != nil {
				return nil, err
			}
			out := &struct{ Body RevisionList }{Body: RevisionList{Items: []RevisionSummary{}}}
			for _, r := range rows {
				var issues []flow.Issue
				_ = json.Unmarshal(r.Errors, &issues)
				out.Body.Items = append(out.Body.Items, RevisionSummary{ID: r.ID, CreatedAt: r.CreatedAt, SnapshotVersion: optInt32(r.Version),
					GitSha: optStr(r.GitSha), Message: optStr(r.Message), Valid: len(issues) == 0, ErrorCount: len(issues)})
			}
			return out, nil
		})

	huma.Register(api, httpx.Op("getFlowRevision", http.MethodGet, "/api/v1/flows/{namespace}/{flowId}/revisions/{revisionId}", viewer),
		func(ctx context.Context, in *struct {
			Namespace  string    `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			FlowID     string    `path:"flowId" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]*$"`
			RevisionID uuid.UUID `path:"revisionId"`
		}) (*struct{ Body Revision }, error) {
			f, err := s.GetFlow(ctx, in.Namespace, in.FlowID)
			if err != nil {
				return nil, err
			}
			rev, err := revisionOp(ctx, s, f.ID, in.RevisionID)
			if err != nil {
				return nil, err
			}
			return &struct{ Body Revision }{Body: rev}, nil
		})

	huma.Register(api, httpx.Op("diffFlowRevisions", http.MethodGet, "/api/v1/flows/{namespace}/{flowId}/diff", viewer),
		func(ctx context.Context, in *struct {
			Namespace string    `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			FlowID    string    `path:"flowId" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]*$"`
			From      uuid.UUID `query:"from" required:"true"`
			To        uuid.UUID `query:"to" required:"true"`
		}) (*struct{ Body TextDiff }, error) {
			f, err := s.GetFlow(ctx, in.Namespace, in.FlowID)
			if err != nil {
				return nil, err
			}
			a, err := revisionOp(ctx, s, f.ID, in.From)
			if err != nil {
				return nil, err
			}
			b, err := revisionOp(ctx, s, f.ID, in.To)
			if err != nil {
				return nil, err
			}
			label := func(r Revision) string {
				if r.SnapshotVersion != nil {
					return fmt.Sprintf("v%d", *r.SnapshotVersion)
				}
				return r.ID.String()[:8]
			}
			return &struct{ Body TextDiff }{Body: TextDiff{Diff: UnifiedDiff(b.Path, label(a), label(b), a.Source, b.Source)}}, nil
		})

	// The schema is the JSON of flow.FlowSchema, sent through a map as before: the keys come out in sorted order.
	huma.Register(api, httpx.Op("getFlowSchema", http.MethodGet, "/api/v1/schemas/flow.json", httpx.Public),
		func(context.Context, *struct{}) (*struct{ Body json.RawMessage }, error) {
			b, err := flow.FlowSchema()
			if err != nil {
				return nil, err
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				return nil, err
			}
			out, err := json.Marshal(m)
			if err != nil {
				return nil, err
			}
			return &struct{ Body json.RawMessage }{Body: out}, nil
		})
}
