package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/runnerapi"
	"github.com/alternayte/sluice/internal/storage"
)

var _ runnerapi.Backend = (*Engine)(nil)

var artifactNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

// Now returns the engine time.
func (e *Engine) Now() time.Time { return e.Clock.Now() }

// TaskRunByTokenHash finds the task run of a run token.
func (e *Engine) TaskRunByTokenHash(ctx context.Context, hash []byte) (dbq.TaskRun, error) {
	return dbq.New(e.Pool).GetTaskRunByTokenHash(ctx, hash)
}

// Spec returns the runner spec with resolved env and mask values.
func (e *Engine) Spec(ctx context.Context, tr dbq.TaskRun) (*runnerapi.Spec, error) {
	s, err := e.RunnerSpec(ctx, tr)
	var pe *PlanError
	if errors.As(err, &pe) {
		return nil, httpx.Errorf(http.StatusConflict, pe.Reason, "%s", pe.Msg)
	}
	return s, err
}

// Bundle returns the bundle of the pinned snapshot (REQ-EXE-001).
func (e *Engine) Bundle(ctx context.Context, tr dbq.TaskRun) (io.ReadCloser, int64, error) {
	ex, err := dbq.New(e.Pool).GetExecution(ctx, tr.ExecutionID)
	if err != nil {
		return nil, 0, err
	}
	m, err := e.Namespaces.Manifest(ctx, e.Pool, ex.SnapshotID)
	if err != nil {
		return nil, 0, err
	}
	key, err := e.Namespaces.EnsureBundle(ctx, m)
	if err != nil {
		return nil, 0, err
	}
	info, err := e.Store.Stat(ctx, key)
	if err != nil {
		return nil, 0, err
	}
	r, err := e.Store.Get(ctx, key)
	return r, info.Size, err
}

// IngestEvents stores outputs and metrics (REQ-RUN-003). Values are masked again (SI-10).
func (e *Engine) IngestEvents(ctx context.Context, tr dbq.TaskRun, seq int, events []runnerapi.Event) error {
	m := e.MaskerFor(ctx, tr)
	q := dbq.New(e.Pool)
	ex, err := q.GetExecution(ctx, tr.ExecutionID)
	if err != nil {
		return err
	}
	outputs := map[string]json.RawMessage{}
	for i, ev := range events {
		switch ev.Type {
		case "output":
			if ev.Key == "" {
				continue
			}
			v := m.Bytes(ev.Value)
			if !json.Valid(v) {
				v, _ = json.Marshal(string(v))
			}
			outputs[ev.Key] = v
		case "metric":
			var val float64
			if json.Unmarshal(ev.Value, &val) != nil {
				continue
			}
			tags := map[string]string{}
			for k, v := range ev.Tags {
				tags[k] = m.String(v)
			}
			tb, _ := json.Marshal(tags)
			ts := ev.TS
			if ts.IsZero() {
				ts = e.Clock.Now()
			}
			if err := q.InsertMetric(ctx, dbq.InsertMetricParams{ExecutionID: tr.ExecutionID, TaskRunID: tr.ID, FlowID: ex.FlowID, Name: ev.Name,
				Value: val, Unit: ev.Unit, Tags: tb, Ts: ts, Seq: int32(seq), Idx: int32(i)}); err != nil {
				return err
			}
		}
	}
	if len(outputs) > 0 {
		b, _ := json.Marshal(outputs)
		if len(b) > runnerapi.MaxOutputBytes {
			return httpx.Errorf(http.StatusRequestEntityTooLarge, "outputs_too_large", "outputs exceed 1 MiB")
		}
		return q.MergeTaskOutputs(ctx, dbq.MergeTaskOutputsParams{ID: tr.ID, Outputs: b})
	}
	return nil
}

// PutArtifact streams an artifact to storage.
func (e *Engine) PutArtifact(ctx context.Context, tr dbq.TaskRun, name, contentType string, body io.Reader) error {
	if !artifactNameRe.MatchString(name) {
		return httpx.Validation(httpx.FieldError{Field: "name", Message: "invalid artifact name"})
	}
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = "application/octet-stream"
	}
	key := storage.ArtifactKey(tr.ExecutionID.String(), tr.ID.String(), name)
	limited := &countingReader{r: io.LimitReader(body, e.Cfg.MaxArtifactBytes+1)}
	n, err := e.Store.Put(ctx, key, limited, contentType)
	if err != nil {
		return err
	}
	if n > e.Cfg.MaxArtifactBytes {
		_ = e.Store.Delete(ctx, key)
		return httpx.Errorf(http.StatusRequestEntityTooLarge, "artifact_too_large", "the artifact is larger than %d bytes", e.Cfg.MaxArtifactBytes)
	}
	id, _ := uuid.NewV7()
	return dbq.New(e.Pool).UpsertArtifact(ctx, dbq.UpsertArtifactParams{ID: id, ExecutionID: tr.ExecutionID, TaskRunID: tr.ID, Name: name,
		StorageKey: key, Size: n, ContentType: contentType, CreatedAt: e.Clock.Now()})
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += int64(n)
	return n, err
}

// Heartbeat records liveness and returns whether the task must stop (REQ-RUN-004).
func (e *Engine) Heartbeat(ctx context.Context, tr dbq.TaskRun) (bool, error) {
	now := e.Clock.Now()
	return dbq.New(e.Pool).TouchHeartbeat(ctx, dbq.TouchHeartbeatParams{ID: tr.ID, HeartbeatAt: &now})
}

// Complete ends the task run from the runner report (§4.3 step 6).
func (e *Engine) Complete(ctx context.Context, tr dbq.TaskRun, c runnerapi.Complete) error {
	m := e.MaskerFor(ctx, tr)
	state, reason := TaskSuccess, ""
	switch {
	case c.Reason == "timeout":
		state, reason = TaskTimedOut, ReasonTimeout
	case c.Reason == "cancelled":
		state, reason = TaskCancelled, ReasonCancelled
	case c.ExitCode != 0:
		state, reason = TaskFailed, ReasonExitCode
		if c.Reason != "" {
			reason = c.Reason
		}
	}
	if state == TaskCancelled && !tr.CancelRequested {
		// The runner stopped without a cancel request, for example on SIGTERM of the instance.
		state = TaskFailed
	}
	code := c.ExitCode
	return e.FinishTask(ctx, tr.ID, []string{TaskRunning}, state, reason, m.String(c.Error), &code)
}
