package execution

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/execution/executiondb"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// LogEntry is one log line of a task run.
type LogEntry struct {
	TaskRunID uuid.UUID `json:"task_run_id"`
	TaskKey   string    `json:"task_key"`
	Attempt   int       `json:"attempt"`
	N         int64     `json:"n" format:"int64" doc:"1-based line number in the task run."`
	TS        time.Time `json:"ts"`
	Stream    string    `json:"stream" enum:"stdout,stderr,system"`
	Text      string    `json:"text"`
}

// LogPage is one page of log lines.
type LogPage struct {
	Lines      []LogEntry `json:"lines"`
	NextCursor *string    `json:"next_cursor,omitempty"`
	Done       bool       `json:"done" doc:"True when the execution ended and no more lines follow."`
}

// MetricPoint is one metric value of a task.
type MetricPoint struct {
	TaskRunID uuid.UUID         `json:"task_run_id"`
	TaskKey   string            `json:"task_key"`
	Name      string            `json:"name"`
	Value     float64           `json:"value" format:"double"`
	Unit      string            `json:"unit"`
	Tags      map[string]string `json:"tags"`
	TS        time.Time         `json:"ts"`
}

// MetricList is the metric list of an execution.
type MetricList struct {
	Items []MetricPoint `json:"items"`
}

