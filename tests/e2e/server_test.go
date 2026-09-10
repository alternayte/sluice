//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
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
