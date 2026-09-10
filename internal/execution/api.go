package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/api/apigen"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/page"
)

// ExecAPI serves the execution operations on the legacy mux. The huma routes in routes.go
// replace it; Task 13 deletes this file.
type ExecAPI struct {
	E *Engine
}

// detailAPI returns Detail as the legacy apigen type. The two types have the same JSON form.
func (e *Engine) detailAPI(ctx context.Context, id uuid.UUID) (apigen.ExecutionDetail, error) {
	d, err := e.Detail(ctx, id)
	if err != nil {
		return apigen.ExecutionDetail{}, err
	}
	var out apigen.ExecutionDetail
	b, _ := json.Marshal(d)
	if err := json.Unmarshal(b, &out); err != nil {
		return apigen.ExecutionDetail{}, err
	}
	return out, nil
}

// TriggerFlow starts a flow manually (REQ-TRG-001).
func (h ExecAPI) TriggerFlow(ctx context.Context, req apigen.TriggerFlowRequestObject) (apigen.TriggerFlowResponseObject, error) {
	var inputs map[string]any
	var labels map[string]string
	if req.Body.Inputs != nil {
		inputs = *req.Body.Inputs
	}
	if req.Body.Labels != nil {
		labels = *req.Body.Labels
	}
	id, err := h.E.Trigger(ctx, TriggerParams{Namespace: req.Namespace, FlowKey: req.FlowId, Inputs: inputs, Labels: labels, TriggerType: "manual"})
	if err != nil {
		return nil, err
	}
	d, err := h.E.detailAPI(ctx, id)
	if err != nil {
		return nil, err
	}
	return apigen.TriggerFlow201JSONResponse(d), nil
}

// RunFile runs a script file (REQ-NS-007).
func (h ExecAPI) RunFile(ctx context.Context, req apigen.RunFileRequestObject) (apigen.RunFileResponseObject, error) {
	var args []string
	if req.Body.Args != nil {
		args = *req.Body.Args
	}
	id, err := h.E.RunFile(ctx, req.Namespace, req.Body.Path, args)
	if err != nil {
		return nil, err
	}
	d, err := h.E.detailAPI(ctx, id)
	if err != nil {
		return nil, err
	}
	return apigen.RunFile201JSONResponse(d), nil
}

