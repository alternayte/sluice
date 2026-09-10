package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/execution/executiondb"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/token"
	"github.com/alternayte/sluice/internal/runnerproto"
)

// RunnerLimits are the limits sent to the runner.
type RunnerLimits struct {
	MaxArtifactBytes int64 `json:"max_artifact_bytes" format:"int64"`
	MaxBundleBytes   int64 `json:"max_bundle_bytes" format:"int64"`
}

// RunnerSpec is the task spec with resolved env, mask values and limits.
type RunnerSpec struct {
	TaskRunID      string            `json:"task_run_id"`
	ExecutionID    string            `json:"execution_id"`
	Namespace      string            `json:"namespace"`
	FlowID         string            `json:"flow_id"`
	TaskID         string            `json:"task_id"`
	Attempt        int               `json:"attempt"`
	Command        []string          `json:"command"`
	Workdir        string            `json:"workdir"`
	Env            map[string]string `json:"env"`
	Runtime        string            `json:"runtime,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	MaskValues     []string          `json:"mask_values"`
	BundleHash     string            `json:"bundle_hash"`
	Limits         RunnerLimits      `json:"limits"`
}

// RunnerLogLine is one log line of a batch.
type RunnerLogLine struct {
	TS     time.Time `json:"ts"`
	Stream string    `json:"stream" enum:"stdout,stderr,system"`
	Text   string    `json:"text"`
}

// RunnerLogBatch is the body of runnerPostLogs.
type RunnerLogBatch struct {
	Seq   int             `json:"seq" minimum:"1"`
	Lines []RunnerLogLine `json:"lines"`
}

// RunnerEvent is one output or metric event.
type RunnerEvent struct {
	Type  string            `json:"type" enum:"output,metric"`
	Key   *string           `json:"key,omitempty"`
	Value any               `json:"value,omitempty"`
	Name  *string           `json:"name,omitempty"`
	Unit  *string           `json:"unit,omitempty"`
	Tags  map[string]string `json:"tags,omitempty"`
	TS    *time.Time        `json:"ts,omitempty"`
}

// RunnerEventBatch is the body of runnerPostEvents.
type RunnerEventBatch struct {
	Seq    int           `json:"seq" minimum:"1"`
	Events []RunnerEvent `json:"events" maxItems:"1000"`
}

// RunnerHeartbeatResponse is the response of runnerHeartbeat.
type RunnerHeartbeatResponse struct {
	Cancel bool `json:"cancel"`
}

// RunnerComplete is the body of runnerComplete.
type RunnerComplete struct {
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error"`
	Reason   string `json:"reason,omitempty"`
}

type runTaskRunKey struct{}

type runContentTypeKey struct{}

// ErrWrongTaskRun is returned when a run token is used for another task run (SI-04).
var ErrWrongTaskRun = httpx.Errorf(http.StatusForbidden, "forbidden", "the run token belongs to another task run")

// ErrArtifactTooLarge is returned for artifacts above SLUICE_MAX_ARTIFACT_BYTES.
var ErrArtifactTooLarge = errors.New("artifact too large")

// RunTokenMiddleware authenticates runner requests with the run token (SI-04). A token
// is valid for one task run, until its expiry, and while the task run is RUNNING.
func RunTokenMiddleware(e *Engine) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, runnerproto.BasePath+"/") {
				next.ServeHTTP(w, r)
				return
			}
			scheme, bearer, _ := strings.Cut(r.Header.Get("Authorization"), " ")
			if !strings.EqualFold(scheme, "Bearer") || bearer == "" {
				httpx.WriteError(w, r, httpx.ErrUnauthorized)
				return
			}
			tr, err := e.TaskRunByTokenHash(r.Context(), token.HashSecret(strings.TrimSpace(bearer)))
			if err != nil || tr.State != "RUNNING" || tr.TokenExpiresAt == nil || !e.Now().Before(*tr.TokenExpiresAt) {
				httpx.WriteError(w, r, httpx.ErrUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), runTaskRunKey{}, tr)))
		})
	}
}

// RunnerContentType keeps the request Content-Type for artifact uploads.
func RunnerContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, runnerproto.BasePath+"/") {
			r = r.WithContext(context.WithValue(r.Context(), runContentTypeKey{}, r.Header.Get("Content-Type")))
		}
		next.ServeHTTP(w, r)
	})
}

func runnerContentType(ctx context.Context) string {
	s, _ := ctx.Value(runContentTypeKey{}).(string)
	return s
}

