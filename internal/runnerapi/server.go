package runnerapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/api/apigen"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Backend is the engine side of the runner protocol.
type Backend interface {
	TaskRunByTokenHash(ctx context.Context, hash []byte) (dbq.TaskRun, error)
	Spec(ctx context.Context, tr dbq.TaskRun) (*Spec, error)
	Bundle(ctx context.Context, tr dbq.TaskRun) (io.ReadCloser, int64, error)
	IngestLogs(ctx context.Context, tr dbq.TaskRun, seq int, lines []LogLine) error
	IngestEvents(ctx context.Context, tr dbq.TaskRun, seq int, events []Event) error
	PutArtifact(ctx context.Context, tr dbq.TaskRun, name, contentType string, body io.Reader) error
	Heartbeat(ctx context.Context, tr dbq.TaskRun) (bool, error)
	Complete(ctx context.Context, tr dbq.TaskRun, c Complete) error
	Now() time.Time
}

type trKey struct{}

// ErrWrongTaskRun is returned when a run token is used for another task run (SI-04).
var ErrWrongTaskRun = httpx.Errorf(http.StatusForbidden, "forbidden", "the run token belongs to another task run")

// TokenMiddleware authenticates runner requests with the run token (SI-04). A token
// is valid for one task run, until its expiry, and while the task run is RUNNING.
func TokenMiddleware(b Backend) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, BasePath+"/") {
				next.ServeHTTP(w, r)
				return
			}
			scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
			if !strings.EqualFold(scheme, "Bearer") || token == "" {
				httpx.WriteError(w, r, httpx.ErrUnauthorized)
				return
			}
			tr, err := b.TaskRunByTokenHash(r.Context(), auth.HashSecret(strings.TrimSpace(token)))
			if err != nil || tr.State != "RUNNING" || tr.TokenExpiresAt == nil || !b.Now().Before(*tr.TokenExpiresAt) {
				httpx.WriteError(w, r, httpx.ErrUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), trKey{}, tr)))
		})
	}
}

// taskRun returns the authenticated task run and checks the path ID.
func taskRun(ctx context.Context, id uuid.UUID) (dbq.TaskRun, error) {
	tr, ok := ctx.Value(trKey{}).(dbq.TaskRun)
	if !ok {
		return dbq.TaskRun{}, httpx.ErrUnauthorized
	}
	if tr.ID != id {
		return dbq.TaskRun{}, ErrWrongTaskRun
	}
	return tr, nil
}

// RunnerAPI serves /api/runner/v1 (Appendix C).
type RunnerAPI struct {
	B Backend
	// MaxArtifactBytes limits one artifact.
	MaxArtifactBytes int64
}