// ListExecutions lists executions with filters and keyset pagination (REQ-API-003, REQ-UI-004).
func (h ExecAPI) ListExecutions(ctx context.Context, req apigen.ListExecutionsRequestObject) (apigen.ListExecutionsResponseObject, error) {
	p := req.Params
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if p.State != nil && *p.State != "" {
		var states []string
		for _, s := range strings.Split(*p.State, ",") {
			if s = strings.TrimSpace(strings.ToUpper(s)); s != "" {
				states = append(states, s)
			}
		}
		where = append(where, "e.state = ANY("+arg(states)+"::text[])")
	}
	if p.Namespace != nil && *p.Namespace != "" {
		a := arg(*p.Namespace)
		where = append(where, fmt.Sprintf("(n.name = %s OR starts_with(n.name, %s || '.'))", a, a))
	}
	if p.Flow != nil && *p.Flow != "" {
		ns, key, ok := strings.Cut(*p.Flow, "/")
		if !ok {
			return nil, httpx.Validation(httpx.FieldError{Field: "flow", Message: "use <namespace>/<flow_id>"})
		}
		where = append(where, "n.name = "+arg(ns)+" AND f.flow_key = "+arg(key))
	}
	if p.TriggerType != nil {
		where = append(where, "e.trigger_type = "+arg(string(*p.TriggerType)))
	}
	if p.Label != nil {
		for _, l := range *p.Label {
			k, v, ok := strings.Cut(l, "=")
			if !ok || k == "" {
				return nil, httpx.Validation(httpx.FieldError{Field: "label", Message: "use key=value"})
			}
			b, _ := json.Marshal(map[string]string{k: v})
			where = append(where, "e.labels @> "+arg(string(b))+"::jsonb")
		}
	}
	if p.From != nil {
		where = append(where, "e.created_at >= "+arg(*p.From))
	}
	if p.To != nil {
		where = append(where, "e.created_at <= "+arg(*p.To))
	}
	byDuration := p.Sort != nil && *p.Sort == apigen.Duration
	if p.Cursor != nil && *p.Cursor != "" {
		if byDuration {
			parts, err := page.DecodeStrings(*p.Cursor, 2)
			if err != nil {
				return nil, err
			}
			d, err1 := strconv.ParseInt(parts[0], 10, 64)
			id, err2 := uuid.Parse(parts[1])
			if err1 != nil || err2 != nil {
				return nil, httpx.Validation(httpx.FieldError{Field: "cursor", Message: "invalid cursor"})
			}
			where = append(where, fmt.Sprintf("(coalesce(e.duration_ms, -1), e.id) < (%s::bigint, %s::uuid)", arg(d), arg(id)))
		} else {
			ts, id, err := page.Decode(*p.Cursor)
			if err != nil {
				return nil, err
			}
			where = append(where, fmt.Sprintf("(e.created_at, e.id) < (%s, %s::uuid)", arg(ts), arg(id)))
		}
	}
	limit := page.LimitPtr(p.Limit)
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
	rows, err := h.E.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := apigen.ListExecutions200JSONResponse{Items: []apigen.ExecutionSummary{}}
	for rows.Next() {
		var s apigen.ExecutionSummary
		var labels []byte
		var state string
		if err := rows.Scan(&s.Id, &s.Namespace, &s.FlowId, &state, &s.TriggerType, &labels, &s.CreatedAt, &s.StartedAt, &s.EndedAt,
			&s.DurationMs, &s.Error, &s.Reason, &s.CreatedBy); err != nil {
			return nil, err
		}
		s.State = apigen.ExecutionState(state)
		s.Labels = strMap(labels)
		out.Items = append(out.Items, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
			c = page.EncodeStrings(strconv.FormatInt(d, 10), last.Id.String())
		} else {
			c = page.Encode(last.CreatedAt, last.Id)
		}
		out.NextCursor = &c
	}
	return out, nil
}

// GetExecution returns one execution.
func (h ExecAPI) GetExecution(ctx context.Context, req apigen.GetExecutionRequestObject) (apigen.GetExecutionResponseObject, error) {
	d, err := h.E.detailAPI(ctx, req.ExecutionId)
	if err != nil {
		return nil, err
	}
	return apigen.GetExecution200JSONResponse(d), nil
}

// CancelExecution cancels an execution.
func (h ExecAPI) CancelExecution(ctx context.Context, req apigen.CancelExecutionRequestObject) (apigen.CancelExecutionResponseObject, error) {
	if err := h.E.Cancel(ctx, req.ExecutionId); err != nil {
		return nil, err
	}
	d, err := h.E.detailAPI(ctx, req.ExecutionId)
	if err != nil {
		return nil, err
	}
	return apigen.CancelExecution202JSONResponse(d), nil
}

// RerunExecution reruns an execution.
func (h ExecAPI) RerunExecution(ctx context.Context, req apigen.RerunExecutionRequestObject) (apigen.RerunExecutionResponseObject, error) {
	id, err := h.E.Rerun(ctx, req.ExecutionId)
	if err != nil {
		return nil, err
	}
	d, err := h.E.detailAPI(ctx, id)
	if err != nil {
		return nil, err
	}
	return apigen.RerunExecution201JSONResponse(d), nil
}

// RestartExecution restarts an execution from failed.
func (h ExecAPI) RestartExecution(ctx context.Context, req apigen.RestartExecutionRequestObject) (apigen.RestartExecutionResponseObject, error) {
	id, err := h.E.Restart(ctx, req.ExecutionId)
	if err != nil {
		return nil, err
	}
	d, err := h.E.detailAPI(ctx, id)
	if err != nil {
		return nil, err
	}
	return apigen.RestartExecution201JSONResponse(d), nil
}

