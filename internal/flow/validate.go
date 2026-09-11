package flow

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var (
	flowIDRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	namespaceRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	cronParser  = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
)

// ValidNamespaceName checks REQ-NS-001.
func ValidNamespaceName(s string) bool { return len(s) <= 128 && namespaceRe.MatchString(s) }

// ParseCron parses a 5-field cron expression or @hourly, @daily, @weekly, @monthly.
func ParseCron(spec string) (cron.Schedule, error) {
	s := strings.TrimSpace(spec)
	switch s {
	case "@hourly":
		s = "0 * * * *"
	case "@daily":
		s = "0 0 * * *"
	case "@weekly":
		s = "0 0 * * 0"
	case "@monthly":
		s = "0 0 1 * *"
	default:
		if strings.HasPrefix(s, "@") {
			return nil, fmt.Errorf("unsupported descriptor %q", s)
		}
	}
	return cronParser.Parse(s)
}

// ParseFlowRef parses "<namespace>/<flow_id>".
func ParseFlowRef(s string) (ns, id string, ok bool) {
	ns, id, ok = strings.Cut(s, "/")
	if !ok || !ValidNamespaceName(ns) || !flowIDRe.MatchString(id) {
		return "", "", false
	}
	return ns, id, true
}

// RuntimeFor returns the script runtime from the field or the file extension.
func RuntimeFor(runtime, file string) string {
	if runtime != "" {
		return runtime
	}
	switch strings.ToLower(path.Ext(file)) {
	case ".py":
		return "python"
	case ".sh":
		return "bash"
	case ".ts":
		return "bun"
	case ".js", ".mjs", ".cjs":
		return "node"
	}
	return ""
}

// ValidPath checks REQ-NS-005: relative, UTF-8, at most 512 characters, no ".." segments.
func ValidPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("path is empty")
	case len(p) > 512:
		return fmt.Errorf("path is longer than 512 characters")
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("path must be relative")
	case strings.ContainsRune(p, '\\') || strings.ContainsRune(p, 0):
		return fmt.Errorf("path contains a forbidden character")
	case !isUTF8(p):
		return fmt.Errorf("path is not UTF-8")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("path has an empty, . or .. segment")
		}
	}
	return nil
}

func isUTF8(s string) bool { return strings.ToValidUTF8(s, "�") == s }

// Context of semantic validation.
type validation struct {
	f      *Flow
	pos    Positions
	files  map[string]bool // nil: do not check file existence
	nsDefs *Defaults
	issues []Issue
}

func (v *validation) add(code, p, format string, args ...any) {
	v.issues = append(v.issues, v.pos.issue(code, p, format, args...))
}

// Semantic validates a structurally valid flow (REQ-FLOW-003).
func Semantic(f *Flow, pos Positions, files map[string]bool, nsDefaults *Defaults) []Issue {
	v := &validation{f: f, pos: pos, files: files, nsDefs: nsDefaults}
	v.inputs()
	taskIdx := v.tasks()
	v.dependencies(taskIdx)
	v.triggers()
	v.templates(taskIdx)
	v.durations()
	SortIssues(v.issues)
	return v.issues
}

func (v *validation) inputs() {
	seen := map[string]bool{}
	for i, in := range v.f.Inputs {
		p := IndexPath("inputs", i)
		if seen[in.ID] {
			v.add(CodeDuplicateInputID, JoinPath(p, "id"), "duplicate input ID %q", in.ID)
		}
		seen[in.ID] = true
		if in.Type == "select" && len(in.Values) == 0 {
			v.add(CodeMissingField, p, "select input %q needs values", in.ID)
		}
		if in.Type != "select" && len(in.Values) > 0 {
			v.add(CodeFieldNotAllowed, JoinPath(p, "values"), "values are allowed only on select inputs")
		}
		if in.Default != nil {
			if _, err := CoerceInput(in, in.Default); err != nil {
				v.add(CodeInvalidInputDefault, JoinPath(p, "default"), "default of input %q: %v", in.ID, err)
			}
		}
	}
}

