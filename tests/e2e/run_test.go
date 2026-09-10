//go:build e2e

package e2e

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pidFromLogs finds the first number after prefix in the task logs.
func pidFromLogs(t *testing.T, c *client, execID, prefix string) int {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(prefix) + `(\d+)`)
	for _, l := range allLogs(t, c, execID, "") {
		if m := re.FindStringSubmatch(l.Text); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func TestSCN_RUN_001_InterleavedLines(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "lines", map[string]string{
		"many.sh":     "i=1\nwhile [ $i -le 10000 ]; do\n  if [ $((i % 2)) -eq 0 ]; then echo \"out $i\"; else echo \"err $i\" >&2; fi\n  i=$((i + 1))\ndone\n",
		"l.flow.yaml": "id: l\ntasks:\n  - {id: t, type: script, file: many.sh}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "lines", "l", nil, nil).ID, 120*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("state %s", d.State)
	}
	lines := allLogs(t, c, d.ID, "t")
	last := map[string]int{}
	count := map[string]int{}
	ns := map[int64]bool{}
	var maxN int64
	for _, l := range lines {
		ns[l.N] = true
		if l.N > maxN {
			maxN = l.N
		}
		if l.Stream == "system" {
			continue
		}
		f := strings.Fields(l.Text)
		if len(f) != 2 {
			t.Fatalf("line %q", l.Text)
		}
		want := map[string]string{"stdout": "out", "stderr": "err"}[l.Stream]
		if f[0] != want {
			t.Fatalf("line %q tagged %s", l.Text, l.Stream)
		}
		n, _ := strconv.Atoi(f[1])
		if n <= last[l.Stream] {
			t.Fatalf("%s out of order: %d after %d", l.Stream, n, last[l.Stream])
		}
		last[l.Stream] = n
		count[l.Stream]++
	}
	if count["stdout"] != 5000 || count["stderr"] != 5000 {
		t.Fatalf("counts %v", count)
	}
	for n := int64(1); n <= maxN; n++ {
		if !ns[n] {
			t.Fatalf("line number %d missing", n)
		}
	}
}

func TestSCN_RUN_002_LongLineTruncated(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "long", map[string]string{
		"long.sh":     "head -c 102400 /dev/zero | tr '\\0' 'x'\necho\necho after\n",
		"l.flow.yaml": "id: l\ntasks:\n  - {id: t, type: script, file: long.sh}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "long", "l", nil, nil).ID, 60*time.Second)
	for _, l := range allLogs(t, c, d.ID, "t") {
		if strings.HasPrefix(l.Text, "xxxx") {
			if len(l.Text) > 16*1024 || !strings.HasSuffix(l.Text, "…[truncated]") {
				t.Fatalf("long line: %d bytes, suffix %q", len(l.Text), l.Text[len(l.Text)-20:])
			}
			return
		}
	}
	t.Fatal("long line not found")
}

func TestSCN_RUN_003_OutputsMetricsArtifacts(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "emit", map[string]string{
		"emit.sh": "echo '<h1>report</h1>' > report.html\n" +
			"echo '{\"type\":\"output\",\"key\":\"rows\",\"value\":1234}' >> \"$SLUICE_OUTPUTS\"\n" +
			"echo '{\"type\":\"metric\",\"name\":\"rows_loaded\",\"value\":1234,\"unit\":\"rows\",\"tags\":{\"table\":\"orders\"}}' >> \"$SLUICE_OUTPUTS\"\n" +
			"echo '{\"type\":\"artifact\",\"path\":\"report.html\",\"name\":\"report\",\"content_type\":\"text/html\"}' >> \"$SLUICE_OUTPUTS\"\n" +
			"echo 'this is not json' >> \"$SLUICE_OUTPUTS\"\n",
		"e.flow.yaml": "id: e\ntasks:\n  - {id: t, type: script, file: emit.sh}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "emit", "e", nil, nil).ID, 60*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("state %s", d.State)
	}
	if v, _ := d.last("t").Outputs["rows"].(float64); v != 1234 {
		t.Fatalf("outputs %v", d.last("t").Outputs)
	}
	var metrics struct {
		Items []struct {
			Name  string            `json:"name"`
			Value float64           `json:"value"`
			Unit  string            `json:"unit"`
			Tags  map[string]string `json:"tags"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/executions/"+d.ID+"/metrics", nil, http.StatusOK, &metrics)
	if len(metrics.Items) != 1 || metrics.Items[0].Name != "rows_loaded" || metrics.Items[0].Tags["table"] != "orders" || metrics.Items[0].Unit != "rows" {
		t.Fatalf("metrics %+v", metrics.Items)
	}
	var arts struct {
		Items []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			ContentType string `json:"content_type"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/executions/"+d.ID+"/artifacts", nil, http.StatusOK, &arts)
	if len(arts.Items) != 1 || arts.Items[0].Name != "report" || arts.Items[0].ContentType != "text/html" {
		t.Fatalf("artifacts %+v", arts.Items)
	}
	r := c.raw(t, http.MethodGet, "/api/v1/executions/"+d.ID+"/artifacts/"+arts.Items[0].ID, nil, nil)
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), "<h1>report</h1>") {
		t.Fatalf("artifact download %d %q", r.Status, r.Body)
	}
	if !strings.Contains(logText(allLogs(t, c, d.ID, "t")), "warning: outputs line 4") {
		t.Fatalf("no warning line: %s", logText(allLogs(t, c, d.ID, "t")))
	}
}

func TestSCN_RUN_004_CancelSignals(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "sig", map[string]string{
		"trap.sh":          "trap 'echo trapped; exit 0' TERM\necho ready\nwhile true; do sleep 0.2; done\n",
		"ignore.sh":        "trap '' TERM\necho ready\nwhile true; do sleep 1; done\n",
		"trap.flow.yaml":   "id: trap\ntasks:\n  - {id: t, type: script, file: trap.sh}\n",
		"ignore.flow.yaml": "id: ignore\ntasks:\n  - {id: t, type: script, file: ignore.sh}\n",
	})
	ready := func(id string) {
		deadline := time.Now().Add(30 * time.Second)
		for !strings.Contains(logText(allLogs(t, c, id, "t")), "ready") {
			if time.Now().After(deadline) {
				t.Fatal("task not ready")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	d := triggerFlow(t, c, "sig", "trap", nil, nil)
	ready(d.ID)
	c.do(t, http.MethodPost, "/api/v1/executions/"+d.ID+"/cancel", nil, http.StatusAccepted, nil)
	d = waitTerminal(t, c, d.ID, 30*time.Second)
	if d.State != "CANCELLED" || !strings.Contains(logText(allLogs(t, c, d.ID, "t")), "stdout: trapped") {
		t.Fatalf("trap: %s\n%s", d.State, logText(allLogs(t, c, d.ID, "t")))
	}
	g := triggerFlow(t, c, "sig", "ignore", nil, nil)
	ready(g.ID)
	start := time.Now()
	c.do(t, http.MethodPost, "/api/v1/executions/"+g.ID+"/cancel", nil, http.StatusAccepted, nil)
	g = waitTerminal(t, c, g.ID, 60*time.Second)
	took := time.Since(start)
	if g.State != "CANCELLED" || took < 10*time.Second || took > 30*time.Second {
		t.Fatalf("ignore: %s after %s", g.State, took)
	}
}

func TestSCN_RUN_009_TaskEnvironment(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "envns", map[string]string{
		"env.sh":      "env | sort\ntest -f \"$SLUICE_OUTPUTS\" && echo outputs-file-exists\ntest -d \"$SLUICE_WORKDIR\" && echo workdir-exists\n",
		"e.flow.yaml": "id: e\nenv: {FLOW_VAR: \"from-flow\"}\ntasks:\n  - {id: show, type: script, file: env.sh}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "envns", "e", nil, nil).ID, 60*time.Second)
	text := logText(allLogs(t, c, d.ID, "show"))
	for _, want := range []string{
		"SLUICE_EXECUTION_ID=" + d.ID, "SLUICE_TASK_ID=show", "SLUICE_ATTEMPT=1", "SLUICE_NAMESPACE=envns", "SLUICE_FLOW_ID=e",
		"SLUICE_OUTPUTS=", "SLUICE_WORKDIR=", "FLOW_VAR=from-flow", "outputs-file-exists", "workdir-exists",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("env misses %s", want)
		}
	}
	if strings.Contains(text, "SLUICE_RUN_TOKEN") || strings.Contains(text, "SLUICE_DATABASE_URL") {
		t.Fatalf("task sees server credentials:\n%s", text)
	}
}

func TestSCN_RUN_010_RunTokens(t *testing.T) {
	dbURL := newDatabase(t)
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL})
	c := adminClient(t, p)
	saveFiles(t, c, "tok", map[string]string{
		"a.sh":        "ps eww -o command= -p $PPID | tr ' ' '\\n' | grep -E '^SLUICE_(RUN_TOKEN|TASK_RUN_ID)='\nsleep 6\n",
		"b.sh":        "sleep 6\n",
		"t.flow.yaml": "id: t\ntasks:\n  - {id: a, type: script, file: a.sh}\n  - {id: b, type: script, file: b.sh}\n",
	})
	d := triggerFlow(t, c, "tok", "t", nil, nil)
	var token, idA string
	deadline := time.Now().Add(30 * time.Second)
	for token == "" || idA == "" {
		for _, l := range allLogs(t, c, d.ID, "a") {
			if v, ok := strings.CutPrefix(l.Text, "SLUICE_RUN_TOKEN="); ok {
				token = v
			}
			if v, ok := strings.CutPrefix(l.Text, "SLUICE_TASK_RUN_ID="); ok {
				idA = v
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no token in logs: %s", logText(allLogs(t, c, d.ID, "a")))
		}
		time.Sleep(200 * time.Millisecond)
	}
	d = waitExec(t, c, d.ID, 30*time.Second, func(x execDetail) bool { return x.last("b").State == "RUNNING" })
	idB := d.last("b").ID
	hb := func(taskRunID, tok string) int {
		req, _ := http.NewRequest(http.MethodPost, p.URL+"/api/runner/v1/task-runs/"+taskRunID+"/heartbeat", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if s := hb(idA, token); s != http.StatusOK {
		t.Fatalf("own task: %d", s)
	}
	if s := hb(idB, token); s != http.StatusForbidden {
		t.Fatalf("token of A on B: %d", s)
	}
	waitExec(t, c, d.ID, 30*time.Second, func(x execDetail) bool { return x.last("a").State == "SUCCESS" })
	if s := hb(idA, token); s != http.StatusUnauthorized {
		t.Fatalf("token after completion: %d", s)
	}
	// Expiry: a run token is valid until the task timeout plus 10 minutes (SI-04).
	e := triggerFlow(t, c, "tok", "t", nil, nil)
	var tok2, id2 string
	deadline = time.Now().Add(30 * time.Second)
	for tok2 == "" || id2 == "" {
		for _, l := range allLogs(t, c, e.ID, "a") {
			if v, ok := strings.CutPrefix(l.Text, "SLUICE_RUN_TOKEN="); ok {
				tok2 = v
			}
			if v, ok := strings.CutPrefix(l.Text, "SLUICE_TASK_RUN_ID="); ok {
				id2 = v
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no second token")
		}
		time.Sleep(200 * time.Millisecond)
	}
	var expires time.Time
	var timeout string
	dbQueryRow(t, dbURL, `SELECT token_expires_at, started_at FROM task_runs WHERE id = $1`, []any{id2}, &expires, new(time.Time))
	_ = timeout
	dbExec(t, dbURL, `UPDATE task_runs SET token_expires_at = now() - interval '1 second' WHERE id = $1`, id2)
	if s := hb(id2, tok2); s != http.StatusUnauthorized {
		t.Fatalf("expired token: %d", s)
	}
	if expires.Before(time.Now().Add(23*time.Hour + 50*time.Minute)) {
		t.Fatalf("token expiry %s is not the 24 h task timeout plus 10 min", expires)
	}
}
