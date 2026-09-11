//go:build integration

package execution_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/storage"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

// TestSCN_EXE_014_Retention deletes executions that ended before the retention period,
// with their task runs, logs, metrics and artifacts. Newer executions stay (REQ-EXE-014).
func TestSCN_EXE_014_Retention(t *testing.T) {
	pool, _ := pgtest.Shared(t).NewPool(t)
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{Type: "postgres"}, pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	e := &execution.Engine{Pool: pool, Clock: fake, Store: store, Log: slog.New(slog.DiscardHandler)}

	ns, snap := uuid.New(), uuid.New()
	mustExec(t, pool, `INSERT INTO namespaces (id, name, source_type) VALUES ($1, 'ret', 'managed')`, ns)
	mustExec(t, pool, `INSERT INTO snapshots (id, namespace_id, version, manifest_hash) VALUES ($1, $2, 1, 'm')`, snap, ns)

	type seeded struct {
		exec uuid.UUID
		keys []string
	}
	seed := func(endedAgo time.Duration) seeded {
		ended := now.Add(-endedAgo)
		ex, tr := uuid.New(), uuid.New()
		mustExec(t, pool, `INSERT INTO executions (id, namespace_id, snapshot_id, state, trigger_type, created_at, ended_at)
			VALUES ($1, $2, $3, 'SUCCESS', 'manual', $4, $5)`, ex, ns, snap, ended.Add(-time.Minute), ended)
		mustExec(t, pool, `INSERT INTO task_runs (id, execution_id, task_key, state) VALUES ($1, $2, 't', 'SUCCESS')`, tr, ex)
		mustExec(t, pool, `INSERT INTO log_chunks (task_run_id, execution_id, seq, first_line, line_count, data) VALUES ($1, $2, 1, 0, 1, '\x00')`, tr, ex)
		mustExec(t, pool, `INSERT INTO metrics (execution_id, task_run_id, name, value, ts) VALUES ($1, $2, 'rows', 1, $3)`, ex, tr, ended)
		artKey := "artifacts/" + ex.String() + "/" + tr.String() + "/report"
		mustExec(t, pool, `INSERT INTO artifacts (id, execution_id, task_run_id, name, storage_key, size) VALUES ($1, $2, $3, 'report', $4, 2)`,
			uuid.New(), ex, tr, artKey)
		keys := []string{"logs/" + ex.String() + "/" + tr.String() + ".ndjson.gz", artKey}
		for _, k := range keys {
			if _, err := store.Put(ctx, k, strings.NewReader("{}"), "application/octet-stream"); err != nil {
				t.Fatal(err)
			}
		}
		return seeded{exec: ex, keys: keys}
	}
	old := seed(100 * 24 * time.Hour)
	recent := seed(10 * 24 * time.Hour)

	retention := 90 * 24 * time.Hour
	if n, err := e.DeleteExpired(ctx, retention); err != nil || n != 1 {
		t.Fatalf("first run deleted %d: %v, want 1", n, err)
	}
	checkRows(t, pool, old.exec, 0)
	checkRows(t, pool, recent.exec, 1)
	checkObjects(t, store, old.keys, false)
	checkObjects(t, store, recent.keys, true)

	fake.Advance(81 * 24 * time.Hour)
	if n, err := e.DeleteExpired(ctx, retention); err != nil || n != 1 {
		t.Fatalf("second run deleted %d: %v, want 1", n, err)
	}
	checkRows(t, pool, recent.exec, 0)
	checkObjects(t, store, recent.keys, false)
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// checkRows checks that each table holds want rows for the execution.
func checkRows(t *testing.T, pool *pgxpool.Pool, exec uuid.UUID, want int) {
	t.Helper()
	for _, table := range []string{"executions", "task_runs", "log_chunks", "metrics", "artifacts"} {
		col := "execution_id"
		if table == "executions" {
			col = "id"
		}
		var n int
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE "+col+" = $1", exec).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s rows of execution %s: %d, want %d", table, exec, n, want)
		}
	}
}

func checkObjects(t *testing.T, store storage.Store, keys []string, exist bool) {
	t.Helper()
	for _, k := range keys {
		_, err := store.Stat(context.Background(), k)
		if exist && err != nil {
			t.Errorf("object %s: %v, want it to exist", k, err)
		}
		if !exist && !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("object %s: %v, want not found", k, err)
		}
	}
}