// fieldRule lists the task fields that belong to one task type.
var typeFields = map[string][]string{
	"script":  {"file", "runtime", "args"},
	"command": {"command", "workdir"},
	"http":    {"method", "url", "headers", "body", "expect_status"},
	"subflow": {"flow", "inputs", "wait"},
}

func taskFieldSet(t Task) map[string]bool {
	set := map[string]bool{}
	mark := func(name string, present bool) {
		if present {
			set[name] = true
		}
	}
	mark("file", t.File != "")
	mark("runtime", t.Runtime != "")
	mark("args", len(t.Args) > 0)
	mark("command", len(t.Command) > 0)
	mark("workdir", t.Workdir != "")
	mark("method", t.Method != "")
	mark("url", t.URL != "")
	mark("headers", len(t.Headers) > 0)
	mark("body", t.Body != "")
	mark("expect_status", len(t.ExpectStatus) > 0)
	mark("flow", t.Flow != "")
	mark("inputs", len(t.Inputs) > 0)
	mark("wait", t.Wait != nil)
	return set
}

func (v *validation) tasks() map[string]int {
	idx := map[string]int{}
	for i, t := range v.f.Tasks {
		p := IndexPath("tasks", i)
		if _, dup := idx[t.ID]; dup {
			v.add(CodeDuplicateTaskID, JoinPath(p, "id"), "duplicate task ID %q", t.ID)
		} else {
			idx[t.ID] = i
		}
		present := taskFieldSet(t)
		allowed := map[string]bool{}
		for _, f := range typeFields[t.Type] {
			allowed[f] = true
		}
		for f := range present {
			if !allowed[f] {
				v.add(CodeFieldNotAllowed, JoinPath(p, f), "field %s is not allowed on %s tasks", f, t.Type)
			}
		}
		switch t.Type {
		case "script":
			if t.File == "" {
				v.add(CodeMissingField, p, "script task %q needs file", t.ID)
			} else if err := ValidPath(t.File); err != nil {
				v.add(CodeInvalidPath, JoinPath(p, "file"), "file: %v", err)
			} else {
				if v.files != nil && !v.files[t.File] {
					v.add(CodeFileNotFound, JoinPath(p, "file"), "file %q is not in the namespace", t.File)
				}
				if RuntimeFor(t.Runtime, t.File) == "" {
					v.add(CodeUnknownRuntime, JoinPath(p, "file"), "set runtime: no default runtime for %q", path.Ext(t.File))
				}
			}
		case "command":
			if len(t.Command) == 0 {
				v.add(CodeMissingField, p, "command task %q needs command", t.ID)
			}
			if t.Workdir != "" {
				if err := ValidPath(t.Workdir); err != nil {
					v.add(CodeInvalidPath, JoinPath(p, "workdir"), "workdir: %v", err)
				}
			}
		case "http":
			if t.URL == "" {
				v.add(CodeMissingField, p, "http task %q needs url", t.ID)
			}
			for j, s := range t.ExpectStatus {
				if s < 100 || s > 599 {
					v.add(CodeOutOfRange, IndexPath(JoinPath(p, "expect_status"), j), "status %d is not an HTTP status", s)
				}
			}
		case "subflow":
			if t.Flow == "" {
				v.add(CodeMissingField, p, "subflow task %q needs flow", t.ID)
			} else if _, _, ok := ParseFlowRef(t.Flow); !ok {
				v.add(CodeInvalidReference, JoinPath(p, "flow"), "flow must be <namespace>/<flow_id>, got %q", t.Flow)
			}
		}
		if t.Type == "http" || t.Type == "subflow" {
			if t.Executor != nil {
				v.add(CodeExecutorNotAllowed, JoinPath(p, "executor"), "executor is not allowed on %s tasks", t.Type)
			}
			continue
		}
		v.executor(p, t)
	}
	if v.f.Executor != nil {
		v.executorFields("executor", v.f.Executor)
	}
	return idx
}

