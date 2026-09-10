//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func overlap(a, b taskRun) bool {
	return a.StartedAt.Before(*b.EndedAt) && b.StartedAt.Before(*a.EndedAt)
}

func dbQueryRow(t *testing.T, dbURL, sql string, args []any, dest ...any) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if err := conn.QueryRow(context.Background(), sql, args...).Scan(dest...); err != nil {
		t.Fatal(err)
	}
}

func dbExec(t *testing.T, dbURL, sql string, args ...any) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestSCN_EXE_001_PinnedSnapshot(t *testing.T) {
	dbURL := newDatabase(t)
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL})
	c := adminClient(t, p)
	saveFiles(t, c, "pin", map[string]string{
		"data.txt":      "v1",
		"run.sh":        "sleep 3\nv=$(cat data.txt)\necho \"data $v\"\necho \"{\\\"type\\\":\\\"output\\\",\\\"key\\\":\\\"data\\\",\\\"value\\\":\\\"$v\\\"}\" >> \"$SLUICE_OUTPUTS\"\n",
		"pin.flow.yaml": "id: pin\ntasks:\n  - id: t\n    type: script\n    file: run.sh\n",
	})
	d := triggerFlow(t, c, "pin", "pin", nil, nil)
	waitExec(t, c, d.ID, 30*time.Second, func(x execDetail) bool { return x.last("t").State == "RUNNING" })
	saveFiles(t, c, "pin", map[string]string{"data.txt": "v2", "pin.flow.yaml": "id: pin\ndescription: changed\ntasks:\n  - id: t\n    type: script\n    file: run.sh\n"})
	d = waitTerminal(t, c, d.ID, 60*time.Second)
	if d.State != "SUCCESS" || d.last("t").Outputs["data"] != "v1" {
		t.Fatalf("state %s outputs %v", d.State, d.last("t").Outputs)
	}
	var pinned, head string
	var bundles int
	dbQueryRow(t, dbURL, `SELECT s.manifest_hash, (SELECT h.manifest_hash FROM namespaces n JOIN snapshots h ON h.id = n.head_snapshot_id WHERE n.name = 'pin'),
		(SELECT count(*) FROM bundles b WHERE b.manifest_hash = s.manifest_hash) FROM executions e JOIN snapshots s ON s.id = e.snapshot_id WHERE e.id = $1`,
		[]any{d.ID}, &pinned, &head, &bundles)
	if pinned == head || bundles != 1 {
		t.Fatalf("pinned %s head %s bundles for pinned manifest %d", pinned, head, bundles)
	}
}

func TestSCN_EXE_003_ParallelAndMaxParallel(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	diamond := func(id, extra string) string {
		return "id: " + id + "\n" + extra + "tasks:\n" +
			"  - {id: a, type: command, command: [sleep, \"0.2\"]}\n" +
			"  - {id: b, type: command, command: [sleep, \"2\"], depends_on: [a]}\n" +
			"  - {id: c, type: command, command: [sleep, \"2\"], depends_on: [a]}\n" +
			"  - {id: d, type: command, command: [sleep, \"0.2\"], depends_on: [b, c]}\n"
	}
	saveFiles(t, c, "dag", map[string]string{"diamond.flow.yaml": diamond("diamond", ""), "serial.flow.yaml": diamond("serial", "max_parallel: 1\n")})
	d := waitTerminal(t, c, triggerFlow(t, c, "dag", "diamond", nil, nil).ID, 60*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("diamond %s", d.State)
	}
	b, cc, dd := d.last("b"), d.last("c"), d.last("d")
	if !overlap(b, cc) {
		t.Fatalf("b and c do not overlap: %v-%v %v-%v", b.StartedAt, b.EndedAt, cc.StartedAt, cc.EndedAt)
	}
	if dd.StartedAt.Before(*b.EndedAt) || dd.StartedAt.Before(*cc.EndedAt) {
		t.Fatal("d started before b and c ended")
	}
	s := waitTerminal(t, c, triggerFlow(t, c, "dag", "serial", nil, nil).ID, 60*time.Second)
	if s.State != "SUCCESS" {
		t.Fatalf("serial %s", s.State)
	}
	for i, x := range s.TaskRuns {
		for _, y := range s.TaskRuns[i+1:] {
			if overlap(x, y) {
				t.Fatalf("%s and %s overlap with max_parallel 1", x.TaskKey, y.TaskKey)
			}
		}
	}
}

