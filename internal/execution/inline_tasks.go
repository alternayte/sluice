package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/execution/executiondb"
	"github.com/alternayte/sluice/internal/executor"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/platform/masking"
)

// MaxSubflowDepth is the deepest allowed subflow chain (REQ-EXE-016).
const MaxSubflowDepth = 10

// maxHTTPBody is the limit of an http task response body in outputs (§6.4).
const maxHTTPBody = 1 << 20

// RunInline runs an http or subflow task in this instance. It is the InlineFunc of the
// inline executor.
func (e *Engine) RunInline(ctx context.Context, t executor.Task) executor.Result {
	tr, err := executiondb.New(e.Pool).GetTaskRun(ctx, t.TaskRunID)
	if err != nil {
		return executor.Result{ExitCode: 1, Err: err}
	}
	plan, err := e.BuildPlan(ctx, tr)
	if err != nil {
		var pe *PlanError
		reason, msg := ReasonTemplateError, err.Error()
		if errors.As(err, &pe) {
			reason, msg = pe.Reason, pe.Msg
		}
		e.SystemLog(ctx, tr, "[sluice] "+reason+": "+msg)
		e.finish(ctx, tr.ID, TaskFailed, reason, msg, nil)
		return executor.Result{ExitCode: 1}
	}
	switch tr.TaskType {
	case "http":
		e.runHTTP(ctx, plan)
	case "subflow":
		e.runSubflow(ctx, plan)
	default:
		e.finish(ctx, tr.ID, TaskFailed, ReasonExecutor, "task type "+tr.TaskType+" does not run inline", nil)
	}
	return executor.Result{}
}

// runHTTP sends the request, records status, headers and body as outputs and fails on an
// unexpected status (REQ-EXE-017).
func (e *Engine) runHTTP(ctx context.Context, p *Plan) {
	m := masking.New(p.MaskValues)
	tr := p.TaskRun
	ctx, cancel := context.WithTimeout(ctx, p.Cfg.Timeout)
	defer cancel()
	var body io.Reader
	if p.Body != "" {
		body = strings.NewReader(p.Body)
	}
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, body)
	if err != nil {
		msg := m.String(err.Error())
		e.SystemLog(ctx, tr, "[sluice] http: "+msg)
		e.finish(context.WithoutCancel(ctx), tr.ID, TaskFailed, ReasonExecutor, msg, nil)
		return
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	e.SystemLog(ctx, tr, m.String(fmt.Sprintf("[sluice] http %s %s", p.Method, p.URL)))
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		state, reason := TaskFailed, ReasonExecutor
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			state, reason = TaskTimedOut, ReasonTimeout
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			state, reason = TaskCancelled, ReasonCancelled
		}
		msg := m.String(err.Error())
		e.SystemLog(context.WithoutCancel(ctx), tr, "[sluice] http error: "+msg)
		e.finish(context.WithoutCancel(ctx), tr.ID, state, reason, msg, nil)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBody+1))
	if len(raw) > maxHTTPBody {
		raw = raw[:maxHTTPBody]
	}
	headers := map[string]any{}
	for k := range resp.Header {
		headers[k] = m.String(resp.Header.Get(k))
	}
	var bodyVal any = m.String(string(raw))
	if masked := m.Bytes(raw); json.Valid(masked) {
		var v any
		if json.Unmarshal(masked, &v) == nil {
			bodyVal = v
		}
	}
	outputs, _ := json.Marshal(map[string]any{"status": resp.StatusCode, "headers": headers, "body": bodyVal})
	if err := executiondb.New(e.Pool).MergeTaskOutputs(ctx, executiondb.MergeTaskOutputsParams{ID: tr.ID, Outputs: outputs}); err != nil {
		e.Log.Warn("http outputs", "err", err)
	}
	e.SystemLog(ctx, tr, fmt.Sprintf("[sluice] http status %d in %s", resp.StatusCode, time.Since(start).Round(time.Millisecond)))
	expected := p.Cfg.Task.ExpectStatus
	ok := false
	if len(expected) == 0 {
		ok = resp.StatusCode >= 200 && resp.StatusCode <= 299
	}
	for _, s := range expected {
		if s == resp.StatusCode {
			ok = true
		}
	}
	code := resp.StatusCode
	if ok {
		e.finish(ctx, tr.ID, TaskSuccess, "", "", &code)
		return
	}
	e.finish(ctx, tr.ID, TaskFailed, ReasonHTTPStatus, fmt.Sprintf("unexpected status %d", resp.StatusCode), &code)
}

