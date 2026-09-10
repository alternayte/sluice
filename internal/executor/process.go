package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/alternayte/sluice/internal/runnerapi"
)

// ProcessExecutor runs `sluice exec` as a child process of the server (REQ-EXR-003).
type ProcessExecutor struct {
	// Binary is the sluice binary. Default os.Executable().
	Binary string
	// Output receives the runner's own diagnostics. Task logs go through the runner API.
	Output io.Writer
	Log    *slog.Logger

	mu    sync.Mutex
	procs map[string]*proc
}

type proc struct {
	cmd  *exec.Cmd
	done chan struct{}
	res  Result
}

// Type returns "process".
func (p *ProcessExecutor) Type() string { return Process }

// passEnv lists server environment variables that the runner inherits. The server
// configuration (database URL, master keys) is not passed to tasks.
var passEnv = []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TMPDIR", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR",
	"UV_CACHE_DIR", "UV_PYTHON_INSTALL_DIR", "BUN_INSTALL", "XDG_CACHE_HOME", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}

// Start starts the runner in its own process group.
func (p *ProcessExecutor) Start(_ context.Context, t Task) (string, error) {
	bin := p.Binary
	if bin == "" {
		var err error
		if bin, err = os.Executable(); err != nil {
			return "", err
		}
	}
	cmd := exec.Command(bin, "exec")
	env := make([]string, 0, len(passEnv)+len(t.Env))
	for _, k := range passEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	keys := make([]string, 0, len(t.Env))
	for k := range t.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+t.Env[k])
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if p.Output != nil {
		cmd.Stdout, cmd.Stderr = p.Output, p.Output
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start runner: %w", err)
	}
	ref := strconv.Itoa(cmd.Process.Pid)
	pr := &proc{cmd: cmd, done: make(chan struct{})}
	p.mu.Lock()
	if p.procs == nil {
		p.procs = map[string]*proc{}
	}
	p.procs[ref] = pr
	p.mu.Unlock()
	go func() {
		err := cmd.Wait()
		code := 0
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		pr.res = Result{ExitCode: code, Err: err}
		close(pr.done)
	}()
	return ref, nil
}

func (p *ProcessExecutor) get(ref string) (*proc, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.procs[ref]
	return pr, ok
}

// Wait blocks until the runner exits.
func (p *ProcessExecutor) Wait(ctx context.Context, ref string) Result {
	pr, ok := p.get(ref)
	if !ok {
		return Result{ExitCode: -1, Err: ErrUnknownRef}
	}
	select {
	case <-pr.done:
	case <-ctx.Done():
		return Result{ExitCode: -1, Err: ctx.Err()}
	}
	p.mu.Lock()
	delete(p.procs, ref)
	p.mu.Unlock()
	return pr.res
}

// Cancel sends SIGTERM to the whole process group, and SIGKILL after 10 s (REQ-EXR-003).
func (p *ProcessExecutor) Cancel(_ context.Context, ref string) error {
	pr, ok := p.get(ref)
	if !ok {
		return ErrUnknownRef
	}
	pgid := pr.cmd.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	go func() {
		select {
		case <-pr.done:
		case <-time.After(runnerapi.KillAfter + 2*time.Second):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}()
	return nil
}

// Kill sends SIGKILL to the process group at once.
func (p *ProcessExecutor) Kill(ref string) {
	if pr, ok := p.get(ref); ok {
		_ = syscall.Kill(-pr.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// Status reports whether the runner process still runs.
func (p *ProcessExecutor) Status(_ context.Context, ref string) (Status, error) {
	pr, ok := p.get(ref)
	if !ok {
		return StatusGone, nil
	}
	select {
	case <-pr.done:
		return StatusGone, nil
	default:
		return StatusRunning, nil
	}
}

// Refs returns the references of running processes.
func (p *ProcessExecutor) Refs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.procs))
	for r := range p.procs {
		out = append(out, r)
	}
	return out
}