func TestSCN_EXE_004_RunIf(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "runif", map[string]string{"f.flow.yaml": "id: f\ntasks:\n" +
		"  - {id: a, type: command, command: [\"false\"]}\n" +
		"  - {id: b, type: command, command: [\"true\"], depends_on: [a]}\n" +
		"  - {id: c, type: command, command: [\"true\"], depends_on: [a], run_if: failure}\n" +
		"  - {id: d, type: command, command: [\"true\"], depends_on: [a], run_if: always}\n"})
	d := waitTerminal(t, c, triggerFlow(t, c, "runif", "f", nil, nil).ID, 60*time.Second)
	if d.State != "FAILED" || d.last("a").State != "FAILED" {
		t.Fatalf("execution %s a %s", d.State, d.last("a").State)
	}
	if b := d.last("b"); b.State != "SKIPPED" || b.Reason != "upstream_failed" {
		t.Fatalf("b %s %s", b.State, b.Reason)
	}
	if d.last("c").State != "SUCCESS" || d.last("d").State != "SUCCESS" {
		t.Fatalf("c %s d %s", d.last("c").State, d.last("d").State)
	}
}

func TestSCN_EXE_005_RetriesWithBackoff(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "retry", map[string]string{
		"flaky.sh":    "echo attempt $SLUICE_ATTEMPT\nif [ \"$SLUICE_ATTEMPT\" -lt 3 ]; then exit 1; fi\necho \"{\\\"type\\\":\\\"output\\\",\\\"key\\\":\\\"attempt\\\",\\\"value\\\":$SLUICE_ATTEMPT}\" >> \"$SLUICE_OUTPUTS\"\n",
		"r.flow.yaml": "id: r\nretry: {max_attempts: 3, backoff: exponential, initial: 1s}\ntasks:\n  - {id: t, type: script, file: flaky.sh}\noutputs:\n  attempt: ${{ tasks.t.outputs.attempt }}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "retry", "r", nil, nil).ID, 60*time.Second)
	runs := d.runs("t")
	if d.State != "SUCCESS" || len(runs) != 3 {
		t.Fatalf("state %s attempts %d", d.State, len(runs))
	}
	g1 := runs[1].StartedAt.Sub(*runs[0].EndedAt)
	g2 := runs[2].StartedAt.Sub(*runs[1].EndedAt)
	if g1 < time.Second || g2 < 2*time.Second {
		t.Fatalf("gaps %s %s", g1, g2)
	}
	if v, _ := d.Outputs["attempt"].(float64); v != 3 {
		t.Fatalf("outputs %v", d.Outputs)
	}
}

func TestSCN_EXE_006_Timeouts(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "tmo", map[string]string{
		"sleep.sh":       "echo pid $$\nexec sleep 60\n",
		"task.flow.yaml": "id: task\ntasks:\n  - {id: t, type: script, file: sleep.sh, timeout: 2s}\n",
		"flow.flow.yaml": "id: flow\ntimeout: 3s\ntasks:\n  - {id: t, type: command, command: [sleep, \"60\"]}\n",
	})
	d := triggerFlow(t, c, "tmo", "task", nil, nil)
	d = waitExec(t, c, d.ID, 30*time.Second, func(x execDetail) bool { return x.last("t").StartedAt != nil })
	d = waitTerminal(t, c, d.ID, 30*time.Second)
	tr := d.last("t")
	if tr.State != "TIMED_OUT" || tr.EndedAt.Sub(*tr.StartedAt) > 5*time.Second {
		t.Fatalf("task %s after %s", tr.State, tr.EndedAt.Sub(*tr.StartedAt))
	}
	if pid := pidFromLogs(t, c, d.ID, "pid "); pid > 0 && processAlive(pid) {
		t.Fatalf("process %d still runs", pid)
	}
	f := waitTerminal(t, c, triggerFlow(t, c, "tmo", "flow", nil, nil).ID, 30*time.Second)
	if f.State != "TIMED_OUT" {
		t.Fatalf("flow timeout: %s", f.State)
	}
	if f.EndedAt.Sub(*f.StartedAt) > 10*time.Second {
		t.Fatalf("flow ended after %s", f.EndedAt.Sub(*f.StartedAt))
	}
}

