package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cmdDocsShots captures the screenshots and the quickstart GIF of the docs site from the real
// UI. It starts Postgres in Docker, runs bin/sluice with seeded demo data and the scripted LLM
// fixture, and drives the UI with agent-browser. It needs Docker, agent-browser, ffmpeg and a
// bin/sluice that `just build-ui build-go` built.
func cmdDocsShots() error {
	for _, tool := range []string{"docker", "agent-browser", "ffmpeg"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("docs-shots needs %s on PATH", tool)
		}
	}
	bin, err := filepath.Abs("bin/sluice")
	if err != nil {
		return err
	}
	if _, err := os.Stat(bin); err != nil {
		return errors.New("docs-shots needs bin/sluice; run `just build-ui build-go` first")
	}
	outDir, err := filepath.Abs(shotsDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "sluice-docs-shots-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	env, stop, err := startShotStack(bin, tmp)
	defer stop()
	if err != nil {
		return err
	}
	api, err := newShotAPI(env.base)
	if err != nil {
		return err
	}
	if err := seedShots(api, env); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	b := &browser{session: fmt.Sprintf("sluice-docs-shots-%d", os.Getpid()), base: env.base}
	defer b.close()
	if err := b.signIn(); err != nil {
		return err
	}
	for _, theme := range []string{"light", "dark"} {
		if err := captureShots(b, api, env, theme, outDir); err != nil {
			return err
		}
	}
	if err := captureQuickstart(b, outDir, tmp); err != nil {
		return err
	}
	fmt.Println("docs-shots: wrote", outDir)
	return nil
}

const shotsDir = "site/src/assets/shots"

const (
	shotAdmin    = "admin@sluice.example"
	shotPassword = "docs-shots-pass-1"
)

type shotEnv struct {
	base       string
	llmControl string
	llmURL     string
}

func freeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// startShotStack starts Postgres, the LLM fixture and the server. stop removes all of them.
func startShotStack(bin, tmp string) (shotEnv, func(), error) {
	var cleanups []func()
	stop := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
	pgPort, err := freeTCPPort()
	if err != nil {
		return shotEnv{}, stop, err
	}
	name := fmt.Sprintf("sluice-docs-shots-pg-%d", os.Getpid())
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-e", "POSTGRES_USER=sluice", "-e", "POSTGRES_PASSWORD=sluice",
		"-e", "POSTGRES_DB=sluice", "-p", fmt.Sprintf("127.0.0.1:%d:5432", pgPort), "postgres:17-alpine").CombinedOutput()
	if err != nil {
		return shotEnv{}, stop, fmt.Errorf("start postgres: %v: %s", err, out)
	}
	cleanups = append(cleanups, func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	for i := 0; ; i++ {
		if exec.Command("docker", "exec", name, "pg_isready", "-U", "sluice", "-d", "sluice").Run() == nil {
			break
		}
		if i > 60 {
			return shotEnv{}, stop, errors.New("postgres did not become ready")
		}
		time.Sleep(time.Second)
	}

	llmBin := filepath.Join(tmp, "llmserver")
	if out, err := exec.Command("go", "build", "-o", llmBin, "./tests/fixtures/llmserver").CombinedOutput(); err != nil {
		return shotEnv{}, stop, fmt.Errorf("build llmserver: %v: %s", err, out)
	}
	llm := exec.Command(llmBin)
	llmOut, err := llm.StdoutPipe()
	if err != nil {
		return shotEnv{}, stop, err
	}
	if err := llm.Start(); err != nil {
		return shotEnv{}, stop, err
	}
	cleanups = append(cleanups, func() { _ = llm.Process.Kill(); _ = llm.Wait() })
	var llmInfo struct {
		Control      string `json:"control"`
		AnthropicURL string `json:"anthropic_url"`
	}
	line, err := bufio.NewReader(llmOut).ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &llmInfo) != nil {
		return shotEnv{}, stop, fmt.Errorf("read llmserver info: %v", err)
	}

	port, err := freeTCPPort()
	if err != nil {
		return shotEnv{}, stop, err
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	srv := exec.Command(bin, "server")
	srv.Env = append(os.Environ(),
		fmt.Sprintf("SLUICE_DATABASE_URL=postgres://sluice:sluice@127.0.0.1:%d/sluice?sslmode=disable", pgPort),
		fmt.Sprintf("SLUICE_LISTEN_ADDR=127.0.0.1:%d", port),
		"SLUICE_PUBLIC_URL="+base,
		"SLUICE_BOOTSTRAP_ADMIN_EMAIL="+shotAdmin,
		"SLUICE_BOOTSTRAP_ADMIN_PASSWORD="+shotPassword,
		"SLUICE_MASTER_KEYS=k1:"+base64.StdEncoding.EncodeToString(key),
		"SLUICE_STORAGE_TYPE=postgres",
		"SLUICE_LOG_FORMAT=text",
	)
	logFile, err := os.Create(filepath.Join(tmp, "server.log"))
	if err != nil {
		return shotEnv{}, stop, err
	}
	srv.Stdout, srv.Stderr = logFile, logFile
	if err := srv.Start(); err != nil {
		return shotEnv{}, stop, err
	}
	cleanups = append(cleanups, func() { _ = srv.Process.Kill(); _ = srv.Wait(); _ = logFile.Close() })
	for i := 0; ; i++ {
		resp, err := http.Get(base + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if i > 120 {
			b, _ := os.ReadFile(filepath.Join(tmp, "server.log"))
			return shotEnv{}, stop, fmt.Errorf("the server did not become ready:\n%s", lastLinesOf(string(b), 20))
		}
		time.Sleep(500 * time.Millisecond)
	}
	return shotEnv{base: base, llmControl: llmInfo.Control, llmURL: llmInfo.AnthropicURL}, stop, nil
}

func lastLinesOf(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// shotAPI is a cookie session of the admin.
type shotAPI struct {
	base string
	c    *http.Client
}

func newShotAPI(base string) (*shotAPI, error) {
	jar, _ := cookiejar.New(nil)
	a := &shotAPI{base: base, c: &http.Client{Jar: jar}}
	return a, a.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": shotAdmin, "password": shotPassword}, nil)
}

func (a *shotAPI) call(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", a.base)
	resp, err := a.c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type shotExec struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

func (a *shotAPI) run(ns, flowID string, inputs map[string]any) (string, error) {
	var e shotExec
	err := a.call(http.MethodPost, "/api/v1/flows/"+ns+"/"+flowID+"/executions", map[string]any{"inputs": inputs}, &e)
	return e.ID, err
}

func (a *shotAPI) wait(id string) (shotExec, error) {
	var e shotExec
	for i := 0; i < 240; i++ {
		if err := a.call(http.MethodGet, "/api/v1/executions/"+id, nil, &e); err != nil {
			return e, err
		}
		switch e.State {
		case "SUCCESS", "FAILED", "TIMED_OUT", "CANCELLED", "SKIPPED":
			return e, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return e, fmt.Errorf("execution %s did not end", id)
}

// shotFlows are the demo namespace of the screenshots.
var shotFlows = map[string]string{
	"namespace.yaml": "description: Nightly sales pipeline\ndefaults:\n  env: { TZ: Europe/Zurich }\n",
	"nightly-load.flow.yaml": `# yaml-language-server: $schema=https://sluice-docs.pages.dev/schemas/flow.schema.json
id: nightly-load
description: Load orders and customers, then build the report.
labels: { team: data }
inputs:
  - { id: run_date, type: string, default: "2026-09-24" }
triggers:
  - { id: nightly, type: schedule, cron: "0 2 * * *", timezone: Europe/Zurich }
retry: { max_attempts: 2, backoff: exponential, initial: 10s }
tasks:
  - id: extract_orders
    type: script
    file: pipelines/extract.sh
    args: ["orders", "40"]
  - id: extract_customers
    type: script
    file: pipelines/extract.sh
    args: ["customers", "24"]
  - id: transform
    type: command
    depends_on: [extract_orders, extract_customers]
    command: ["sh", "-c", "echo transforming 2 tables; sleep 1.5; echo 'warning: 3 rows skipped' >&2; echo done"]
  - id: report
    type: command
    depends_on: [transform]
    command: ["sh", "-c", "echo report for ${{ inputs.run_date }}: ${{ tasks.extract_orders.outputs.rows }} orders; sleep 0.5"]
outputs:
  orders: ${{ tasks.extract_orders.outputs.rows }}
`,
	"pipelines/extract.sh": `table="$1"; n="$2"
for i in $(seq 1 "$n"); do echo "extracted batch $i of $table"; sleep 0.08; done
echo "loaded $n batches into raw.$table, see https://example.com/runs/$table"
echo "{\"type\":\"output\",\"key\":\"rows\",\"value\":$((n * 125))}" >> "$SLUICE_OUTPUTS"
echo "{\"type\":\"metric\",\"name\":\"rows_loaded\",\"value\":$((n * 125)),\"unit\":\"rows\",\"tags\":{\"table\":\"$table\"}}" >> "$SLUICE_OUTPUTS"
`,
	"alert.flow.yaml": `id: alert
description: Post the load summary to the team webhook.
tasks:
  - id: prepare
    type: command
    command: ["sh", "-c", "echo building the summary; sleep 0.4"]
  - id: post
    type: command
    depends_on: [prepare]
    command: ["sh", "-c", "echo posting to hooks.example.com; echo 'ERROR: connection refused to hooks.example.com:443' >&2; exit 7"]
`,
	"slow.flow.yaml": `id: slow
description: A long backfill.
tasks:
  - id: backfill
    type: command
    command: ["sh", "-c", "for i in $(seq 1 600); do echo backfill day $i; sleep 1; done"]
  - id: publish
    type: command
    depends_on: [backfill]
    command: ["echo", "published"]
`,
}

func seedShots(a *shotAPI, env shotEnv) error {
	if err := a.call(http.MethodPost, "/api/v1/namespaces", map[string]string{"name": "sales", "description": "Nightly sales pipeline"}, nil); err != nil {
		return err
	}
	var changes []map[string]any
	for p, c := range shotFlows {
		changes = append(changes, map[string]any{"op": "put", "path": p, "content": c, "executable": strings.HasSuffix(p, ".sh")})
	}
	if err := a.call(http.MethodPost, "/api/v1/namespaces/sales/changes", map[string]any{"message": "Add the sales pipeline", "changes": changes}, nil); err != nil {
		return err
	}
	if err := a.call(http.MethodPut, "/api/v1/namespaces/sales/variables/WAREHOUSE", map[string]string{"value": "analytics"}, nil); err != nil {
		return err
	}
	if err := a.call(http.MethodPut, "/api/v1/namespaces/sales/secrets/WAREHOUSE_PASSWORD", map[string]string{"value": "docs-shots-secret"}, nil); err != nil {
		return err
	}
	// The AI provider is the scripted LLM fixture, so the assistant and the triage have answers.
	if err := a.call(http.MethodPut, "/api/v1/secrets/LLM_API_KEY", map[string]string{"value": "fixture-key"}, nil); err != nil {
		return err
	}
	if err := a.call(http.MethodPut, "/api/v1/ai/provider", map[string]any{"type": "anthropic", "base_url": env.llmURL, "model": "claude-sonnet-5",
		"api_key_secret_key": "LLM_API_KEY", "auto_triage": false}, nil); err != nil {
		return err
	}
	for i := 0; i < 5; i++ {
		id, err := a.run("sales", "nightly-load", nil)
		if err != nil {
			return err
		}
		if _, err := a.wait(id); err != nil {
			return err
		}
	}
	var lastFailed string
	for i := 0; i < 2; i++ {
		id, err := a.run("sales", "alert", nil)
		if err != nil {
			return err
		}
		if _, err := a.wait(id); err != nil {
			return err
		}
		lastFailed = id
	}
	// A triage of the last failed execution, answered by the scripted LLM.
	triage, _ := json.Marshal([]map[string]string{{"json": `{"summary":"The task post failed because hooks.example.com refused the connection.",` +
		`"probable_cause":"The webhook host does not accept connections on port 443.",` +
		`"evidence":[{"task":"post","line":2,"text":"ERROR: connection refused to hooks.example.com:443"}],` +
		`"suggested_fix":"Point the webhook URL in alert.flow.yaml at a reachable host, then run the flow again.","confidence":"high"}`}})
	resp, err := http.Post(env.llmControl+"/script", "application/json", bytes.NewReader(triage))
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if err := a.call(http.MethodPost, "/api/v1/executions/"+lastFailed+"/insights", nil, nil); err != nil {
		return err
	}
	for i := 0; i < 60; i++ {
		var list struct {
			Items []struct {
				Status string `json:"status"`
			} `json:"items"`
		}
		if err := a.call(http.MethodGet, "/api/v1/executions/"+lastFailed+"/insights", nil, &list); err != nil {
			return err
		}
		if len(list.Items) > 0 && (list.Items[0].Status == "done" || list.Items[0].Status == "failed") {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("the triage did not end")
}

// browser drives agent-browser in its own session.
type browser struct {
	session string
	base    string
}

func (b *browser) ab(args ...string) (string, error) {
	cmd := exec.Command("agent-browser", args...)
	cmd.Env = append(os.Environ(), "AGENT_BROWSER_SESSION="+b.session)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("agent-browser %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func (b *browser) close() { _, _ = b.ab("close") }

func (b *browser) open(path string) error {
	_, err := b.ab("open", b.base+path)
	return err
}

func (b *browser) eval(js string) (string, error) { return b.ab("eval", js) }

func (b *browser) shot(path string) error {
	_, err := b.ab("screenshot", path)
	return err
}

// ref returns the ref of the first element of the snapshot whose line contains want.
func (b *browser) ref(want string) (string, error) {
	out, err := b.ab("snapshot", "-i")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, want) {
			if i := strings.Index(line, "ref="); i >= 0 {
				r := line[i+4:]
				if j := strings.IndexAny(r, "],"); j >= 0 {
					r = r[:j]
				}
				return "@" + r, nil
			}
		}
	}
	return "", fmt.Errorf("no element %q in the page", want)
}

func (b *browser) click(want string) error {
	r, err := b.ref(want)
	if err != nil {
		return err
	}
	_, err = b.ab("click", r)
	return err
}

func (b *browser) signIn() error {
	if _, err := b.ab("set", "viewport", "1440", "900"); err != nil {
		return err
	}
	if err := b.open("/login"); err != nil {
		return err
	}
	if _, err := b.ab("find", "label", "Email", "fill", shotAdmin); err != nil {
		return err
	}
	if _, err := b.ab("find", "label", "Password", "fill", shotPassword); err != nil {
		return err
	}
	if err := b.click(`button "Sign in"`); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	return nil
}

// key sends a keydown to the page. agent-browser's press can repeat a key, so a script sends it.
func (b *browser) key(key string, meta bool) error {
	_, err := b.eval(fmt.Sprintf(`document.body.dispatchEvent(new KeyboardEvent("keydown", {key: %q, metaKey: %t, ctrlKey: %t, bubbles: true})); 1`, key, meta, meta))
	return err
}

func (b *browser) theme(theme string) error {
	_, err := b.eval(fmt.Sprintf(`localStorage.setItem("sluice-theme", %q); localStorage.setItem("sluice-nav-collapsed", "0"); localStorage.setItem("sluice-exec-split", "46"); 1`, theme))
	return err
}

func (b *browser) settle(d time.Duration) {
	time.Sleep(d)
}

func captureShots(b *browser, api *shotAPI, env shotEnv, theme, out string) error {
	if _, err := b.ab("set", "viewport", "1440", "900"); err != nil {
		return err
	}
	if err := b.theme(theme); err != nil {
		return err
	}
	name := func(n string) string { return filepath.Join(out, n+"-"+theme+".png") }
	var list struct {
		Items []struct {
			ID     string  `json:"id"`
			FlowID *string `json:"flow_id"`
			State  string  `json:"state"`
		} `json:"items"`
	}
	if err := api.call(http.MethodGet, "/api/v1/executions?limit=50", nil, &list); err != nil {
		return err
	}
	var okID, failedID string
	for _, e := range list.Items {
		if e.FlowID != nil && *e.FlowID == "nightly-load" && e.State == "SUCCESS" && okID == "" {
			okID = e.ID
		}
		if e.FlowID != nil && *e.FlowID == "alert" && e.State == "FAILED" && failedID == "" {
			failedID = e.ID
		}
	}

	// The succeeded execution with the task extract_orders selected in the inspector.
	var detail struct {
		TaskRuns []struct {
			ID      string `json:"id"`
			TaskKey string `json:"task_key"`
		} `json:"task_runs"`
	}
	if err := api.call(http.MethodGet, "/api/v1/executions/"+okID, nil, &detail); err != nil {
		return err
	}
	runID := ""
	for _, r := range detail.TaskRuns {
		if r.TaskKey == "extract_orders" {
			runID = r.ID
		}
	}

	pages := []struct{ shot, path string }{
		{"dashboard", "/"},
		{"executions", "/executions"},
		{"flows", "/flows"},
		{"flow", "/flows/sales/nightly-load"},
		{"editor", "/namespaces/sales?file=nightly-load.flow.yaml"},
		{"secrets", "/namespaces/sales?tab=secrets"},
		{"execution", "/executions/" + okID + "?task=extract_orders&run=" + runID},
	}
	for _, p := range pages {
		if err := b.open(p.path); err != nil {
			return err
		}
		b.settle(2500 * time.Millisecond)
		if err := b.shot(name(p.shot)); err != nil {
			return err
		}
	}

	// A running execution: the waterfall grows and the logs follow.
	liveID, err := api.run("sales", "nightly-load", nil)
	if err != nil {
		return err
	}
	if err := b.open("/executions/" + liveID); err != nil {
		return err
	}
	b.settle(3500 * time.Millisecond)
	if err := b.shot(name("execution-live")); err != nil {
		return err
	}
	if _, err := api.wait(liveID); err != nil {
		return err
	}

	// A failed execution with its failed task selected, and the assistant fixing it.
	if err := b.open("/executions/" + failedID); err != nil {
		return err
	}
	b.settle(2000 * time.Millisecond)
	if err := b.click(`button "Jump to first failure"`); err != nil {
		return err
	}
	b.settle(1200 * time.Millisecond)
	if err := b.shot(name("execution-failed")); err != nil {
		return err
	}
	if err := scriptLLM(env.llmControl, "The task `post` failed because hooks.example.com refused the connection on port 443. "+
		"The prepare step succeeded, so the payload is fine.\n\nFix: in alert.flow.yaml, read the webhook URL from a variable such as WEBHOOK_URL "+
		"and set it to a reachable host. Then run the flow again. If the host was only down for a while, Restart from failed runs only `post` again."); err != nil {
		return err
	}
	if err := b.click(`button "Fix with assistant"`); err != nil {
		return err
	}
	b.settle(3000 * time.Millisecond)
	if err := b.shot(name("assistant")); err != nil {
		return err
	}
	if err := b.click(`button "Close assistant"`); err != nil {
		return err
	}
	b.settle(600 * time.Millisecond)

	// The command palette and the shortcut sheet.
	if err := b.open("/executions/" + okID); err != nil {
		return err
	}
	b.settle(1500 * time.Millisecond)
	if err := b.key("k", true); err != nil {
		return err
	}
	b.settle(800 * time.Millisecond)
	if _, err := b.ab("keyboard", "inserttext", "run night"); err != nil {
		return err
	}
	b.settle(800 * time.Millisecond)
	if err := b.shot(name("palette")); err != nil {
		return err
	}
	if _, err := b.eval(`document.querySelector('dialog[open]')?.close(); 1`); err != nil {
		return err
	}
	b.settle(500 * time.Millisecond)
	if err := b.key("?", false); err != nil {
		return err
	}
	b.settle(800 * time.Millisecond)
	if err := b.shot(name("shortcuts")); err != nil {
		return err
	}
	if _, err := b.eval(`document.querySelector('dialog[open]')?.close(); 1`); err != nil {
		return err
	}

	// A phone.
	if _, err := b.ab("set", "viewport", "390", "844"); err != nil {
		return err
	}
	if err := b.open("/executions/" + okID); err != nil {
		return err
	}
	b.settle(2500 * time.Millisecond)
	if err := b.shot(name("mobile")); err != nil {
		return err
	}
	_, err = b.ab("set", "viewport", "1440", "900")
	return err
}

func scriptLLM(control, text string) error {
	b, _ := json.Marshal([]map[string]string{{"text": text}})
	resp, err := http.Post(control+"/script", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("script the LLM fixture: status %d", resp.StatusCode)
	}
	return nil
}

// captureQuickstart records the quickstart: open a flow, run it, and watch the execution.
func captureQuickstart(b *browser, out, tmp string) error {
	if err := b.theme("light"); err != nil {
		return err
	}
	if _, err := b.ab("set", "viewport", "1280", "800"); err != nil {
		return err
	}
	if err := b.open("/flows"); err != nil {
		return err
	}
	b.settle(1500 * time.Millisecond)
	video := filepath.Join(tmp, "quickstart.webm")
	if _, err := b.ab("record", "start", video, "--fps", "20"); err != nil {
		return err
	}
	steps := []func() error{
		func() error { b.settle(1200 * time.Millisecond); return nil },
		func() error { return b.click(`link "nightly-load"`) },
		func() error { b.settle(1800 * time.Millisecond); return nil },
		func() error { return b.click(`button "Run"`) },
		func() error { b.settle(1500 * time.Millisecond); return nil },
		func() error { return b.click(`button "Run"`) },
		func() error { b.settle(9000 * time.Millisecond); return nil },
		func() error { return b.click(`tab "Outputs"`) },
		func() error { b.settle(2000 * time.Millisecond); return nil },
	}
	var stepErr error
	for _, s := range steps {
		if stepErr = s(); stepErr != nil {
			break
		}
	}
	if _, err := b.ab("record", "stop"); err != nil {
		return err
	}
	if stepErr != nil {
		return fmt.Errorf("quickstart: %w", stepErr)
	}
	gif := filepath.Join(out, "quickstart.gif")
	palette := filepath.Join(tmp, "palette.png")
	filter := "fps=12,scale=960:-1:flags=lanczos"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if o, err := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", video, "-vf", filter+",palettegen=stats_mode=diff", palette).CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg palette: %v: %s", err, lastLinesOf(string(o), 5))
	}
	if o, err := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", video, "-i", palette, "-lavfi", filter+"[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle", gif).CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg gif: %v: %s", err, lastLinesOf(string(o), 5))
	}
	return nil
}
