//go:build e2e

package e2e

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestSCN_EXE_010_ClaimsAcrossInstances runs 100 queued tasks on two instances with 4
// slots each. No task is claimed twice, at most 8 run at once, and tasks for another
// pool are never claimed (REQ-EXE-010).
func TestSCN_EXE_010_ClaimsAcrossInstances(t *testing.T) {
	dbURL := newDatabase(t)
	env := map[string]string{"SLUICE_DATABASE_URL": dbURL, "SLUICE_WORKER_SLOTS": "4"}
	a := startServer(t, env)
	b := startServer(t, env)
	c := adminClient(t, a)
	saveFiles(t, c, "claims", map[string]string{
		"work.flow.yaml":  "id: work\ntasks:\n  - {id: t, type: command, command: [\"sh\", \"-c\", \"echo claimed-once; sleep 0.3\"]}\n",
		"other.flow.yaml": "id: other\nexecutor: {pool: elsewhere}\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n",
	})
	for i := 0; i < 3; i++ {
		triggerFlow(t, c, "claims", "other", nil, nil)
	}
	var mu sync.Mutex
	var ids []string
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d := triggerFlow(t, c, "claims", "work", nil, nil)
			mu.Lock()
			ids = append(ids, d.ID)
			mu.Unlock()
		}()
	}
	wg.Wait()
	cb := tokenClient(b.URL, c.token)
	for _, id := range ids {
		d := waitTerminal(t, cb, id, 180*time.Second)
		if d.State != "SUCCESS" {
			t.Fatalf("execution %s ended %s: %s", id, d.State, d.Error)
		}
		if n := strings.Count(logText(allLogs(t, cb, id, "t")), "claimed-once"); n != 1 {
			t.Fatalf("execution %s ran its task %d times", id, n)
		}
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var runs, claimers int
	if err := conn.QueryRow(ctx, `SELECT count(*), count(DISTINCT t.claimed_by) FROM task_runs t
		JOIN executions e ON e.id = t.execution_id JOIN flows f ON f.id = e.flow_id WHERE f.flow_key = 'work'`).Scan(&runs, &claimers); err != nil {
		t.Fatal(err)
	}
	if runs != 100 || claimers != 2 {
		t.Fatalf("work task runs %d claimed by %d instances, want 100 runs and 2 instances", runs, claimers)
	}

	type edge struct {
		at    time.Time
		delta int
	}
	rows, err := conn.Query(ctx, `SELECT t.started_at, t.ended_at FROM task_runs t
		JOIN executions e ON e.id = t.execution_id JOIN flows f ON f.id = e.flow_id WHERE f.flow_key = 'work'`)
	if err != nil {
		t.Fatal(err)
	}
	var edges []edge
	for rows.Next() {
		var start, end time.Time
		if err := rows.Scan(&start, &end); err != nil {
			t.Fatal(err)
		}
		edges = append(edges, edge{start, 1}, edge{end, -1})
	}
	rows.Close()
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at.Equal(edges[j].at) {
			return edges[i].delta < edges[j].delta
		}
		return edges[i].at.Before(edges[j].at)
	})
	running, peak := 0, 0
	for _, e := range edges {
		running += e.delta
		peak = max(peak, running)
	}
	if peak > 8 {
		t.Fatalf("%d task runs ran at once, want at most 8", peak)
	}

	var otherClaimed, otherQueued int
	if err := conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE t.claimed_by IS NOT NULL), count(*) FILTER (WHERE t.state = 'QUEUED') FROM task_runs t
		JOIN executions e ON e.id = t.execution_id JOIN flows f ON f.id = e.flow_id WHERE f.flow_key = 'other'`).Scan(&otherClaimed, &otherQueued); err != nil {
		t.Fatal(err)
	}
	if otherClaimed != 0 || otherQueued != 3 {
		t.Fatalf("tasks for pool elsewhere: %d claimed, %d queued, want 0 and 3", otherClaimed, otherQueued)
	}
}
