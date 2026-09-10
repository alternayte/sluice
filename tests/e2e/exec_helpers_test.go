//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type taskRun struct {
	ID               string          `json:"id"`
	TaskKey          string          `json:"task_key"`
	TaskType         string          `json:"task_type"`
	Attempt          int             `json:"attempt"`
	State            string          `json:"state"`
	Reason           string          `json:"reason"`
	ExecutorType     string          `json:"executor_type"`
	Pool             string          `json:"pool"`
	QueuedAt         *time.Time      `json:"queued_at"`
	StartedAt        *time.Time      `json:"started_at"`
	EndedAt          *time.Time      `json:"ended_at"`
	ExitCode         *int            `json:"exit_code"`
	Error            string          `json:"error"`
	Outputs          map[string]any  `json:"outputs"`
	ReusedFromID     *string         `json:"reused_from_id"`
	ChildExecutionID *string         `json:"child_execution_id"`
	Raw              json.RawMessage `json:"-"`
}

type execDetail struct {
	ID              string            `json:"id"`
	Namespace       string            `json:"namespace"`
	FlowID          *string           `json:"flow_id"`
	State           string            `json:"state"`
	TriggerType     string            `json:"trigger_type"`
	Reason          string            `json:"reason"`
	Error           string            `json:"error"`
	Labels          map[string]string `json:"labels"`
	Inputs          map[string]any    `json:"inputs"`
	Outputs         map[string]any    `json:"outputs"`
	TriggerPayload  map[string]any    `json:"trigger_payload"`
	SnapshotID      string            `json:"snapshot_id"`
	SnapshotVersion *int              `json:"snapshot_version"`
	ChainDepth      int               `json:"chain_depth"`
	SecretKeysUsed  []string          `json:"secret_keys_used"`
	TaskRuns        []taskRun         `json:"task_runs"`
	Children        []struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"children"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
}

// runs returns the task runs of a task key in attempt order.
func (d execDetail) runs(key string) []taskRun {
	var out []taskRun
	for _, t := range d.TaskRuns {
		if t.TaskKey == key {
			out = append(out, t)
		}
	}
	return out
}

// last returns the latest attempt of a task.
func (d execDetail) last(key string) taskRun {
	rs := d.runs(key)
	if len(rs) == 0 {
		return taskRun{}
	}
	return rs[len(rs)-1]
}

var terminal = map[string]bool{"SUCCESS": true, "FAILED": true, "TIMED_OUT": true, "CANCELLED": true, "SKIPPED": true}

// saveFiles creates the namespace (when missing) and saves files in one version.
func saveFiles(t testing.TB, c *client, ns string, files map[string]string) {
	t.Helper()
	r := c.raw(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": ns}, nil)
	if r.Status != http.StatusCreated && r.Status != http.StatusConflict {
		t.Fatalf("create namespace %s: %d %s", ns, r.Status, r.Body)
	}
	var changes []map[string]any
	for p, content := range files {
		ch := map[string]any{"op": "put", "path": p, "content": content}
		if strings.HasSuffix(p, ".sh") {
			ch["executable"] = true
		}
		changes = append(changes, ch)
	}
	c.do(t, http.MethodPost, "/api/v1/namespaces/"+ns+"/changes", map[string]any{"message": "e2e files", "changes": changes}, http.StatusCreated, nil)
}

// triggerFlow starts a flow and returns the execution.
func triggerFlow(t testing.TB, c *client, ns, flowID string, inputs map[string]any, labels map[string]string) execDetail {
	t.Helper()
	body := map[string]any{}
	if inputs != nil {
		body["inputs"] = inputs
	}
	if labels != nil {
		body["labels"] = labels
	}
	var d execDetail
	c.do(t, http.MethodPost, fmt.Sprintf("/api/v1/flows/%s/%s/executions", ns, flowID), body, http.StatusCreated, &d)
	return d
}

func getExec(t testing.TB, c *client, id string) execDetail {
	t.Helper()
	var d execDetail
	c.do(t, http.MethodGet, "/api/v1/executions/"+id, nil, http.StatusOK, &d)
	return d
}

// waitExec polls until pred is true or the timeout passes.
func waitExec(t testing.TB, c *client, id string, timeout time.Duration, pred func(execDetail) bool) execDetail {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var d execDetail
	for {
		d = getExec(t, c, id)
		if pred(d) {
			return d
		}
		if time.Now().After(deadline) {
			b, _ := json.MarshalIndent(d, "", "  ")
			t.Fatalf("execution %s: condition not met within %s:\n%s", id, timeout, b)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitTerminal(t testing.TB, c *client, id string, timeout time.Duration) execDetail {
	t.Helper()
	return waitExec(t, c, id, timeout, func(d execDetail) bool { return terminal[d.State] })
}

type logEntry struct {
	TaskRunID string    `json:"task_run_id"`
	TaskKey   string    `json:"task_key"`
	Attempt   int       `json:"attempt"`
	N         int64     `json:"n"`
	TS        time.Time `json:"ts"`
	Stream    string    `json:"stream"`
	Text      string    `json:"text"`
}

// allLogs pages through the log endpoint until no more lines come.
func allLogs(t testing.TB, c *client, id, task string) []logEntry {
	t.Helper()
	var out []logEntry
	cursor := ""
	for i := 0; i < 1000; i++ {
		q := url.Values{"limit": {"5000"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		if task != "" {
			q.Set("task", task)
		}
		var page struct {
			Lines      []logEntry `json:"lines"`
			NextCursor string     `json:"next_cursor"`
			Done       bool       `json:"done"`
		}
		c.do(t, http.MethodGet, "/api/v1/executions/"+id+"/logs?"+q.Encode(), nil, http.StatusOK, &page)
		out = append(out, page.Lines...)
		cursor = page.NextCursor
		if len(page.Lines) == 0 {
			return out
		}
	}
	return out
}

func logText(lines []logEntry) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Stream + ": " + l.Text + "\n")
	}
	return b.String()
}

// TestExecutionSmoke runs a bash task that logs a line and emits an output.
func TestExecutionSmoke(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	admin := adminClient(t, p)
	saveFiles(t, admin, "smoke", map[string]string{
		"hello.sh":        "echo hello from bash\necho '{\"type\":\"output\",\"key\":\"rows\",\"value\":5}' >> \"$SLUICE_OUTPUTS\"\n",
		"hello.flow.yaml": "id: hello\ntasks:\n  - id: say\n    type: script\n    file: hello.sh\n",
	})
	d := triggerFlow(t, admin, "smoke", "hello", nil, nil)
	d = waitTerminal(t, admin, d.ID, 60*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("state %s: %+v\n%s", d.State, d, p.Logs())
	}
	if v, _ := d.last("say").Outputs["rows"].(float64); v != 5 {
		t.Fatalf("outputs %+v", d.last("say").Outputs)
	}
	if !strings.Contains(logText(allLogs(t, admin, d.ID, "")), "stdout: hello from bash") {
		t.Fatalf("logs: %s", logText(allLogs(t, admin, d.ID, "")))
	}
}
