package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/execution/executiondb"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/runnerproto"
)

// SecretResolver resolves secret('KEY') references for a namespace (REQ-SEC-003).
type SecretResolver interface {
	Resolve(ctx context.Context, namespace, key string) (string, error)
}

// SecretNotFoundError names the key and the scopes searched (REQ-SEC-009).
type SecretNotFoundError struct {
	Key    string
	Scopes []string
}

func (e *SecretNotFoundError) Error() string {
	return fmt.Sprintf("secret %q not found in scopes %s", e.Key, strings.Join(e.Scopes, ", "))
}

// NamespaceChain returns the namespace and its parents, nearest first.
func NamespaceChain(ns string) []string {
	var out []string
	for n := ns; n != ""; {
		out = append(out, n)
		i := strings.LastIndex(n, ".")
		if i < 0 {
			break
		}
		n = n[:i]
	}
	return out
}

// Scopes returns the search scopes of a namespace with "global" last.
func Scopes(ns string) []string { return append(NamespaceChain(ns), "global") }

// PlanError fails a task before any process starts (REQ-EXE-012, REQ-SEC-009).
type PlanError struct {
	Reason string
	Msg    string
}

func (e *PlanError) Error() string { return e.Msg }

// Plan is the resolved work of one task run.
type Plan struct {
	TaskRun    executiondb.TaskRun
	Exec       execInfo
	Def        *Definition
	Cfg        TaskConfig
	Env        map[string]string
	Command    []string
	Runtime    string
	MaskValues []string
	SecretKeys []string
	// http task
	Method  string
	URL     string
	Headers map[string]string
	Body    string
	// subflow task
	SubflowInputs map[string]string
}

type execInfo struct {
	ID          uuid.UUID
	NamespaceID uuid.UUID
	FlowID      *uuid.UUID
	SnapshotID  uuid.UUID
	CreatedAt   time.Time
	Inputs      json.RawMessage
	Payload     json.RawMessage
	ChainDepth  int
}

func infoOf(ex executiondb.Execution) execInfo {
	return execInfo{ID: ex.ID, NamespaceID: ex.NamespaceID, FlowID: ex.FlowID, SnapshotID: ex.SnapshotID, CreatedAt: ex.CreatedAt,
		Inputs: ex.Inputs, Payload: ex.TriggerPayload, ChainDepth: int(ex.ChainDepth)}
}

func infoOfRow(ex executiondb.GetExecutionRow) execInfo {
	return execInfo{ID: ex.ID, NamespaceID: ex.NamespaceID, FlowID: ex.FlowID, SnapshotID: ex.SnapshotID, CreatedAt: ex.CreatedAt,
		Inputs: ex.Inputs, Payload: ex.TriggerPayload, ChainDepth: int(ex.ChainDepth)}
}

// secretRecorder resolves secrets and records keys and values for masking.
type secretRecorder struct {
	ctx      context.Context
	resolver SecretResolver
	ns       string
	mu       sync.Mutex
	keys     map[string]bool
	values   []string
}

func (s *secretRecorder) resolve(key string) (string, error) {
	if s.resolver == nil {
		return "", &SecretNotFoundError{Key: key, Scopes: Scopes(s.ns)}
	}
	v, err := s.resolver.Resolve(s.ctx, s.ns, key)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.keys == nil {
		s.keys = map[string]bool{}
	}
	s.keys[key] = true
	s.values = append(s.values, v)
	s.mu.Unlock()
	return v, nil
}

func (s *secretRecorder) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.keys))
	for k := range s.keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// variables returns vars with the precedence flow variables, namespace, parents, global (§6.7).