// runSubflow creates the child execution. With wait the task ends when the child ends
// (subflowParentEnd). Depth above 10 fails with depth_exceeded (REQ-EXE-016).
func (e *Engine) runSubflow(ctx context.Context, p *Plan) {
	tr := p.TaskRun
	ns, key, ok := flow.ParseFlowRef(p.Cfg.Task.Flow)
	if !ok {
		e.finish(ctx, tr.ID, TaskFailed, ReasonTemplateError, "invalid subflow reference", nil)
		return
	}
	depth := p.Exec.ChainDepth + 1
	if depth > MaxSubflowDepth {
		msg := fmt.Sprintf("subflow depth %d exceeds %d", depth, MaxSubflowDepth)
		e.SystemLog(ctx, tr, "[sluice] "+msg)
		e.finish(ctx, tr.ID, TaskFailed, ReasonDepthExceeded, msg, nil)
		return
	}
	ref, err := e.LoadFlow(ctx, ns, key)
	if err != nil {
		e.SystemLog(ctx, tr, "[sluice] subflow "+p.Cfg.Task.Flow+": "+err.Error())
		e.finish(ctx, tr.ID, TaskFailed, ReasonExecutor, err.Error(), nil)
		return
	}
	given := map[string]any{}
	for k, v := range p.SubflowInputs {
		given[k] = parseMaybeJSON(v)
	}
	// Inputs from templates are strings; coerce them to the declared types.
	for _, in := range ref.Def.Flow.Inputs {
		if v, ok := given[in.ID]; ok {
			if s, isStr := v.(string); isStr && in.Type == "string" {
				given[in.ID] = s
			}
		}
	}
	inputs, ierrs := flow.ResolveInputs(ref.Def.Flow.Inputs, given)
	if err := InputErrors(ierrs); err != nil {
		e.finish(ctx, tr.ID, TaskFailed, ReasonTemplateError, fmt.Sprintf("subflow inputs: %v", ierrs), nil)
		return
	}
	wait := p.Cfg.Task.Wait == nil || *p.Cfg.Task.Wait
	parentExec := tr.ExecutionID
	parentTask := tr.ID
	err = pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		cp := CreateParams{NamespaceID: ref.Flow.NamespaceID, FlowID: &ref.Flow.ID, RevisionID: &ref.Revision.ID, SnapshotID: ref.Revision.SnapshotID,
			Def: ref.Def, TriggerType: "subflow", Inputs: inputs, ChainDepth: depth, ParentExecID: &parentExec,
			TriggerPayload: map[string]any{"parent_execution_id": parentExec.String(), "parent_task": tr.TaskKey}}
		if wait {
			cp.ParentTaskRun = &parentTask
		}
		childID, _, err := e.Create(ctx, tx, cp)
		if err != nil {
			return err
		}
		if err := executiondb.New(tx).SetTaskRunChild(ctx, executiondb.SetTaskRunChildParams{ID: tr.ID, ChildExecutionID: &childID}); err != nil {
			return err
		}
		if !wait {
			out, _ := json.Marshal(map[string]any{"execution_id": childID.String()})
			if err := executiondb.New(tx).MergeTaskOutputs(ctx, executiondb.MergeTaskOutputsParams{ID: tr.ID, Outputs: out}); err != nil {
				return err
			}
			return e.finishTaskTx(ctx, tx, tr.ID, []string{TaskRunning}, TaskSuccess, "", "", nil)
		}
		return nil
	})
	if err != nil {
		e.finish(context.WithoutCancel(ctx), tr.ID, TaskFailed, ReasonExecutor, err.Error(), nil)
		return
	}
	e.SystemLog(ctx, tr, "[sluice] started subflow "+p.Cfg.Task.Flow)
}
