//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestSCN_TRG_001_ManualTrigger(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "manual", map[string]string{"m.flow.yaml": "id: m\ninputs:\n  - {id: count, type: int, required: true}\n  - {id: mode, type: select, values: [a, b], default: a}\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"})
	r := c.raw(t, http.MethodPost, "/api/v1/flows/manual/m/executions", map[string]any{"inputs": map[string]any{"count": 3}, "labels": map[string]string{"who": "me"}}, nil)
	if r.Status != http.StatusCreated {
		t.Fatalf("trigger: %d %s", r.Status, r.Body)
	}
	var d execDetail
	_ = json.Unmarshal(r.Body, &d)
	d = getExec(t, c, d.ID)
	if d.Inputs["count"] != float64(3) || d.Inputs["mode"] != "a" || d.Labels["who"] != "me" || d.TriggerType != "manual" {
		t.Fatalf("execution: inputs %v labels %v trigger %s", d.Inputs, d.Labels, d.TriggerType)
	}
	bad := c.raw(t, http.MethodPost, "/api/v1/flows/manual/m/executions", map[string]any{"inputs": map[string]any{"count": "x"}}, nil)
	if bad.Status != http.StatusUnprocessableEntity || errCode(bad.Body) != "validation_failed" {
		t.Fatalf("bad input: %d %s", bad.Status, bad.Body)
	}
}

func TestSCN_AUTH_005_TokenRoles(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	admin := adminClient(t, p)
	saveFiles(t, admin, "roles", map[string]string{"r.flow.yaml": "id: r\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"})
	_, op := createUser(t, admin, "op5@example.com", "operator", "operator-pass-1")
	if r := op.raw(t, http.MethodPost, "/api/v1/tokens", map[string]any{"name": "x", "role": "editor"}, nil); r.Status != http.StatusForbidden {
		t.Fatalf("operator requests editor token: %d", r.Status)
	}
	_, vs := createUser(t, admin, "viewer5@example.com", "viewer", "viewer-pass-1")
	secret, tokID := createToken(t, vs, "viewer")
	viewer := tokenClient(p.URL, secret)
	viewer.do(t, http.MethodGet, "/api/v1/executions", nil, http.StatusOK, nil)
	if r := viewer.raw(t, http.MethodPost, "/api/v1/flows/roles/r/executions", map[string]any{}, nil); r.Status != http.StatusForbidden {
		t.Fatalf("viewer trigger: %d", r.Status)
	}
	var list struct {
		Items []struct {
			ID         string     `json:"id"`
			LastUsedAt *time.Time `json:"last_used_at"`
		} `json:"items"`
	}
	vs.do(t, http.MethodGet, "/api/v1/tokens", nil, http.StatusOK, &list)
	if len(list.Items) != 1 || list.Items[0].LastUsedAt == nil {
		t.Fatalf("last_used_at not updated: %+v", list.Items)
	}
	vs.do(t, http.MethodDelete, "/api/v1/tokens/"+tokID, nil, http.StatusNoContent, nil)
	if r := viewer.raw(t, http.MethodGet, "/api/v1/executions", nil, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", r.Status)
	}
}

func TestSCN_NS_006_NamespaceDefaults(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_POOLS": "default,nspool"})
	c := adminClient(t, p)
	saveFiles(t, c, "defs", map[string]string{
		"namespace.yaml":  "description: defaults\ndefaults:\n  executor: {type: process, pool: nspool}\n  env: {GREETING: from-ns, OTHER: ns-other}\n",
		"show.sh":         "echo \"greeting=$GREETING other=$OTHER\"\n",
		"plain.flow.yaml": "id: plain\ntasks:\n  - {id: t, type: script, file: show.sh}\n",
		"own.flow.yaml":   "id: own\nenv: {GREETING: from-flow}\nexecutor: {pool: default}\ntasks:\n  - {id: t, type: script, file: show.sh}\n",
	})
	a := waitTerminal(t, c, triggerFlow(t, c, "defs", "plain", nil, nil).ID, 60*time.Second)
	b := waitTerminal(t, c, triggerFlow(t, c, "defs", "own", nil, nil).ID, 60*time.Second)
	if a.State != "SUCCESS" || b.State != "SUCCESS" {
		t.Fatalf("states %s %s", a.State, b.State)
	}
	if a.last("t").Pool != "nspool" || a.last("t").ExecutorType != "process" || b.last("t").Pool != "default" {
		t.Fatalf("pools %s %s", a.last("t").Pool, b.last("t").Pool)
	}
	if !strings.Contains(logText(allLogs(t, c, a.ID, "t")), "greeting=from-ns other=ns-other") {
		t.Fatalf("defaults: %s", logText(allLogs(t, c, a.ID, "t")))
	}
	if !strings.Contains(logText(allLogs(t, c, b.ID, "t")), "greeting=from-flow other=ns-other") {
		t.Fatalf("override: %s", logText(allLogs(t, c, b.ID, "t")))
	}
}

