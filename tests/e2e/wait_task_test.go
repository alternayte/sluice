//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWaitTask pins the wait task: the task run waits with no instance, a resume gives its
// values as outputs, a reject fails it with reason rejected, the answer is validated, a second
// answer is refused, the task timeout ends the wait, and the audit log records the answer.
func TestWaitTask(t *testing.T) {
	dbURL := newDatabase(t)
	env := map[string]string{"SLUICE_DATABASE_URL": dbURL}
	p := startServer(t, env)
	c := adminClient(t, p)
	saveFiles(t, c, "appr", map[string]string{
		"deploy.flow.yaml": `id: deploy
inputs:
  - { id: version, type: string, default: "1.4.0" }
tasks:
  - { id: build, type: command, command: ["echo", "built"] }
  - id: approve
    type: wait
    depends_on: [build]
    message: "Deploy ${{ inputs.version }}?"
    fields:
      - { id: reason, type: string, required: true }
      - { id: replicas, type: int, default: 2 }
  - id: deploy
    type: command
    depends_on: [approve]
    command: ["echo", "deploy ${{ tasks.approve.outputs.reason }} x${{ tasks.approve.outputs.replicas }}"]
  - id: rollback
    type: command
    depends_on: [approve]
    run_if: failure
    command: ["echo", "rolled back"]
`,
		"short.flow.yaml": `id: short
tasks:
  - { id: approve, type: wait, timeout: 2s, retry: { max_attempts: 3 } }
`,
	})
	session := login(t, p.URL, adminEmail, adminPassword)
	operatorToken, _ := createToken(t, session, "operator")
	viewerToken, _ := createToken(t, session, "viewer")
	cli := map[string]string{"SLUICE_URL": p.URL, "SLUICE_TOKEN": operatorToken}
	waiting := func(id string) execDetail {
		t.Helper()
		return waitExec(t, c, id, 30*time.Second, func(x execDetail) bool { return x.last("approve").State == "WAITING" })
	}

	outer := t
	t.Run("the task waits through a restart of the server, and a resume gives its values as outputs", func(t *testing.T) {
		d := waiting(triggerFlow(t, c, "appr", "deploy", nil, nil).ID)
		if w := d.last("approve").Wait; d.State != "RUNNING" || w == nil || w.Message != "Deploy 1.4.0?" || len(w.Fields) != 2 {
			t.Fatalf("%s: approve waits with %+v", d.State, w)
		}
		stdout, _, code := runCLI(t, cli, "executions", "list", "--waiting", "-o", "json")
		if code != 0 || !strings.Contains(stdout, d.ID) {
			t.Fatalf("list --waiting: exit %d: %s", code, stdout)
		}
		// All instances stop. The wait belongs to no instance, so lost detection leaves it.
		if err := p.Stop(syscall.SIGTERM); err != nil {
			t.Logf("stop: %v", err)
		}
		// The new server belongs to the whole test, not to this subtest.
		p = startServer(outer, env)
		c = tokenClient(p.URL, c.token)
		cli["SLUICE_URL"] = p.URL
		time.Sleep(3 * time.Second)
		if s := getExec(t, c, d.ID).last("approve").State; s != "WAITING" {
			t.Fatalf("after the restart: approve is %s", s)
		}

		_, stderr, code := runCLI(t, cli, "executions", "resume", d.ID, "--task", "approve")
		if code != 1 || !strings.Contains(stderr, "inputs.reason: required input is missing") {
			t.Fatalf("resume without a required field: exit %d: %s", code, stderr)
		}
		viewer := tokenClient(p.URL, viewerToken)
		viewer.do(t, http.MethodPost, "/api/v1/executions/"+d.ID+"/tasks/approve/resume", map[string]any{"inputs": map[string]any{"reason": "ok"}}, http.StatusForbidden, nil)
		if s := getExec(t, c, d.ID).last("approve").State; s != "WAITING" {
			t.Fatalf("after the refused answers: approve is %s", s)
		}

		if _, stderr, code := runCLI(t, cli, "executions", "resume", d.ID, "--task", "approve", "--input", "reason=2026"); code != 0 {
			t.Fatalf("resume: exit %d: %s", code, stderr)
		}
		d = waitTerminal(t, c, d.ID, 60*time.Second)
		if d.State != "SUCCESS" || d.last("rollback").State != "SKIPPED" {
			t.Fatalf("%s %s, rollback %s", d.State, d.Error, d.last("rollback").State)
		}
		// reason is a string field, so 2026 stays text. replicas takes its default.
		if logs := logText(allLogs(t, c, d.ID, "deploy")); !strings.Contains(logs, "deploy 2026 x2") {
			t.Fatalf("deploy:\n%s", logs)
		}
		if _, stderr, code := runCLI(t, cli, "executions", "resume", d.ID, "--task", "approve", "--input", "reason=again"); code != 1 || !strings.Contains(stderr, "not_waiting") {
			t.Fatalf("second resume: exit %d: %s", code, stderr)
		}
		var audit struct {
			Items []struct {
				Action  string          `json:"action"`
				Details json.RawMessage `json:"details"`
			} `json:"items"`
		}
		c.do(t, http.MethodGet, "/api/v1/audit?action=execution.resume", nil, http.StatusOK, &audit)
		if len(audit.Items) != 1 || !strings.Contains(string(audit.Items[0].Details), `"reason":"2026"`) {
			t.Fatalf("audit: %+v", audit.Items)
		}
	})

	t.Run("a reject fails the task with reason rejected, and run_if failure runs", func(t *testing.T) {
		d := waiting(triggerFlow(t, c, "appr", "deploy", nil, nil).ID)
		if _, stderr, code := runCLI(t, cli, "executions", "reject", d.ID, "--task", "approve", "--message", "not today"); code != 0 {
			t.Fatalf("reject: exit %d: %s", code, stderr)
		}
		d = waitTerminal(t, c, d.ID, 60*time.Second)
		a := d.last("approve")
		if d.State != "FAILED" || a.State != "FAILED" || a.Reason != "rejected" || a.Error != "not today" || len(d.runs("approve")) != 1 {
			t.Fatalf("%s: approve %s %s %q", d.State, a.State, a.Reason, a.Error)
		}
		if d.last("deploy").State != "SKIPPED" || d.last("rollback").State != "SUCCESS" {
			t.Fatalf("deploy %s, rollback %s", d.last("deploy").State, d.last("rollback").State)
		}
	})

	t.Run("the task timeout ends the wait TIMED_OUT, with no retry", func(t *testing.T) {
		d := waitTerminal(t, c, triggerFlow(t, c, "appr", "short", nil, nil).ID, 90*time.Second)
		a := d.runs("approve")
		if d.State != "FAILED" && d.State != "TIMED_OUT" || len(a) != 1 || a[0].State != "TIMED_OUT" {
			t.Fatalf("%s: approve %+v", d.State, a)
		}
	})

	t.Run("a cancel ends the wait CANCELLED", func(t *testing.T) {
		d := waiting(triggerFlow(t, c, "appr", "deploy", nil, nil).ID)
		c.do(t, http.MethodPost, "/api/v1/executions/"+d.ID+"/cancel", nil, http.StatusAccepted, nil)
		d = waitTerminal(t, c, d.ID, 60*time.Second)
		if d.State != "CANCELLED" || d.last("approve").State != "CANCELLED" {
			t.Fatalf("%s: approve %s", d.State, d.last("approve").State)
		}
	})
}
