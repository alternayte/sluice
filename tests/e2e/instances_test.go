//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"slices"
	"syscall"
	"testing"
	"time"
)

type instanceItem struct {
	Hostname  string   `json:"hostname"`
	Pools     []string `json:"pools"`
	Executors []string `json:"executors"`
	Online    bool     `json:"online"`
	ID        string   `json:"id"`
}

func listInstances(t *testing.T, c *client) []instanceItem {
	t.Helper()
	var out struct {
		Items []instanceItem `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/instances", nil, http.StatusOK, &out)
	return out.Items
}

func TestSCN_CORE_006_InstancesRegistry(t *testing.T) {
	dbURL := newDatabase(t)
	a := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL, "SLUICE_POOLS": "default,alpha"})
	b := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL, "SLUICE_POOLS": "beta"})
	c := adminClient(t, a)

	items := listInstances(t, c)
	if len(items) != 2 {
		t.Fatalf("want 2 instances, got %d", len(items))
	}
	var sawAlpha, sawBeta bool
	for _, i := range items {
		if !i.Online || len(i.Executors) == 0 || !slices.Contains(i.Executors, "process") {
			t.Fatalf("instance %+v: want online with executors", i)
		}
		sawAlpha = sawAlpha || slices.Contains(i.Pools, "alpha")
		sawBeta = sawBeta || slices.Contains(i.Pools, "beta")
	}
	if !sawAlpha || !sawBeta {
		t.Fatalf("pools not registered: %+v", items)
	}

	if err := b.Stop(syscall.SIGTERM); err != nil {
		t.Logf("stop b: %v", err)
	}
	stoppedAt := time.Now()
	deadline := stoppedAt.Add(90 * time.Second)
	for time.Now().Before(deadline) {
		items = listInstances(t, c)
		for _, i := range items {
			if slices.Contains(i.Pools, "beta") && !i.Online {
				if since := time.Since(stoppedAt); since < 45*time.Second {
					t.Fatalf("instance offline after %s, before the 60 s threshold", since)
				}
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	b2, _ := json.Marshal(items)
	t.Fatalf("stopped instance not offline after 90 s: %s", b2)
}