// Artifact is one artifact of a task.
type Artifact struct {
	ID          uuid.UUID `json:"id"`
	TaskRunID   uuid.UUID `json:"task_run_id"`
	TaskKey     string    `json:"task_key"`
	Name        string    `json:"name"`
	Size        int64     `json:"size" format:"int64"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
}

// ArtifactList is the artifact list of an execution.
type ArtifactList struct {
	Items []Artifact `json:"items"`
}

// positions is the read position per task run: the last line number sent.
type positions map[uuid.UUID]int64

func encodePositions(p positions) string {
	m := map[string]int64{}
	for k, v := range p {
		m[k.String()] = v
	}
	b, _ := json.Marshal(m)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodePositions(s string) (positions, error) {
	p := positions{}
	if s == "" {
		return p, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, httpx.Validation(httpx.FieldError{Field: "cursor", Message: "invalid cursor"})
	}
	m := map[string]int64{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, httpx.Validation(httpx.FieldError{Field: "cursor", Message: "invalid cursor"})
	}
	for k, v := range m {
		id, err := uuid.Parse(k)
		if err != nil {
			return nil, httpx.Validation(httpx.FieldError{Field: "cursor", Message: "invalid cursor"})
		}
		p[id] = v
	}
	return p, nil
}

func logEntryOf(l LogLine) LogEntry {
	return LogEntry{TaskRunID: l.TaskRunID, TaskKey: l.TaskKey, Attempt: l.Attempt, N: l.N, TS: l.TS, Stream: l.Stream, Text: l.Text}
}

func (e *Engine) execEnded(ctx context.Context, id uuid.UUID) (bool, error) {
	var state string
	err := e.Pool.QueryRow(ctx, "SELECT state FROM executions WHERE id = $1", id).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return ExecutionTerminal(state), err
}

func sseHeaders(w http.ResponseWriter) *http.ResponseController {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	return rc
}

// streamLogs streams lines until the execution ends (REQ-RUN-008, REQ-API-004).
func (e *Engine) streamLogs(ctx context.Context, w http.ResponseWriter, id uuid.UUID, task string, pos positions) {
	rc := sseHeaders(w)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	idle := 0
	for {
		ended, err := e.execEnded(ctx, id)
		if err != nil {
			return // the stream ends when the execution is gone
		}
		lines, err := e.ExecutionLines(ctx, id, task, pos)
		if err != nil {
			return // a read error ends the stream; the client reconnects with Last-Event-ID
		}
		for _, l := range lines {
			pos[l.TaskRunID] = l.N
			b, _ := json.Marshal(logEntryOf(l))
			if _, err := fmt.Fprintf(w, "id: %s\nevent: line\ndata: %s\n\n", encodePositions(pos), b); err != nil {
				return // the client closed the stream
			}
		}
		if len(lines) > 0 {
			_ = rc.Flush()
			idle = 0
		} else {
			idle++
			if idle%30 == 0 {
				_, _ = fmt.Fprint(w, ": keep-alive\n\n")
				_ = rc.Flush()
			}
		}
		if ended && len(lines) == 0 && idle >= 2 {
			_, _ = fmt.Fprintf(w, "event: end\ndata: {}\n\n")
			_ = rc.Flush()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func hashDetail(d ExecutionDetail) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|", d.State, d.Reason)
	for _, t := range d.TaskRuns {
		fmt.Fprintf(h, "%s:%d:%s:%s;", t.TaskKey, t.Attempt, t.State, t.Reason)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// streamEvents sends the execution each time its state changes (REQ-UI-012).
func (e *Engine) streamEvents(ctx context.Context, w http.ResponseWriter, id uuid.UUID, last string) {
	rc := sseHeaders(w)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	idle := 0
	for {
		d, err := e.Detail(ctx, id)
		if err != nil {
			return // the stream ends when the execution is gone
		}
		if h := hashDetail(d); h != last {
			last = h
			b, _ := json.Marshal(d)
			if _, err := fmt.Fprintf(w, "id: %s\nevent: execution\ndata: %s\n\n", h, b); err != nil {
				return // the client closed the stream
			}
			_ = rc.Flush()
			idle = 0
		} else {
			idle++
			if idle%30 == 0 {
				_, _ = fmt.Fprint(w, ": keep-alive\n\n")
				_ = rc.Flush()
			}
		}
		if ExecutionTerminal(d.State) && idle >= 2 {
			_, _ = fmt.Fprintf(w, "event: end\ndata: {}\n\n")
			_ = rc.Flush()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// rawExecution checks the executionId path parameter of a Raw route and that the execution
// exists. It writes the error and returns false on a failure.
func (e *Engine) rawExecution(w http.ResponseWriter, req *http.Request) (uuid.UUID, bool) {
	var fields []httpx.FieldError
	id := pathUUID(req, "executionId", &fields)
	if len(fields) > 0 {
		httpx.WriteError(w, req, httpx.Validation(fields...))
		return id, false
	}
	if _, err := e.execEnded(req.Context(), id); err != nil {
		httpx.WriteError(w, req, err)
		return id, false
	}
	return id, true
}

func registerLogs(api huma.API, r chi.Router, e *Engine, viewer httpx.Access) {
	huma.Register(api, httpx.Op("getExecutionLogs", http.MethodGet, "/api/v1/executions/{executionId}/logs", viewer),
		func(ctx context.Context, in *struct {
			ExecutionID uuid.UUID `path:"executionId"`
			Task        string    `query:"task" doc:"Task ID filter."`
			Search      string    `query:"search" doc:"Case-insensitive text filter."`
			Cursor      string    `query:"cursor" doc:"Opaque cursor from next_cursor of the previous page."`
			Limit       int       `query:"limit" minimum:"1" maximum:"5000" default:"1000"`
		}) (*struct{ Body LogPage }, error) {
			ended, err := e.execEnded(ctx, in.ExecutionID)
			if err != nil {
				return nil, err
			}
			pos, err := decodePositions(in.Cursor)
			if err != nil {
				return nil, err
			}
			limit := 1000
			if in.Limit != 0 {
				limit = in.Limit
			}
			lines, err := e.ExecutionLines(ctx, in.ExecutionID, in.Task, pos)
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
			out := LogPage{Lines: []LogEntry{}, Done: ended && !more}
			for _, l := range FilterLines(lines, in.Search) {
				out.Lines = append(out.Lines, logEntryOf(l))
			}
			c := encodePositions(pos)
			out.NextCursor = &c
			return &struct{ Body LogPage }{Body: out}, nil
		})

	taskQuery := &huma.Param{Name: "task", In: "query", Schema: &huma.Schema{Type: huma.TypeString}}
	lastEventID := &huma.Param{Name: "Last-Event-ID", In: "header", Schema: &huma.Schema{Type: huma.TypeString}}

	streamLogs := httpx.Op("streamExecutionLogs", http.MethodGet, "/api/v1/executions/{executionId}/logs/stream", viewer)
	streamLogs.Parameters = []*huma.Param{uuidParam("executionId"), taskQuery, lastEventID}
	streamLogs.Responses = httpx.RawResponse(http.StatusOK, "text/event-stream", "Event stream.")
	httpx.Raw(api, r, streamLogs, func(w http.ResponseWriter, req *http.Request) {
		id, ok := e.rawExecution(w, req)
		if !ok {
			return
		}
		pos, err := decodePositions(req.Header.Get("Last-Event-ID"))
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		e.streamLogs(req.Context(), w, id, req.URL.Query().Get("task"), pos)
	})

	download := httpx.Op("downloadExecutionLogs", http.MethodGet, "/api/v1/executions/{executionId}/logs/download", viewer)
	download.Parameters = []*huma.Param{uuidParam("executionId"), taskQuery}
	download.Responses = httpx.RawResponse(http.StatusOK, "text/plain", "Log file.")
	httpx.Raw(api, r, download, func(w http.ResponseWriter, req *http.Request) {
		id, ok := e.rawExecution(w, req)
		if !ok {
			return
		}
		lines, err := e.ExecutionLines(req.Context(), id, req.URL.Query().Get("task"), positions{})
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "execution-"+id.String()+".log"))
		w.WriteHeader(http.StatusOK)
		for _, l := range lines {
			if _, err := fmt.Fprintf(w, "%s [%s#%d] %s: %s\n", l.TS.UTC().Format(time.RFC3339Nano), l.TaskKey, l.Attempt, l.Stream, l.Text); err != nil {
				return
			}
		}
	})

	events := httpx.Op("streamExecutionEvents", http.MethodGet, "/api/v1/executions/{executionId}/events", viewer)
	events.Parameters = []*huma.Param{uuidParam("executionId"), lastEventID}
	events.Responses = httpx.RawResponse(http.StatusOK, "text/event-stream", "Event stream.")
	httpx.Raw(api, r, events, func(w http.ResponseWriter, req *http.Request) {
		id, ok := e.rawExecution(w, req)
		if !ok {
			return
		}
		e.streamEvents(req.Context(), w, id, req.Header.Get("Last-Event-ID"))
	})
}

func registerArtifacts(api huma.API, r chi.Router, e *Engine, viewer httpx.Access) {
	huma.Register(api, httpx.Op("listExecutionMetrics", http.MethodGet, "/api/v1/executions/{executionId}/metrics", viewer),
		func(ctx context.Context, in *executionIDIn) (*struct{ Body MetricList }, error) {
			if _, err := e.execEnded(ctx, in.ExecutionID); err != nil {
				return nil, err
			}
			rows, err := executiondb.New(e.Pool).ListExecutionMetrics(ctx, in.ExecutionID)
			if err != nil {
				return nil, err
			}
			out := MetricList{Items: []MetricPoint{}}
			for _, m := range rows {
				out.Items = append(out.Items, MetricPoint{TaskRunID: m.TaskRunID, TaskKey: m.TaskKey, Name: m.Name, Value: m.Value, Unit: m.Unit,
					Tags: strMap(m.Tags), TS: m.Ts})
			}
			return &struct{ Body MetricList }{Body: out}, nil
		})

	huma.Register(api, httpx.Op("listExecutionArtifacts", http.MethodGet, "/api/v1/executions/{executionId}/artifacts", viewer),
		func(ctx context.Context, in *executionIDIn) (*struct{ Body ArtifactList }, error) {
			if _, err := e.execEnded(ctx, in.ExecutionID); err != nil {
				return nil, err
			}
			rows, err := executiondb.New(e.Pool).ListExecutionArtifacts(ctx, in.ExecutionID)
			if err != nil {
				return nil, err
			}
			out := ArtifactList{Items: []Artifact{}}
			for _, a := range rows {
				out.Items = append(out.Items, Artifact{ID: a.ID, TaskRunID: a.TaskRunID, TaskKey: a.TaskKey, Name: a.Name, Size: a.Size,
					ContentType: a.ContentType, CreatedAt: a.CreatedAt})
			}
			return &struct{ Body ArtifactList }{Body: out}, nil
		})

	download := httpx.Op("downloadArtifact", http.MethodGet, "/api/v1/executions/{executionId}/artifacts/{artifactId}", viewer)
	download.Parameters = []*huma.Param{uuidParam("executionId"), uuidParam("artifactId")}
	download.Responses = httpx.RawResponse(http.StatusOK, "application/octet-stream", "Artifact content.")
	httpx.Raw(api, r, download, func(w http.ResponseWriter, req *http.Request) {
		var fields []httpx.FieldError
		execID := pathUUID(req, "executionId", &fields)
		artID := pathUUID(req, "artifactId", &fields)
		if len(fields) > 0 {
			httpx.WriteError(w, req, httpx.Validation(fields...))
			return
		}
		ctx := req.Context()
		a, err := executiondb.New(e.Pool).GetArtifact(ctx, executiondb.GetArtifactParams{ID: artID, ExecutionID: execID})
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, req, httpx.Errorf(http.StatusNotFound, "artifact_not_found", "artifact not found"))
			return
		}
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		rc, err := e.Store.Get(ctx, a.StorageKey)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		defer func() { _ = rc.Close() }()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(a.Size, 10))
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(a.Name)))
		w.Header().Set("X-Sluice-Content-Type", a.ContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
	})
}