func (e *Engine) variables(ctx context.Context, ns string, flowVars map[string]string) (map[string]string, error) {
	out := map[string]string{}
	chain := NamespaceChain(ns)
	rows, err := e.Pool.Query(ctx, `SELECT coalesce(n.name, ''), v.key, v.value FROM variables v
		LEFT JOIN namespaces n ON n.id = v.namespace_id AND n.deleted_at IS NULL
		WHERE v.namespace_id IS NULL OR n.name = ANY($1::text[])`, chain)
	if err != nil {
		return nil, err
	}
	rank := map[string]int{"": len(chain)}
	for i, n := range chain {
		rank[n] = i
	}
	best := map[string]int{}
	for rows.Next() {
		var n, k, v string
		if err := rows.Scan(&n, &k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		r, ok := rank[n]
		if !ok {
			continue
		}
		if cur, seen := best[k]; !seen || r < cur {
			best[k] = r
			out[k] = v
		}
	}
	rows.Close()
	for k, v := range flowVars {
		out[k] = v
	}
	return out, rows.Err()
}

func (e *Engine) templateContext(ctx context.Context, ex execInfo, def *Definition, latest map[string]executiondb.TaskRun, secret func(string) (string, error)) (*flow.Context, error) {
	var inputs, payload map[string]any
	_ = json.Unmarshal(ex.Inputs, &inputs)
	_ = json.Unmarshal(ex.Payload, &payload)
	vars, err := e.variables(ctx, def.Namespace, def.Flow.Variables)
	if err != nil {
		return nil, err
	}
	tasks := map[string]map[string]any{}
	for k, tr := range latest {
		if tr.State != TaskSuccess || len(tr.Outputs) == 0 {
			continue
		}
		var o map[string]any
		if json.Unmarshal(tr.Outputs, &o) == nil {
			tasks[k] = o
		}
	}
	flowID := def.FlowKey
	return &flow.Context{Inputs: nonNilMap(inputs), Vars: vars, Tasks: tasks, Trigger: nonNilMap(payload), Secret: secret,
		Execution: map[string]any{"id": ex.ID.String(), "namespace": def.Namespace, "flow_id": flowID, "created_at": ex.CreatedAt.UTC().Format(time.RFC3339)}}, nil
}

func planErr(err error) error {
	var snf *SecretNotFoundError
	if errors.As(err, &snf) {
		return &PlanError{Reason: ReasonSecretNotFound, Msg: snf.Error()}
	}
	var pe *PlanError
	if errors.As(err, &pe) {
		return pe
	}
	return &PlanError{Reason: ReasonTemplateError, Msg: err.Error()}
}

// BuildPlan resolves the templates of a task run at dispatch (REQ-EXE-012).
func (e *Engine) BuildPlan(ctx context.Context, tr executiondb.TaskRun) (*Plan, error) {
	q := executiondb.New(e.Pool)
	row, err := q.GetExecution(ctx, tr.ExecutionID)
	if err != nil {
		return nil, err
	}
	def, err := ParseDefinition(row.Definition)
	if err != nil {
		return nil, err
	}
	t, ok := def.Task(tr.TaskKey)
	if !ok {
		return nil, &PlanError{Reason: ReasonTemplateError, Msg: "task not in definition"}
	}
	runs, err := q.ListExecutionTaskRuns(ctx, tr.ExecutionID)
	if err != nil {
		return nil, err
	}
	rec := &secretRecorder{ctx: ctx, resolver: e.Secrets, ns: def.Namespace}
	info := infoOfRow(row)
	tc, err := e.templateContext(ctx, info, def, latestRuns(runs), rec.resolve)
	if err != nil {
		return nil, err
	}
	p := &Plan{TaskRun: tr, Exec: info, Def: def, Cfg: def.Config(*t), Env: map[string]string{}}
	render := func(field, s string, allowSecret bool) (string, error) {
		c := tc
		if !allowSecret {
			cc := *tc
			cc.Secret = nil
			c = &cc
		}
		v, err := flow.RenderString(s, c)
		if err != nil {
			return "", planErr(fmt.Errorf("%s: %w", field, err))
		}
		return v, nil
	}
	for _, k := range sortedKeys(p.Cfg.Env) {
		v, err := render("env."+k, p.Cfg.Env[k], true)
		if err != nil {
			return nil, err
		}
		p.Env[k] = v
	}
	switch t.Type {
	case "script":
		args := make([]string, 0, len(t.Args))
		for i, a := range t.Args {
			v, err := render(fmt.Sprintf("args[%d]", i), a, false)
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		}
		p.Command, p.Runtime = ScriptCommand(*t, args)
		if len(p.Command) == 0 {
			return nil, &PlanError{Reason: ReasonTemplateError, Msg: "no runtime for " + t.File}
		}
	case "command":
		for i, a := range t.Command {
			v, err := render(fmt.Sprintf("command[%d]", i), a, false)
			if err != nil {
				return nil, err
			}
			p.Command = append(p.Command, v)
		}
	case "http":
		p.Method = t.Method
		if p.Method == "" {
			p.Method = "GET"
		}
		if p.URL, err = render("url", t.URL, true); err != nil {
			return nil, err
		}
		p.Headers = map[string]string{}
		for _, k := range sortedKeys(t.Headers) {
			v, err := render("headers."+k, t.Headers[k], true)
			if err != nil {
				return nil, err
			}
			p.Headers[k] = v
		}
		if p.Body, err = render("body", t.Body, true); err != nil {
			return nil, err
		}
	case "subflow":
		p.SubflowInputs = map[string]string{}
		for _, k := range sortedKeys(t.Inputs) {
			v, err := render("inputs."+k, t.Inputs[k], false)
			if err != nil {
				return nil, err
			}
			p.SubflowInputs[k] = v
		}
	}
	p.MaskValues = rec.values
	p.SecretKeys = rec.Keys()
	return p, nil
}

// RunnerSpec builds the spec of GET /task-runs/{id}/spec.
func (e *Engine) RunnerSpec(ctx context.Context, tr executiondb.TaskRun) (*runnerproto.Spec, error) {
	p, err := e.BuildPlan(ctx, tr)
	if err != nil {
		return nil, err
	}
	sn, err := executiondb.New(e.Pool).GetSnapshot(ctx, p.Exec.SnapshotID)
	if err != nil {
		return nil, err
	}
	return &runnerproto.Spec{TaskRunID: tr.ID.String(), ExecutionID: tr.ExecutionID.String(), Namespace: p.Def.Namespace, FlowID: p.Def.FlowKey,
		TaskID: tr.TaskKey, Attempt: int(tr.Attempt), Command: p.Command, Workdir: p.Cfg.Task.Workdir, Env: p.Env, Runtime: p.Runtime,
		TimeoutSeconds: int(p.Cfg.Timeout.Seconds()), MaskValues: nonNilStrings(p.MaskValues), BundleHash: sn.ManifestHash,
		Limits: runnerproto.Limits{MaxArtifactBytes: e.Cfg.MaxArtifactBytes, MaxBundleBytes: e.Cfg.MaxBundleBytes}}, nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// recordSecretKeys adds the used secret keys to the execution (REQ-SEC-008).
func (e *Engine) recordSecretKeys(ctx context.Context, execID uuid.UUID, keys []string) {
	if len(keys) == 0 {
		return
	}
	_, err := e.Pool.Exec(ctx, `UPDATE executions SET secret_keys_used = ARRAY(SELECT DISTINCT unnest(secret_keys_used || $2::text[]) ORDER BY 1)
		WHERE id = $1`, execID, keys)
	if err != nil {
		e.Log.Warn("record secret keys", "err", err)
	}
}
