package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// remote is the HTTP client of the client commands. It reads SLUICE_URL and SLUICE_TOKEN,
// and authenticates every request with the bearer token.
type remote struct {
	base  string
	token string
	http  *http.Client
}

// apiError is an error answer of the API.
type apiError struct {
	Status  int
	Code    string
	Message string
	// Details is error.details of the envelope, or nil.
	Details json.RawMessage
}

// envelope returns the error as the API envelope {"error":{"code","message","details"}}.
func (e *apiError) envelope() map[string]any {
	body := map[string]any{"code": e.Code, "message": e.Message}
	if len(e.Details) > 0 {
		body["details"] = e.Details
	}
	return map[string]any{"error": body}
}

// detailLines returns one line for each detail. A field error is "field: message". Details
// of another shape are one line of JSON.
func (e *apiError) detailLines() []string {
	if len(e.Details) == 0 || string(e.Details) == "null" {
		return nil
	}
	var fields []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	}
	if json.Unmarshal(e.Details, &fields) == nil {
		var out []string
		for _, f := range fields {
			switch {
			case f.Field != "" && f.Message != "":
				out = append(out, f.Field+": "+f.Message)
			case f.Message != "":
				out = append(out, f.Message)
			default:
				out = nil
			}
			if out == nil {
				break
			}
		}
		if len(out) == len(fields) && len(out) > 0 {
			return out
		}
	}
	var buf bytes.Buffer
	if json.Compact(&buf, e.Details) != nil {
		return nil
	}
	return []string{buf.String()}
}

func (e *apiError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// errRemoteConfig is a missing or wrong SLUICE_URL or SLUICE_TOKEN.
var errRemoteConfig = errors.New("set SLUICE_URL to the Sluice URL and SLUICE_TOKEN to an API token")

func newRemote() (*remote, error) {
	base := strings.TrimRight(os.Getenv("SLUICE_URL"), "/")
	token := os.Getenv("SLUICE_TOKEN")
	if base == "" || token == "" {
		return nil, errRemoteConfig
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("SLUICE_URL %q is not an http or https URL", base)
	}
	return &remote{base: base, token: token, http: &http.Client{}}, nil
}

func (c *remote) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "sluice-cli/"+Version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do sends a request and decodes a JSON answer into out, or returns the API error.
func (c *remote) do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return decodeAPIError(resp.StatusCode, b)
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], b...)
		return nil
	}
	return json.Unmarshal(b, out)
}

func decodeAPIError(status int, b []byte) error {
	var env struct {
		Error struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &env) == nil && env.Error.Code != "" {
		return &apiError{Status: status, Code: env.Error.Code, Message: env.Error.Message, Details: env.Error.Details}
	}
	return &apiError{Status: status, Code: "http_" + fmt.Sprint(status), Message: strings.TrimSpace(string(b))}
}

// sseEvent is one server-sent event.
type sseEvent struct {
	ID    string
	Event string
	Data  string
}

// stream reads a server-sent event stream until it ends, ctx ends or fn returns false. A
// dropped connection opens again with Last-Event-ID, so no event is lost or repeated.
func (c *remote) stream(ctx context.Context, path string, fn func(sseEvent) bool) error {
	lastID := ""
	backoff := 500 * time.Millisecond
	for {
		req, err := c.request(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "text/event-stream")
		if lastID != "" {
			req.Header.Set("Last-Event-ID", lastID)
		}
		resp, err := c.http.Do(req)
		if err == nil && resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return decodeAPIError(resp.StatusCode, b)
		}
		if err == nil {
			done, id := readEvents(resp.Body, lastID, fn)
			_ = resp.Body.Close()
			lastID = id
			if done {
				return nil
			}
			backoff = 500 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 10*time.Second)
	}
}

// readEvents parses events from r. It returns true when fn stops the stream or the server
// sends "end", and the ID of the last event.
func readEvents(r io.Reader, lastID string, fn func(sseEvent) bool) (bool, string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var ev sseEvent
	var data []string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) > 0 || ev.Event != "" {
				ev.Data = strings.Join(data, "\n")
				if ev.ID != "" {
					lastID = ev.ID
				}
				if ev.Event == "end" || !fn(ev) {
					return true, lastID
				}
			}
			ev, data = sseEvent{}, nil
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "id:"):
			ev.ID = strings.TrimSpace(line[3:])
		case strings.HasPrefix(line, "event:"):
			ev.Event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[5:], " "))
		}
	}
	return false, lastID
}