func TestSCN_NS_008_DeleteWithRunningExecution(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "gone", map[string]string{"g.flow.yaml": "id: g\ntasks:\n  - {id: t, type: command, command: [sleep, \"3\"]}\n"})
	d := triggerFlow(t, c, "gone", "g", nil, nil)
	waitExec(t, c, d.ID, 30*time.Second, func(x execDetail) bool { return x.State == "RUNNING" })
	if r := c.raw(t, http.MethodDelete, "/api/v1/namespaces/gone", nil, nil); r.Status != http.StatusConflict || errCode(r.Body) != "executions_running" {
		t.Fatalf("delete while running: %d %s", r.Status, r.Body)
	}
	waitTerminal(t, c, d.ID, 60*time.Second)
	c.do(t, http.MethodDelete, "/api/v1/namespaces/gone", nil, http.StatusNoContent, nil)
	if x := getExec(t, c, d.ID); x.State != "SUCCESS" {
		t.Fatalf("execution after delete: %s", x.State)
	}
	var l execList
	c.do(t, http.MethodGet, "/api/v1/executions?namespace=gone", nil, http.StatusOK, &l)
	if len(l.Items) != 1 {
		t.Fatalf("history: %d", len(l.Items))
	}
}

func TestSCN_EXR_002_CancelProcessGroup(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "grp", map[string]string{
		"spawn.sh":    "sleep 300 &\necho \"grandchild $!\"\necho \"child $$\"\nwait\n",
		"g.flow.yaml": "id: g\ntasks:\n  - {id: t, type: script, file: spawn.sh}\n",
	})
	d := triggerFlow(t, c, "grp", "g", nil, nil)
	var child, grand int
	deadline := time.Now().Add(30 * time.Second)
	for child == 0 || grand == 0 {
		child, grand = pidFromLogs(t, c, d.ID, "child "), pidFromLogs(t, c, d.ID, "grandchild ")
		if time.Now().After(deadline) {
			t.Fatal("pids not logged")
		}
		time.Sleep(200 * time.Millisecond)
	}
	c.do(t, http.MethodPost, "/api/v1/executions/"+d.ID+"/cancel", nil, http.StatusAccepted, nil)
	waitTerminal(t, c, d.ID, 30*time.Second)
	deadline = time.Now().Add(15 * time.Second)
	for processAlive(child) || processAlive(grand) {
		if time.Now().After(deadline) {
			t.Fatalf("child alive %v grandchild alive %v", processAlive(child), processAlive(grand))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestSCN_EXR_008_NoInstanceForPool(t *testing.T) {
	dbURL := newDatabase(t)
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL})
	c := adminClient(t, p)
	saveFiles(t, c, "gpu", map[string]string{"g.flow.yaml": "id: g\nexecutor: {pool: gpu}\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"})
	d := triggerFlow(t, c, "gpu", "g", nil, nil)
	waitExec(t, c, d.ID, 30*time.Second, func(x execDetail) bool {
		tr := x.last("t")
		return tr.State == "QUEUED" && tr.Reason == "no_instance_for_pool"
	})
	startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL, "SLUICE_POOLS": "gpu"})
	if x := waitTerminal(t, c, d.ID, 60*time.Second); x.State != "SUCCESS" || x.last("t").Pool != "gpu" {
		t.Fatalf("after gpu instance: %s", x.State)
	}
}

