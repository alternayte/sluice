// Package runner implements `sluice exec`, the task runner (REQ-RUN-001).
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/alternayte/sluice/internal/runnerapi"
)

// Client calls the runner API with the run token.
type Client struct {
	BaseURL   string
	Token     string
	TaskRunID string
	HTTP      *http.Client
	// RetryWindow bounds retries of one request. Default runnerapi.RetryWindow.
	RetryWindow time.Duration
}

// PermanentError is an API error that retries cannot fix (4xx).
type PermanentError struct {
	Status int
	Body   string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("runner api: status %d: %s", e.Status, e.Body)
}

func (c *Client) url(suffix string) string {
	return strings.TrimRight(c.BaseURL, "/") + runnerapi.BasePath + "/task-runs/" + c.TaskRunID + suffix
}

// do sends a request and retries network errors, 429 and 5xx with backoff for up to
// the retry window (REQ-RUN-006). body is re-read for each attempt.
func (c *Client) do(ctx context.Context, method, suffix string, body func() (io.Reader, error), contentType string, out any) error {
	window := c.RetryWindow
	if window <= 0 {
		window = runnerapi.RetryWindow
	}
	deadline := time.Now().Add(window)
	delay := 100 * time.Millisecond
	for {
		err := c.once(ctx, method, suffix, body, contentType, out)
		if err == nil {
			return nil
		}
		var pe *PermanentError
		if errors.As(err, &pe) || ctx.Err() != nil || time.Now().After(deadline) {
			return err
		}
		jitter := time.Duration(rand.Int64N(int64(delay) / 2))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay + jitter):
		}
		delay *= 2
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
	}
}

func (c *Client) once(ctx context.Context, method, suffix string, body func() (io.Reader, error), contentType string, out any) error {
	var rd io.Reader
	if body != nil {
		var err error
		if rd, err = body(); err != nil {
			return &PermanentError{Status: 0, Body: err.Error()}
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(suffix), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return fmt.Errorf("runner api: status %d: %s", resp.StatusCode, b)
		}
		return &PermanentError{Status: resp.StatusCode, Body: string(b)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if rs, ok := out.(interface{ Reset() error }); ok {
		if err := rs.Reset(); err != nil {
			return &PermanentError{Body: err.Error()}
		}
	}
	if w, ok := out.(io.Writer); ok {
		_, err = io.Copy(w, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func jsonBody(v any) func() (io.Reader, error) {
	b, err := json.Marshal(v)
	return func() (io.Reader, error) {
		if err != nil {
			return nil, err
		}
		return bytes.NewReader(b), nil
	}
}

// Spec fetches the task spec.
func (c *Client) Spec(ctx context.Context) (*runnerapi.Spec, error) {
	var s runnerapi.Spec
	if err := c.do(ctx, http.MethodGet, "/spec", nil, "", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Bundle downloads the snapshot bundle into w.
func (c *Client) Bundle(ctx context.Context, w io.Writer) error {
	return c.do(ctx, http.MethodGet, "/bundle", nil, "", w)
}

// Logs sends one log batch.
func (c *Client) Logs(ctx context.Context, b runnerapi.LogBatch) error {
	return c.do(ctx, http.MethodPost, "/logs", jsonBody(b), "application/json", nil)
}

// Events sends one event batch.
func (c *Client) Events(ctx context.Context, b runnerapi.EventBatch) error {
	return c.do(ctx, http.MethodPost, "/events", jsonBody(b), "application/json", nil)
}

// Heartbeat reports liveness and returns whether the task must be cancelled.
func (c *Client) Heartbeat(ctx context.Context) (bool, error) {
	var hr runnerapi.HeartbeatResponse
	// Heartbeats do not retry for long: the next one follows in 10 s.
	short := *c
	short.RetryWindow = 5 * time.Second
	if err := short.do(ctx, http.MethodPost, "/heartbeat", jsonBody(struct{}{}), "application/json", &hr); err != nil {
		return false, err
	}
	return hr.Cancel, nil
}

// Artifact uploads one artifact from open().
func (c *Client) Artifact(ctx context.Context, name, contentType string, open func() (io.Reader, error)) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return c.do(ctx, http.MethodPut, "/artifacts/"+urlPathEscape(name), open, contentType, nil)
}

// Complete reports the end of the task.
func (c *Client) Complete(ctx context.Context, done runnerapi.Complete) error {
	return c.do(ctx, http.MethodPost, "/complete", jsonBody(done), "application/json", nil)
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			for _, x := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", x)
			}
		}
	}
	return b.String()
}
