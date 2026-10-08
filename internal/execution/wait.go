package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/execution/executiondb"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// ErrNotWaiting is returned for an answer to a task that does not wait, or that has its answer.
var ErrNotWaiting = httpx.Errorf(http.StatusConflict, "not_waiting", "the task does not wait for an answer")

// startWait moves the task run of a wait task from PENDING to WAITING. No instance claims
// it and it uses no pool slot, so it stays through a restart of every instance. The task
// timeout counts from this time.
func (e *Engine) startWait(ctx context.Context, tx pgx.Tx, ex executiondb.Execution, def *Definition, t flow.Task, tr executiondb.TaskRun,
	byTask map[string][]executiondb.TaskRun) error {
	q := executiondb.New(tx)
	now := e.Clock.Now()
	tc, err := e.templateContext(ctx, infoOf(ex), def, byTask, nil)
	if err != nil {
		return err
	}
	msg, err := flow.RenderString(t.Message, tc)
	if err != nil {
		return e.endHere(ctx, q, tr.ID, now, TaskFailed, ReasonTemplateError, "message: "+err.Error())
	}
	return q.WaitTaskRun(ctx, executiondb.WaitTaskRunParams{ID: tr.ID, StartedAt: &now, WaitMessage: truncate(msg, 4000)})
}

// endHere ends a PENDING task run in this transaction, with no instance and no process. It
// passes QUEUED and RUNNING at the same time, so the transitions of §6.6 hold.
func (e *Engine) endHere(ctx context.Context, q *executiondb.Queries, id uuid.UUID, now time.Time, state, reason, errText string) error {
	if _, err := q.QueueTaskRun(ctx, executiondb.QueueTaskRunParams{ID: id, QueuedAt: &now}); err != nil {
		return err
	}
	if err := q.StartTaskRunHere(ctx, executiondb.StartTaskRunHereParams{ID: id, StartedAt: &now}); err != nil {
		return err
	}
	_, err := q.FinishTaskRun(ctx, executiondb.FinishTaskRunParams{ToState: state, Reason: reason, Error: truncate(errText, 4000),
		EndedAt: &now, ID: id, FromState: TaskRunning})
	return err
}

// waitDeadlines ends waiting task runs that passed their task timeout as TIMED_OUT.
func (e *Engine) waitDeadlines(ctx context.Context, now time.Time) error {
	rows, err := e.Pool.Query(ctx, `SELECT t.id, t.task_key, t.started_at, e.definition FROM task_runs t JOIN executions e ON e.id = t.execution_id
		WHERE t.state = 'WAITING' AND t.started_at IS NOT NULL ORDER BY t.started_at LIMIT 500`)
	if err != nil {
		return err
	}
	type row struct {
		id      uuid.UUID
		key     string
		started time.Time
		def     []byte
	}
	var list []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.key, &r.started, &r.def) == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	for _, r := range list {
		def, err := ParseDefinition(r.def)
		if err != nil {
			continue
		}
		t, ok := def.Task(r.key)
		if !ok || now.Sub(r.started) <= def.Config(*t).Timeout {
			continue
		}
		if err := e.FinishTask(ctx, r.id, []string{TaskWaiting}, TaskTimedOut, ReasonTimeout, "no answer before the task timeout", nil); err != nil {
			e.Log.Warn("wait timeout", "task_run", r.id, "err", err)
		}
	}
	return nil
}

// waitingRun locks the WAITING task run of a task of an execution. It returns ErrNotWaiting
// when the task does not wait: a second answer finds no row.
func (e *Engine) waitingRun(ctx context.Context, tx pgx.Tx, execID uuid.UUID, task string) (executiondb.TaskRun, *flow.Task, error) {
	q := executiondb.New(tx)
	ex, err := q.LockExecution(ctx, execID)
	if errors.Is(err, pgx.ErrNoRows) {
		return executiondb.TaskRun{}, nil, ErrNotFound
	}
	if err != nil {
		return executiondb.TaskRun{}, nil, err
	}
	def, err := ParseDefinition(ex.Definition)
	if err != nil {
		return executiondb.TaskRun{}, nil, err
	}
	t, ok := def.Task(task)
	if !ok {
		return executiondb.TaskRun{}, nil, httpx.Errorf(http.StatusNotFound, "task_not_found", "the flow has no task %q", task)
	}
	tr, err := q.LockWaitingTaskRun(ctx, executiondb.LockWaitingTaskRunParams{ExecutionID: execID, TaskKey: task})
	if errors.Is(err, pgx.ErrNoRows) {
		return executiondb.TaskRun{}, nil, ErrNotWaiting
	}
	return tr, t, err
}

// ResumeTask answers a waiting task. The values are checked against the fields of the task
// and become its outputs. The task run ends SUCCESS.
func (e *Engine) ResumeTask(ctx context.Context, execID uuid.UUID, task string, values map[string]any) error {
	var done uuid.UUID
	err := pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		tr, t, err := e.waitingRun(ctx, tx, execID, task)
		if err != nil {
			return err
		}
		out, ierrs := flow.ResolveInputs(t.Fields, values)
		if err := InputErrors(ierrs); err != nil {
			return err
		}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err := executiondb.New(tx).MergeTaskOutputs(ctx, executiondb.MergeTaskOutputsParams{ID: tr.ID, Outputs: b}); err != nil {
			return err
		}
		if err := e.Audit.Record(ctx, tx, audit.Event{Action: "execution.resume", TargetType: "execution", TargetID: execID.String(),
			Details: map[string]any{"task": task, "values": out}}); err != nil {
			return err
		}
		done = tr.ID
		return e.finishTaskTx(ctx, tx, tr.ID, []string{TaskWaiting}, TaskSuccess, "", "", nil)
	})
	if err == nil {
		e.afterTaskEnd(done)
		e.Wake()
	}
	return err
}

// RejectTask answers a waiting task with a refusal. The task run ends FAILED with reason
// rejected, so run_if decides what runs next.
func (e *Engine) RejectTask(ctx context.Context, execID uuid.UUID, task, message string) error {
	var done uuid.UUID
	err := pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		tr, _, err := e.waitingRun(ctx, tx, execID, task)
		if err != nil {
			return err
		}
		if err := e.Audit.Record(ctx, tx, audit.Event{Action: "execution.reject", TargetType: "execution", TargetID: execID.String(),
			Details: map[string]any{"task": task, "message": message}}); err != nil {
			return err
		}
		if message == "" {
			message = "rejected"
		}
		done = tr.ID
		return e.finishTaskTx(ctx, tx, tr.ID, []string{TaskWaiting}, TaskFailed, ReasonRejected, message, nil)
	})
	if err == nil {
		e.afterTaskEnd(done)
		e.Wake()
	}
	return err
}
