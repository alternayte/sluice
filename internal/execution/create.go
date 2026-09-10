package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Errors of execution creation.
var (
	ErrFlowInvalid    = httpx.Errorf(http.StatusUnprocessableEntity, "flow_invalid", "the flow is invalid")
	ErrNotRunnable    = httpx.Validation(httpx.FieldError{Field: "path", Message: "the file has no runnable extension (.py, .sh, .ts, .js)"})
	ErrNotFound       = httpx.Errorf(http.StatusNotFound, "execution_not_found", "execution not found")
	ErrNotRestartable = httpx.Errorf(http.StatusConflict, "not_restartable", "only ended executions that did not succeed can restart from failed")
	ErrNotEnded       = httpx.Errorf(http.StatusConflict, "execution_active", "the execution has not ended")
)

// CreateParams describe a new execution.
type CreateParams struct {
	NamespaceID    uuid.UUID
	FlowID         *uuid.UUID
	RevisionID     *uuid.UUID
	SnapshotID     uuid.UUID
	Def            *Definition
	TriggerType    string
	TriggerID      *uuid.UUID
	ScheduledFor   *time.Time
	TriggerPayload map[string]any
	Inputs         map[string]any
	Labels         map[string]string
	ParentExecID   *uuid.UUID
	ParentTaskRun  *uuid.UUID
	RestartOf      *uuid.UUID
	ChainDepth     int
	CreatedBy      string
}

func actorLabel(ctx context.Context) string {
	if p := kernel.FromContext(ctx); p != nil {
		return p.Email
	}
	a := audit.ActorFrom(ctx)
	if a.Type == audit.ActorSystem {
		return "system"
	}
	return a.ID
}

