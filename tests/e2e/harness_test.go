//go:build e2e

// Package e2e runs end-to-end scenarios against the built sluice binary.
package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// binary returns the sluice binary: SLUICE_E2E_BINARY or a fresh build.
func binary(t testing.TB) string {
	t.Helper()
	binOnce.Do(func() {
		if p := os.Getenv("SLUICE_E2E_BINARY"); p != "" {
			binPath = p
			return
		}
		dir, err := os.MkdirTemp("", "sluice-e2e")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "sluice")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/sluice")
		cmd.Dir = repoRoot()
		out, err := cmd.CombinedOutput()
		if err != nil {
			binErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
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

// runCLI runs the binary with args and env and returns stdout, stderr and the exit code.
func runCLI(t testing.TB, env map[string]string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary(t), args...)
	cmd.Env = baseEnv(env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

// baseEnv keeps PATH and HOME and drops other SLUICE_ variables of the test process.
func baseEnv(env map[string]string) []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "SLUICE_") {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
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

// Proc is a running sluice server.
type Proc struct {
	URL  string
	Cmd  *exec.Cmd
	Env  map[string]string
	logs *syncBuffer
	done chan error
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

// Logs returns the server output.
func (p *Proc) Logs() string { return p.logs.String() }

// newDatabase returns a fresh database URL.
func newDatabase(t testing.TB) string {
	return pgtest.Shared(t).NewDatabase(t)
}

// startServer starts `sluice server` and waits until /readyz is 200.
func startServer(t testing.TB, env map[string]string) *Proc {
	t.Helper()
	port := freePort(t)
	full := map[string]string{
		"SLUICE_LISTEN_ADDR":    fmt.Sprintf("127.0.0.1:%d", port),
		"SLUICE_PUBLIC_URL":     fmt.Sprintf("http://127.0.0.1:%d", port),
		"SLUICE_LOG_FORMAT":     "text",
		"SLUICE_LOG_LEVEL":      "debug",
		"SLUICE_EXECUTORS":      "process",
		"SLUICE_SHUTDOWN_GRACE": "10s",

		"SLUICE_BOOTSTRAP_ADMIN_EMAIL":    adminEmail,
		"SLUICE_BOOTSTRAP_ADMIN_PASSWORD": adminPassword,
	}
	for k, v := range env {
		full[k] = v
	}
	cmd := exec.Command(binary(t), "server")
	cmd.Env = baseEnv(full)
	logs := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &Proc{URL: fmt.Sprintf("http://127.0.0.1:%d", port), Cmd: cmd, Env: full, logs: logs, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = p.Stop(syscall.SIGKILL)
		if t.Failed() {
			t.Logf("server %s logs:\n%s", p.URL, lastLines(logs.String(), 200))
		}
	})
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-p.done:
			p.done <- err
			t.Fatalf("server exited during start: %v\n%s", err, logs.String())
		default:
		}
		resp, err := http.Get(p.URL + "/readyz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return p
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server not ready within 60 s\n%s", logs.String())
	return nil
}

// Stop sends sig and waits for the process to end. It returns the wait error.
func (p *Proc) Stop(sig syscall.Signal) error {
	if p.Cmd.ProcessState != nil {
		return nil
	}
	_ = p.Cmd.Process.Signal(sig)
	select {
	case err := <-p.done:
		p.done <- err
		return err
	case <-time.After(60 * time.Second):
		_ = p.Cmd.Process.Kill()
		return fmt.Errorf("process did not exit")
	}
}

// WaitExit waits up to d for the process to exit.
func (p *Proc) WaitExit(d time.Duration) (error, bool) {
	select {
	case err := <-p.done:
		p.done <- err
		return err, true
	case <-time.After(d):
		return nil, false
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
