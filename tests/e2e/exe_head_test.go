//go:build e2e

package e2e

import (
	"net/http"
	"testing"
	"time"
)

// TestSCN_EXE_001_NextExecutionPinsHead checks the other half of REQ-EXE-001 (DI-41): a
// save that changes only a script or data file reaches the next execution, although the
// flow file and its revision stay the same.
func TestSCN_EXE_001_NextExecutionPinsHead(t *testing.T) {
	dbURL := newDatabase(t)
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL})
	c := adminClient(t, p)
	saveFiles(t, c, "head", map[string]string{
		"data.txt":       "v1",
		"run.sh":         "v=$(cat data.txt)\necho \"{\\\"type\\\":\\\"output\\\",\\\"key\\\":\\\"data\\\",\\\"value\\\":\\\"$v\\\"}\" >> \"$SLUICE_OUTPUTS\"\n",
		"head.flow.yaml": "id: head\ntasks:\n  - {id: t, type: script, file: run.sh}\n",
	})
	first := waitTerminal(t, c, triggerFlow(t, c, "head", "head", nil, nil).ID, time.Minute)
	if first.State != "SUCCESS" || first.last("t").Outputs["data"] != "v1" {
		t.Fatalf("first run %s outputs %v", first.State, first.last("t").Outputs)
	}

	saveFiles(t, c, "head", map[string]string{"data.txt": "v2"})
	second := waitTerminal(t, c, triggerFlow(t, c, "head", "head", nil, nil).ID, time.Minute)
	if second.State != "SUCCESS" || second.last("t").Outputs["data"] != "v2" {
		t.Fatalf("second run %s outputs %v: the data change did not reach the new execution", second.State, second.last("t").Outputs)
	}
	if second.SnapshotVersion == nil || *second.SnapshotVersion != 2 {
		t.Fatalf("second run pins version %v, want 2", second.SnapshotVersion)
	}
	var revisions int
	dbQueryRow(t, dbURL, `SELECT count(*) FROM flow_revisions r JOIN flows f ON f.id = r.flow_id WHERE f.flow_key = 'head'`, nil, &revisions)
	if revisions != 1 {
		t.Fatalf("%d revisions, want 1: the flow file did not change", revisions)
	}

	var runs struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/executions?flow=head/head", nil, http.StatusOK, &runs)
	if len(runs.Items) != 2 {
		t.Fatalf("%d executions", len(runs.Items))
	}
}
