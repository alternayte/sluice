package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/executor"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/token"
	"github.com/alternayte/sluice/internal/runnerproto"
)

// TokenGrace is added to the task timeout for the run token lifetime (SI-04).
const TokenGrace = 10 * time.Minute

// Run runs the engine loop on this instance until ctx ends: start queued executions,
// promote retries and claim task runs (§4.3 steps 2 and 3).
func (e *Engine) Run(ctx context.Context) {
	e.initWake()
	go e.localWatch(ctx)
	poll := e.Cfg.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		if !e.isDraining() {
			if err := e.StartQueued(ctx); err != nil && ctx.Err() == nil {
				e.Log.Warn("start queued executions", "err", err)
			}
			if err := e.promoteRetries(ctx); err != nil && ctx.Err() == nil {
				e.Log.Warn("promote retries", "err", err)
			}
			if err := e.claim(ctx); err != nil && ctx.Err() == nil {
				e.Log.Warn("claim task runs", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.wake:
		}
	}
}

func (e *Engine) isDraining() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.draining
}

// promoteRetries advances executions that have a retry attempt whose backoff passed.
func (e *Engine) promoteRetries(ctx context.Context) error {
	rows, err := e.Pool.Query(ctx, `SELECT DISTINCT execution_id FROM task_runs
		WHERE state = 'PENDING' AND not_before IS NOT NULL AND not_before <= $1 LIMIT 50`, e.Clock.Now())
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error { return e.advance(ctx, tx, id) }); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) localCounts() (slots, inline int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range e.local {
		switch r.executorType {
		case executor.Process, executor.Docker:
			slots++
		case executor.Inline:
			inline++
		}
	}
	return slots, inline
}

type claimed struct {
	id    uuid.UUID
	token string
}

