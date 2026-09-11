//go:build perf

package perf

import (
	"bufio"
	"bytes"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

const (
	ingestSeconds    = 60
	ingestLinesPerS  = 5000
	ingestRoute      = `"/api/runner/v1/task-runs/{taskRunId}/logs"`
	ingestP95Seconds = "0.5"
)

// TestSCN_NFR_002_LogIngest sends 5 000 lines per second for 60 s from one runner to one
// instance. No line is lost, and 95 % of the log batches are ingested within 500 ms (NFR-002).
func TestSCN_NFR_002_LogIngest(t *testing.T) {
	s := startServer(t, map[string]string{"SLUICE_DATABASE_URL": pgtest.Shared(t).NewDatabase(t)})
	c := adminClient(t, s)
	c.do(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": "perf"}, http.StatusCreated, nil)
	gen := "for s in $(seq 1 " + strconv.Itoa(ingestSeconds) + "); do seq -f \"l$s-%g\" 1 " + strconv.Itoa(ingestLinesPerS) + "; sleep 1; done\n"
	c.do(t, http.MethodPost, "/api/v1/namespaces/perf/changes", map[string]any{"message": "perf", "changes": []map[string]any{
		{"op": "put", "path": "gen.sh", "content": gen, "executable": true},
		{"op": "put", "path": "ingest.flow.yaml", "content": "id: ingest\ntasks:\n  - {id: t, type: script, file: gen.sh}\n"},
	}}, http.StatusCreated, nil)
	var ex struct {
		ID    string `json:"id"`
		State string `json:"state"`
		Error string `json:"error"`
	}
	c.do(t, http.MethodPost, "/api/v1/flows/perf/ingest/executions", map[string]any{}, http.StatusCreated, &ex)
	start := time.Now()
	for deadline := time.Now().Add(5 * time.Minute); ; time.Sleep(time.Second) {
		c.do(t, http.MethodGet, "/api/v1/executions/"+ex.ID, nil, http.StatusOK, &ex)
		if ex.State == "SUCCESS" || ex.State == "FAILED" || ex.State == "TIMED_OUT" || ex.State == "CANCELLED" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution still %s after 5 minutes", ex.State)
		}
	}
	if ex.State != "SUCCESS" {
		t.Fatalf("execution %s: %s", ex.State, ex.Error)
	}

	body := c.do(t, http.MethodGet, "/api/v1/executions/"+ex.ID+"/logs/download?task=t", nil, http.StatusOK, nil)
	lineRe := regexp.MustCompile(`^l\d+-\d+$`)
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		if _, text, ok := strings.Cut(sc.Text(), "] stdout: "); ok && lineRe.MatchString(text) {
			seen[text] = true
		}
	}
	want := ingestSeconds * ingestLinesPerS
	lost := want - len(seen)

	resp, err := http.Get(s.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	var within, batches float64
	msc := bufio.NewScanner(resp.Body)
	msc.Buffer(make([]byte, 64<<10), 4<<20)
	for msc.Scan() {
		l := msc.Text()
		if !strings.Contains(l, ingestRoute) {
			continue
		}
		fields := strings.Fields(l)
		v, _ := strconv.ParseFloat(fields[len(fields)-1], 64)
		switch {
		case strings.HasPrefix(l, "sluice_http_request_duration_seconds_bucket{") && strings.Contains(l, `le="`+ingestP95Seconds+`"`):
			within += v
		case strings.HasPrefix(l, "sluice_http_request_duration_seconds_count{"):
			batches += v
		}
	}
	_ = resp.Body.Close()
	if batches == 0 {
		t.Fatal("no log batch durations in /metrics")
	}
	share := within / batches
	writeReport(t, "nfr-002", map[string]any{
		"scenario": "SCN-NFR-002", "lines_sent": want, "lines_stored": len(seen), "lines_lost": lost,
		"batches": batches, "share_within_500ms": share, "run_seconds": time.Since(start).Seconds(),
	})
	if lost != 0 {
		t.Errorf("%d of %d lines lost", lost, want)
	}
	if share < 0.95 {
		t.Errorf("%.1f %% of %v batches ingested within 500 ms, want at least 95 %%", share*100, batches)
	}
}
