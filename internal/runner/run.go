package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/platform/masking"
	"github.com/alternayte/sluice/internal/runnerproto"
	"github.com/alternayte/sluice/internal/snapshot"
)

// Options configure one runner process.
type Options struct {
	APIURL    string
	Token     string
	TaskRunID string
	// Stderr receives local diagnostics of the runner itself.
	Stderr io.Writer
	// HeartbeatEvery overrides runnerproto.HeartbeatInterval (tests).
	HeartbeatEvery time.Duration
}

// FromEnv reads the options from SLUICE_API_URL, SLUICE_RUN_TOKEN and SLUICE_TASK_RUN_ID.
func FromEnv() (Options, error) {
	o := Options{APIURL: os.Getenv(runnerproto.EnvAPIURL), Token: os.Getenv(runnerproto.EnvRunToken), TaskRunID: os.Getenv(runnerproto.EnvTaskRunID), Stderr: os.Stderr}
	var missing []string
	for k, v := range map[string]string{runnerproto.EnvAPIURL: o.APIURL, runnerproto.EnvRunToken: o.Token, runnerproto.EnvTaskRunID: o.TaskRunID} {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return o, fmt.Errorf("missing environment: %s", strings.Join(missing, ", "))
	}
	return o, nil
}

// Run executes the task and returns the exit code of the child (REQ-RUN-001).
func Run(ctx context.Context, o Options) int {
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	c := &Client{BaseURL: o.APIURL, Token: o.Token, TaskRunID: o.TaskRunID}
	spec, err := c.Spec(ctx)
	if err != nil {
		fmt.Fprintln(o.Stderr, "sluice exec: get spec:", err)
		return 1
	}
	r := &run{c: c, spec: spec, o: o, masker: masking.New(spec.MaskValues)}
	return r.execute(ctx)
}

type run struct {
	c       *Client
	spec    *runnerproto.Spec
	o       Options
	masker  *masking.Masker
	ship    *LogShipper
	lastErr lastLine
}

type lastLine struct {
	mu   sync.Mutex
	text string
}

func (l *lastLine) set(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	l.mu.Lock()
	l.text = s
	l.mu.Unlock()
}

func (l *lastLine) get() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text
}

func (r *run) sys(format string, args ...any) { r.ship.Add("system", fmt.Sprintf(format, args...)) }

func (r *run) complete(ctx context.Context, code int, errText, reason string) int {
	if err := r.ship.Close(); err != nil {
		fmt.Fprintln(r.o.Stderr, "sluice exec: logs:", err)
	}
	done := runnerproto.Complete{ExitCode: code, Error: r.masker.String(errText), Reason: reason}
	if err := r.c.Complete(context.WithoutCancel(ctx), done); err != nil {
		fmt.Fprintln(r.o.Stderr, "sluice exec: complete:", err)
	}
	return code
}

// runtimeTools maps script runtimes to the tool that must exist.
var runtimeTools = map[string]string{"python": "uv", "bash": "bash", "bun": "bun", "node": "node"}

