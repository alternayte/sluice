package execution

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/execution/executiondb"
	"github.com/alternayte/sluice/internal/platform/masking"
)

// ListExecutions lists executions, newest first, with the filters of listExecutions. The
// AI tools use it.
func (e *Engine) ListExecutions(ctx context.Context, namespace, flow, state string, limit int) (ExecutionList, error) {
	return e.listExecutions(ctx, &listExecutionsIn{Namespace: namespace, Flow: flow, State: state, Sort: "created", Limit: limit})
}

// Metrics returns the metrics of an execution.
func (e *Engine) Metrics(ctx context.Context, id uuid.UUID) (MetricList, error) {
	q := executiondb.New(e.Pool)
	if _, err := q.GetExecution(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return MetricList{}, ErrNotFound
	} else if err != nil {
		return MetricList{}, err
	}
	rows, err := q.ListExecutionMetrics(ctx, id)
	if err != nil {
		return MetricList{}, err
	}
	out := MetricList{Items: []MetricPoint{}}
	for _, m := range rows {
		out.Items = append(out.Items, MetricPoint{TaskRunID: m.TaskRunID, TaskKey: m.TaskKey, Name: m.Name, Value: m.Value, Unit: m.Unit,
			Tags: strMap(m.Tags), TS: m.Ts})
	}
	return out, nil
}

// ExecutionMasker masks the secret values of all task runs of an execution (SI-01).
func (e *Engine) ExecutionMasker(ctx context.Context, id uuid.UUID) (*masking.Masker, error) {
	runs, err := executiondb.New(e.Pool).ListExecutionTaskRuns(ctx, id)
	if err != nil {
		return nil, err
	}
	ms := make([]*masking.Masker, 0, len(runs))
	for _, tr := range runs {
		ms = append(ms, e.MaskerFor(ctx, tr))
	}
	return masking.Merge(ms...), nil
}

// TriageFacts are the execution facts of a failure triage (REQ-AI-007).
type TriageFacts struct {
	Detail ExecutionDetail
	// Definition is the flow definition of the execution.
	Definition json.RawMessage
	// LastSuccessID and LastSuccessSnapshot name the last SUCCESS execution of the flow
	// before this execution.
	LastSuccessID       *uuid.UUID
	LastSuccessSnapshot *uuid.UUID
	// Durations are the durations in ms of the last 10 ended executions of the flow.
	Durations []int64
}

// TriageFacts returns the facts of a failure triage of an execution.
func (e *Engine) TriageFacts(ctx context.Context, id uuid.UUID) (TriageFacts, error) {
	d, err := e.Detail(ctx, id)
	if err != nil {
		return TriageFacts{}, err
	}
	ex, err := executiondb.New(e.Pool).GetExecution(ctx, id)
	if err != nil {
		return TriageFacts{}, err
	}
	f := TriageFacts{Detail: d, Definition: ex.Definition, Durations: []int64{}}
	if ex.FlowID == nil {
		return f, nil
	}
	var sid, snap uuid.UUID
	err = e.Pool.QueryRow(ctx, `SELECT id, snapshot_id FROM executions
		WHERE flow_id = $1 AND state = 'SUCCESS' AND created_at < $2 ORDER BY created_at DESC, id DESC LIMIT 1`,
		*ex.FlowID, ex.CreatedAt).Scan(&sid, &snap)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return TriageFacts{}, err
	default:
		f.LastSuccessID, f.LastSuccessSnapshot = &sid, &snap
	}
	rows, err := e.Pool.Query(ctx, `SELECT duration_ms FROM executions
		WHERE flow_id = $1 AND duration_ms IS NOT NULL AND created_at <= $2 ORDER BY created_at DESC, id DESC LIMIT 10`,
		*ex.FlowID, ex.CreatedAt)
	if err != nil {
		return TriageFacts{}, err
	}
	durations, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return TriageFacts{}, err
	}
	f.Durations = durations
	return f, nil
}
