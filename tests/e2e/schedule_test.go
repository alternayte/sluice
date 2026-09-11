//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The tests in this file wait for real minute boundaries of `* * * * *` schedules. They
// run in parallel to share the wait.

const everyMinute = "triggers:\n  - {id: m, type: schedule, cron: \"* * * * *\"}\n"

// waitScheduled waits until a flow has a schedule execution and returns the list.
func waitScheduled(t testing.TB, c *client, ns, flowID string) execList {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(time.Second) {
		if l := flowExecs(t, c, ns, flowID, "schedule"); len(l.Items) > 0 {
			return l
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s/%s: no schedule execution within 90 s", ns, flowID)
		}
	}
}

// TestSCN_FLOW_003_InvalidFlow saves an invalid flow. It is listed as invalid with errors,
// a manual trigger returns 422 flow_invalid and its schedule does not fire (REQ-FLOW-004).
func TestSCN_FLOW_003_InvalidFlow(t *testing.T) {
	t.Parallel()
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "inval", map[string]string{
		"bad.flow.yaml":  "id: bad\n" + everyMinute + "tasks:\n  - {id: t, type: script, file: missing.py}\n",
		"good.flow.yaml": "id: good\n" + everyMinute + "tasks:\n  - {id: t, type: command, command: [\"true\"]}\n",
	})
	var f struct {
		Valid      bool `json:"valid"`
		ErrorCount int  `json:"error_count"`
		Revision   struct {
			Errors []map[string]any `json:"errors"`
		} `json:"revision"`
	}
	c.do(t, http.MethodGet, "/api/v1/flows/inval/bad", nil, http.StatusOK, &f)
	if f.Valid || f.ErrorCount == 0 || len(f.Revision.Errors) == 0 {
		t.Fatalf("invalid flow: valid %v, %d errors", f.Valid, f.ErrorCount)
	}
	var list struct {
		Items []struct {
			FlowID string `json:"flow_id"`
			Valid  bool   `json:"valid"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/flows?namespace=inval", nil, http.StatusOK, &list)
	for _, it := range list.Items {
		if it.FlowID == "bad" && it.Valid {
			t.Fatal("the flow list shows the invalid flow as valid")
		}
	}
	r := c.raw(t, http.MethodPost, "/api/v1/flows/inval/bad/executions", map[string]any{}, nil)
	if r.Status != http.StatusUnprocessableEntity || errCode(r.Body) != "flow_invalid" {
		t.Fatalf("manual trigger: %d %s", r.Status, r.Body)
	}
	waitScheduled(t, c, "inval", "good")
	if n := len(flowExecs(t, c, "inval", "bad", "").Items); n != 0 {
		t.Fatalf("the invalid flow has %d executions, want 0", n)
	}
}

// TestSCN_FLOW_005_DisabledFlow disables a flow. Its schedule does not fire, its webhook
// returns 409 flow_disabled and a manual trigger works (REQ-FLOW-006).
func TestSCN_FLOW_005_DisabledFlow(t *testing.T) {
	t.Parallel()
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	task := "tasks:\n  - {id: t, type: command, command: [\"true\"]}\n"
	saveFiles(t, c, "dis", map[string]string{
		"off.flow.yaml": "id: off\ntriggers:\n  - {id: m, type: schedule, cron: \"* * * * *\"}\n  - {id: in, type: webhook}\n" + task,
		"on.flow.yaml":  "id: on\n" + everyMinute + task,
	})
	k := rotateKey(t, c, "dis", "off", "in")
	c.do(t, http.MethodPatch, "/api/v1/flows/dis/off", map[string]any{"disabled": true}, http.StatusOK, nil)
	if code, b := postHook(t, k.URL, []byte(`{}`), nil); code != http.StatusConflict || errCode(b) != "flow_disabled" {
		t.Fatalf("webhook of a disabled flow: %d %s", code, b)
	}
	d := triggerFlow(t, c, "dis", "off", nil, nil)
	if d = waitTerminal(t, c, d.ID, 60*time.Second); d.State != "SUCCESS" {
		t.Fatalf("manual trigger of a disabled flow: %s", d.State)
	}
	waitScheduled(t, c, "dis", "on")
	if n := len(flowExecs(t, c, "dis", "off", "schedule").Items); n != 0 {
		t.Fatalf("the disabled flow has %d schedule executions, want 0", n)
	}
}

// TestSCN_DEP_004_TwoPools runs instances with pools cluster-a and cluster-b on one
// database. Tasks run only on their pool, schedules fire once and both instances list all
// executions (REQ-DEP-004).
func TestSCN_DEP_004_TwoPools(t *testing.T) {
	t.Parallel()
	dbURL := newDatabase(t)
	a := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL, "SLUICE_POOLS": "cluster-a"})
	b := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL, "SLUICE_POOLS": "cluster-b"})
	ca := adminClient(t, a)
	cb := tokenClient(b.URL, ca.token)
	task := "tasks:\n  - {id: t, type: command, command: [\"true\"]}\n"
	saveFiles(t, ca, "dep", map[string]string{
		"fa.flow.yaml":   "id: fa\nexecutor: {pool: cluster-a}\n" + task,
		"fb.flow.yaml":   "id: fb\nexecutor: {pool: cluster-b}\n" + task,
		"tick.flow.yaml": "id: tick\nexecutor: {pool: cluster-b}\n" + everyMinute + task,
	})
	var inst struct {
		Items []struct {
			ID    string   `json:"id"`
			Pools []string `json:"pools"`
		} `json:"items"`
	}
	ca.do(t, http.MethodGet, "/api/v1/instances", nil, http.StatusOK, &inst)
	byPool := map[string]string{}
	for _, it := range inst.Items {
		for _, p := range it.Pools {
			byPool[p] = it.ID
		}
	}
	if byPool["cluster-a"] == "" || byPool["cluster-b"] == "" {
		t.Fatalf("instances %+v", inst.Items)
	}
	want := map[string]string{} // execution ID -> pool
	for i := 0; i < 3; i++ {
		want[triggerFlow(t, ca, "dep", "fa", nil, nil).ID] = "cluster-a"
		want[triggerFlow(t, cb, "dep", "fb", nil, nil).ID] = "cluster-b"
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for id, pool := range want {
		if d := waitTerminal(t, ca, id, 60*time.Second); d.State != "SUCCESS" {
			t.Fatalf("execution %s: %s", id, d.State)
		}
		var claimer string
		if err := conn.QueryRow(ctx, "SELECT claimed_by::text FROM task_runs WHERE execution_id = $1", id).Scan(&claimer); err != nil {
			t.Fatal(err)
		}
		if claimer != byPool[pool] {
			t.Fatalf("task of pool %s ran on instance %s, want %s", pool, claimer, byPool[pool])
		}
	}
	for _, c := range []*client{ca, cb} {
		var l execList
		c.do(t, http.MethodGet, "/api/v1/executions?namespace=dep&limit=200", nil, http.StatusOK, &l)
		seen := map[string]bool{}
		for _, it := range l.Items {
			seen[it.ID] = true
		}
		for id := range want {
			if !seen[id] {
				t.Fatalf("instance %s does not list execution %s", c.base, id)
			}
		}
	}
	waitScheduled(t, ca, "dep", "tick")
	time.Sleep(3 * time.Second)
	var dup int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM (SELECT scheduled_for FROM executions WHERE trigger_type = 'schedule'
		GROUP BY scheduled_for HAVING count(*) > 1) d`).Scan(&dup); err != nil {
		t.Fatal(err)
	}
	if dup != 0 {
		t.Fatalf("%d schedule times fired more than once", dup)
	}
}