// RunnerGetSpec returns the task spec.
func (h RunnerAPI) RunnerGetSpec(ctx context.Context, req apigen.RunnerGetSpecRequestObject) (apigen.RunnerGetSpecResponseObject, error) {
	tr, err := taskRun(ctx, req.TaskRunId)
	if err != nil {
		return nil, err
	}
	s, err := h.B.Spec(ctx, tr)
	if err != nil {
		return nil, err
	}
	var out apigen.RunnerGetSpec200JSONResponse
	b, _ := json.Marshal(s)
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type bundleResponse struct {
	r    io.ReadCloser
	size int64
}

func (b bundleResponse) VisitRunnerGetBundleResponse(w http.ResponseWriter) error {
	defer func() { _ = b.r.Close() }()
	w.Header().Set("Content-Type", "application/gzip")
	if b.size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(b.size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, b.r)
	return err
}

// RunnerGetBundle streams the snapshot bundle.
func (h RunnerAPI) RunnerGetBundle(ctx context.Context, req apigen.RunnerGetBundleRequestObject) (apigen.RunnerGetBundleResponseObject, error) {
	tr, err := taskRun(ctx, req.TaskRunId)
	if err != nil {
		return nil, err
	}
	r, size, err := h.B.Bundle(ctx, tr)
	if err != nil {
		return nil, err
	}
	return bundleResponse{r: r, size: size}, nil
}

// RunnerPostLogs ingests a log batch.
func (h RunnerAPI) RunnerPostLogs(ctx context.Context, req apigen.RunnerPostLogsRequestObject) (apigen.RunnerPostLogsResponseObject, error) {
	tr, err := taskRun(ctx, req.TaskRunId)
	if err != nil {
		return nil, err
	}
	lines := make([]LogLine, len(req.Body.Lines))
	for i, l := range req.Body.Lines {
		lines[i] = LogLine{TS: l.Ts, Stream: string(l.Stream), Text: l.Text}
	}
	if err := h.B.IngestLogs(ctx, tr, req.Body.Seq, lines); err != nil {
		return nil, err
	}
	return apigen.RunnerPostLogs204Response{}, nil
}

// RunnerPostEvents ingests outputs and metrics.
func (h RunnerAPI) RunnerPostEvents(ctx context.Context, req apigen.RunnerPostEventsRequestObject) (apigen.RunnerPostEventsResponseObject, error) {
	tr, err := taskRun(ctx, req.TaskRunId)
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(req.Body.Events))
	for _, ev := range req.Body.Events {
		e := Event{Type: string(ev.Type)}
		if ev.Key != nil {
			e.Key = *ev.Key
		}
		if ev.Name != nil {
			e.Name = *ev.Name
		}
		if ev.Unit != nil {
			e.Unit = *ev.Unit
		}
		if ev.Tags != nil {
			e.Tags = *ev.Tags
		}
		if ev.Ts != nil {
			e.TS = *ev.Ts
		}
		if ev.Value != nil {
			e.Value, _ = json.Marshal(ev.Value)
		}
		events = append(events, e)
	}
	if err := h.B.IngestEvents(ctx, tr, req.Body.Seq, events); err != nil {
		return nil, err
	}
	return apigen.RunnerPostEvents204Response{}, nil
}

// RunnerPutArtifact stores one artifact.
func (h RunnerAPI) RunnerPutArtifact(ctx context.Context, req apigen.RunnerPutArtifactRequestObject) (apigen.RunnerPutArtifactResponseObject, error) {
	tr, err := taskRun(ctx, req.TaskRunId)
	if err != nil {
		return nil, err
	}
	if err := h.B.PutArtifact(ctx, tr, req.Name, contentTypeFrom(ctx), req.Body); err != nil {
		return nil, err
	}
	return apigen.RunnerPutArtifact204Response{}, nil
}

// RunnerHeartbeat records liveness and returns the cancel flag.
func (h RunnerAPI) RunnerHeartbeat(ctx context.Context, req apigen.RunnerHeartbeatRequestObject) (apigen.RunnerHeartbeatResponseObject, error) {
	tr, err := taskRun(ctx, req.TaskRunId)
	if err != nil {
		return nil, err
	}
	cancel, err := h.B.Heartbeat(ctx, tr)
	if err != nil {
		return nil, err
	}
	return apigen.RunnerHeartbeat200JSONResponse{Cancel: cancel}, nil
}

// RunnerComplete records the end of the task.
func (h RunnerAPI) RunnerComplete(ctx context.Context, req apigen.RunnerCompleteRequestObject) (apigen.RunnerCompleteResponseObject, error) {
	tr, err := taskRun(ctx, req.TaskRunId)
	if err != nil {
		return nil, err
	}
	c := Complete{ExitCode: req.Body.ExitCode, Error: req.Body.Error}
	if req.Body.Reason != nil {
		c.Reason = *req.Body.Reason
	}
	if err := h.B.Complete(ctx, tr, c); err != nil {
		return nil, err
	}
	return apigen.RunnerComplete204Response{}, nil
}

type ctKey struct{}

// ContentTypeMiddleware keeps the request Content-Type for artifact uploads.
func ContentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, BasePath+"/") {
			r = r.WithContext(context.WithValue(r.Context(), ctKey{}, r.Header.Get("Content-Type")))
		}
		next.ServeHTTP(w, r)
	})
}

func contentTypeFrom(ctx context.Context) string {
	s, _ := ctx.Value(ctKey{}).(string)
	return s
}

// ErrArtifactTooLarge is returned for artifacts above SLUICE_MAX_ARTIFACT_BYTES.
var ErrArtifactTooLarge = errors.New("artifact too large")