// EffectiveExecutor merges task, flow and namespace executors in resolution order.
func EffectiveExecutor(task, flow, ns *Executor) Executor {
	var out Executor
	for _, e := range []*Executor{ns, flow, task} {
		if e == nil {
			continue
		}
		if e.Type != "" {
			out.Type = e.Type
		}
		if e.Pool != "" {
			out.Pool = e.Pool
		}
		if e.Image != "" {
			out.Image = e.Image
		}
		if e.InjectRunner != nil {
			out.InjectRunner = e.InjectRunner
		}
		if e.Pull != "" {
			out.Pull = e.Pull
		}
		if e.Network != "" {
			out.Network = e.Network
		}
		if e.Resources != nil {
			out.Resources = e.Resources
		}
		if e.Kubernetes != nil {
			out.Kubernetes = e.Kubernetes
		}
	}
	return out
}

func (v *validation) executor(p string, t Task) {
	if t.Executor != nil {
		v.executorFields(JoinPath(p, "executor"), t.Executor)
	}
	var ns *Executor
	if v.nsDefs != nil {
		ns = v.nsDefs.Executor
	}
	eff := EffectiveExecutor(t.Executor, v.f.Executor, ns)
	if (eff.Type == "docker" || eff.Type == "kubernetes") && eff.Image == "" {
		at := p
		if t.Executor != nil {
			at = JoinPath(p, "executor")
		}
		v.add(CodeImageRequired, at, "task %q runs on %s and needs executor.image", t.ID, eff.Type)
	}
}

func (v *validation) executorFields(p string, e *Executor) {
	if e.Type == "" {
		return
	}
	check := func(name string, present bool, types ...string) {
		if !present {
			return
		}
		for _, t := range types {
			if t == e.Type {
				return
			}
		}
		v.add(CodeFieldNotAllowed, JoinPath(p, name), "executor.%s is not allowed for type %s", name, e.Type)
	}
	check("image", e.Image != "", "docker", "kubernetes")
	check("inject_runner", e.InjectRunner != nil, "docker", "kubernetes")
	check("pull", e.Pull != "", "docker")
	check("network", e.Network != "", "docker")
	check("resources", e.Resources != nil, "docker", "kubernetes")
	check("kubernetes", e.Kubernetes != nil, "kubernetes")
}

func (v *validation) dependencies(idx map[string]int) {
	for i, t := range v.f.Tasks {
		seen := map[string]bool{}
		for j, d := range t.DependsOn {
			p := IndexPath(JoinPath(IndexPath("tasks", i), "depends_on"), j)
			if _, ok := idx[d]; !ok {
				v.add(CodeUnknownDependency, p, "task %q depends on unknown task %q", t.ID, d)
			}
			if d == t.ID {
				v.add(CodeDependencyCycle, p, "task %q depends on itself", t.ID)
			}
			if seen[d] {
				v.add(CodeInvalidValue, p, "duplicate dependency %q", d)
			}
			seen[d] = true
		}
	}
	if cyc := findCycle(v.f.Tasks, idx); len(cyc) > 0 {
		first := idx[cyc[0]]
		v.add(CodeDependencyCycle, JoinPath(IndexPath("tasks", first), "depends_on"), "dependency cycle: %s", strings.Join(cyc, " -> "))
	}
}