func (r *run) execute(ctx context.Context) int {
	r.ship = &LogShipper{Send: r.c.Logs, Masker: r.masker}
	r.ship.Start(context.WithoutCancel(ctx))

	workdir := os.Getenv(runnerproto.EnvWorkdir)
	if workdir == "" {
		d, err := os.MkdirTemp("", "sluice-run-")
		if err != nil {
			return r.complete(ctx, 1, "create workdir: "+err.Error(), runnerapiReasonExecutor)
		}
		defer func() { _ = os.RemoveAll(d) }()
		workdir = d
	} else if err := os.MkdirAll(workdir, 0o755); err != nil {
		return r.complete(ctx, 1, "create workdir: "+err.Error(), runnerapiReasonExecutor)
	}
	if err := r.fetchBundle(ctx, workdir); err != nil {
		r.sys("[sluice] bundle: %v", err)
		return r.complete(ctx, 1, "bundle: "+err.Error(), runnerapiReasonExecutor)
	}
	if err := writeFiles(workdir, r.spec.Files); err != nil {
		r.sys("[sluice] files: %v", r.masker.String(err.Error()))
		return r.complete(ctx, 1, "files: "+err.Error(), runnerapiReasonExecutor)
	}
	outFile, err := os.CreateTemp("", "sluice-outputs-*.jsonl")
	if err != nil {
		return r.complete(ctx, 1, "create outputs file: "+err.Error(), runnerapiReasonExecutor)
	}
	outPath := outFile.Name()
	_ = outFile.Close()
	defer func() { _ = os.Remove(outPath) }()

	if tool := runtimeTools[r.spec.Runtime]; tool != "" {
		if _, err := exec.LookPath(tool); err != nil {
			r.sys("[sluice] runtime not found: %s is not on PATH", tool)
			return r.complete(ctx, 127, "runtime not found: "+tool, "runtime_not_found")
		}
	}
	if len(r.spec.Command) == 0 {
		return r.complete(ctx, 1, "empty command", runnerapiReasonExecutor)
	}
	dir := workdir
	if r.spec.Workdir != "" {
		if err := flow.ValidPath(r.spec.Workdir); err != nil {
			return r.complete(ctx, 1, "workdir: "+err.Error(), runnerapiReasonExecutor)
		}
		dir = filepath.Join(workdir, filepath.FromSlash(r.spec.Workdir))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return r.complete(ctx, 1, "workdir: "+err.Error(), runnerapiReasonExecutor)
		}
	}
	cmd := exec.Command(r.spec.Command[0], r.spec.Command[1:]...)
	cmd.Dir = dir
	cmd.Env = r.childEnv(workdir, outPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return r.complete(ctx, 1, err.Error(), runnerapiReasonExecutor)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return r.complete(ctx, 1, err.Error(), runnerapiReasonExecutor)
	}
	if err := cmd.Start(); err != nil {
		r.sys("[sluice] start: %v", err)
		code := 127
		reason := runnerapiReasonExecutor
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			reason = "runtime_not_found"
		}
		return r.complete(ctx, code, "start: "+err.Error(), reason)
	}
	pgid := cmd.Process.Pid
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); readLines(stdout, func(s string) { r.ship.Add("stdout", s) }) }()
	go func() {
		defer wg.Done()
		readLines(stderr, func(s string) {
			r.ship.Add("stderr", s)
			r.lastErr.set(s)
		})
	}()

	var reasonMu sync.Mutex
	reason := ""
	terminated := make(chan struct{})
	var termOnce sync.Once
	terminate := func(why string) {
		termOnce.Do(func() {
			reasonMu.Lock()
			reason = why
			reasonMu.Unlock()
			r.sys("[sluice] %s: sending SIGTERM", why)
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			close(terminated)
			go func() {
				time.Sleep(runnerproto.KillAfter)
				if cmd.ProcessState == nil {
					r.sys("[sluice] %s: sending SIGKILL", why)
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
				}
			}()
		})
	}
	waitDone := make(chan struct{})
	hbEvery := r.o.HeartbeatEvery
	if hbEvery <= 0 {
		hbEvery = runnerproto.HeartbeatInterval
	}
	go func() {
		t := time.NewTicker(hbEvery)
		defer t.Stop()
		for {
			select {
			case <-waitDone:
				return
			case <-t.C:
				cancel, err := r.c.Heartbeat(ctx)
				if err != nil {
					fmt.Fprintln(r.o.Stderr, "sluice exec: heartbeat:", err)
					var pe *PermanentError
					if errors.As(err, &pe) && pe.Status == 401 {
						terminate("cancelled")
					}
					continue
				}
				if cancel {
					terminate("cancelled")
				}
			}
		}
	}()
	if r.spec.TimeoutSeconds > 0 {
		timer := time.AfterFunc(time.Duration(r.spec.TimeoutSeconds)*time.Second, func() { terminate("timeout") })
		defer timer.Stop()
	}
	go func() {
		select {
		case <-ctx.Done():
			terminate("cancelled")
		case <-waitDone:
		}
	}()

	wg.Wait()
	werr := cmd.Wait()
	close(waitDone)
	// Remove processes that stay in the group, for example background children.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if ctx.Err() != nil {
		// A stop signal ended the task. The API can be gone, so the flush is short.
		r.c.Stop()
	}
	code := exitCode(cmd, werr)
	reasonMu.Lock()
	why := reason
	reasonMu.Unlock()

	r.sendOutputs(ctx, outPath, workdir)
	errText := ""
	if code != 0 {
		errText = fmt.Sprintf("exit code %d", code)
		if l := r.lastErr.get(); l != "" {
			errText += ": " + truncateLine(l)
		}
	}
	switch why {
	case "timeout":
		errText = fmt.Sprintf("task timeout of %ds reached", r.spec.TimeoutSeconds)
	case "cancelled":
		errText = "cancelled"
	}
	return r.complete(ctx, code, errText, why)
}

