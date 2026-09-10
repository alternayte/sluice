package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/page"
)

// Labels are the labels of a trigger request. The schema limits the count and the value
// length as api/openapi.yaml TriggerRequest does.
type Labels map[string]string

// Schema implements huma.SchemaProvider.
func (Labels) Schema(huma.Registry) *huma.Schema {
	maxProps, maxLen := 20, 256
	return &huma.Schema{Type: huma.TypeObject, MaxProperties: &maxProps,
		AdditionalProperties: &huma.Schema{Type: huma.TypeString, MaxLength: &maxLen}}
}

// TriggerRequest is the body of triggerFlow.
type TriggerRequest struct {
	Inputs map[string]any `json:"inputs,omitempty"`
	Labels Labels         `json:"labels,omitempty"`
}

// RunFileRequest is the body of runFile.
type RunFileRequest struct {
	Path string   `json:"path" maxLength:"512"`
	Args []string `json:"args,omitempty" maxItems:"100"`
}

// ExecutionSummary is one execution without its task runs.
type ExecutionSummary struct {
	ID          uuid.UUID         `json:"id"`
	Namespace   string            `json:"namespace"`
	FlowID      *string           `json:"flow_id,omitempty" nullable:"true" doc:"Flow ID. Null for file runs."`
	State       string            `json:"state" enum:"QUEUED,RUNNING,CANCELLING,SUCCESS,FAILED,TIMED_OUT,CANCELLED,SKIPPED"`
	TriggerType string            `json:"trigger_type"`
	Labels      map[string]string `json:"labels"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   *time.Time        `json:"started_at,omitempty" nullable:"true"`
	EndedAt     *time.Time        `json:"ended_at,omitempty" nullable:"true"`
	DurationMs  *int64            `json:"duration_ms,omitempty" nullable:"true" format:"int64"`
	Error       string            `json:"error"`
	Reason      string            `json:"reason"`
	CreatedBy   string            `json:"created_by"`
}

// ExecutionList is one page of executions.
type ExecutionList struct {
	Items      []ExecutionSummary `json:"items"`
	NextCursor *string            `json:"next_cursor,omitempty"`
}

// TaskRun is one attempt of one task.
type TaskRun struct {
	ID               uuid.UUID       `json:"id"`
	TaskKey          string          `json:"task_key"`
	TaskType         string          `json:"task_type"`
	Attempt          int             `json:"attempt"`
	State            string          `json:"state" enum:"PENDING,QUEUED,RUNNING,SUCCESS,FAILED,TIMED_OUT,CANCELLED,SKIPPED"`
	Reason           string          `json:"reason"`
	ExecutorType     string          `json:"executor_type"`
	Pool             string          `json:"pool"`
	QueuedAt         *time.Time      `json:"queued_at,omitempty" nullable:"true"`
	StartedAt        *time.Time      `json:"started_at,omitempty" nullable:"true"`
	EndedAt          *time.Time      `json:"ended_at,omitempty" nullable:"true"`
	DurationMs       *int64          `json:"duration_ms,omitempty" nullable:"true" format:"int64"`
	ExitCode         *int            `json:"exit_code,omitempty" nullable:"true"`
	Error            string          `json:"error"`
	Outputs          *map[string]any `json:"outputs,omitempty" nullable:"true"`
	ReusedFromID     *uuid.UUID      `json:"reused_from_id,omitempty" nullable:"true"`
	ChildExecutionID *uuid.UUID      `json:"child_execution_id,omitempty" nullable:"true"`
}

// ExecutionRef names a child execution. The app package maps it to the ExecutionRef schema
// of the namespace package, because the two types have the same JSON form.
type ExecutionRef struct {
	ID        uuid.UUID `json:"id"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// ExecutionDetail is one execution with its task runs.
type ExecutionDetail struct {
	ExecutionSummary
	Inputs            map[string]any  `json:"inputs"`
	Outputs           *map[string]any `json:"outputs,omitempty" nullable:"true"`
	TriggerPayload    map[string]any  `json:"trigger_payload"`
	SnapshotID        uuid.UUID       `json:"snapshot_id"`
	SnapshotVersion   *int            `json:"snapshot_version,omitempty" nullable:"true"`
	GitSha            *string         `json:"git_sha,omitempty"`
	FlowRevisionID    *uuid.UUID      `json:"flow_revision_id,omitempty" nullable:"true"`
	ParentExecutionID *uuid.UUID      `json:"parent_execution_id,omitempty" nullable:"true"`
	RestartOfID       *uuid.UUID      `json:"restart_of_id,omitempty" nullable:"true"`
	ChainDepth        int             `json:"chain_depth"`
	SecretKeysUsed    []string        `json:"secret_keys_used"`
	TaskRuns          []TaskRun       `json:"task_runs"`
	Children          []ExecutionRef  `json:"children"`
}

type detailOut struct{ Body ExecutionDetail }

func rawMap(b []byte) map[string]any {
	m := map[string]any{}
	if len(b) > 0 {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func rawMapPtr(b []byte) *map[string]any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	m := rawMap(b)
	return &m
}

func strMap(b []byte) map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal(b, &m)
	return m
}

func durOf(start, end *time.Time) *int64 {
	if start == nil || end == nil {
		return nil
	}
	d := end.Sub(*start).Milliseconds()
	return &d
}

// Detail returns the API view of an execution.
func (e *Engine) Detail(ctx context.Context, id uuid.UUID) (ExecutionDetail, error) {
	q := dbq.New(e.Pool)
	ex, err := q.GetExecution(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionDetail{}, ErrNotFound
	}
	if err != nil {
		return ExecutionDetail{}, err
	}
	runs, err := q.ListExecutionTaskRuns(ctx, id)
	if err != nil {
		return ExecutionDetail{}, err
	}
	children, err := q.ListChildExecutions(ctx, &id)
	if err != nil {
		return ExecutionDetail{}, err
	}
	d := ExecutionDetail{ExecutionSummary: ExecutionSummary{ID: ex.ID, Namespace: ex.NamespaceName, FlowID: ex.FlowKey, State: ex.State,
		TriggerType: ex.TriggerType, Labels: strMap(ex.Labels), CreatedAt: ex.CreatedAt, StartedAt: ex.StartedAt, EndedAt: ex.EndedAt,
		DurationMs: ex.DurationMs, Error: ex.Error, Reason: ex.Reason, CreatedBy: ex.CreatedBy}, Inputs: rawMap(ex.Inputs),
		Outputs: rawMapPtr(ex.Outputs), TriggerPayload: rawMap(ex.TriggerPayload), SnapshotID: ex.SnapshotID, FlowRevisionID: ex.FlowRevisionID,
		ParentExecutionID: ex.ParentExecutionID, RestartOfID: ex.RestartOfID, ChainDepth: int(ex.ChainDepth),
		SecretKeysUsed: nonNilStrings(ex.SecretKeysUsed), TaskRuns: []TaskRun{}, Children: []ExecutionRef{}}
	if ex.SnapshotVersion != nil {
		v := int(*ex.SnapshotVersion)
		d.SnapshotVersion = &v
	}
	if ex.SnapshotGitSha != "" {
		s := ex.SnapshotGitSha
		d.GitSha = &s
	}
	for _, tr := range runs {
		t := TaskRun{ID: tr.ID, TaskKey: tr.TaskKey, TaskType: tr.TaskType, Attempt: int(tr.Attempt), State: tr.State,
			Reason: tr.Reason, ExecutorType: tr.ExecutorType, Pool: tr.Pool, QueuedAt: tr.QueuedAt, StartedAt: tr.StartedAt, EndedAt: tr.EndedAt,
			DurationMs: durOf(tr.StartedAt, tr.EndedAt), Error: tr.Error, Outputs: rawMapPtr(tr.Outputs), ReusedFromID: tr.ReusedFromID,
			ChildExecutionID: tr.ChildExecutionID}
		if tr.ExitCode != nil {
			c := int(*tr.ExitCode)
			t.ExitCode = &c
		}
		d.TaskRuns = append(d.TaskRuns, t)
	}
	for _, c := range children {
		d.Children = append(d.Children, ExecutionRef{ID: c.ID, State: c.State, CreatedAt: c.CreatedAt})
	}
	return d, nil
}

// listExecutionsIn holds the query of listExecutions. label uses the exploded form: the UI
// sends ?label=a&label=b.
type listExecutionsIn struct {
	State       string    `query:"state" doc:"Comma-separated execution states."`
	Namespace   string    `query:"namespace" doc:"Namespace and its children."`
	Flow        string    `query:"flow" doc:"Flow as <namespace>/<flow_id>."`
	TriggerType string    `query:"trigger_type" enum:"manual,schedule,webhook,flow,file,subflow,rerun,restart"`
	Label       []string  `query:"label,explode" doc:"Label filter key=value. Repeat for several labels."`
	From        time.Time `query:"from"`
	To          time.Time `query:"to"`
	Sort        string    `query:"sort" enum:"created,duration" default:"created"`
	Cursor      string    `query:"cursor" doc:"Opaque cursor from next_cursor of the previous page."`
	Limit       int       `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// listExecutions lists executions with filters and keyset pagination (REQ-API-003, REQ-UI-004).
func (e *Engine) listExecutions(ctx context.Context, p *listExecutionsIn) (ExecutionList, error) {
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if p.State != "" {
		var states []string
		for _, s := range strings.Split(p.State, ",") {
			if s = strings.TrimSpace(strings.ToUpper(s)); s != "" {
				states = append(states, s)
			}
		}
		where = append(where, "e.state = ANY("+arg(states)+"::text[])")
	}
	if p.Namespace != "" {
		a := arg(p.Namespace)
		where = append(where, fmt.Sprintf("(n.name = %s OR starts_with(n.name, %s || '.'))", a, a))
	}
	if p.Flow != "" {
		ns, key, ok := strings.Cut(p.Flow, "/")
		if !ok {
			return ExecutionList{}, httpx.Validation(httpx.FieldError{Field: "flow", Message: "use <namespace>/<flow_id>"})
		}
		where = append(where, "n.name = "+arg(ns)+" AND f.flow_key = "+arg(key))
	}
	if p.TriggerType != "" {
		where = append(where, "e.trigger_type = "+arg(p.TriggerType))
	}
	for _, l := range p.Label {
		k, v, ok := strings.Cut(l, "=")
		if !ok || k == "" {
			return ExecutionList{}, httpx.Validation(httpx.FieldError{Field: "label", Message: "use key=value"})
		}
		b, _ := json.Marshal(map[string]string{k: v})
		where = append(where, "e.labels @> "+arg(string(b))+"::jsonb")
	}
	if !p.From.IsZero() {
		where = append(where, "e.created_at >= "+arg(p.From))
	}
	if !p.To.IsZero() {
		where = append(where, "e.created_at <= "+arg(p.To))
	}
	byDuration := p.Sort == "duration"
	if p.Cursor != "" {
		if byDuration {
			parts, err := page.DecodeStrings(p.Cursor, 2)
			if err != nil {
				return ExecutionList{}, err
			}
			d, err1 := strconv.ParseInt(parts[0], 10, 64)
			id, err2 := uuid.Parse(parts[1])
			if err1 != nil || err2 != nil {
				return ExecutionList{}, httpx.Validation(httpx.FieldError{Field: "cursor", Message: "invalid cursor"})
			}
			where = append(where, fmt.Sprintf("(coalesce(e.duration_ms, -1), e.id) < (%s::bigint, %s::uuid)", arg(d), arg(id)))
		} else {
			ts, id, err := page.Decode(p.Cursor)
			if err != nil {
				return ExecutionList{}, err
			}
			where = append(where, fmt.Sprintf("(e.created_at, e.id) < (%s, %s::uuid)", arg(ts), arg(id)))
		}
	}
	limit := page.Limit(p.Limit)
	sql := `SELECT e.id, n.name, f.flow_key, e.state, e.trigger_type, e.labels, e.created_at, e.started_at, e.ended_at, e.duration_ms,
		e.error, e.reason, e.created_by FROM executions e JOIN namespaces n ON n.id = e.namespace_id LEFT JOIN flows f ON f.id = e.flow_id`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	if byDuration {
		sql += " ORDER BY coalesce(e.duration_ms, -1) DESC, e.id DESC"
	} else {
		sql += " ORDER BY e.created_at DESC, e.id DESC"
	}
	sql += fmt.Sprintf(" LIMIT %d", limit+1)
	rows, err := e.Pool.Query(ctx, sql, args...)
	if err != nil {
		return ExecutionList{}, err
	}
	defer rows.Close()
	out := ExecutionList{Items: []ExecutionSummary{}}
	for rows.Next() {
		var s ExecutionSummary
		var labels []byte
		if err := rows.Scan(&s.ID, &s.Namespace, &s.FlowID, &s.State, &s.TriggerType, &labels, &s.CreatedAt, &s.StartedAt, &s.EndedAt,
			&s.DurationMs, &s.Error, &s.Reason, &s.CreatedBy); err != nil {
			return ExecutionList{}, err
		}
		s.Labels = strMap(labels)
		out.Items = append(out.Items, s)
	}
	if err := rows.Err(); err != nil {
		return ExecutionList{}, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[len(out.Items)-1]
		var c string
		if byDuration {
			d := int64(-1)
			if last.DurationMs != nil {
				d = *last.DurationMs
			}
			c = page.EncodeStrings(strconv.FormatInt(d, 10), last.ID.String())
		} else {
			c = page.Encode(last.CreatedAt, last.ID)
		}
		out.NextCursor = &c
	}
	return out, nil
}

func withStatus(op huma.Operation, status int) huma.Operation {
	op.DefaultStatus = status
	return op
}

// unlimitedBody removes the huma body size limit and read timeout. The old server had no
// limit on these bodies, so the service limits apply as before.
func unlimitedBody(op huma.Operation) huma.Operation {
	op.MaxBodyBytes = -1
	op.BodyReadTimeout = -1
	return op
}

type executionIDIn struct {
	ExecutionID uuid.UUID `path:"executionId"`
}

// Routes registers the execution operations (REQ-TRG-001, REQ-NS-007, REQ-API-003, REQ-RUN-008).
// Registration does not use e: `sluice openapi` passes nil.
func Routes(api huma.API, r chi.Router, e *Engine) {
	viewer := httpx.MinRole(kernel.Viewer)
	operator := httpx.MinRole(kernel.Operator)

	huma.Register(api, unlimitedBody(withStatus(httpx.Op("triggerFlow", http.MethodPost, "/api/v1/flows/{namespace}/{flowId}/executions", operator), http.StatusCreated)),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			FlowID    string `path:"flowId" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]*$"`
			Body      TriggerRequest
		}) (*detailOut, error) {
			id, err := e.Trigger(ctx, TriggerParams{Namespace: in.Namespace, FlowKey: in.FlowID, Inputs: in.Body.Inputs, Labels: in.Body.Labels, TriggerType: "manual"})
			if err != nil {
				return nil, err
			}
			return e.detailOut(ctx, id)
		})

	huma.Register(api, unlimitedBody(withStatus(httpx.Op("runFile", http.MethodPost, "/api/v1/namespaces/{namespace}/run", operator), http.StatusCreated)),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			Body      RunFileRequest
		}) (*detailOut, error) {
			id, err := e.RunFile(ctx, in.Namespace, in.Body.Path, in.Body.Args)
			if err != nil {
				return nil, err
			}
			return e.detailOut(ctx, id)
		})

	huma.Register(api, httpx.Op("listExecutions", http.MethodGet, "/api/v1/executions", viewer),
		func(ctx context.Context, in *listExecutionsIn) (*struct{ Body ExecutionList }, error) {
			list, err := e.listExecutions(ctx, in)
			if err != nil {
				return nil, err
			}
			return &struct{ Body ExecutionList }{Body: list}, nil
		})

	huma.Register(api, httpx.Op("getExecution", http.MethodGet, "/api/v1/executions/{executionId}", viewer),
		func(ctx context.Context, in *executionIDIn) (*detailOut, error) {
			return e.detailOut(ctx, in.ExecutionID)
		})

	huma.Register(api, withStatus(httpx.Op("cancelExecution", http.MethodPost, "/api/v1/executions/{executionId}/cancel", operator), http.StatusAccepted),
		func(ctx context.Context, in *executionIDIn) (*detailOut, error) {
			if err := e.Cancel(ctx, in.ExecutionID); err != nil {
				return nil, err
			}
			return e.detailOut(ctx, in.ExecutionID)
		})

	huma.Register(api, withStatus(httpx.Op("rerunExecution", http.MethodPost, "/api/v1/executions/{executionId}/rerun", operator), http.StatusCreated),
		func(ctx context.Context, in *executionIDIn) (*detailOut, error) {
			id, err := e.Rerun(ctx, in.ExecutionID)
			if err != nil {
				return nil, err
			}
			return e.detailOut(ctx, id)
		})

	huma.Register(api, withStatus(httpx.Op("restartExecution", http.MethodPost, "/api/v1/executions/{executionId}/restart", operator), http.StatusCreated),
		func(ctx context.Context, in *executionIDIn) (*detailOut, error) {
			id, err := e.Restart(ctx, in.ExecutionID)
			if err != nil {
				return nil, err
			}
			return e.detailOut(ctx, id)
		})

	registerLogs(api, r, e, viewer)
	registerArtifacts(api, r, e, viewer)
}

func (e *Engine) detailOut(ctx context.Context, id uuid.UUID) (*detailOut, error) {
	d, err := e.Detail(ctx, id)
	if err != nil {
		return nil, err
	}
	return &detailOut{Body: d}, nil
}

// pathUUID reads the UUID path parameter name of a Raw route. The old validator checked the
// format uuid of each such parameter and named the parameter in a 422 validation_failed.
func pathUUID(req *http.Request, name string, fields *[]httpx.FieldError) uuid.UUID {
	id, err := uuid.Parse(chi.URLParam(req, name))
	if err != nil {
		*fields = append(*fields, httpx.FieldError{Field: name, Message: "value must be a UUID"})
	}
	return id
}

func uuidParam(name string) *huma.Param {
	return &huma.Param{Name: name, In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, Format: "uuid"}}
}
