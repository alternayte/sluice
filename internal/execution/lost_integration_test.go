//go:build integration

package execution_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/storage"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

const lostDef = `{"namespace":"lost","flow":{"id":"f","tasks":[
	{"id":"a","type":"command","retry":{"max_attempts":2},"executor":{"type":"docker","image":"alpine"}},
	{"id":"b","type":"command","retry":{"max_attempts":2},"executor":{"type":"docker","image":"alpine"}}]}}`

// TestLostTaskOfOfflineInstance checks that the maintenance leader marks a running docker
// task run lost when its instance is offline and its heartbeat is stale. The retry policy
// then queues a new attempt. A task run of an online instance with a fresh heartbeat stays
// RUNNING (REQ-CORE-007, REQ-EXR-006).
func TestLostTaskOfOfflineInstance(t *testing.T) {
	pool, _ := pgtest.Shared(t).NewPool(t)
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{Type: "postgres"}, pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	e := &execution.Engine{Pool: pool, Clock: fake, Store: store, Log: slog.New(slog.DiscardHandler),
		OfflineAfter: time.Minute, Cfg: execution.Config{HeartbeatTimeout: time.Minute}}

	ns, snap, ex := uuid.New(), uuid.New(), uuid.New()
	mustExec(t, pool, `INSERT INTO namespaces (id, name, source_type) VALUES ($1, 'lost', 'managed')`, ns)
	mustExec(t, pool, `INSERT INTO snapshots (id, namespace_id, version, manifest_hash) VALUES ($1, $2, 1, 'm')`, snap, ns)
	mustExec(t, pool, `INSERT INTO executions (id, namespace_id, snapshot_id, state, trigger_type, created_at, started_at, definition)
		VALUES ($1, $2, $3, 'RUNNING', 'manual', $4, $4, $5)`, ex, ns, snap, now.Add(-10*time.Minute), lostDef)

	// The old instance stopped 5 minutes ago. The new instance is online.
	offline, online := uuid.New(), uuid.New()
	for id, hb := range map[uuid.UUID]time.Time{offline: now.Add(-5 * time.Minute), online: now} {
		mustExec(t, pool, `INSERT INTO instances (id, hostname, version, pools, executors, started_at, heartbeat_at)
			VALUES ($1, 'h', 'v', '{default}', '{docker}', $2, $3)`, id, now.Add(-time.Hour), hb)
	}
	lost, live := uuid.New(), uuid.New()
	for _, r := range []struct {
		id, by uuid.UUID
		key    string
		hb     time.Time
	}{{lost, offline, "a", now.Add(-5 * time.Minute)}, {live, online, "b", now.Add(-10 * time.Second)}} {
		mustExec(t, pool, `INSERT INTO task_runs (id, execution_id, task_key, task_type, state, executor_type, claimed_by, external_ref, started_at, heartbeat_at)
			VALUES ($1, $2, $3, 'command', 'RUNNING', 'docker', $4, 'container', $5, $6)`, r.id, ex, r.key, r.by, now.Add(-10*time.Minute), r.hb)
	}

	e.LeaderTick(ctx)

	checkTaskRun(t, pool, lost, "FAILED", execution.ReasonLost)
	checkTaskRun(t, pool, live, "RUNNING", "")
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM task_runs WHERE execution_id = $1 AND task_key = 'a' AND attempt = 2`, ex).Scan(&state); err != nil {
		t.Fatalf("no retry attempt of the lost task run: %v", err)
	}
	if state != "PENDING" && state != "QUEUED" {
		t.Errorf("retry attempt state %s, want PENDING or QUEUED", state)
	}

	// A second tick does not finish the task run again.
	e.LeaderTick(ctx)
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM task_runs WHERE execution_id = $1 AND task_key = 'a'`, ex).Scan(&n); err != nil || n != 2 {
		t.Errorf("attempts of task a: %d, %v, want 2", n, err)
	}
}

func checkTaskRun(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, wantState, wantReason string) {
	t.Helper()
	var state, reason string
	if err := pool.QueryRow(context.Background(), `SELECT state, reason FROM task_runs WHERE id = $1`, id).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state != wantState || reason != wantReason {
		t.Errorf("task run %s: %s %q, want %s %q", id, state, reason, wantState, wantReason)
	}
}
