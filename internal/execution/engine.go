package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/execution/executiondb"
	"github.com/alternayte/sluice/internal/executor"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/storage"
)

// Config holds engine settings from the server configuration.
type Config struct {
	WorkerSlots      int
	K8sMaxJobs       int
	PollInterval     time.Duration
	HeartbeatTimeout time.Duration
	// APIURL is the runner callback URL for process tasks (loopback).
	APIURL string
	// DockerAPIURL is the runner callback URL for docker tasks.
	DockerAPIURL string
	// ClusterAPIURL is the runner callback URL for kubernetes tasks.
	ClusterAPIURL    string
	MaxArtifactBytes int64
	MaxBundleBytes   int64
	InlineSlots      int
}

// EndHook runs in the transaction that ends an execution.
type EndHook func(ctx context.Context, tx pgx.Tx, exec executiondb.Execution) error

// Engine creates, advances and finalizes executions and dispatches task runs.
type Engine struct {
	Pool       *pgxpool.Pool
	Clock      clock.Clock
	Log        *slog.Logger
	Audit      *audit.Writer
	Namespaces Namespaces
	Store      storage.Store
	Instance   uuid.UUID
	Pools      []string
	Executors  map[string]executor.Executor
	Cfg        Config
	Secrets    SecretResolver
	// OfflineAfter is the heartbeat age after which an instance is offline (REQ-CORE-007).
	// internal/app sets it from the instance registry.
	OfflineAfter time.Duration
	// EndHooks run when an execution ends (flow triggers, triage).
	EndHooks []EndHook

	wake     chan struct{}
	wakeOnce sync.Once

	mu       sync.Mutex
	local    map[uuid.UUID]*localRun
	draining bool
}

type localRun struct {
	id           uuid.UUID
	executorType string
	ref          string
	cancelSent   bool
}

func (e *Engine) initWake() {
	e.wakeOnce.Do(func() { e.wake = make(chan struct{}, 1) })
}

// Wake makes the engine loop run soon.
func (e *Engine) Wake() {
	e.initWake()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// StartQueued moves queued executions to RUNNING when concurrency allows (§4.3 step 2).
func (e *Engine) StartQueued(ctx context.Context) error {
	return pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		q := executiondb.New(tx)
		ids, err := q.ClaimQueuedExecutions(ctx, 50)
		if err != nil {
			return err
		}
		for _, id := range ids {
			ex, err := q.LockExecution(ctx, id)
			if err != nil {
				return err
			}
			def, err := ParseDefinition(ex.Definition)
			if err != nil {
				return err
			}
			if c := def.Flow.Concurrency; c != nil && ex.FlowID != nil {
				if err := q.LockFlowRow(ctx, *ex.FlowID); err != nil {
					return err
				}
				active, err := q.CountActiveFlowExecutions(ctx, ex.FlowID)
				if err != nil {
					return err
				}
				if int(active) >= c.Limit {
					continue // queue: stays QUEUED until a slot is free
				}
			}
			if err := e.start(ctx, tx, ex, def); err != nil {
				return err
			}
		}
		return nil
	})
}

func (e *Engine) start(ctx context.Context, tx pgx.Tx, ex executiondb.Execution, def *Definition) error {
	q := executiondb.New(tx)
	now := e.Clock.Now()
	var deadline *time.Time
	if d := def.FlowTimeout(); d > 0 {
		t := now.Add(d)
		deadline = &t
	}
	if err := q.StartExecution(ctx, executiondb.StartExecutionParams{ID: ex.ID, StartedAt: &now, DeadlineAt: deadline}); err != nil {
		return err
	}
	existing, err := q.ListExecutionTaskRuns(ctx, ex.ID)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, tr := range existing {
		have[tr.TaskKey] = true
	}
	for _, t := range def.Flow.Tasks {
		if have[t.ID] {
			continue // reused by restart
		}
		cfg := def.Config(t)
		id, _ := uuid.NewV7()
		if err := q.InsertTaskRun(ctx, executiondb.InsertTaskRunParams{ID: id, ExecutionID: ex.ID, TaskKey: t.ID, TaskType: t.Type, Attempt: 1,
			State: TaskPending, ExecutorType: cfg.Executor.Type, Pool: cfg.Pool}); err != nil {
			return err
		}
	}
	return e.advance(ctx, tx, ex.ID)
}