// findCycle returns one cycle as task IDs, or nil.
func findCycle(tasks []Task, idx map[string]int) []string {
	const (
		white = iota
		gray
		black
	)
	color := map[string]int{}
	var stack []string
	var cycle []string
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = gray
		stack = append(stack, id)
		i, ok := idx[id]
		if ok {
			for _, d := range tasks[i].DependsOn {
				if _, known := idx[d]; !known || d == id {
					continue
				}
				switch color[d] {
				case gray:
					for k, s := range stack {
						if s == d {
							cycle = append(append([]string{}, stack[k:]...), d)
							return true
						}
					}
				case white:
					if visit(d) {
						return true
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}
	ids := make([]string, 0, len(idx))
	for id := range idx {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == white && visit(id) {
			return cycle
		}
	}
	return nil
}

// TransitiveDeps returns the transitive dependencies of each task.
func TransitiveDeps(tasks []Task) map[string]map[string]bool {
	idx := map[string]int{}
	for i, t := range tasks {
		idx[t.ID] = i
	}
	out := map[string]map[string]bool{}
	var walk func(id string, acc map[string]bool, depth int)
	walk = func(id string, acc map[string]bool, depth int) {
		i, ok := idx[id]
		if !ok || depth > len(tasks) {
			return
		}
		for _, d := range tasks[i].DependsOn {
			if !acc[d] {
				acc[d] = true
				walk(d, acc, depth+1)
			}
		}
	}
	for _, t := range tasks {
		acc := map[string]bool{}
		walk(t.ID, acc, 0)
		out[t.ID] = acc
	}
	return out
}

func (v *validation) triggers() {
	seen := map[string]bool{}
	for i, tr := range v.f.Triggers {
		p := IndexPath("triggers", i)
		if seen[tr.ID] {
			v.add(CodeDuplicateTriggerID, JoinPath(p, "id"), "duplicate trigger ID %q", tr.ID)
		}
		seen[tr.ID] = true
		allowed := map[string][]string{
			"schedule": {"cron", "timezone", "catch_up", "inputs"},
			"webhook":  {"inputs"},
			"flow":     {"flow", "states", "inputs"},
		}[tr.Type]
		present := map[string]bool{"cron": tr.Cron != "", "timezone": tr.Timezone != "", "catch_up": tr.CatchUp != "",
			"flow": tr.Flow != "", "states": len(tr.States) > 0, "inputs": len(tr.Inputs) > 0}
		for f, ok := range present {
			if ok && !contains(allowed, f) {
				v.add(CodeFieldNotAllowed, JoinPath(p, f), "field %s is not allowed on %s triggers", f, tr.Type)
			}
		}
		switch tr.Type {
		case "schedule":
			if tr.Cron == "" {
				v.add(CodeMissingField, p, "schedule trigger %q needs cron", tr.ID)
			} else if _, err := ParseCron(tr.Cron); err != nil {
				v.add(CodeInvalidCron, JoinPath(p, "cron"), "invalid cron %q: %v", tr.Cron, err)
			}
			if tr.Timezone != "" {
				if _, err := time.LoadLocation(tr.Timezone); err != nil || tr.Timezone == "Local" {
					v.add(CodeUnknownTimezone, JoinPath(p, "timezone"), "unknown time zone %q", tr.Timezone)
				}
			}
		case "flow":
			if tr.Flow == "" {
				v.add(CodeMissingField, p, "flow trigger %q needs flow", tr.ID)
			} else if _, _, ok := ParseFlowRef(tr.Flow); !ok {
				v.add(CodeInvalidReference, JoinPath(p, "flow"), "flow must be <namespace>/<flow_id>, got %q", tr.Flow)
			}
			for j, s := range tr.States {
				if !contains([]string{"SUCCESS", "FAILED", "TIMED_OUT", "CANCELLED"}, s) {
					v.add(CodeInvalidValue, IndexPath(JoinPath(p, "states"), j), "state %q is not SUCCESS, FAILED, TIMED_OUT or CANCELLED", s)
				}
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (v *validation) durations() {
	check := func(p string, d Duration) {
		if d == "" {
			return
		}
		if x, err := time.ParseDuration(string(d)); err != nil || x <= 0 {
			v.add(CodeInvalidDuration, p, "invalid duration %q", d)
		}
	}
	retry := func(p string, r *Retry) {
		if r == nil {
			return
		}
		check(JoinPath(p, "initial"), r.Initial)
		check(JoinPath(p, "max"), r.Max)
	}
	check("timeout", v.f.Timeout)
	retry("retry", v.f.Retry)
	for i, t := range v.f.Tasks {
		p := IndexPath("tasks", i)
		check(JoinPath(p, "timeout"), t.Timeout)
		retry(JoinPath(p, "retry"), t.Retry)
	}
}

// templateSite describes where a template is.
type templateSite struct {
	path        string
	value       string
	allowSecret bool
	task        *Task // nil for flow-level sites
	flowOutputs bool
	triggerSite bool
}

func (v *validation) templates(idx map[string]int) {
	deps := TransitiveDeps(v.f.Tasks)
	inputs := map[string]bool{}
	for _, in := range v.f.Inputs {
		inputs[in.ID] = true
	}
	var sites []templateSite
	for k, val := range v.f.Env {
		sites = append(sites, templateSite{path: JoinPath("env", k), value: val, allowSecret: true})
	}
	for k, val := range v.f.Outputs {
		sites = append(sites, templateSite{path: JoinPath("outputs", k), value: val, flowOutputs: true})
	}
	for i, tr := range v.f.Triggers {
		for k, val := range tr.Inputs {
			sites = append(sites, templateSite{path: JoinPath(JoinPath(IndexPath("triggers", i), "inputs"), k), value: val, triggerSite: true})
		}
	}
	for i := range v.f.Tasks {
		t := &v.f.Tasks[i]
		p := IndexPath("tasks", i)
		for k, val := range t.Env {
			sites = append(sites, templateSite{path: JoinPath(JoinPath(p, "env"), k), value: val, allowSecret: true, task: t})
		}
		for j, a := range t.Args {
			sites = append(sites, templateSite{path: IndexPath(JoinPath(p, "args"), j), value: a, task: t})
		}
		for j, a := range t.Command {
			sites = append(sites, templateSite{path: IndexPath(JoinPath(p, "command"), j), value: a, task: t})
		}
		if t.URL != "" {
			sites = append(sites, templateSite{path: JoinPath(p, "url"), value: t.URL, allowSecret: true, task: t})
		}
		for k, val := range t.Headers {
			sites = append(sites, templateSite{path: JoinPath(JoinPath(p, "headers"), k), value: val, allowSecret: true, task: t})
		}
		if t.Body != "" {
			sites = append(sites, templateSite{path: JoinPath(p, "body"), value: t.Body, allowSecret: true, task: t})
		}
		for k, val := range t.Inputs {
			sites = append(sites, templateSite{path: JoinPath(JoinPath(p, "inputs"), k), value: val, task: t})
		}
		// Fields where templates are not allowed.
		for name, val := range map[string]string{"file": t.File, "workdir": t.Workdir, "flow": t.Flow} {
			if strings.Contains(strings.ReplaceAll(val, "$${{", ""), "${{") {
				v.add(CodeTemplateNotAllowed, JoinPath(p, name), "templates are not allowed in %s", name)
			}
		}
	}
	sort.Slice(sites, func(a, b int) bool { return sites[a].path < sites[b].path })
	for _, s := range sites {
		tpl, err := ParseTemplate(s.value)
		if err != nil {
			v.add(CodeInvalidTemplate, s.path, "%v", err)
			continue
		}
		for _, r := range tpl.Refs() {
			// A trigger renders its inputs over the trigger payload only (§6.5), so other
			// references have no value at fire time.
			if s.triggerSite {
				if r.Kind != RefTrigger {
					v.add(CodeTriggerInputRef, s.path, "trigger inputs can use only trigger.<path>: %q has no value when the trigger fires", r.Expr)
				}
				continue
			}
			switch r.Kind {
			case RefSecret:
				if !s.allowSecret {
					v.add(CodeSecretNotAllowed, s.path, "secret() is allowed only in env values and http url, headers and body")
				}
			case RefInputs:
				if !inputs[r.Key()] {
					v.add(CodeUnknownInput, s.path, "unknown input %q", r.Key())
				}
			case RefTasks:
				if _, ok := idx[r.Key()]; !ok {
					v.add(CodeUnknownTask, s.path, "unknown task %q", r.Key())
					continue
				}
				if s.task != nil && !deps[s.task.ID][r.Key()] {
					v.add(CodeOutputNotDependency, s.path, "task %q uses outputs of %q, which is not a dependency", s.task.ID, r.Key())
				}
				if s.task == nil && !s.flowOutputs {
					v.add(CodeInvalidTemplate, s.path, "flow env cannot use task outputs")
				}
			}
		}
	}
}
