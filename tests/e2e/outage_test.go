//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/toxiproxy"
)

// TestSCN_RUN_006_APIOutage blocks the runner API with Toxiproxy for 30 s during a run.
// After recovery no log line is lost (REQ-RUN-006).
func TestSCN_RUN_006_APIOutage(t *testing.T) {
	ctx := context.Background()
	port := freePort(t)
	tp, err := toxiproxy.Run(ctx, "ghcr.io/shopify/toxiproxy:2.12.0",
		testcontainers.WithExposedPorts("8666/tcp"), testcontainers.WithHostPortAccess(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(tp) })
	control, err := tp.URI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proxy := func(method, path string, body any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, control+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			t.Fatalf("toxiproxy %s %s: status %d", method, path, resp.StatusCode)
		}
	}
	proxy(http.MethodPost, "/proxies", map[string]any{"name": "api", "listen": "0.0.0.0:8666",
		"upstream": fmt.Sprintf("%s:%d", testcontainers.HostInternal, port), "enabled": true})
	host, err := tp.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := tp.MappedPort(ctx, "8666/tcp")
	if err != nil {
		t.Fatal(err)
	}

	p := startServerOnPort(t, port, map[string]string{
		"SLUICE_DATABASE_URL": newDatabase(t),
		"SLUICE_INTERNAL_URL": fmt.Sprintf("http://%s:%s", host, mapped.Port()),
	})
	c := adminClient(t, p)
	saveFiles(t, c, "outage", map[string]string{
		"lines.sh":    "for i in $(seq 1 400); do echo \"line $i\"; sleep 0.1; done\n",
		"o.flow.yaml": "id: o\ntasks:\n  - {id: t, type: script, file: lines.sh}\n",
	})
	d := triggerFlow(t, c, "outage", "o", nil, nil)
	for deadline := time.Now().Add(60 * time.Second); !strings.Contains(logText(allLogs(t, c, d.ID, "t")), "stdout: line 20\n"); {
		if time.Now().After(deadline) {
			t.Fatal("no log lines arrived before the outage")
		}
		time.Sleep(200 * time.Millisecond)
	}
	proxy(http.MethodPost, "/proxies/api", map[string]any{"enabled": false})
	time.Sleep(30 * time.Second)
	proxy(http.MethodPost, "/proxies/api", map[string]any{"enabled": true})

	d = waitTerminal(t, c, d.ID, 120*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("execution %s: %s", d.State, d.Error)
	}
	var got []string
	for _, l := range allLogs(t, c, d.ID, "t") {
		if l.Stream == "stdout" {
			got = append(got, l.Text)
		}
	}
	if len(got) != 400 {
		t.Fatalf("%d stdout lines stored, want 400", len(got))
	}
	for i, s := range got {
		if want := "line " + strconv.Itoa(i+1); s != want {
			t.Fatalf("stdout line %d is %q, want %q", i+1, s, want)
		}
	}
}
