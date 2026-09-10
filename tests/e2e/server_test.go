//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// httpResult is a fully read response.
type httpResult struct {
	StatusCode int
	Header     http.Header
}

func get(t *testing.T, url string) (httpResult, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return httpResult{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
}

// TestServerBasics checks health endpoints, request IDs, JSON 404 for unknown API
// routes and the Prometheus series of REQ-CORE-004.
func TestServerBasics(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})

	resp, _ := get(t, p.URL+"/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz %d", resp.StatusCode)
	}
	resp, body := get(t, p.URL+"/readyz")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"database":"ok"`) || !strings.Contains(body, `"migrations":"ok"`) {
		t.Fatalf("readyz %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Fatal("no X-Request-Id header")
	}

	resp, body = get(t, p.URL+"/api/v1/does-not-exist")
	if resp.StatusCode != http.StatusNotFound || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("unknown route: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Error.Code != "not_found" {
		t.Fatalf("unknown route body: %s", body)
	}

	_, body = get(t, p.URL+"/metrics")
	for _, series := range []string{"sluice_executions{", "sluice_task_runs{", "sluice_queue_depth{", "sluice_http_request_duration_seconds_bucket{"} {
		if !strings.Contains(body, series) {
			t.Errorf("metrics miss %s", series)
		}
	}
	if !strings.Contains(p.Logs(), "server started") {
		t.Errorf("no structured start log:\n%s", lastLines(p.Logs(), 20))
	}
}

// TestServerHeadAndBareAPI checks HEAD support on the health and metrics
// routes, and the JSON 404 of the bare /api. The HEAD check guards a
// regression found in the chi router fix round 1.
func TestServerHeadAndBareAPI(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		req, err := http.NewRequest(http.MethodHead, p.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("HEAD %s: %d", path, resp.StatusCode)
		}
		if len(b) != 0 {
			t.Fatalf("HEAD %s: non-empty body %q", path, b)
		}
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(p.URL + "/api")
	if err != nil {
		t.Fatal(err)
	}
	var bare struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&bare)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || bare.Error.Code != "not_found" ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("bare /api: %d %s %q", resp.StatusCode, resp.Header.Get("Content-Type"), bare.Error.Code)
	}

	res, _ := get(t, p.URL+"/api/v1/nothing/here")
	if res.StatusCode != http.StatusNotFound || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("unknown route: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}

	// HEAD on an API GET route gets the status of GET and no body. The first path is a
	// huma route, the second path is an httpx.Raw route. The /api/* catch-all must not
	// take these requests.
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/namespaces/team/file?path=a.py"} {
		getRes, _ := get(t, p.URL+path)
		req, err := http.NewRequest(http.MethodHead, p.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if getRes.StatusCode != http.StatusUnauthorized || resp.StatusCode != getRes.StatusCode {
			t.Fatalf("HEAD %s: %d, GET: %d, want 401 for both", path, resp.StatusCode, getRes.StatusCode)
		}
		if len(b) != 0 {
			t.Fatalf("HEAD %s: non-empty body %q", path, b)
		}
		if resp.Header.Get("X-Request-Id") == "" || resp.Header.Get("X-Content-Type-Options") == "" {
			t.Fatalf("HEAD %s: no request ID or security headers: %v", path, resp.Header)
		}
	}

	// HEAD on an unknown API path still gets the JSON 404 status.
	req, err := http.NewRequest(http.MethodHead, p.URL+"/api/v1/nothing/here", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("HEAD unknown route: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}