func toLogEntry(l LogLine) apigen.LogEntry {
	return apigen.LogEntry{TaskRunId: l.TaskRunID, TaskKey: l.TaskKey, Attempt: l.Attempt, N: l.N, Ts: l.TS, Stream: apigen.LogEntryStream(l.Stream), Text: l.Text}
}

// GetExecutionLogs reads log lines from Postgres and the archive (REQ-RUN-008).
func (h ExecAPI) GetExecutionLogs(ctx context.Context, req apigen.GetExecutionLogsRequestObject) (apigen.GetExecutionLogsResponseObject, error) {
	ended, err := h.E.execEnded(ctx, req.ExecutionId)
	if err != nil {
		return nil, err
	}
	pos := positions{}
	if req.Params.Cursor != nil {
		if pos, err = decodePositions(*req.Params.Cursor); err != nil {
			return nil, err
		}
	}
	task := ""
	if req.Params.Task != nil {
		task = *req.Params.Task
	}
	limit := 1000
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	lines, err := h.E.ExecutionLines(ctx, req.ExecutionId, task, pos)
	if err != nil {
		return nil, err
	}
	more := len(lines) > limit
	if more {
		lines = lines[:limit]
	}
	for _, l := range lines {
		if l.N > pos[l.TaskRunID] {
			pos[l.TaskRunID] = l.N
		}
	}
	search := ""
	if req.Params.Search != nil {
		search = *req.Params.Search
	}
	out := apigen.GetExecutionLogs200JSONResponse{Lines: []apigen.LogEntry{}, Done: ended && !more}
	for _, l := range FilterLines(lines, search) {
		out.Lines = append(out.Lines, toLogEntry(l))
	}
	c := encodePositions(pos)
	out.NextCursor = &c
	return out, nil
}

type sseLogs struct {
	ctx  context.Context
	e    *Engine
	id   uuid.UUID
	task string
	pos  positions
}

// VisitStreamExecutionLogsResponse streams lines until the execution ends (REQ-RUN-008, REQ-API-004).
func (s sseLogs) VisitStreamExecutionLogsResponse(w http.ResponseWriter) error {
	s.e.streamLogs(s.ctx, w, s.id, s.task, s.pos)
	return nil
}

// StreamExecutionLogs streams log lines as server-sent events.
func (h ExecAPI) StreamExecutionLogs(ctx context.Context, req apigen.StreamExecutionLogsRequestObject) (apigen.StreamExecutionLogsResponseObject, error) {
	if _, err := h.E.execEnded(ctx, req.ExecutionId); err != nil {
		return nil, err
	}
	pos := positions{}
	if req.Params.LastEventID != nil {
		var err error
		if pos, err = decodePositions(*req.Params.LastEventID); err != nil {
			return nil, err
		}
	}
	task := ""
	if req.Params.Task != nil {
		task = *req.Params.Task
	}
	return sseLogs{ctx: ctx, e: h.E, id: req.ExecutionId, task: task, pos: pos}, nil
}

type logDownload struct {
	ctx  context.Context
	e    *Engine
	id   uuid.UUID
	task string
}

func (d logDownload) VisitDownloadExecutionLogsResponse(w http.ResponseWriter) error {
	lines, err := d.e.ExecutionLines(d.ctx, d.id, d.task, positions{})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "execution-"+d.id.String()+".log"))
	w.WriteHeader(http.StatusOK)
	for _, l := range lines {
		if _, err := fmt.Fprintf(w, "%s [%s#%d] %s: %s\n", l.TS.UTC().Format(time.RFC3339Nano), l.TaskKey, l.Attempt, l.Stream, l.Text); err != nil {
			return err
		}
	}
	return nil
}

// DownloadExecutionLogs returns all lines as a text file.
func (h ExecAPI) DownloadExecutionLogs(ctx context.Context, req apigen.DownloadExecutionLogsRequestObject) (apigen.DownloadExecutionLogsResponseObject, error) {
	if _, err := h.E.execEnded(ctx, req.ExecutionId); err != nil {
		return nil, err
	}
	task := ""
	if req.Params.Task != nil {
		task = *req.Params.Task
	}
	return logDownload{ctx: ctx, e: h.E, id: req.ExecutionId, task: task}, nil
}

