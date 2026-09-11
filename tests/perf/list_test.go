//go:build perf

package perf

import (
	"context"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

const (
	listExecutions = 100_000
	listTasksPer   = 4
	listWarmup     = 10
	listRequests   = 50
	listP95Limit   = 300 * time.Millisecond
)

// TestSCN_NFR_001_ExecutionList seeds 100 000 executions and 400 000 task runs and measures
// GET /api/v1/executions with the default filters and limit 50 over 50 requests after warmup.
// The p95 must be 300 ms or less (NFR-001).
func TestSCN_NFR_001_ExecutionList(t *testing.T) {
	dbURL := pgtest.Shared(t).NewDatabase(t)
	s := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL})
	c := adminClient(t, s)
	c.do(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": "perf"}, http.StatusCreated, nil)
	c.do(t, http.MethodPost, "/api/v1/namespaces/perf/changes", map[string]any{"message": "perf", "changes": []map[string]any{
		{"op": "put", "path": "list.flow.yaml", "content": "id: list\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"},
	}}, http.StatusCreated, nil)
	var ex struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	c.do(t, http.MethodPost, "/api/v1/flows/perf/list/executions", map[string]any{}, http.StatusCreated, &ex)
	for deadline := time.Now().Add(time.Minute); ex.State != "SUCCESS"; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("seed execution still %s after 1 minute", ex.State)
		}
		c.do(t, http.MethodGet, "/api/v1/executions/"+ex.ID, nil, http.StatusOK, &ex)
	}

	// Copy the real execution: the copies reference its namespace, flow, revision and snapshot.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	seedStart := time.Now()
	for _, st := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO executions (id, namespace_id, flow_id, flow_revision_id, snapshot_id, state, trigger_type, labels,
			created_at, started_at, ended_at, duration_ms)
		SELECT gen_random_uuid(), e.namespace_id, e.flow_id, e.flow_revision_id, e.snapshot_id,
			CASE WHEN g % 10 = 0 THEN 'FAILED' ELSE 'SUCCESS' END, 'manual', jsonb_build_object('batch', (g % 20)::text),
			now() - make_interval(secs => g * 30), now() - make_interval(secs => g * 30 - 1),
			now() - make_interval(secs => g * 30 - 5), 4000 + g % 1000
		FROM executions e, generate_series(1, $2::int) g WHERE e.id = $1::uuid`, []any{ex.ID, listExecutions}},
		{`INSERT INTO task_runs (id, execution_id, task_key, task_type, state, executor_type, started_at, ended_at, exit_code)
		SELECT gen_random_uuid(), e.id, 't' || k, 'command', CASE WHEN e.state = 'FAILED' AND k = $2::int THEN 'FAILED' ELSE 'SUCCESS' END,
			'process', e.started_at, e.ended_at, CASE WHEN e.state = 'FAILED' AND k = $2::int THEN 1 ELSE 0 END
		FROM executions e, generate_series(1, $2::int) k WHERE e.id <> $1::uuid`, []any{ex.ID, listTasksPer}},
		{`ANALYZE executions`, nil},
		{`ANALYZE task_runs`, nil},
	} {
		if _, err := conn.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	var executions, taskRuns int
	if err := conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM executions), (SELECT count(*) FROM task_runs)").Scan(&executions, &taskRuns); err != nil {
		t.Fatal(err)
	}
	if executions < listExecutions || taskRuns < listExecutions*listTasksPer {
		t.Fatalf("seeded %d executions and %d task runs", executions, taskRuns)
	}
	seedTime := time.Since(seedStart)

	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	for range listWarmup {
		c.do(t, http.MethodGet, "/api/v1/executions", nil, http.StatusOK, &page)
	}
	if len(page.Items) != 50 {
		t.Fatalf("the default page has %d items, want 50", len(page.Items))
	}
	durations := make([]time.Duration, 0, listRequests)
	for range listRequests {
		start := time.Now()
		c.do(t, http.MethodGet, "/api/v1/executions", nil, http.StatusOK, nil)
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(listRequests*95+99)/100-1]
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	writeReport(t, "nfr-001-execution-list", map[string]any{
		"scenario":        "SCN-NFR-001",
		"executions":      executions,
		"task_runs":       taskRuns,
		"seed_seconds":    seedTime.Seconds(),
		"requests":        listRequests,
		"warmup_requests": listWarmup,
		"p50_ms":          ms(durations[listRequests/2-1]),
		"p95_ms":          ms(p95),
		"max_ms":          ms(durations[listRequests-1]),
		"limit_ms":        ms(listP95Limit),
		"passed":          p95 <= listP95Limit,
	})
	if p95 > listP95Limit {
		t.Fatalf("p95 %v is above the limit %v", p95, listP95Limit)
	}
}