// Create inserts an execution in QUEUED, or SKIPPED when the flow concurrency is
// `skip` and the limit is reached (REQ-EXE-007). Labels are flow labels merged with
// trigger labels (REQ-EXE-015). It must run in a transaction.
func (e *Engine) Create(ctx context.Context, tx pgx.Tx, p CreateParams) (uuid.UUID, string, error) {
	labels := map[string]string{}
	for k, v := range p.Def.Flow.Labels {
		labels[k] = v
	}
	for k, v := range p.Labels {
		labels[k] = v
	}
	state := ExecQueued
	reason := ""
	var endedAt *time.Time
	now := e.Clock.Now()
	if c := p.Def.Flow.Concurrency; c != nil && p.FlowID != nil {
		if err := dbq.New(tx).LockFlowRow(ctx, *p.FlowID); err != nil {
			return uuid.Nil, "", err
		}
		if c.Behavior == "skip" {
			var active int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM executions WHERE flow_id = $1 AND state IN ('QUEUED', 'RUNNING', 'CANCELLING')`,
				*p.FlowID).Scan(&active); err != nil {
				return uuid.Nil, "", err
			}
			if active >= c.Limit {
				state, reason, endedAt = ExecSkipped, "concurrency_limit", &now
			}
		}
	}
	def, _ := json.Marshal(p.Def)
	payload, _ := json.Marshal(nonNilMap(p.TriggerPayload))
	inputs, _ := json.Marshal(nonNilMap(p.Inputs))
	lb, _ := json.Marshal(labels)
	id, _ := uuid.NewV7()
	createdBy := p.CreatedBy
	if createdBy == "" {
		createdBy = actorLabel(ctx)
	}
	err := dbq.New(tx).InsertExecution(ctx, dbq.InsertExecutionParams{ID: id, NamespaceID: p.NamespaceID, FlowID: p.FlowID,
		FlowRevisionID: p.RevisionID, SnapshotID: p.SnapshotID, State: state, TriggerType: p.TriggerType, TriggerID: p.TriggerID,
		ScheduledFor: p.ScheduledFor, TriggerPayload: payload, Definition: def, Inputs: inputs, Labels: lb,
		ParentExecutionID: p.ParentExecID, ParentTaskRunID: p.ParentTaskRun, RestartOfID: p.RestartOf, ChainDepth: int32(p.ChainDepth),
		CreatedBy: createdBy, CreatedAt: now, EndedAt: endedAt, Reason: reason})
	if err != nil {
		return uuid.Nil, "", err
	}
	e.Wake()
	return id, state, nil
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// validateLabels checks trigger labels.
func validateLabels(labels map[string]string) error {
	if len(labels) > 20 {
		return httpx.Validation(httpx.FieldError{Field: "labels", Message: "at most 20 labels"})
	}
	for k, v := range labels {
		if k == "" || len(k) > 63 || strings.ContainsAny(k, "=,") {
			return httpx.Validation(httpx.FieldError{Field: "labels." + k, Message: "label keys have 1 to 63 characters without = or ,"})
		}
		if len(v) > 256 {
			return httpx.Validation(httpx.FieldError{Field: "labels." + k, Message: "label values have at most 256 characters"})
		}
	}
	return nil
}

// FlowRef is a loaded flow with its current revision.
type FlowRef struct {
	Flow     dbq.GetFlowRow
	Revision dbq.FlowRevision
	Def      *Definition
}

// LoadFlow loads a flow and builds its effective definition at the current revision.
func (e *Engine) LoadFlow(ctx context.Context, ns, flowKey string) (*FlowRef, error) {
	f, err := e.Namespaces.GetFlow(ctx, ns, flowKey)
	if err != nil {
		return nil, err
	}
	if !f.Valid || f.CurrentRevisionID == nil {
		return nil, ErrFlowInvalid
	}
	rev, err := dbq.New(e.Pool).GetFlowRevision(ctx, *f.CurrentRevisionID)
	if err != nil {
		return nil, err
	}
	var fl flow.Flow
	if err := json.Unmarshal(rev.Definition, &fl); err != nil {
		return nil, ErrFlowInvalid
	}
	defaults, err := e.namespaceDefaults(ctx, rev.SnapshotID)
	if err != nil {
		return nil, err
	}
	return &FlowRef{Flow: f, Revision: rev, Def: &Definition{Namespace: ns, FlowKey: flowKey, Flow: fl, Defaults: defaults}}, nil
}

// namespaceDefaults parses namespace.yaml of a snapshot.
func (e *Engine) namespaceDefaults(ctx context.Context, snapshotID uuid.UUID) (*flow.Defaults, error) {
	entry, err := dbq.New(e.Pool).GetSnapshotFile(ctx, dbq.GetSnapshotFileParams{SnapshotID: snapshotID, Path: flow.NamespaceFileName})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := e.Namespaces.ReadBlob(ctx, entry.Hash)
	if err != nil {
		return nil, err
	}
	nf, issues := flow.ParseNamespaceFile(b)
	if len(issues) > 0 || nf == nil {
		return nil, nil
	}
	return nf.Defaults, nil
}

// TriggerParams is a manual or automatic trigger of a flow.
type TriggerParams struct {
	Namespace      string
	FlowKey        string
	Inputs         map[string]any
	Labels         map[string]string
	TriggerType    string
	TriggerID      *uuid.UUID
	ScheduledFor   *time.Time
	TriggerPayload map[string]any
	ChainDepth     int
	ParentExecID   *uuid.UUID
	ParentTaskRun  *uuid.UUID
}

// InputErrors converts input errors to a validation error (REQ-FLOW-008).
func InputErrors(errs []flow.InputError) error {
	if len(errs) == 0 {
		return nil
	}
	fields := make([]httpx.FieldError, 0, len(errs))
	for _, e := range errs {
		fields = append(fields, httpx.FieldError{Field: e.Field, Message: e.Message})
	}
	return httpx.Validation(fields...)
}

// Trigger creates an execution of a flow at its current revision.
func (e *Engine) Trigger(ctx context.Context, r TriggerParams) (uuid.UUID, error) {
	if err := validateLabels(r.Labels); err != nil {
		return uuid.Nil, err
	}
	ref, err := e.LoadFlow(ctx, r.Namespace, r.FlowKey)
	if err != nil {
		return uuid.Nil, err
	}
	inputs, ierrs := flow.ResolveInputs(ref.Def.Flow.Inputs, r.Inputs)
	if err := InputErrors(ierrs); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		var state string
		var err error
		id, state, err = e.Create(ctx, tx, CreateParams{NamespaceID: ref.Flow.NamespaceID, FlowID: &ref.Flow.ID, RevisionID: &ref.Revision.ID,
			SnapshotID: ref.Revision.SnapshotID, Def: ref.Def, TriggerType: r.TriggerType, TriggerID: r.TriggerID, ScheduledFor: r.ScheduledFor,
			TriggerPayload: r.TriggerPayload, Inputs: inputs, Labels: r.Labels, ChainDepth: r.ChainDepth,
			ParentExecID: r.ParentExecID, ParentTaskRun: r.ParentTaskRun})
		if err != nil {
			return err
		}
		return e.Audit.Record(ctx, tx, audit.Event{Action: "execution.trigger", TargetType: "execution", TargetID: id.String(),
			Details: map[string]any{"flow": r.Namespace + "/" + r.FlowKey, "trigger_type": r.TriggerType, "state": state}})
	})
	return id, err
}

// RunFile creates an execution with one script task for a file (REQ-NS-007).
func (e *Engine) RunFile(ctx context.Context, ns, filePath string, args []string) (uuid.UUID, error) {
	if err := flow.ValidPath(filePath); err != nil {
		return uuid.Nil, httpx.Validation(httpx.FieldError{Field: "path", Message: err.Error()})
	}
	if flow.RuntimeFor("", filePath) == "" {
		return uuid.Nil, ErrNotRunnable
	}
	nsRow, err := e.Namespaces.Get(ctx, ns)
	if err != nil {
		return uuid.Nil, err
	}
	if nsRow.HeadSnapshotID == nil {
		return uuid.Nil, ErrFileNotFound
	}
	if _, err := dbq.New(e.Pool).GetSnapshotFile(ctx, dbq.GetSnapshotFileParams{SnapshotID: *nsRow.HeadSnapshotID, Path: filePath}); err != nil {
		return uuid.Nil, ErrFileNotFound
	}
	defaults, err := e.namespaceDefaults(ctx, *nsRow.HeadSnapshotID)
	if err != nil {
		return uuid.Nil, err
	}
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = EscapeTemplate(a)
	}
	def := &Definition{Namespace: ns, Defaults: defaults, Flow: flow.Flow{ID: "file-" + strings.TrimSuffix(path.Base(filePath), path.Ext(filePath)),
		Tasks: []flow.Task{{ID: "run", Type: "script", File: filePath, Args: escaped}}}}
	var id uuid.UUID
	err = pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		var err error
		id, _, err = e.Create(ctx, tx, CreateParams{NamespaceID: nsRow.ID, SnapshotID: *nsRow.HeadSnapshotID, Def: def, TriggerType: "file",
			TriggerPayload: map[string]any{"path": filePath, "args": args}})
		if err != nil {
			return err
		}
		return e.Audit.Record(ctx, tx, audit.Event{Action: "execution.run_file", TargetType: "execution", TargetID: id.String(),
			Details: map[string]any{"namespace": ns, "path": filePath}})
	})
	return id, err
}

// Rerun creates a new execution with the same snapshot, definition and inputs (REQ-EXE-009).
func (e *Engine) Rerun(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return e.copyExecution(ctx, id, false)
}

// Restart creates a new execution that reuses SUCCESS tasks of an ended execution (REQ-EXE-009).
func (e *Engine) Restart(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return e.copyExecution(ctx, id, true)
}

func (e *Engine) copyExecution(ctx context.Context, id uuid.UUID, restart bool) (uuid.UUID, error) {
	old, err := dbq.New(e.Pool).GetExecution(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if restart && (!ExecutionTerminal(old.State) || old.State == ExecSuccess || old.State == ExecSkipped) {
		return uuid.Nil, ErrNotRestartable
	}
	def, err := ParseDefinition(old.Definition)
	if err != nil {
		return uuid.Nil, err
	}
	var inputs, payload map[string]any
	_ = json.Unmarshal(old.Inputs, &inputs)
	_ = json.Unmarshal(old.TriggerPayload, &payload)
	var labels map[string]string
	_ = json.Unmarshal(old.Labels, &labels)
	var reuse []dbq.TaskRun
	if restart {
		runs, err := dbq.New(e.Pool).ListExecutionTaskRuns(ctx, id)
		if err != nil {
			return uuid.Nil, err
		}
		for _, tr := range latestRuns(runs) {
			if tr.State == TaskSuccess {
				reuse = append(reuse, tr)
			}
		}
	}
	trigger := "rerun"
	var restartOf *uuid.UUID
	if restart {
		trigger, restartOf = "restart", &id
	}
	var newID uuid.UUID
	err = pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		var err error
		newID, _, err = e.Create(ctx, tx, CreateParams{NamespaceID: old.NamespaceID, FlowID: old.FlowID, RevisionID: old.FlowRevisionID,
			SnapshotID: old.SnapshotID, Def: def, TriggerType: trigger, TriggerPayload: payload, Inputs: inputs, Labels: labels, RestartOf: restartOf})
		if err != nil {
			return err
		}
		now := e.Clock.Now()
		for _, tr := range reuse {
			rid := tr.ID
			nid, _ := uuid.NewV7()
			if err := dbq.New(tx).InsertTaskRun(ctx, dbq.InsertTaskRunParams{ID: nid, ExecutionID: newID, TaskKey: tr.TaskKey, TaskType: tr.TaskType,
				Attempt: 1, State: TaskSuccess, Reason: "reused", ExecutorType: tr.ExecutorType, Pool: tr.Pool, StartedAt: tr.StartedAt,
				EndedAt: &now, Outputs: tr.Outputs, ReusedFromID: &rid, ExitCode: tr.ExitCode}); err != nil {
				return err
			}
		}
		action := "execution.rerun"
		if restart {
			action = "execution.restart"
		}
		return e.Audit.Record(ctx, tx, audit.Event{Action: action, TargetType: "execution", TargetID: newID.String(),
			Details: map[string]any{"source": id.String(), "reused": len(reuse)}})
	})
	return newID, err
}

// latestRuns returns the latest attempt per task key.
func latestRuns(runs []dbq.TaskRun) map[string]dbq.TaskRun {
	out := map[string]dbq.TaskRun{}
	for _, tr := range runs {
		if cur, ok := out[tr.TaskKey]; !ok || tr.Attempt > cur.Attempt {
			out[tr.TaskKey] = tr
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