type sseEvents struct {
	ctx  context.Context
	e    *Engine
	id   uuid.UUID
	last string
}

// VisitStreamExecutionEventsResponse sends the execution each time its state changes (REQ-UI-012).
func (s sseEvents) VisitStreamExecutionEventsResponse(w http.ResponseWriter) error {
	s.e.streamEvents(s.ctx, w, s.id, s.last)
	return nil
}

// StreamExecutionEvents streams execution state changes.
func (h ExecAPI) StreamExecutionEvents(ctx context.Context, req apigen.StreamExecutionEventsRequestObject) (apigen.StreamExecutionEventsResponseObject, error) {
	if _, err := h.E.execEnded(ctx, req.ExecutionId); err != nil {
		return nil, err
	}
	last := ""
	if req.Params.LastEventID != nil {
		last = *req.Params.LastEventID
	}
	return sseEvents{ctx: ctx, e: h.E, id: req.ExecutionId, last: last}, nil
}

// ListExecutionMetrics lists metrics of an execution.
func (h ExecAPI) ListExecutionMetrics(ctx context.Context, req apigen.ListExecutionMetricsRequestObject) (apigen.ListExecutionMetricsResponseObject, error) {
	if _, err := h.E.execEnded(ctx, req.ExecutionId); err != nil {
		return nil, err
	}
	rows, err := dbq.New(h.E.Pool).ListExecutionMetrics(ctx, req.ExecutionId)
	if err != nil {
		return nil, err
	}
	out := apigen.ListExecutionMetrics200JSONResponse{Items: []apigen.MetricPoint{}}
	for _, m := range rows {
		out.Items = append(out.Items, apigen.MetricPoint{TaskRunId: m.TaskRunID, TaskKey: m.TaskKey, Name: m.Name, Value: m.Value, Unit: m.Unit,
			Tags: strMap(m.Tags), Ts: m.Ts})
	}
	return out, nil
}

// ListExecutionArtifacts lists artifacts of an execution.
func (h ExecAPI) ListExecutionArtifacts(ctx context.Context, req apigen.ListExecutionArtifactsRequestObject) (apigen.ListExecutionArtifactsResponseObject, error) {
	if _, err := h.E.execEnded(ctx, req.ExecutionId); err != nil {
		return nil, err
	}
	rows, err := dbq.New(h.E.Pool).ListExecutionArtifacts(ctx, req.ExecutionId)
	if err != nil {
		return nil, err
	}
	out := apigen.ListExecutionArtifacts200JSONResponse{Items: []apigen.Artifact{}}
	for _, a := range rows {
		out.Items = append(out.Items, apigen.Artifact{Id: a.ID, TaskRunId: a.TaskRunID, TaskKey: a.TaskKey, Name: a.Name, Size: a.Size,
			ContentType: a.ContentType, CreatedAt: a.CreatedAt})
	}
	return out, nil
}

type artifactResponse struct {
	r           io.ReadCloser
	size        int64
	name, ctype string
}

func (a artifactResponse) VisitDownloadArtifactResponse(w http.ResponseWriter) error {
	defer func() { _ = a.r.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(a.size, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(a.name)))
	w.Header().Set("X-Sluice-Content-Type", a.ctype)
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, a.r)
	return err
}

// DownloadArtifact streams an artifact as an attachment.
func (h ExecAPI) DownloadArtifact(ctx context.Context, req apigen.DownloadArtifactRequestObject) (apigen.DownloadArtifactResponseObject, error) {
	a, err := dbq.New(h.E.Pool).GetArtifact(ctx, dbq.GetArtifactParams{ID: req.ArtifactId, ExecutionID: req.ExecutionId})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.Errorf(http.StatusNotFound, "artifact_not_found", "artifact not found")
	}
	if err != nil {
		return nil, err
	}
	r, err := h.E.Store.Get(ctx, a.StorageKey)
	if err != nil {
		return nil, err
	}
	return artifactResponse{r: r, size: a.Size, name: a.Name, ctype: a.ContentType}, nil
}
