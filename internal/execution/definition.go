package execution

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/alternayte/sluice/internal/flow"
)

// Definition is the effective definition that an execution pins (DI-18).
type Definition struct {
	Namespace string         `json:"namespace"`
	FlowKey   string         `json:"flow_key,omitempty"`
	Flow      flow.Flow      `json:"flow"`
	Defaults  *flow.Defaults `json:"defaults,omitempty"`
}

// ParseDefinition decodes executions.definition.
func ParseDefinition(b []byte) (*Definition, error) {
	var d Definition
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Task returns the task with the key.
func (d *Definition) Task(key string) (*flow.Task, bool) {
	for i := range d.Flow.Tasks {
		if d.Flow.Tasks[i].ID == key {
			return &d.Flow.Tasks[i], true
		}
	}
	return nil, false
}

// Defaults of §6.4.
const (
	DefaultTaskTimeout = 24 * time.Hour
	DefaultInitial     = 10 * time.Second
	DefaultMaxBackoff  = 10 * time.Minute
	DefaultPool        = "default"
	DefaultExecutor    = "process"
)

// TaskConfig is the effective configuration of one task.
type TaskConfig struct {
	Task     flow.Task
	Executor flow.Executor
	Pool     string
	Timeout  time.Duration
	Retry    flow.Retry
	// Env holds the merged env templates: namespace defaults, flow env, task env.
	Env map[string]string
}

// Inline reports whether the task runs inline in the claiming instance.
func (c TaskConfig) Inline() bool { return c.Task.Type == "http" || c.Task.Type == "subflow" }

func mergeRetry(dst *flow.Retry, src *flow.Retry) {
	if src == nil {
		return
	}
	if src.MaxAttempts > 0 {
		dst.MaxAttempts = src.MaxAttempts
	}
	if src.Backoff != "" {
		dst.Backoff = src.Backoff
	}
	if src.Initial != "" {
		dst.Initial = src.Initial
	}
	if src.Max != "" {
		dst.Max = src.Max
	}
}

// Config returns the effective configuration of a task (§6.4, §6.8).
func (d *Definition) Config(t flow.Task) TaskConfig {
	c := TaskConfig{Task: t, Env: map[string]string{}}
	var nsExec *flow.Executor
	retry := flow.Retry{MaxAttempts: 1, Backoff: "fixed"}
	if d.Defaults != nil {
		nsExec = d.Defaults.Executor
		for k, v := range d.Defaults.Env {
			c.Env[k] = v
		}
		mergeRetry(&retry, d.Defaults.Retry)
	}
	for k, v := range d.Flow.Env {
		c.Env[k] = v
	}
	for k, v := range t.Env {
		c.Env[k] = v
	}
	mergeRetry(&retry, d.Flow.Retry)
	mergeRetry(&retry, t.Retry)
	c.Retry = retry
	if c.Inline() {
		c.Executor = flow.Executor{Type: "inline"}
		c.Pool = ""
	} else {
		c.Executor = flow.EffectiveExecutor(t.Executor, d.Flow.Executor, nsExec)
		if c.Executor.Type == "" {
			c.Executor.Type = DefaultExecutor
		}
		c.Pool = c.Executor.Pool
		if c.Pool == "" {
			c.Pool = DefaultPool
		}
	}
	c.Timeout = DefaultTaskTimeout
	if d.Defaults != nil && d.Defaults.Timeout != "" {
		if x, err := time.ParseDuration(string(d.Defaults.Timeout)); err == nil && x > 0 {
			c.Timeout = x
		}
	}
	if t.Timeout != "" {
		if x, err := time.ParseDuration(string(t.Timeout)); err == nil && x > 0 {
			c.Timeout = x
		}
	}
	return c
}

// Backoff returns the delay before the attempt after `attempt` (REQ-EXE-005).
func Backoff(r flow.Retry, attempt int) time.Duration {
	initial := DefaultInitial
	if r.Initial != "" {
		if x, err := time.ParseDuration(string(r.Initial)); err == nil {
			initial = x
		}
	}
	maxD := DefaultMaxBackoff
	if r.Max != "" {
		if x, err := time.ParseDuration(string(r.Max)); err == nil {
			maxD = x
		}
	}
	d := initial
	if r.Backoff == "exponential" {
		for i := 1; i < attempt && d < maxD; i++ {
			d *= 2
		}
	}
	if d > maxD {
		d = maxD
	}
	return d
}

// FlowTimeout returns the execution wall time, or 0.
func (d *Definition) FlowTimeout() time.Duration {
	if d.Flow.Timeout == "" {
		return 0
	}
	x, err := time.ParseDuration(string(d.Flow.Timeout))
	if err != nil {
		return 0
	}
	return x
}

// ScriptCommand returns the runtime command of a script task (§6.4) and the tool it needs.
func ScriptCommand(t flow.Task, args []string) ([]string, string) {
	rt := flow.RuntimeFor(t.Runtime, t.File)
	var cmd []string
	tool := ""
	switch rt {
	case "python":
		cmd, tool = []string{"uv", "run", t.File}, "python"
	case "bash":
		cmd, tool = []string{"bash", t.File}, "bash"
	case "bun":
		cmd, tool = []string{"bun", "run", t.File}, "bun"
	case "node":
		cmd, tool = []string{"node", t.File}, "node"
	}
	return append(cmd, args...), tool
}

// EscapeTemplate makes a literal string safe for template rendering.
func EscapeTemplate(s string) string { return strings.ReplaceAll(s, "${{", "$${{") }