// dependencyDecision decides a PENDING task whose dependencies all ended (§6.6, DI-21).
// It returns "run", or "skip" with a reason.
func dependencyDecision(runIf string, deps []executiondb.TaskRun) (string, string) {
	anyFailed, allSuccess, anyUpstream := false, true, false
	for _, d := range deps {
		switch d.State {
		case TaskSuccess:
		case TaskFailed, TaskTimedOut:
			anyFailed, allSuccess, anyUpstream = true, false, true
		case TaskCancelled:
			allSuccess, anyUpstream = false, true
		case TaskSkipped:
			allSuccess = false
			if d.Reason == ReasonUpstreamFailed {
				anyUpstream = true
			}
		}
	}
	switch runIf {
	case "failure":
		if anyFailed {
			return "run", ""
		}
		return "skip", ReasonRunIfNotMet
	case "always":
		return "run", ""
	default:
		if allSuccess {
			return "run", ""
		}
		if anyUpstream {
			return "skip", ReasonUpstreamFailed
		}
		return "skip", ReasonRunIfNotMet
	}
}

// advance queues ready tasks, skips tasks whose run_if is not met and finalizes the
// execution when every task ended (REQ-EXE-003, REQ-EXE-004).
func (e *Engine) advance(ctx context.Context, tx pgx.Tx, execID uuid.UUID) error {
	q := executiondb.New(tx)
	ex, err := q.LockExecution(ctx, execID)
	if err != nil {
		return err
	}
	if ex.State != ExecRunning && ex.State != ExecCancelling {
		return nil
	}
	def, err := ParseDefinition(ex.Definition)
	if err != nil {
		return err
	}
	now := e.Clock.Now()
	for changed := true; changed; {
		changed = false
		runs, err := q.ListExecutionTaskRuns(ctx, execID)
		if err != nil {
			return err
		}
		latest := latestRuns(runs)
		active := 0
		for _, tr := range latest {
			if tr.State == TaskQueued || tr.State == TaskRunning {
				active++
			}
		}
		stopping := ex.State == ExecCancelling || ex.Reason == ReasonTimeout
		for _, t := range def.Flow.Tasks {
			tr, ok := latest[t.ID]
			if !ok || tr.State != TaskPending {
				continue
			}
			if stopping {
				continue
			}
			if tr.NotBefore != nil && now.Before(*tr.NotBefore) {
				continue
			}
			decision, reason := "run", ""
			if tr.Attempt == 1 {
				var deps []executiondb.TaskRun
				ready := true
				for _, d := range t.DependsOn {
					dr, ok := latest[d]
					if !ok || !TaskTerminal(dr.State) {
						ready = false
						break
					}
					deps = append(deps, dr)
				}
				if !ready {
					continue
				}
				decision, reason = dependencyDecision(t.RunIf, deps)
			}
			if decision == "skip" {
				if _, err := q.SkipTaskRun(ctx, executiondb.SkipTaskRunParams{ID: tr.ID, Reason: reason, EndedAt: &now}); err != nil {
					return err
				}
				changed = true
				continue
			}
			if def.Flow.MaxParallel > 0 && active >= def.Flow.MaxParallel {
				continue
			}
			if _, err := q.QueueTaskRun(ctx, executiondb.QueueTaskRunParams{ID: tr.ID, QueuedAt: &now}); err != nil {
				return err
			}
			active++
			changed = true
		}
	}
	runs, err := q.ListExecutionTaskRuns(ctx, execID)
	if err != nil {
		return err
	}
	latest := latestRuns(runs)
	for _, t := range def.Flow.Tasks {
		if tr, ok := latest[t.ID]; !ok || !TaskTerminal(tr.State) {
			return nil
		}
	}
	e.Wake()
	return e.finalize(ctx, tx, ex, def, latest)
}

// finalize ends an execution when all tasks ended (§6.6 execution result, REQ-EXE-013).
func (e *Engine) finalize(ctx context.Context, tx pgx.Tx, ex executiondb.Execution, def *Definition, latest map[string]executiondb.TaskRun) error {
	q := executiondb.New(tx)
	now := e.Clock.Now()
	to, reason, errText := ExecSuccess, "", ""
	var outputs json.RawMessage
	switch {
	case ex.State == ExecCancelling:
		to, reason = ExecCancelled, ReasonCancelled
	case ex.Reason == ReasonTimeout:
		to, reason, errText = ExecTimedOut, ReasonTimeout, "flow timeout reached"
	default:
		for _, t := range def.Flow.Tasks {
			tr := latest[t.ID]
			if tr.State == TaskSuccess || (tr.State == TaskSkipped && tr.Reason == ReasonRunIfNotMet) {
				continue
			}
			to = ExecFailed
			if errText == "" {
				errText = fmt.Sprintf("task %s %s", t.ID, strings.ToLower(tr.State))
				if tr.Error != "" {
					errText += ": " + tr.Error
				}
			}
		}
		if to == ExecSuccess && len(def.Flow.Outputs) > 0 {
			out, err := e.resolveFlowOutputs(ctx, ex, def, latest)
			if err != nil {
				to, reason, errText = ExecFailed, ReasonOutputError, err.Error()
			} else {
				outputs, _ = json.Marshal(out)
			}
		}
	}
	if err := CheckExecution(ex.State, to); err != nil {
		return err
	}
	var dur *int64
	if ex.StartedAt != nil {
		d := now.Sub(*ex.StartedAt).Milliseconds()
		dur = &d
	}
	n, err := q.SetExecutionState(ctx, executiondb.SetExecutionStateParams{ToState: to, Reason: reason, Error: errText, EndedAt: &now,
		DurationMs: dur, Outputs: outputs, ID: ex.ID, FromState: ex.State})
	if err != nil || n == 0 {
		return err
	}
	ex.State, ex.Reason, ex.Error, ex.EndedAt, ex.Outputs = to, reason, errText, &now, outputs
	for _, h := range e.EndHooks {
		if err := h(ctx, tx, ex); err != nil {
			return err
		}
	}
	return e.subflowParentEnd(ctx, tx, ex)
}

