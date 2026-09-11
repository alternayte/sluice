//go:build perf

// Package perf runs the [P] scenarios against the built sluice binary.
package perf

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
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	adminEmail    = "admin@example.com"
	adminPassword = "admin-password-1"
)

// binary returns the binary from SLUICE_E2E_BINARY. `just perf` sets it after `just build`.
func binary(t testing.TB) string {
	t.Helper()
	p := os.Getenv("SLUICE_E2E_BINARY")
	if p == "" {
		t.Fatal("SLUICE_E2E_BINARY is not set: run `just build`, then `just perf`")
	}
	return p
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

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// server is a running `sluice server`.
type server struct {
	URL string
}

// startServer starts `sluice server` with env and waits until /readyz is 200.
func startServer(t testing.TB, env map[string]string) *server {
	t.Helper()
	port := freePort(t)
	full := map[string]string{
		"SLUICE_LISTEN_ADDR":              fmt.Sprintf("127.0.0.1:%d", port),
		"SLUICE_PUBLIC_URL":               fmt.Sprintf("http://127.0.0.1:%d", port),
		"SLUICE_LOG_FORMAT":               "text",
		"SLUICE_LOG_LEVEL":                "warn",
		"SLUICE_EXECUTORS":                "process",
		"SLUICE_BOOTSTRAP_ADMIN_EMAIL":    adminEmail,
		"SLUICE_BOOTSTRAP_ADMIN_PASSWORD": adminPassword,
	}
	for k, v := range env {
		full[k] = v
	}
	var list []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(k, "SLUICE_") {
			list = append(list, kv)
		}
	}
	for k, v := range full {
		list = append(list, k+"="+v)
	}
	cmd := exec.Command(binary(t), "server")
	cmd.Env = list
	logs := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(40 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("server logs:\n%s", logs.String())
		}
	})
	s := &server{URL: fmt.Sprintf("http://127.0.0.1:%d", port)}
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		select {
		case <-done:
			t.Fatalf("server exited during start:\n%s", logs.String())
		default:
		}
		if resp, err := http.Get(s.URL + "/readyz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s
			}
		}
	}
	t.Fatalf("server not ready within 60 s:\n%s", logs.String())
	return nil
}

// client calls the API with a bearer token.
type client struct {
	base, token string
}

// do sends a JSON request, checks the status and decodes the body into out when out is not nil.
func (c *client) do(t testing.TB, method, path string, body any, want int, out any) []byte {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return b
}

// adminClient signs in as the bootstrap admin and creates an admin API token.
func adminClient(t testing.TB, s *server) *client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}
	post := func(path string, body any) *http.Response {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, s.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", s.URL)
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
	resp = post("/api/v1/tokens", map[string]string{"name": "perf", "role": "admin"})
	defer func() { _ = resp.Body.Close() }()
	var tok struct {
		Secret string `json:"secret"`
	}
	if resp.StatusCode != http.StatusCreated || json.NewDecoder(resp.Body).Decode(&tok) != nil {
		t.Fatalf("create token: status %d", resp.StatusCode)
	}
	return &client{base: s.URL, token: tok.Secret}
}

// writeReport writes a perf result to build/reports/perf/<name>.json.
func writeReport(t testing.TB, name string, v any) {
	t.Helper()
	dir := filepath.Join(repoRoot(), "build", "reports", "perf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %s", name, b)
}

func repoRoot() string {
	wd, _ := os.Getwd()
	for d := wd; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	return wd
}