// runnerTaskRun returns the authenticated task run and checks the path ID.
func runnerTaskRun(ctx context.Context, id uuid.UUID) (executiondb.TaskRun, error) {
	tr, ok := ctx.Value(runTaskRunKey{}).(executiondb.TaskRun)
	if !ok {
		return executiondb.TaskRun{}, httpx.ErrUnauthorized
	}
	if tr.ID != id {
		return executiondb.TaskRun{}, ErrWrongTaskRun
	}
	return tr, nil
}

type taskRunIn struct {
	TaskRunID uuid.UUID `path:"taskRunId"`
}

// runnerBody removes the huma body size limit and read timeout. The old server had neither on
// the runner routes: the service size errors apply, and a slow runner gets no 408.
func runnerBody(op huma.Operation) huma.Operation {
	return unlimitedBody(op)
}

// RunnerRoutes registers the runner protocol under /api/runner/v1 (Appendix C). The run
// token middleware authenticates each request before these handlers. The engine limits an
// artifact to maxArtifactBytes (e.Cfg.MaxArtifactBytes has the same value).
// Registration does not use e: `sluice openapi` passes nil.
func RunnerRoutes(api huma.API, r chi.Router, e *Engine, maxArtifactBytes int64) {
	base := runnerproto.BasePath + "/task-runs/{taskRunId}"

	huma.Register(api, httpx.Op("runnerGetSpec", http.MethodGet, base+"/spec", httpx.RunToken),
		func(ctx context.Context, in *taskRunIn) (*struct{ Body RunnerSpec }, error) {
			tr, err := runnerTaskRun(ctx, in.TaskRunID)
			if err != nil {
				return nil, err
			}
			s, err := e.Spec(ctx, tr)
			if err != nil {
				return nil, err
			}
			return &struct{ Body RunnerSpec }{Body: RunnerSpec{TaskRunID: s.TaskRunID, ExecutionID: s.ExecutionID, Namespace: s.Namespace,
				FlowID: s.FlowID, TaskID: s.TaskID, Attempt: s.Attempt, Command: s.Command, Workdir: s.Workdir, Env: s.Env, Runtime: s.Runtime,
				TimeoutSeconds: s.TimeoutSeconds, MaskValues: s.MaskValues, BundleHash: s.BundleHash, Limits: RunnerLimits(s.Limits)}}, nil
		})

	bundle := httpx.Op("runnerGetBundle", http.MethodGet, base+"/bundle", httpx.RunToken)
	bundle.Parameters = []*huma.Param{uuidParam("taskRunId")}
	bundle.Responses = httpx.RawResponse(http.StatusOK, "application/gzip", "Bundle.")
	httpx.Raw(api, r, bundle, func(w http.ResponseWriter, req *http.Request) {
		var fields []httpx.FieldError
		id := pathUUID(req, "taskRunId", &fields)
		if len(fields) > 0 {
			httpx.WriteError(w, req, httpx.Validation(fields...))
			return
		}
		tr, err := runnerTaskRun(req.Context(), id)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		rc, size, err := e.Bundle(req.Context(), tr)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		defer func() { _ = rc.Close() }()
		w.Header().Set("Content-Type", "application/gzip")
		if size > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
	})

	huma.Register(api, runnerBody(withStatus(httpx.Op("runnerPostLogs", http.MethodPost, base+"/logs", httpx.RunToken), http.StatusNoContent)),
		func(ctx context.Context, in *struct {
			TaskRunID uuid.UUID `path:"taskRunId"`
			Body      RunnerLogBatch
		}) (*struct{}, error) {
			tr, err := runnerTaskRun(ctx, in.TaskRunID)
			if err != nil {
				return nil, err
			}
			lines := make([]runnerproto.LogLine, len(in.Body.Lines))
			for i, l := range in.Body.Lines {
				lines[i] = runnerproto.LogLine{TS: l.TS, Stream: l.Stream, Text: l.Text}
			}
			return nil, e.IngestLogs(ctx, tr, in.Body.Seq, lines)
		})

	huma.Register(api, runnerBody(withStatus(httpx.Op("runnerPostEvents", http.MethodPost, base+"/events", httpx.RunToken), http.StatusNoContent)),
		func(ctx context.Context, in *struct {
			TaskRunID uuid.UUID `path:"taskRunId"`
			Body      RunnerEventBatch
		}) (*struct{}, error) {
			tr, err := runnerTaskRun(ctx, in.TaskRunID)
			if err != nil {
				return nil, err
			}
			return nil, e.IngestEvents(ctx, tr, in.Body.Seq, toEvents(in.Body.Events))
		})

	registerRunnerArtifact(api, r, e)

	huma.Register(api, httpx.Op("runnerHeartbeat", http.MethodPost, base+"/heartbeat", httpx.RunToken),
		func(ctx context.Context, in *taskRunIn) (*struct{ Body RunnerHeartbeatResponse }, error) {
			tr, err := runnerTaskRun(ctx, in.TaskRunID)
			if err != nil {
				return nil, err
			}
			cancel, err := e.Heartbeat(ctx, tr)
			if err != nil {
				return nil, err
			}
			return &struct{ Body RunnerHeartbeatResponse }{Body: RunnerHeartbeatResponse{Cancel: cancel}}, nil
		})

	huma.Register(api, withStatus(httpx.Op("runnerComplete", http.MethodPost, base+"/complete", httpx.RunToken), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			TaskRunID uuid.UUID `path:"taskRunId"`
			Body      RunnerComplete
		}) (*struct{}, error) {
			tr, err := runnerTaskRun(ctx, in.TaskRunID)
			if err != nil {
				return nil, err
			}
			return nil, e.Complete(ctx, tr, runnerproto.Complete{ExitCode: in.Body.ExitCode, Error: in.Body.Error, Reason: in.Body.Reason})
		})

	_ = maxArtifactBytes
}

