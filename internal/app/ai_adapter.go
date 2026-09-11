package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/ai"
	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/execution/executiondb"
	"github.com/alternayte/sluice/internal/gitsync"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/masking"
)

// aiData adapts the namespace, execution and git features to ai.Data.
type aiData struct {
	ns  *namespace.Service
	e   *execution.Engine
	git *gitsync.Service
}

func (d aiData) Namespaces(ctx context.Context) ([]ai.NamespaceInfo, error) {
	nodes, err := d.ns.Tree(ctx)
	if err != nil {
		return nil, err
	}
	out := []ai.NamespaceInfo{}
	for _, n := range nodes {
		if n.Row == nil {
			continue
		}
		out = append(out, ai.NamespaceInfo{Name: n.Name, Source: n.Row.SourceType, Description: n.Row.Description})
	}
	return out, nil
}

func (d aiData) Flows(ctx context.Context, ns string) ([]ai.FlowInfo, error) {
	rows, err := d.ns.ListFlows(ctx, ns, nil, 200)
	if err != nil {
		return nil, err
	}
	out := make([]ai.FlowInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, ai.FlowInfo{Namespace: r.Namespace, FlowID: r.FlowID, Path: r.Path, Valid: r.Valid, Disabled: r.Disabled,
			Description: r.Description, LastState: r.LastState})
	}
	return out, nil
}

func (d aiData) Flow(ctx context.Context, ns, flowID string) (ai.FlowInfo, string, error) {
	f, err := d.ns.GetFlow(ctx, ns, flowID)
	if err != nil {
		return ai.FlowInfo{}, "", err
	}
	src, _, err := d.ReadFile(ctx, ns, f.Path)
	if err != nil {
		return ai.FlowInfo{}, "", err
	}
	return ai.FlowInfo{Namespace: f.NamespaceName, FlowID: f.FlowKey, Path: f.Path, Valid: f.Valid, Disabled: f.Disabled}, src, nil
}

func (d aiData) Files(ctx context.Context, ns string) ([]ai.FileInfo, error) {
	entries, err := d.ns.HeadFiles(ctx, ns)
	if err != nil {
		return nil, err
	}
	out := make([]ai.FileInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, ai.FileInfo{Path: e.Path, Size: e.Size, Executable: e.Executable})
	}
	return out, nil
}

