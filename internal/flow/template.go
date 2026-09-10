package flow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Template reference kinds (§6.7).
const (
	RefInputs    = "inputs"
	RefVars      = "vars"
	RefSecret    = "secret"
	RefTasks     = "tasks"
	RefTrigger   = "trigger"
	RefExecution = "execution"
)

// ExecutionFields are the allowed execution.<field> lookups.
var ExecutionFields = map[string]bool{"id": true, "namespace": true, "flow_id": true, "created_at": true}

// Ref is one ${{ expr }} reference.
type Ref struct {
	Kind string   // inputs, vars, secret, tasks, trigger, execution
	Path []string // lookup path after the kind; for tasks: [task_id, "outputs", key]
	Expr string   // the expression text
	Pos  int      // byte offset of "${{" in the template
}

// Key returns the first path element (input ID, variable key, secret key, task ID).
func (r Ref) Key() string {
	if len(r.Path) == 0 {
		return ""
	}
	return r.Path[0]
}

type segment struct {
	lit string
	ref *Ref
}

// Template is a parsed template string.
type Template struct {
	src  string
	segs []segment
}

// TemplateError is a template syntax or resolution error.
type TemplateError struct {
	Pos int
	Msg string
}

func (e *TemplateError) Error() string { return e.Msg }

var (
	identRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
	secretRe = regexp.MustCompile(`^secret\(\s*(?:'([^']+)'|"([^"]+)")\s*\)$`)
)

// ParseTemplate parses a template. "$${{" is a literal "${{".
func ParseTemplate(s string) (*Template, error) {
	t := &Template{src: s}
	var lit strings.Builder
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], "$${{") {
			lit.WriteString("${{")
			i += 4
			continue
		}
		if strings.HasPrefix(s[i:], "${{") {
			end := strings.Index(s[i+3:], "}}")
			if end < 0 {
				return nil, &TemplateError{Pos: i, Msg: "unclosed ${{"}
			}
			expr := strings.TrimSpace(s[i+3 : i+3+end])
			ref, err := parseExpr(expr)
			if err != nil {
				return nil, &TemplateError{Pos: i, Msg: err.Error()}
			}
			ref.Pos = i
			if lit.Len() > 0 {
				t.segs = append(t.segs, segment{lit: lit.String()})
				lit.Reset()
			}
			t.segs = append(t.segs, segment{ref: ref})
			i += 3 + end + 2
			continue
		}
		lit.WriteByte(s[i])
		i++
	}
	if lit.Len() > 0 {
		t.segs = append(t.segs, segment{lit: lit.String()})
	}
	return t, nil
}

func parseExpr(expr string) (*Ref, error) {
	if expr == "" {
		return nil, fmt.Errorf("empty expression")
	}
	if strings.HasPrefix(expr, "secret(") {
		m := secretRe.FindStringSubmatch(expr)
		if m == nil {
			return nil, fmt.Errorf("invalid secret() call %q", expr)
		}
		key := m[1] + m[2]
		return &Ref{Kind: RefSecret, Path: []string{key}, Expr: expr}, nil
	}
	parts := strings.Split(expr, ".")
	for _, p := range parts {
		if !identRe.MatchString(p) {
			return nil, fmt.Errorf("invalid expression %q: expressions are lookups only", expr)
		}
	}
	ref := &Ref{Kind: parts[0], Path: parts[1:], Expr: expr}
	switch ref.Kind {
	case RefInputs, RefVars:
		if len(ref.Path) != 1 {
			return nil, fmt.Errorf("%q: use %s.<key>", expr, ref.Kind)
		}
	case RefTasks:
		if len(ref.Path) != 3 || ref.Path[1] != "outputs" {
			return nil, fmt.Errorf("%q: use tasks.<task_id>.outputs.<key>", expr)
		}
	case RefTrigger:
		if len(ref.Path) < 1 {
			return nil, fmt.Errorf("%q: use trigger.<path>", expr)
		}
	case RefExecution:
		if len(ref.Path) != 1 || !ExecutionFields[ref.Path[0]] {
			return nil, fmt.Errorf("%q: use execution.id, execution.namespace, execution.flow_id or execution.created_at", expr)
		}
	default:
		return nil, fmt.Errorf("unknown reference %q", parts[0])
	}
	return ref, nil
}

// Refs returns the references of the template.
func (t *Template) Refs() []Ref {
	var out []Ref
	for _, s := range t.segs {
		if s.ref != nil {
			out = append(out, *s.ref)
		}
	}
	return out
}

// IsStatic reports whether the template has no references.
func (t *Template) IsStatic() bool { return len(t.Refs()) == 0 }

// Context holds the values for template resolution.
type Context struct {
	Inputs    map[string]any
	Vars      map[string]string
	Tasks     map[string]map[string]any // task ID -> outputs
	Trigger   map[string]any
	Execution map[string]any
	// Secret returns a secret value. Nil means secret() is not allowed here.
	Secret func(key string) (string, error)
}

// Render resolves the template. Strings render raw; other JSON values render as compact JSON.
func (t *Template) Render(c *Context) (string, error) {
	var b strings.Builder
	for _, s := range t.segs {
		if s.ref == nil {
			b.WriteString(s.lit)
			continue
		}
		v, err := c.lookup(*s.ref)
		if err != nil {
			return "", &TemplateError{Pos: s.ref.Pos, Msg: err.Error()}
		}
		b.WriteString(renderValue(v))
	}
	return b.String(), nil
}

func renderValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	default:
		j, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(j)
	}
}

func (c *Context) lookup(r Ref) (any, error) {
	switch r.Kind {
	case RefInputs:
		v, ok := c.Inputs[r.Key()]
		if !ok {
			return nil, fmt.Errorf("input %q has no value", r.Key())
		}
		return v, nil
	case RefVars:
		v, ok := c.Vars[r.Key()]
		if !ok {
			return nil, fmt.Errorf("variable %q is not defined", r.Key())
		}
		return v, nil
	case RefSecret:
		if c.Secret == nil {
			return nil, fmt.Errorf("secret() is not allowed here")
		}
		return c.Secret(r.Key())
	case RefTasks:
		outs, ok := c.Tasks[r.Path[0]]
		if !ok {
			return nil, fmt.Errorf("task %q has no outputs", r.Path[0])
		}
		v, ok := outs[r.Path[2]]
		if !ok {
			return nil, fmt.Errorf("task %q has no output %q", r.Path[0], r.Path[2])
		}
		return v, nil
	case RefTrigger:
		var cur any = c.Trigger
		for _, p := range r.Path {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("trigger.%s is not defined", strings.Join(r.Path, "."))
			}
			v, ok := lookupFold(m, p)
			if !ok {
				return nil, fmt.Errorf("trigger.%s is not defined", strings.Join(r.Path, "."))
			}
			cur = v
		}
		return cur, nil
	case RefExecution:
		v, ok := c.Execution[r.Key()]
		if !ok {
			return nil, fmt.Errorf("execution.%s is not defined", r.Key())
		}
		return v, nil
	}
	return nil, fmt.Errorf("unknown reference %q", r.Kind)
}

// lookupFold finds a key, then falls back to a case-insensitive match (HTTP headers).
func lookupFold(m map[string]any, k string) (any, bool) {
	if v, ok := m[k]; ok {
		return v, true
	}
	for mk, v := range m {
		if strings.EqualFold(mk, k) {
			return v, true
		}
	}
	return nil, false
}

// RenderString parses and renders s.
func RenderString(s string, c *Context) (string, error) {
	t, err := ParseTemplate(s)
	if err != nil {
		return "", err
	}
	return t.Render(c)
}