const runnerapiReasonExecutor = "executor_error"

func exitCode(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState == nil {
		return 1
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if err != nil && cmd.ProcessState.ExitCode() == 0 {
		return 1
	}
	return cmd.ProcessState.ExitCode()
}

// childEnv builds the task environment (§6.10): the runner environment without its
// own credentials, the resolved env and the SLUICE_* task variables.
func (r *run) childEnv(workdir, outputs string) []string {
	drop := map[string]bool{runnerproto.EnvRunToken: true, runnerproto.EnvAPIURL: true, runnerproto.EnvTaskRunID: true}
	env := map[string]string{}
	var order []string
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if drop[k] {
			continue
		}
		if _, ok := env[k]; !ok {
			order = append(order, k)
		}
		env[k] = v
	}
	set := func(k, v string) {
		if _, ok := env[k]; !ok {
			order = append(order, k)
		}
		env[k] = v
	}
	for k, v := range r.spec.Env {
		set(k, v)
	}
	set(runnerproto.EnvExecutionID, r.spec.ExecutionID)
	set(runnerproto.EnvTaskID, r.spec.TaskID)
	set(runnerproto.EnvAttempt, strconv.Itoa(r.spec.Attempt))
	set(runnerproto.EnvNamespace, r.spec.Namespace)
	set(runnerproto.EnvFlowID, r.spec.FlowID)
	set(runnerproto.EnvOutputs, outputs)
	set(runnerproto.EnvWorkdir, workdir)
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+env[k])
	}
	return out
}

// fileSink is a download target that is truncated before each attempt.
type fileSink struct{ f *os.File }

func (s *fileSink) Write(b []byte) (int, error) { return s.f.Write(b) }

func (s *fileSink) Reset() error {
	if err := s.f.Truncate(0); err != nil {
		return err
	}
	_, err := s.f.Seek(0, io.SeekStart)
	return err
}

func (r *run) fetchBundle(ctx context.Context, workdir string) error {
	f, err := os.CreateTemp("", "sluice-bundle-*.tar.gz")
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	if err := r.c.do(ctx, "GET", "/bundle", nil, "", &fileSink{f: f}); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return snapshot.ExtractBundle(f, workdir, r.spec.Limits.MaxBundleBytes)
}