func TestSCN_EXE_007_Concurrency(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "conc", map[string]string{
		"q.flow.yaml": "id: q\nconcurrency: {limit: 1, behavior: queue}\ntasks:\n  - {id: t, type: command, command: [sleep, \"1\"]}\n",
		"s.flow.yaml": "id: s\nconcurrency: {limit: 1, behavior: skip}\ntasks:\n  - {id: t, type: command, command: [sleep, \"2\"]}\n",
	})
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, triggerFlow(t, c, "conc", "q", nil, nil).ID)
	}
	var ex []execDetail
	for _, id := range ids {
		ex = append(ex, waitTerminal(t, c, id, 60*time.Second))
	}
	sort.Slice(ex, func(i, j int) bool { return ex[i].StartedAt.Before(*ex[j].StartedAt) })
	for i := 0; i < 3; i++ {
		if ex[i].State != "SUCCESS" {
			t.Fatalf("queued execution %d: %s", i, ex[i].State)
		}
		if i > 0 && ex[i].StartedAt.Before(*ex[i-1].EndedAt) {
			t.Fatalf("executions %d and %d overlap", i-1, i)
		}
	}
	first := triggerFlow(t, c, "conc", "s", nil, nil)
	second := triggerFlow(t, c, "conc", "s", nil, nil)
	if second.State != "SKIPPED" {
		t.Fatalf("second skip execution: %s", second.State)
	}
	if waitTerminal(t, c, first.ID, 60*time.Second).State != "SUCCESS" {
		t.Fatal("first skip execution failed")
	}
}

func TestSCN_EXE_013_FlowAndSubflowOutputs(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "sub.child", map[string]string{
		"emit.sh":         "echo '{\"type\":\"output\",\"key\":\"rows\",\"value\":42}' >> \"$SLUICE_OUTPUTS\"\n",
		"child.flow.yaml": "id: child\ninputs:\n  - {id: n, type: int, default: 1}\ntasks:\n  - {id: t, type: script, file: emit.sh}\noutputs:\n  rows: ${{ tasks.t.outputs.rows }}\n  n: ${{ inputs.n }}\n",
	})
	saveFiles(t, c, "sub", map[string]string{
		"parent.flow.yaml": "id: parent\ntasks:\n  - {id: c, type: subflow, flow: sub.child/child, inputs: {n: \"7\"}}\noutputs:\n  child_rows: ${{ tasks.c.outputs.rows }}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "sub", "parent", nil, nil).ID, 60*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("parent %s: %+v", d.State, d.TaskRuns)
	}
	if v, _ := d.Outputs["child_rows"].(float64); v != 42 {
		t.Fatalf("parent outputs %v", d.Outputs)
	}
	ct := d.last("c")
	if v, _ := ct.Outputs["rows"].(float64); v != 42 || ct.ChildExecutionID == nil {
		t.Fatalf("subflow task outputs %v child %v", ct.Outputs, ct.ChildExecutionID)
	}
	child := getExec(t, c, *ct.ChildExecutionID)
	if v, _ := child.Outputs["n"].(float64); v != 7 || child.TriggerType != "subflow" {
		t.Fatalf("child outputs %v trigger %s", child.Outputs, child.TriggerType)
	}
}