func TestSCN_EXE_011_LostRunner(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_HEARTBEAT_TIMEOUT": "5s"})
	c := adminClient(t, p)
	saveFiles(t, c, "lost", map[string]string{
		"once.sh":     "if [ \"$SLUICE_ATTEMPT\" = 1 ]; then echo \"runner $PPID\"; echo \"self $$\"; sleep 120; fi\necho done\n",
		"l.flow.yaml": "id: l\nretry: {max_attempts: 2, initial: 1s}\ntasks:\n  - {id: t, type: script, file: once.sh}\n",
	})
	d := triggerFlow(t, c, "lost", "l", nil, nil)
	var runner, self int
	deadline := time.Now().Add(30 * time.Second)
	for runner == 0 || self == 0 {
		runner, self = pidFromLogs(t, c, d.ID, "runner "), pidFromLogs(t, c, d.ID, "self ")
		if time.Now().After(deadline) {
			t.Fatal("runner pid not logged")
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Cleanup(func() { _ = syscall.Kill(self, syscall.SIGKILL) })
	if err := syscall.Kill(runner, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	d = waitTerminal(t, c, d.ID, 90*time.Second)
	runs := d.runs("t")
	if d.State != "SUCCESS" || len(runs) != 2 || runs[0].State != "FAILED" || runs[0].Reason != "lost" {
		t.Fatalf("state %s runs %+v", d.State, runs)
	}
}

func TestSCN_CORE_007_GracefulShutdown(t *testing.T) {
	dbURL := newDatabase(t)
	a := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL, "SLUICE_SHUTDOWN_GRACE": "10s"})
	c := adminClient(t, a)
	saveFiles(t, c, "sd", map[string]string{
		"work.sh":     "if [ \"$SLUICE_ATTEMPT\" = 1 ]; then sleep 60; fi\necho finished\n",
		"w.flow.yaml": "id: w\nretry: {max_attempts: 2, initial: 1s}\ntasks:\n  - {id: t, type: script, file: work.sh}\n",
	})
	d := triggerFlow(t, c, "sd", "w", nil, nil)
	waitExec(t, c, d.ID, 30*time.Second, func(x execDetail) bool { return x.last("t").State == "RUNNING" })
	b := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL})
	cb := tokenClient(b.URL, c.token)
	start := time.Now()
	if err := a.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if _, exited := a.WaitExit(15 * time.Second); !exited {
		t.Fatal("instance A did not exit within the grace period")
	}
	if took := time.Since(start); took > 11*time.Second {
		t.Fatalf("exit after %s", took)
	}
	d = waitTerminal(t, cb, d.ID, 60*time.Second)
	runs := d.runs("t")
	if d.State != "SUCCESS" || len(runs) != 2 || runs[0].Reason != "instance_shutdown" || runs[0].State != "FAILED" {
		t.Fatalf("state %s runs %+v", d.State, runs)
	}
	var inst struct {
		Items []struct {
			ID     string `json:"id"`
			Online bool   `json:"online"`
		} `json:"items"`
	}
	cb.do(t, http.MethodGet, "/api/v1/instances", nil, http.StatusOK, &inst)
	if len(inst.Items) != 2 {
		t.Fatalf("instances %+v", inst.Items)
	}
}

func TestSCN_API_003_StablePagination(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "pages", map[string]string{"p.flow.yaml": "id: p\nexecutor: {pool: nowhere}\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"})
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := 0; i < 450; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			triggerFlow(t, c, "pages", "p", nil, nil)
		}()
	}
	wg.Wait()
	seen := map[string]int{}
	cursor := ""
	pages := 0
	for {
		q := "/api/v1/executions?namespace=pages&limit=200"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		var l execList
		c.do(t, http.MethodGet, q, nil, http.StatusOK, &l)
		pages++
		for _, i := range l.Items {
			seen[i.ID]++
		}
		for i := 0; i < 5; i++ {
			triggerFlow(t, c, "pages", "p", nil, nil)
		}
		if l.NextCursor == "" {
			break
		}
		cursor = l.NextCursor
	}
	if pages != 3 || len(seen) != 450 {
		t.Fatalf("pages %d rows %d", pages, len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s seen %d times", id, n)
		}
	}
}

// readSSE reads up to max line events and returns their data and the last event id.
func readSSE(t *testing.T, base, path, token, lastID string, max int) ([]logEntry, string, bool) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sse status %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var out []logEntry
	id, event := lastID, ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
			if event == "end" {
				return out, id, true
			}
		case strings.HasPrefix(line, "data: ") && event == "line":
			var l logEntry
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &l); err != nil {
				t.Fatal(err)
			}
			out = append(out, l)
			if len(out) >= max {
				// Read the id line of this event already happened; stop here.
				return out, id, false
			}
		}
	}
	return out, id, false
}

func TestSCN_API_004_LogStreamResume(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "sse", map[string]string{
		"slow.sh":     "i=1\nwhile [ $i -le 200 ]; do echo \"line $i\"; i=$((i + 1)); sleep 0.02; done\n",
		"s.flow.yaml": "id: s\ntasks:\n  - {id: t, type: script, file: slow.sh}\n",
	})
	d := triggerFlow(t, c, "sse", "s", nil, nil)
	path := "/api/v1/executions/" + d.ID + "/logs/stream"
	first, lastID, ended := readSSE(t, p.URL, path, c.token, "", 50)
	if ended || len(first) != 50 {
		t.Fatalf("first read: %d lines, ended %v", len(first), ended)
	}
	rest, _, ended := readSSE(t, p.URL, path, c.token, lastID, 1_000_000)
	if !ended {
		t.Fatal("stream did not end")
	}
	got := append(first, rest...)
	seen := map[string]bool{}
	var stdout []string
	for _, l := range got {
		k := fmt.Sprintf("%s:%d", l.TaskRunID, l.N)
		if seen[k] {
			t.Fatalf("duplicate line %s", k)
		}
		seen[k] = true
		if l.Stream == "stdout" {
			stdout = append(stdout, l.Text)
		}
	}
	all := allLogs(t, c, d.ID, "")
	if len(got) != len(all) {
		t.Fatalf("stream %d lines, stored %d", len(got), len(all))
	}
	if len(stdout) != 200 || stdout[0] != "line 1" || stdout[199] != "line 200" {
		t.Fatalf("stdout %d lines", len(stdout))
	}
}