func (e *Engine) resolveFlowOutputs(ctx context.Context, ex executiondb.Execution, def *Definition, latest map[string]executiondb.TaskRun) (map[string]any, error) {
	tc, err := e.templateContext(ctx, infoOf(ex), def, latest, nil)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, k := range sortedKeys(def.Flow.Outputs) {
		v, err := flow.RenderString(def.Flow.Outputs[k], tc)
		if err != nil {
			return nil, fmt.Errorf("output %s: %v", k, err)
		}
		out[k] = parseMaybeJSON(v)
	}
	return out, nil
}

// parseMaybeJSON returns numbers, booleans, objects and arrays as JSON values and other text as a string.
func parseMaybeJSON(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		switch v.(type) {
		case map[string]any, []any, float64, bool:
			return v
		}
	}
	return s
}

// FinishTask ends a task run and applies the retry policy (REQ-EXE-005).
func (e *Engine) FinishTask(ctx context.Context, taskRunID uuid.UUID, fromStates []string, state, reason, errText string, exitCode *int) error {
	err := pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		return e.finishTaskTx(ctx, tx, taskRunID, fromStates, state, reason, errText, exitCode)
	})
	if err == nil {
		e.afterTaskEnd(taskRunID)
	}
	return err
}

func (e *Engine) finishTaskTx(ctx context.Context, tx pgx.Tx, taskRunID uuid.UUID, fromStates []string, state, reason, errText string, exitCode *int) error {
	q := executiondb.New(tx)
	tr, err := q.LockTaskRun(ctx, taskRunID)
	if err != nil {
		return err
	}
	allowed := false
	for _, s := range fromStates {
		if tr.State == s {
			allowed = true
		}
	}
	if !allowed {
		return nil // already ended
	}
	if tr.CancelRequested && state != TaskSuccess {
		state, reason = TaskCancelled, ReasonCancelled
	}
	if err := CheckTask(tr.State, state); err != nil {
		return err
	}
	now := e.Clock.Now()
	var ec *int32
	if exitCode != nil {
		x := int32(*exitCode)
		ec = &x
	}
	if _, err := q.FinishTaskRun(ctx, executiondb.FinishTaskRunParams{ToState: state, Reason: reason, Error: truncate(errText, 4000), ExitCode: ec,
		EndedAt: &now, ID: tr.ID, FromState: tr.State}); err != nil {
		return err
	}
	ex, err := q.LockExecution(ctx, tr.ExecutionID)
	if err != nil {
		return err
	}
	if (state == TaskFailed || state == TaskTimedOut) && ex.State == ExecRunning && ex.Reason != ReasonTimeout {
		def, err := ParseDefinition(ex.Definition)
		if err != nil {
			return err
		}
		if t, ok := def.Task(tr.TaskKey); ok {
			cfg := def.Config(*t)
			if int(tr.Attempt) < cfg.Retry.MaxAttempts {
				nb := now.Add(Backoff(cfg.Retry, int(tr.Attempt)))
				id, _ := uuid.NewV7()
				if err := q.InsertTaskRun(ctx, executiondb.InsertTaskRunParams{ID: id, ExecutionID: tr.ExecutionID, TaskKey: tr.TaskKey, TaskType: tr.TaskType,
					Attempt: tr.Attempt + 1, State: TaskPending, ExecutorType: tr.ExecutorType, Pool: tr.Pool, NotBefore: &nb}); err != nil {
					return err
				}
			}
		}
	}
	return e.advance(ctx, tx, tr.ExecutionID)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Cancel cancels an execution and its subflow children (REQ-EXE-008, REQ-EXE-016).
func (e *Engine) Cancel(ctx context.Context, id uuid.UUID) error {
	err := pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		return e.cancelTx(ctx, tx, id, true)
	})
	e.Wake()
	return err
}