func (d aiData) ReadFile(ctx context.Context, ns, path string) (string, bool, error) {
	r, _, err := d.ns.ReadFile(ctx, ns, path, nil)
	if errors.Is(err, namespace.ErrFileNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

func (d aiData) Validate(ctx context.Context, ns, path, content string) ([]ai.Issue, error) {
	_, _, issues, err := d.ns.ValidateFile(ctx, ns, path, content)
	if err != nil {
		return nil, err
	}
	out := make([]ai.Issue, 0, len(issues))
	for _, i := range issues {
		out = append(out, ai.Issue{Code: i.Code, Path: i.Path, Line: i.Line, Column: i.Column, Message: i.Message})
	}
	return out, nil
}

func (d aiData) Executions(ctx context.Context, f ai.ExecutionFilter) (any, error) {
	return d.e.ListExecutions(ctx, f.Namespace, f.Flow, f.State, f.Limit)
}

func (d aiData) Execution(ctx context.Context, id uuid.UUID) (any, error) { return d.e.Detail(ctx, id) }

func (d aiData) ExecutionState(ctx context.Context, id uuid.UUID) (string, error) {
	ex, err := executiondb.New(d.e.Pool).GetExecution(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", execution.ErrNotFound
	}
	return ex.State, err
}

func (d aiData) Logs(ctx context.Context, id uuid.UUID, task string) ([]ai.LogLine, error) {
	if _, err := d.ExecutionState(ctx, id); err != nil {
		return nil, err
	}
	lines, err := d.e.ExecutionLines(ctx, id, task, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ai.LogLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, ai.LogLine{TaskKey: l.TaskKey, Attempt: l.Attempt, Line: l.N, Stream: l.Stream, Text: l.Text})
	}
	return out, nil
}

func (d aiData) Metrics(ctx context.Context, id uuid.UUID) (any, error) { return d.e.Metrics(ctx, id) }

func (d aiData) Masker(ctx context.Context, id uuid.UUID) (*masking.Masker, error) {
	return d.e.ExecutionMasker(ctx, id)
}

func (d aiData) Trigger(ctx context.Context, ns, flowID string, inputs map[string]any, labels map[string]string) (any, error) {
	id, err := d.e.Trigger(ctx, execution.TriggerParams{Namespace: ns, FlowKey: flowID, Inputs: inputs, Labels: labels, TriggerType: "manual"})
	if err != nil {
		return nil, err
	}
	return d.e.Detail(ctx, id)
}

func (d aiData) Cancel(ctx context.Context, id uuid.UUID) error { return d.e.Cancel(ctx, id) }

func (d aiData) SourceType(ctx context.Context, ns string) (string, error) {
	row, err := d.ns.Get(ctx, ns)
	if err != nil {
		return "", err
	}
	return row.SourceType, nil
}

func (d aiData) Save(ctx context.Context, ns string, changes []ai.FileChange, message string) (int, error) {
	cs := make([]namespace.Change, 0, len(changes))
	for _, c := range changes {
		op := "put"
		if c.Delete {
			op = "delete"
		}
		cs = append(cs, namespace.Change{Op: op, Path: c.Path, Content: []byte(c.Content)})
	}
	info, err := d.ns.Save(ctx, ns, cs, message, nil)
	if err != nil {
		return 0, err
	}
	if info.Version == nil {
		return 0, nil
	}
	return int(*info.Version), nil
}

func (d aiData) Push(ctx context.Context, ns string, changes []ai.FileChange, message string) (string, string, error) {
	cs := make([]gitsync.Change, 0, len(changes))
	for _, c := range changes {
		op := "put"
		if c.Delete {
			op = "delete"
		}
		cs = append(cs, gitsync.Change{Op: op, Path: c.Path, Content: []byte(c.Content)})
	}
	return d.git.Push(ctx, ns, cs, message)
}

// Triage collects the context of a failure triage (REQ-AI-007). All texts are masked with
// the secret values of the execution.
func (d aiData) Triage(ctx context.Context, id uuid.UUID) (ai.TriageData, error) {
	f, err := d.e.TriageFacts(ctx, id)
	if err != nil {
		return ai.TriageData{}, err
	}
	m, err := d.e.ExecutionMasker(ctx, id)
	if err != nil {
		return ai.TriageData{}, err
	}
	ex := f.Detail
	out := ai.TriageData{ExecutionID: id, Namespace: ex.Namespace, State: ex.State, Error: m.String(ex.Error),
		LastSuccessID: f.LastSuccessID, Durations: f.Durations}
	if ex.FlowID != nil {
		out.FlowID = *ex.FlowID
	}

	// The failed task is the failed or timed out attempt that ended last.
	var failed *execution.TaskRun
	for i := range ex.TaskRuns {
		tr := &ex.TaskRuns[i]
		if tr.State != "FAILED" && tr.State != "TIMED_OUT" {
			continue
		}
		if failed == nil || (tr.EndedAt != nil && (failed.EndedAt == nil || tr.EndedAt.After(*failed.EndedAt))) {
			failed = tr
		}
	}
	if failed != nil {
		out.FailedTask, out.ExitCode, out.TaskError = failed.TaskKey, failed.ExitCode, m.String(failed.Error)
		if failed.Outputs != nil {
			b, _ := json.Marshal(*failed.Outputs)
			out.Outputs = m.Bytes(b)
		}
		var def struct {
			Tasks []json.RawMessage `json:"tasks"`
		}
		_ = json.Unmarshal(f.Definition, &def)
		for _, t := range def.Tasks {
			var head struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(t, &head) == nil && head.ID == failed.TaskKey {
				out.TaskSpec = m.Bytes(t)
			}
		}
		lines, err := d.e.ExecutionLines(ctx, id, failed.TaskKey, nil)
		if err != nil {
			return ai.TriageData{}, err
		}
		for _, l := range lines {
			if l.TaskRunID == failed.ID {
				out.Logs = append(out.Logs, ai.LogLine{TaskKey: l.TaskKey, Attempt: l.Attempt, Line: l.N, Stream: l.Stream, Text: m.String(l.Text)})
			}
		}
	}
	if metrics, err := d.e.Metrics(ctx, id); err == nil {
		b, _ := json.Marshal(metrics.Items)
		out.Metrics = m.Bytes(b)
	}
	if ex.FlowID != nil {
		if fl, err := d.ns.GetFlow(ctx, ex.Namespace, *ex.FlowID); err == nil {
			out.FlowPath = fl.Path
			if src, ok, err := d.ns.FileAt(ctx, ex.SnapshotID, fl.Path); err == nil && ok {
				out.FlowSource = m.String(src)
			}
		}
	}
	if f.LastSuccessSnapshot != nil {
		diff, err := d.ns.DiffSnapshots(ctx, *f.LastSuccessSnapshot, ex.SnapshotID)
		if err != nil {
			return ai.TriageData{}, err
		}
		out.Diff = m.String(diff)
	}
	return out, nil
}

// aiTriageHook queues automatic triages in the transaction that ends an execution.
func aiTriageHook(a *ai.Service) execution.EndHook {
	return func(ctx context.Context, tx pgx.Tx, ex executiondb.Execution) error {
		return a.OnEnd(ctx, tx, ex.ID, ex.State)
	}
}
