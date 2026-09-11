package execution

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/storage"
)

// TaskGrace is the extra time after a task timeout before the engine marks a task that
// did not report as TIMED_OUT (DI-20).
const TaskGrace = 60 * time.Second

// LeaderTick runs checks that one instance does for all: flow deadlines, task deadlines,
// task runs of offline instances and the no_instance_for_pool reason.
func (e *Engine) LeaderTick(ctx context.Context) {
	now := e.Clock.Now()
	steps := []struct {
		name string
		fn   func(context.Context, time.Time) error
	}{
		{"flow deadlines", e.flowDeadlines},
		{"task deadlines", e.taskDeadlines},
		{"offline instances", e.offlineRuns},
		{"pool reasons", e.poolReasons},
	}
	for _, s := range steps {
		if err := s.fn(ctx, now); err != nil && ctx.Err() == nil {
			e.Log.Warn("engine leader step", "step", s.name, "err", err)
		}
	}
}

func (e *Engine) flowDeadlines(ctx context.Context, now time.Time) error {
	rows, err := e.Pool.Query(ctx, `SELECT id FROM executions WHERE state = 'RUNNING' AND deadline_at < $1 AND reason <> 'timeout' LIMIT 100`, now)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := e.Timeout(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// taskDeadlines marks running task runs TIMED_OUT when they pass their timeout by
// TaskGrace without a complete call. Subflow tasks end with their child.
func (e *Engine) taskDeadlines(ctx context.Context, now time.Time) error {
	rows, err := e.Pool.Query(ctx, `SELECT t.id, t.task_key, t.started_at, e.definition FROM task_runs t JOIN executions e ON e.id = t.execution_id
		WHERE t.state = 'RUNNING' AND t.task_type <> 'subflow' AND t.started_at < $1 LIMIT 500`, now.Add(-TaskGrace))
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
		if !ok {
			continue
		}
		if now.Sub(r.started) > def.Config(*t).Timeout+TaskGrace {
			_, _ = e.Pool.Exec(ctx, "UPDATE task_runs SET cancel_requested = true WHERE id = $1", r.id)
			e.finish(ctx, r.id, TaskTimedOut, ReasonTimeout, "task timeout reached", nil)
		}
	}
	return nil
}

// offlineRuns marks running process and inline task runs of offline instances as lost.
func (e *Engine) offlineRuns(ctx context.Context, now time.Time) error {
	rows, err := e.Pool.Query(ctx, `SELECT t.id FROM task_runs t LEFT JOIN instances i ON i.id = t.claimed_by
		WHERE t.state = 'RUNNING' AND t.executor_type IN ('process', 'inline') AND t.task_type <> 'subflow'
			AND (i.id IS NULL OR i.heartbeat_at < $1) LIMIT 500`, now.Add(-e.OfflineAfter))
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		e.finish(ctx, id, TaskFailed, ReasonLost, "the claiming instance is offline", nil)
	}
	return nil
}

// poolReasons sets no_instance_for_pool on queued task runs without an online instance
// for their pool and executor, and clears it again (REQ-EXR-008).
func (e *Engine) poolReasons(ctx context.Context, now time.Time) error {
	online := now.Add(-e.OfflineAfter)
	if _, err := e.Pool.Exec(ctx, `UPDATE task_runs t SET reason = 'no_instance_for_pool'
		WHERE t.state = 'QUEUED' AND t.executor_type <> 'inline' AND t.reason = ''
			AND NOT EXISTS (SELECT 1 FROM instances i WHERE i.heartbeat_at > $1 AND t.pool = ANY(i.pools) AND t.executor_type = ANY(i.executors))`, online); err != nil {
		return err
	}
	_, err := e.Pool.Exec(ctx, `UPDATE task_runs t SET reason = ''
		WHERE t.state = 'QUEUED' AND t.reason = 'no_instance_for_pool'
			AND EXISTS (SELECT 1 FROM instances i WHERE i.heartbeat_at > $1 AND t.pool = ANY(i.pools) AND t.executor_type = ANY(i.executors))`, online)
	return err
}

// DeleteExpired deletes executions that ended more than retention ago, with task runs,
// logs, metrics and artifacts (REQ-EXE-014).
func (e *Engine) DeleteExpired(ctx context.Context, retention time.Duration) (int, error) {
	cutoff := e.Clock.Now().Add(-retention)
	total := 0
	for {
		rows, err := e.Pool.Query(ctx, `SELECT id FROM executions WHERE ended_at < $1 ORDER BY ended_at LIMIT 200`, cutoff)
		if err != nil {
			return total, err
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		if len(ids) == 0 {
			return total, nil
		}
		for _, id := range ids {
			for _, prefix := range []string{"logs/" + id.String() + "/", "artifacts/" + id.String() + "/"} {
				var keys []string
				_ = e.Store.List(ctx, prefix, func(i storage.Info) error { keys = append(keys, i.Key); return nil })
				for _, k := range keys {
					if err := e.Store.Delete(ctx, k); err != nil {
						return total, err
					}
				}
			}
			if _, err := e.Pool.Exec(ctx, "DELETE FROM executions WHERE id = $1", id); err != nil {
				return total, err
			}
			total++
		}
	}
}