func (e *Engine) cancelTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, record bool) error {
	q := executiondb.New(tx)
	ex, err := q.LockExecution(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	now := e.Clock.Now()
	switch ex.State {
	case ExecQueued:
		if _, err := q.SetExecutionState(ctx, executiondb.SetExecutionStateParams{ToState: ExecCancelled, Reason: ReasonCancelled, EndedAt: &now,
			ID: id, FromState: ExecQueued}); err != nil {
			return err
		}
		if _, err := q.CancelPendingTaskRuns(ctx, executiondb.CancelPendingTaskRunsParams{ExecutionID: id, EndedAt: &now}); err != nil {
			return err
		}
		ex.State = ExecCancelled
		ex.EndedAt = &now
		if err := e.subflowParentEnd(ctx, tx, ex); err != nil {
			return err
		}
	case ExecRunning:
		if _, err := q.SetExecutionState(ctx, executiondb.SetExecutionStateParams{ToState: ExecCancelling, Reason: ReasonCancelled, ID: id, FromState: ExecRunning}); err != nil {
			return err
		}
		if err := e.stopTasks(ctx, tx, id); err != nil {
			return err
		}
	default:
		if record {
			return ErrNotEnded.WithDetails(map[string]string{"state": ex.State})
		}
		return nil
	}
	if record {
		if err := e.Audit.Record(ctx, tx, audit.Event{Action: "execution.cancel", TargetType: "execution", TargetID: id.String()}); err != nil {
			return err
		}
	}
	return e.advance(ctx, tx, id)
}

// stopTasks cancels pending and queued task runs, asks running ones to stop and
// cancels running subflow children.
func (e *Engine) stopTasks(ctx context.Context, tx pgx.Tx, execID uuid.UUID) error {
	q := executiondb.New(tx)
	now := e.Clock.Now()
	if _, err := q.CancelPendingTaskRuns(ctx, executiondb.CancelPendingTaskRunsParams{ExecutionID: execID, EndedAt: &now}); err != nil {
		return err
	}
	if _, err := q.RequestCancelRunning(ctx, execID); err != nil {
		return err
	}
	runs, err := q.ListExecutionTaskRuns(ctx, execID)
	if err != nil {
		return err
	}
	for _, tr := range runs {
		if tr.State == TaskRunning && tr.ChildExecutionID != nil {
			if err := e.cancelTx(ctx, tx, *tr.ChildExecutionID, false); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}
	return nil
}

// Timeout ends an execution whose flow timeout passed (REQ-EXE-006).
func (e *Engine) Timeout(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		q := executiondb.New(tx)
		ex, err := q.LockExecution(ctx, id)
		if err != nil {
			return err
		}
		if ex.State != ExecRunning || ex.Reason == ReasonTimeout {
			return nil
		}
		if _, err := tx.Exec(ctx, "UPDATE executions SET reason = $2 WHERE id = $1", id, ReasonTimeout); err != nil {
			return err
		}
		if err := e.stopTasks(ctx, tx, id); err != nil {
			return err
		}
		// Running task runs end as TIMED_OUT when their runner reports.
		if _, err := tx.Exec(ctx, "UPDATE task_runs SET reason = $2 WHERE execution_id = $1 AND state = 'RUNNING'", id, ReasonTimeout); err != nil {
			return err
		}
		return e.advance(ctx, tx, id)
	})
}

// subflowParentEnd finishes the parent subflow task when a child execution ends (REQ-EXE-016).
func (e *Engine) subflowParentEnd(ctx context.Context, tx pgx.Tx, child executiondb.Execution) error {
	if child.ParentTaskRunID == nil || !ExecutionTerminal(child.State) {
		return nil
	}
	q := executiondb.New(tx)
	parent, err := q.LockTaskRun(ctx, *child.ParentTaskRunID)
	if err != nil || parent.State != TaskRunning {
		return nil //nolint:nilerr // a missing or ended parent task needs no update
	}
	if child.State == ExecSuccess {
		outputs := child.Outputs
		if len(outputs) == 0 {
			outputs = json.RawMessage(`{}`)
		}
		if err := q.MergeTaskOutputs(ctx, executiondb.MergeTaskOutputsParams{ID: parent.ID, Outputs: outputs}); err != nil {
			return err
		}
		return e.finishTaskTx(ctx, tx, parent.ID, []string{TaskRunning}, TaskSuccess, "", "", nil)
	}
	return e.finishTaskTx(ctx, tx, parent.ID, []string{TaskRunning}, TaskFailed, ReasonChildFailed,
		fmt.Sprintf("child execution %s ended %s", child.ID, child.State), nil)
}
