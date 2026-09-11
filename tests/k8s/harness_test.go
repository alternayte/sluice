//go:build k8s

// Package k8s runs the [K] scenarios against a kind cluster that tests/k8s/run.sh
// prepares: the Helm chart release, Postgres, Vault and the images.
package k8s

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	adminEmail    = "admin@example.com"
	adminPassword = "admin-password-1"
)

func env(t testing.TB, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is not set: run the [K] scenarios with tests/k8s/run.sh (just e2e-k8s)", key)
	}
	return v
}

// kubectl runs kubectl in the test context and namespace and returns its output.
func kubectl(t testing.TB, args ...string) string {
	t.Helper()
	out, err := kubectlErr(t, args...)
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func kubectlErr(t testing.TB, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"--context", env(t, "SLUICE_K8S_CONTEXT"), "-n", env(t, "SLUICE_K8S_TEST_NAMESPACE")}, args...)
	out, err := exec.Command("kubectl", full...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func freePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// forward is a port-forward to the Sluice Service. It starts again when a server pod
// that it used goes away.
type forward struct {
	t    testing.TB
	port int
	mu   sync.Mutex
	cmd  *exec.Cmd
	// static is a fixed base URL, for a server outside the cluster. It needs no port-forward.
	static string
}

// adminClientAt returns an admin client for a server at a fixed base URL.
func adminClientAt(t testing.TB, base string) *client {
	t.Helper()
	return adminClient(t, &forward{t: t, static: base})
}

func newForward(t testing.TB) *forward {
	t.Helper()
	f := &forward{t: t, port: freePort(t)}
	f.restart()
	t.Cleanup(f.stop)
	return f
}

func (f *forward) stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cmd != nil && f.cmd.Process != nil {
		_ = f.cmd.Process.Kill()
		_ = f.cmd.Wait()
	}
	f.cmd = nil
}

// restart starts the port-forward again and waits until /readyz answers 200.
func (f *forward) restart() {
	f.t.Helper()
	if f.static != "" {
		return
	}
	f.stop()
	for deadline := time.Now().Add(3 * time.Minute); ; time.Sleep(time.Second) {
		f.mu.Lock()
		f.cmd = exec.Command("kubectl", "--context", env(f.t, "SLUICE_K8S_CONTEXT"), "-n", env(f.t, "SLUICE_K8S_TEST_NAMESPACE"),
			"port-forward", "svc/"+env(f.t, "SLUICE_K8S_RELEASE"), fmt.Sprintf("%d:8080", f.port))
		if err := f.cmd.Start(); err != nil {
			f.mu.Unlock()
			f.t.Fatal(err)
		}
		f.mu.Unlock()
		for i := 0; i < 20; i++ {
			if resp, err := http.Get(f.URL() + "/readyz"); err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		f.stop()
		if time.Now().After(deadline) {
			f.t.Fatal("the Sluice Service is not ready through a port-forward")
		}
	}
}

func (f *forward) URL() string {
	if f.static != "" {
		return f.static
	}
	return fmt.Sprintf("http://127.0.0.1:%d", f.port)
}

// client calls the API with a bearer token through a port-forward.
type client struct {
	f     *forward
	token string
}

type response struct {
	Status int
	Body   []byte
}

// raw sends a JSON request. A connection error restarts the port-forward and retries once.
func (c *client) raw(t testing.TB, method, path string, body any) response {
	t.Helper()
	for attempt := 0; ; attempt++ {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, c.f.URL()+path, rd)
		req.Header.Set("Authorization", "Bearer "+c.token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if attempt < 3 {
				c.f.restart()
				continue
			}
			t.Fatalf("%s %s: %v", method, path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusBadGateway && attempt < 3 {
			c.f.restart()
			continue
		}
		return response{Status: resp.StatusCode, Body: b}
	}
}

func (c *client) do(t testing.TB, method, path string, body any, want int, out any) response {
	t.Helper()
	r := c.raw(t, method, path, body)
	if r.Status != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, r.Status, want, r.Body)
	}
	if out != nil {
		if err := json.Unmarshal(r.Body, out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return r
}

// adminClient signs in as the bootstrap admin and creates an admin token.
func adminClient(t testing.TB, f *forward) *client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}
	post := func(path string, body any) *http.Response {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, f.URL()+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", f.URL())
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post("/api/v1/auth/login", map[string]string{"email": adminEmail, "password": adminPassword})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: status %d", resp.StatusCode)
	}
	resp = post("/api/v1/tokens", map[string]string{"name": fmt.Sprintf("k8s-%d", time.Now().UnixNano()), "role": "admin"})
	defer func() { _ = resp.Body.Close() }()
	var tok struct {
		Secret string `json:"secret"`
	}
	if resp.StatusCode != http.StatusCreated || json.NewDecoder(resp.Body).Decode(&tok) != nil {
		t.Fatalf("create token: status %d", resp.StatusCode)
	}
	return &client{f: f, token: tok.Secret}
}