// writeFiles writes the rendered files of the task into the workdir. A file of the
// bundle at the same path is replaced. The mode is 0600 because content can hold secrets.
func writeFiles(workdir string, files map[string]string) error {
	for p, content := range files {
		if err := flow.ValidPath(p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		dst := filepath.Join(workdir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if st, err := os.Lstat(dst); err == nil && !st.Mode().IsRegular() {
			return fmt.Errorf("%s: path exists and is not a regular file", p)
		}
		if err := os.WriteFile(dst, []byte(content), 0o600); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

// readLines calls fn for each line. Lines above 16 KiB are cut with a marker.
func readLines(rd io.Reader, fn func(string)) {
	br := bufio.NewReaderSize(rd, 64<<10)
	var buf []byte
	over := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(chunk) > 0 || (!isPrefix && err == nil) {
			if len(buf) <= runnerproto.MaxLineBytes {
				buf = append(buf, chunk...)
			} else {
				over = true
			}
		}
		if !isPrefix && err == nil {
			line := string(buf)
			if over || len(buf) > runnerproto.MaxLineBytes {
				line = truncateLine(line + strings.Repeat(" ", runnerproto.MaxLineBytes))
			}
			fn(line)
			buf = buf[:0]
			over = false
		}
		if err != nil {
			if len(buf) > 0 {
				fn(truncateLine(string(buf)))
			}
			return
		}
	}
}

var (
	metricNameRe   = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,99}$`)
	artifactNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)
)

type emitLine struct {
	Type        string            `json:"type"`
	Key         string            `json:"key"`
	Value       json.RawMessage   `json:"value"`
	Name        string            `json:"name"`
	Unit        string            `json:"unit"`
	Tags        map[string]string `json:"tags"`
	Path        string            `json:"path"`
	ContentType string            `json:"content_type"`
}

type artifact struct {
	name, path, contentType string
}

// sendOutputs reads the outputs file (§6.9) and sends outputs, metrics and artifacts
// (REQ-RUN-003). Invalid lines produce a warning line and are ignored.
func (r *run) sendOutputs(ctx context.Context, path, workdir string) {
	f, err := os.Open(path)
	if err != nil {
		r.sys("[sluice] outputs file: %v", err)
		return
	}
	defer func() { _ = f.Close() }()
	var events []runnerproto.Event
	var arts []artifact
	outBytes := 0
	metrics := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 2<<20)
	n := 0
	warn := func(format string, args ...any) {
		r.sys("[sluice] warning: outputs line %d: %s", n, fmt.Sprintf(format, args...))
	}
	for sc.Scan() {
		n++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var l emitLine
		if err := json.Unmarshal([]byte(text), &l); err != nil {
			warn("invalid JSON")
			continue
		}
		switch l.Type {
		case "output":
			if l.Key == "" || len(l.Key) > 256 || len(l.Value) == 0 {
				warn("output needs key and value")
				continue
			}
			masked := r.masker.Bytes(l.Value)
			if !json.Valid(masked) {
				masked, _ = json.Marshal(string(masked))
			}
			if outBytes+len(l.Key)+len(masked) > runnerproto.MaxOutputBytes {
				warn("outputs exceed 1 MiB, output %q ignored", l.Key)
				continue
			}
			outBytes += len(l.Key) + len(masked)
			events = append(events, runnerproto.Event{Type: "output", Key: l.Key, Value: masked, TS: time.Now().UTC()})
		case "metric":
			if !metricNameRe.MatchString(l.Name) {
				warn("invalid metric name %q", l.Name)
				continue
			}
			var v float64
			if err := json.Unmarshal(l.Value, &v); err != nil {
				warn("metric %s needs a numeric value", l.Name)
				continue
			}
			if len(l.Tags) > runnerproto.MaxMetricTags {
				warn("metric %s has more than %d tags", l.Name, runnerproto.MaxMetricTags)
				continue
			}
			bad := false
			for _, tv := range l.Tags {
				if len(tv) > runnerproto.MaxTagValueLen {
					bad = true
				}
			}
			if bad {
				warn("metric %s has a tag value longer than %d characters", l.Name, runnerproto.MaxTagValueLen)
				continue
			}
			if metrics >= runnerproto.MaxMetrics {
				warn("more than %d metrics", runnerproto.MaxMetrics)
				continue
			}
			metrics++
			tags := map[string]string{}
			for k, tv := range l.Tags {
				tags[k] = r.masker.String(tv)
			}
			val, _ := json.Marshal(v)
			events = append(events, runnerproto.Event{Type: "metric", Name: l.Name, Value: val, Unit: l.Unit, Tags: tags, TS: time.Now().UTC()})
		case "artifact":
			if err := flow.ValidPath(l.Path); err != nil {
				warn("artifact path: %v", err)
				continue
			}
			name := l.Name
			if name == "" {
				name = filepath.Base(l.Path)
			}
			if !artifactNameRe.MatchString(name) {
				warn("invalid artifact name %q", name)
				continue
			}
			arts = append(arts, artifact{name: name, path: filepath.Join(workdir, filepath.FromSlash(l.Path)), contentType: l.ContentType})
		default:
			warn("unknown type %q", l.Type)
		}
	}
	if err := sc.Err(); err != nil {
		r.sys("[sluice] warning: outputs file: %v", err)
	}
	for seq, start := 1, 0; start < len(events); seq, start = seq+1, start+1000 {
		end := min(start+1000, len(events))
		if err := r.c.Events(ctx, runnerproto.EventBatch{Seq: seq, Events: events[start:end]}); err != nil {
			r.sys("[sluice] events: %v", err)
			break
		}
	}
	for _, a := range arts {
		st, err := os.Lstat(a.path)
		if err != nil || !st.Mode().IsRegular() {
			r.sys("[sluice] warning: artifact %s: file %s not found", a.name, a.path)
			continue
		}
		if st.Size() > r.spec.Limits.MaxArtifactBytes {
			r.sys("[sluice] warning: artifact %s is larger than %d bytes", a.name, r.spec.Limits.MaxArtifactBytes)
			continue
		}
		open := func() (io.Reader, error) { return os.Open(a.path) }
		if err := r.c.Artifact(ctx, a.name, a.contentType, open); err != nil {
			r.sys("[sluice] artifact %s: %v", a.name, err)
		}
	}
}