func toEvents(in []RunnerEvent) []runnerproto.Event {
	events := make([]runnerproto.Event, 0, len(in))
	for _, ev := range in {
		e := runnerproto.Event{Type: ev.Type, Tags: ev.Tags}
		if ev.Key != nil {
			e.Key = *ev.Key
		}
		if ev.Name != nil {
			e.Name = *ev.Name
		}
		if ev.Unit != nil {
			e.Unit = *ev.Unit
		}
		if ev.TS != nil {
			e.TS = *ev.TS
		}
		if ev.Value != nil {
			e.Value, _ = json.Marshal(ev.Value)
		}
		events = append(events, e)
	}
	return events
}

// registerRunnerArtifact registers runnerPutArtifact as a Raw route: the body is a stream.
// The engine reads at most MaxArtifactBytes+1 bytes and answers 413 artifact_too_large above
// the limit, as before.
func registerRunnerArtifact(api huma.API, r chi.Router, e *Engine) {
	op := httpx.Op("runnerPutArtifact", http.MethodPut, runnerproto.BasePath+"/task-runs/{taskRunId}/artifacts/{name}", httpx.RunToken)
	maxName := 200
	op.Parameters = []*huma.Param{uuidParam("taskRunId"),
		{Name: "name", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: &maxName}}}
	op.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
		"application/octet-stream": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}}}
	op.Responses = httpx.RawResponse(http.StatusNoContent, "", "Stored.")
	httpx.Raw(api, r, op, func(w http.ResponseWriter, req *http.Request) {
		var fields []httpx.FieldError
		id := pathUUID(req, "taskRunId", &fields)
		name := chi.URLParam(req, "name")
		if len(name) > maxName {
			fields = append(fields, httpx.FieldError{Field: "name", Message: fmt.Sprintf("maximum string length is %d", maxName)})
		}
		fields = append(fields, artifactBodyErrors(req)...)
		if len(fields) > 0 {
			httpx.WriteError(w, req, httpx.Validation(fields...))
			return
		}
		tr, err := runnerTaskRun(req.Context(), id)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		if err := e.PutArtifact(req.Context(), tr, name, runnerContentType(req.Context()), req.Body); err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// artifactBodyErrors checks the runnerPutArtifact body as the old validator did
// (the deleted internal/api/server.go). The old validator skipped only a non-empty body with a
// Content-Type that is not JSON. In each other case it checked the required
// application/octet-stream body: an empty body, no Content-Type and a JSON Content-Type
// each gave 422 validation_failed with the field "body".
func artifactBodyErrors(req *http.Request) []httpx.FieldError {
	ct := req.Header.Get("Content-Type")
	switch {
	case req.Body == nil || req.ContentLength == 0:
		return []httpx.FieldError{{Field: "body", Message: "value is required but missing"}}
	case ct == "" || strings.HasPrefix(ct, "application/json"):
		return []httpx.FieldError{{Field: "body", Message: fmt.Sprintf("header Content-Type has unexpected value %q", ct)}}
	}
	return nil
}