// claim takes queued task runs with FOR UPDATE SKIP LOCKED, filtered by pool, executor
// type and free slots (REQ-EXE-010).
func (e *Engine) claim(ctx context.Context) error {
	usedSlots, usedInline := e.localCounts()
	freeSlots := e.Cfg.WorkerSlots - usedSlots
	inlineSlots := e.Cfg.InlineSlots
	if inlineSlots <= 0 {
		inlineSlots = 64
	}
	freeInline := inlineSlots - usedInline
	var types []string
	for t := range e.Executors {
		if t != executor.Inline {
			types = append(types, t)
		}
	}
	limit := freeSlots + freeInline
	if _, ok := e.Executors[executor.Kubernetes]; ok {
		limit += e.Cfg.K8sMaxJobs
	}
	if limit <= 0 {
		return nil
	}
	var got []claimed
	now := e.Clock.Now()
	err := pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT t.id, t.executor_type, t.pool, e.definition, t.task_key FROM task_runs t
			JOIN executions e ON e.id = t.execution_id
			WHERE t.state = 'QUEUED' AND (t.executor_type = 'inline' OR (t.executor_type = ANY($1::text[]) AND t.pool = ANY($2::text[])))
			ORDER BY t.queued_at, t.id LIMIT $3 FOR UPDATE OF t SKIP LOCKED`, types, e.Pools, limit)
		if err != nil {
			return err
		}
		type cand struct {
			id        uuid.UUID
			typ, pool string
			def       []byte
			key       string
		}
		var cands []cand
		for rows.Next() {
			var c cand
			if err := rows.Scan(&c.id, &c.typ, &c.pool, &c.def, &c.key); err != nil {
				rows.Close()
				return err
			}
			cands = append(cands, c)
		}
		rows.Close()
		k8sRunning := map[string]int{}
		for _, c := range cands {
			switch c.typ {
			case executor.Inline:
				if freeInline <= 0 {
					continue
				}
				freeInline--
			case executor.Kubernetes:
				n, ok := k8sRunning[c.pool]
				if !ok {
					if err := tx.QueryRow(ctx, `SELECT count(*) FROM task_runs WHERE state = 'RUNNING' AND executor_type = 'kubernetes' AND pool = $1`, c.pool).Scan(&n); err != nil {
						return err
					}
				}
				if n >= e.Cfg.K8sMaxJobs {
					k8sRunning[c.pool] = n
					continue
				}
				k8sRunning[c.pool] = n + 1
			default:
				if freeSlots <= 0 {
					continue
				}
				freeSlots--
			}
			timeout := DefaultTaskTimeout
			if def, err := ParseDefinition(c.def); err == nil {
				if t, ok := def.Task(c.key); ok {
					timeout = def.Config(*t).Timeout
				}
			}
			runToken, hash := token.NewSecret()
			exp := now.Add(timeout + TokenGrace)
			if _, err := tx.Exec(ctx, `UPDATE task_runs SET state = 'RUNNING', claimed_by = $2, started_at = $3, heartbeat_at = $3,
				run_token_hash = $4, token_expires_at = $5, reason = '' WHERE id = $1 AND state = 'QUEUED'`,
				c.id, e.Instance, now, hash, exp); err != nil {
				return err
			}
			got = append(got, claimed{id: c.id, token: runToken})
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, c := range got {
		e.launch(ctx, c.id, c.token)
	}
	return nil
}

func (e *Engine) apiURLFor(typ string) string {
	switch typ {
	case executor.Docker:
		return e.Cfg.DockerAPIURL
	case executor.Kubernetes:
		return e.Cfg.ClusterAPIURL
	}
	return e.Cfg.APIURL
}

// launch resolves the plan and starts the executor. A template or secret error fails
// the task before any process starts (REQ-EXE-012).
func (e *Engine) launch(ctx context.Context, id uuid.UUID, token string) {
	tr, err := dbq.New(e.Pool).GetTaskRun(ctx, id)
	if err != nil {
		e.Log.Warn("launch: load task run", "task_run", id, "err", err)
		return
	}
	plan, err := e.BuildPlan(ctx, tr)
	if err != nil {
		reason, msg := ReasonTemplateError, err.Error()
		var pe *PlanError
		if errors.As(err, &pe) {
			reason, msg = pe.Reason, pe.Msg
		}
		e.SystemLog(ctx, tr, "[sluice] "+reason+": "+msg)
		e.finish(ctx, id, TaskFailed, reason, msg, nil)
		return
	}
	e.recordSecretKeys(ctx, tr.ExecutionID, plan.SecretKeys)
	ex, ok := e.Executors[tr.ExecutorType]
	if !ok {
		msg := fmt.Sprintf("executor %s is not enabled on this instance", tr.ExecutorType)
		e.SystemLog(ctx, tr, "[sluice] "+msg)
		e.finish(ctx, id, TaskFailed, ReasonExecutor, msg, nil)
		return
	}
	task := executor.Task{TaskRunID: tr.ID, ExecutionID: tr.ExecutionID, TaskKey: tr.TaskKey, Attempt: int(tr.Attempt), Pool: tr.Pool,
		Executor: plan.Cfg.Executor, Timeout: plan.Cfg.Timeout,
		Env: map[string]string{runnerproto.EnvAPIURL: e.apiURLFor(tr.ExecutorType), runnerproto.EnvRunToken: token, runnerproto.EnvTaskRunID: tr.ID.String()}}
	e.mu.Lock()
	if e.local == nil {
		e.local = map[uuid.UUID]*localRun{}
	}
	lr := &localRun{id: tr.ID, executorType: tr.ExecutorType}
	e.local[tr.ID] = lr
	e.mu.Unlock()
	ref, err := ex.Start(ctx, task)
	if err != nil {
		e.dropLocal(tr.ID)
		reason := ReasonExecutor
		var re *executor.ReasonError
		if errors.As(err, &re) {
			reason = re.Reason
		}
		e.SystemLog(ctx, tr, "[sluice] start failed: "+err.Error())
		e.finish(ctx, id, TaskFailed, reason, err.Error(), nil)
		return
	}
	e.mu.Lock()
	lr.ref = ref
	e.mu.Unlock()
	if err := dbq.New(e.Pool).SetTaskRunExternal(ctx, dbq.SetTaskRunExternalParams{ID: tr.ID, ExternalRef: ref}); err != nil {
		e.Log.Warn("set external ref", "err", err)
	}
	go func() {
		res := ex.Wait(context.WithoutCancel(ctx), ref)
		e.dropLocal(tr.ID)
		if res.Reason != "" {
			msg := res.Reason
			if res.Err != nil {
				msg = res.Err.Error()
			}
			e.SystemLog(context.WithoutCancel(ctx), tr, "[sluice] "+res.Reason+": "+msg)
			e.finish(context.WithoutCancel(ctx), tr.ID, TaskFailed, res.Reason, msg, nil)
		}
		e.Wake()
	}()
}

func (e *Engine) dropLocal(id uuid.UUID) {
	e.mu.Lock()
	delete(e.local, id)
	e.mu.Unlock()
}

// finish ends a running task run and logs a failure.
func (e *Engine) finish(ctx context.Context, id uuid.UUID, state, reason, msg string, exitCode *int) {
	if err := e.FinishTask(ctx, id, []string{TaskRunning, TaskQueued}, state, reason, msg, exitCode); err != nil {
		e.Log.Warn("finish task run", "task_run", id, "err", err)
	}
}

// afterTaskEnd archives the logs of an ended task run.
func (e *Engine) afterTaskEnd(id uuid.UUID) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := e.ArchiveTaskLogs(ctx, id); err != nil {
			e.Log.Warn("archive logs", "task_run", id, "err", err)
		}
	}()
	e.Wake()
}

// localWatch sends cancels to local work, refreshes inline heartbeats and marks lost
// task runs of this instance (REQ-EXE-011).
func (e *Engine) localWatch(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n++
		e.watchCancels(ctx)
		if n%5 == 0 {
			e.checkHeartbeats(ctx)
		}
	}
}

func (e *Engine) watchCancels(ctx context.Context) {
	e.mu.Lock()
	var ids, inline []uuid.UUID
	for id, r := range e.local {
		if !r.cancelSent && r.ref != "" {
			ids = append(ids, id)
		}
		if r.executorType == executor.Inline {
			inline = append(inline, id)
		}
	}
	e.mu.Unlock()
	if len(inline) > 0 {
		_, _ = e.Pool.Exec(ctx, `UPDATE task_runs SET heartbeat_at = $2 WHERE id = ANY($1::uuid[]) AND state = 'RUNNING'`, uuidStrings(inline), e.Clock.Now())
	}
	if len(ids) == 0 {
		return
	}
	rows, err := e.Pool.Query(ctx, `SELECT id FROM task_runs WHERE id = ANY($1::uuid[]) AND state = 'RUNNING' AND cancel_requested`, uuidStrings(ids))
	if err != nil {
		return
	}
	var cancel []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			cancel = append(cancel, id)
		}
	}
	rows.Close()
	for _, id := range cancel {
		e.mu.Lock()
		r, ok := e.local[id]
		if ok {
			r.cancelSent = true
		}
		e.mu.Unlock()
		if !ok {
			continue
		}
		if ex, ok := e.Executors[r.executorType]; ok {
			if err := ex.Cancel(ctx, r.ref); err != nil && !errors.Is(err, executor.ErrUnknownRef) {
				e.Log.Warn("cancel task run", "task_run", id, "err", err)
			}
		}
	}
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// checkHeartbeats checks task runs of this instance without heartbeat with their executor.
func (e *Engine) checkHeartbeats(ctx context.Context) {
	timeout := e.Cfg.HeartbeatTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	rows, err := e.Pool.Query(ctx, `SELECT id, executor_type, external_ref FROM task_runs
		WHERE claimed_by = $1 AND state = 'RUNNING' AND task_type <> 'subflow' AND heartbeat_at < $2`,
		e.Instance, e.Clock.Now().Add(-timeout))
	if err != nil {
		return
	}
	type row struct {
		id       uuid.UUID
		typ, ref string
	}
	var list []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.typ, &r.ref) == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	for _, r := range list {
		ex, ok := e.Executors[r.typ]
		gone := !ok || r.ref == ""
		if ok && r.ref != "" {
			st, err := ex.Status(ctx, r.ref)
			gone = err == nil && st == executor.StatusGone
		}
		if gone {
			e.Log.Warn("task run lost", "task_run", r.id)
			e.finish(ctx, r.id, TaskFailed, ReasonLost, "no heartbeat and the work is gone", nil)
		}
	}
}

// Shutdown stops claiming, stops local process and inline tasks and marks them FAILED
// with reason instance_shutdown. Docker and Kubernetes tasks continue (REQ-CORE-008).
func (e *Engine) Shutdown(ctx context.Context) {
	e.mu.Lock()
	e.draining = true
	var stop []*localRun
	for _, r := range e.local {
		if r.executorType == executor.Process || r.executorType == executor.Inline {
			stop = append(stop, r)
		}
	}
	e.mu.Unlock()
	for _, r := range stop {
		if ex, ok := e.Executors[r.executorType]; ok && r.ref != "" {
			_ = ex.Cancel(ctx, r.ref)
		}
		e.finish(ctx, r.id, TaskFailed, ReasonInstanceShutdown, "the instance shut down", nil)
	}
}