// uniq returns a unique suffix for names.
func uniq() string { return fmt.Sprintf("%x", time.Now().UnixNano()&0xffffffffff) }

// saveFiles creates a namespace and saves files in one version.
func saveFiles(t testing.TB, c *client, ns string, files map[string]string) {
	t.Helper()
	if r := c.raw(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": ns}); r.Status != http.StatusCreated && r.Status != http.StatusConflict {
		t.Fatalf("create namespace: %d %s", r.Status, r.Body)
	}
	var changes []map[string]any
	for p, content := range files {
		changes = append(changes, map[string]any{"op": "put", "path": p, "content": content})
	}
	c.do(t, http.MethodPost, "/api/v1/namespaces/"+ns+"/changes", map[string]any{"message": "k8s files", "changes": changes}, http.StatusCreated, nil)
}

type taskRun struct {
	TaskKey  string         `json:"task_key"`
	Attempt  int            `json:"attempt"`
	State    string         `json:"state"`
	Reason   string         `json:"reason"`
	Error    string         `json:"error"`
	Outputs  map[string]any `json:"outputs"`
	ExitCode *int           `json:"exit_code"`
}

type execDetail struct {
	ID       string    `json:"id"`
	State    string    `json:"state"`
	Error    string    `json:"error"`
	TaskRuns []taskRun `json:"task_runs"`
}

func (d execDetail) last(key string) taskRun {
	var out taskRun
	for _, tr := range d.TaskRuns {
		if tr.TaskKey == key {
			out = tr
		}
	}
	return out
}

func trigger(t testing.TB, c *client, ns, flowID string) execDetail {
	t.Helper()
	var d execDetail
	c.do(t, http.MethodPost, fmt.Sprintf("/api/v1/flows/%s/%s/executions", ns, flowID), map[string]any{}, http.StatusCreated, &d)
	return d
}

var terminal = map[string]bool{"SUCCESS": true, "FAILED": true, "TIMED_OUT": true, "CANCELLED": true, "SKIPPED": true}

func waitExec(t testing.TB, c *client, id string, timeout time.Duration, pred func(execDetail) bool) execDetail {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var d execDetail
		c.do(t, http.MethodGet, "/api/v1/executions/"+id, nil, http.StatusOK, &d)
		if pred(d) {
			return d
		}
		if time.Now().After(deadline) {
			b, _ := json.MarshalIndent(d, "", "  ")
			t.Fatalf("execution %s: condition not met within %s:\n%s", id, timeout, b)
		}
		time.Sleep(time.Second)
	}
}

func waitTerminal(t testing.TB, c *client, id string, timeout time.Duration) execDetail {
	t.Helper()
	return waitExec(t, c, id, timeout, func(d execDetail) bool { return terminal[d.State] })
}

// logText returns all stored log lines of an execution.
func logText(t testing.TB, c *client, id string) string {
	t.Helper()
	r := c.do(t, http.MethodGet, "/api/v1/executions/"+id+"/logs/download", nil, http.StatusOK, nil)
	return string(r.Body)
}

// k8sExecutor is the flow executor of the kubernetes tests.
const k8sExecutor = "executor: {type: kubernetes, image: \"sluice-uv:dev\"}\n"

// jobsOf returns the names of the Jobs of an execution.
func jobsOf(t testing.TB, execID string) []string {
	t.Helper()
	out := kubectl(t, "get", "jobs", "-l", "sluice.dev/execution-id="+execID, "-o", "jsonpath={.items[*].metadata.name}")
	return strings.Fields(out)
}

// waitJob waits until the execution has a Job and returns its name.
func waitJob(t testing.TB, execID string, timeout time.Duration) string {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(time.Second) {
		if jobs := jobsOf(t, execID); len(jobs) > 0 {
			return jobs[len(jobs)-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no Job of execution %s within %s", execID, timeout)
		}
	}
}