type execList struct {
	Items []struct {
		ID     string            `json:"id"`
		State  string            `json:"state"`
		Labels map[string]string `json:"labels"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
}

func TestSCN_EXE_015_LabelFilter(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "labels", map[string]string{"l.flow.yaml": "id: l\nlabels: {team: data}\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"})
	a := triggerFlow(t, c, "labels", "l", nil, map[string]string{"run": "nightly"})
	b := triggerFlow(t, c, "labels", "l", nil, map[string]string{"run": "adhoc"})
	if a.Labels["team"] != "data" || a.Labels["run"] != "nightly" {
		t.Fatalf("labels %v", a.Labels)
	}
	ids := func(q string) map[string]bool {
		var l execList
		c.do(t, http.MethodGet, "/api/v1/executions?"+q, nil, http.StatusOK, &l)
		out := map[string]bool{}
		for _, i := range l.Items {
			out[i.ID] = true
		}
		return out
	}
	byFlowLabel := ids("label=" + url.QueryEscape("team=data"))
	if !byFlowLabel[a.ID] || !byFlowLabel[b.ID] {
		t.Fatalf("flow label filter: %v", byFlowLabel)
	}
	byTrigger := ids("label=" + url.QueryEscape("team=data") + "&label=" + url.QueryEscape("run=nightly"))
	if !byTrigger[a.ID] || byTrigger[b.ID] {
		t.Fatalf("trigger label filter: %v", byTrigger)
	}
	if none := ids("label=" + url.QueryEscape("run=weekly")); len(none) != 0 {
		t.Fatalf("unmatched label: %v", none)
	}
}

func TestSCN_EXE_016_SubflowCancelAndDepth(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "tree", map[string]string{
		"child.flow.yaml":  "id: child\ntasks:\n  - {id: t, type: command, command: [sleep, \"60\"]}\n",
		"parent.flow.yaml": "id: parent\ntasks:\n  - {id: c, type: subflow, flow: tree/child}\n",
		"loop.flow.yaml":   "id: loop\ntasks:\n  - {id: again, type: subflow, flow: tree/loop}\n",
	})
	d := triggerFlow(t, c, "tree", "parent", nil, nil)
	d = waitExec(t, c, d.ID, 30*time.Second, func(x execDetail) bool {
		if x.last("c").ChildExecutionID == nil {
			return false
		}
		return getExec(t, c, *x.last("c").ChildExecutionID).last("t").State == "RUNNING"
	})
	childID := *d.last("c").ChildExecutionID
	c.do(t, http.MethodPost, "/api/v1/executions/"+d.ID+"/cancel", nil, http.StatusAccepted, nil)
	if s := waitTerminal(t, c, d.ID, 30*time.Second).State; s != "CANCELLED" {
		t.Fatalf("parent %s", s)
	}
	if s := waitTerminal(t, c, childID, 30*time.Second).State; s != "CANCELLED" {
		t.Fatalf("child %s", s)
	}
	root := waitTerminal(t, c, triggerFlow(t, c, "tree", "loop", nil, nil).ID, 120*time.Second)
	if root.State != "FAILED" {
		t.Fatalf("root loop %s", root.State)
	}
	id := root.ID
	for depth := 0; depth < 20; depth++ {
		x := getExec(t, c, id)
		tr := x.last("again")
		if tr.Reason == "depth_exceeded" {
			if x.ChainDepth != 10 {
				t.Fatalf("depth_exceeded at chain depth %d", x.ChainDepth)
			}
			return
		}
		if tr.ChildExecutionID == nil {
			t.Fatalf("depth %d: no child and reason %q", depth, tr.Reason)
		}
		id = *tr.ChildExecutionID
	}
	t.Fatal("depth_exceeded not found")
}

func TestSCN_EXE_017_HTTPTask(t *testing.T) {
	var mu sync.Mutex
	var gotAuth, gotBody, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotAuth, gotBody, gotQuery = r.Header.Get("X-Token"), string(b), r.URL.Query().Get("x")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "n": 3})
	}))
	defer srv.Close()
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "web", map[string]string{
		"ok.flow.yaml": "id: ok\ninputs:\n  - {id: x, type: string}\n  - {id: tok, type: string}\ntasks:\n" +
			"  - id: call\n    type: http\n    method: POST\n    url: \"" + srv.URL + "/hook?x=${{ inputs.x }}\"\n" +
			"    headers: {X-Token: \"${{ inputs.tok }}\"}\n    body: '{\"x\": \"${{ inputs.x }}\"}'\n",
		"bad.flow.yaml": "id: bad\ntasks:\n  - {id: call, type: http, url: \"" + srv.URL + "/\", expect_status: [201]}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "web", "ok", map[string]any{"x": "abc", "tok": "t-123"}, nil).ID, 30*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("http task %s: %+v", d.State, d.last("call"))
	}
	mu.Lock()
	if gotAuth != "t-123" || gotQuery != "abc" || !strings.Contains(gotBody, `"x": "abc"`) {
		t.Fatalf("request: header %q query %q body %q", gotAuth, gotQuery, gotBody)
	}
	mu.Unlock()
	out := d.last("call").Outputs
	body, _ := out["body"].(map[string]any)
	if s, _ := out["status"].(float64); s != 200 || body["n"] != float64(3) {
		t.Fatalf("outputs %v", out)
	}
	f := waitTerminal(t, c, triggerFlow(t, c, "web", "bad", nil, nil).ID, 30*time.Second)
	if f.State != "FAILED" || f.last("call").Reason != "http_status" {
		t.Fatalf("unexpected status: %s %s", f.State, f.last("call").Reason)
	}
}
