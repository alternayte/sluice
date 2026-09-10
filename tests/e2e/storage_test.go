//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/testutil/storetest"
)

func TestSCN_CORE_003_HealthReadyMetrics(t *testing.T) {
	ctx := context.Background()
	m, err := storetest.StartMinIO(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Container.Terminate(context.Background()) })
	m.CreateBucket(t, "ready")
	p := startServer(t, map[string]string{
		"SLUICE_DATABASE_URL":         newDatabase(t),
		"SLUICE_STORAGE_TYPE":         "s3",
		"SLUICE_S3_BUCKET":            "ready",
		"SLUICE_S3_REGION":            "us-east-1",
		"SLUICE_S3_ENDPOINT":          m.Endpoint,
		"SLUICE_S3_FORCE_PATH_STYLE":  "true",
		"SLUICE_S3_ACCESS_KEY_ID":     m.User,
		"SLUICE_S3_SECRET_ACCESS_KEY": m.Password,
	})
	c := newCookieClient(p.URL)
	if r := c.raw(t, http.MethodGet, "/readyz", nil, nil); r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"storage":"ok"`) {
		t.Fatalf("readyz before: %d %s", r.Status, r.Body)
	}
	r := c.raw(t, http.MethodGet, "/metrics", nil, nil)
	for _, series := range []string{"sluice_executions{", "sluice_task_runs{", "sluice_queue_depth{", "sluice_http_request_duration_seconds_bucket{"} {
		if !strings.Contains(string(r.Body), series) {
			t.Errorf("metrics miss %s", series)
		}
	}

	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	timeout := 5 * time.Second
	if err := m.Container.Stop(stopCtx, &timeout); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		r = c.raw(t, http.MethodGet, "/readyz", nil, nil)
		if r.Status == http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("readyz stayed %d after storage stop: %s", r.Status, r.Body)
		}
		time.Sleep(500 * time.Millisecond)
	}
	var res struct {
		Status string   `json:"status"`
		Failed []string `json:"failed"`
	}
	if err := json.Unmarshal(r.Body, &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "fail" || !slices.Contains(res.Failed, "storage") || slices.Contains(res.Failed, "database") {
		t.Fatalf("readyz body: %s", r.Body)
	}
	if h := c.raw(t, http.MethodGet, "/healthz", nil, nil); h.Status != http.StatusOK {
		t.Fatalf("healthz during storage outage: %d", h.Status)
	}
}
