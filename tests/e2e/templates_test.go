//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSCN_EXE_012_TemplateResolution resolves inputs, vars precedence, trigger, execution
// and task outputs in args and env. A missing output fails the task with template_error
// before a process starts (REQ-EXE-012).
func TestSCN_EXE_012_TemplateResolution(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	createNamespace(t, c, "tpl")
	saveFiles(t, c, "tpl.child", map[string]string{
		"t.flow.yaml": `id: t
inputs:
  - {id: who, type: string, default: nobody}
variables: {D: flow-d}
triggers:
  - {id: hook, type: webhook, inputs: {who: "${{ trigger.body.who }}"}}
tasks:
  - id: first
    type: command
    command: ["sh", "-c", "echo '{\"type\":\"output\",\"key\":\"v\",\"value\":\"from-first\"}' >> \"$SLUICE_OUTPUTS\""]
  - id: second
    type: command
    depends_on: [first]
    env: {FROM_ENV: "${{ tasks.first.outputs.v }} ${{ execution.namespace }} ${{ execution.flow_id }}"}
    command: ["sh", "-c", "echo \"args $0 $1 $2 $3 $4 $5 $6 env $FROM_ENV\"", "${{ inputs.who }}", "${{ vars.A }}", "${{ vars.B }}", "${{ vars.C }}", "${{ vars.D }}", "${{ trigger.body.who }}", "${{ execution.id }}"]
`,
		"m.flow.yaml": `id: m
tasks:
  - {id: first, type: command, command: ["true"]}
  - {id: second, type: command, depends_on: [first], command: ["echo", "started ${{ tasks.first.outputs.nope }}"]}
`,
	})
	for key, v := range map[string]string{"A": "global-a", "B": "global-b", "C": "global-c", "D": "global-d"} {
		c.do(t, http.MethodPut, "/api/v1/variables/"+key, map[string]string{"value": v}, http.StatusOK, nil)
	}
	c.do(t, http.MethodPut, "/api/v1/namespaces/tpl/variables/B", map[string]string{"value": "parent-b"}, http.StatusOK, nil)
	c.do(t, http.MethodPut, "/api/v1/namespaces/tpl/variables/C", map[string]string{"value": "parent-c"}, http.StatusOK, nil)
	c.do(t, http.MethodPut, "/api/v1/namespaces/tpl.child/variables/C", map[string]string{"value": "child-c"}, http.StatusOK, nil)

	k := rotateKey(t, c, "tpl.child", "t", "hook")
	code, body := postHook(t, k.URL, []byte(`{"who":"hook"}`), nil)
	if code != http.StatusAccepted {
		t.Fatalf("webhook: %d %s", code, body)
	}
	var acc struct {
		ExecutionID string `json:"execution_id"`
	}
	_ = json.Unmarshal(body, &acc)
	d := waitTerminal(t, c, acc.ExecutionID, 60*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("execution %s: %s", d.State, d.Error)
	}
	want := "args hook global-a parent-b child-c flow-d hook " + d.ID + " env from-first tpl.child t"
	if logs := logText(allLogs(t, c, d.ID, "second")); !strings.Contains(logs, want) {
		t.Fatalf("logs of second do not contain %q:\n%s", want, logs)
	}

	m := waitTerminal(t, c, triggerFlow(t, c, "tpl.child", "m", nil, nil).ID, 60*time.Second)
	second := m.last("second")
	if m.State != "FAILED" || second.State != "FAILED" || second.Reason != "template_error" || second.ExitCode != nil {
		t.Fatalf("execution %s, second %s %s exit %v", m.State, second.State, second.Reason, second.ExitCode)
	}
	for _, l := range allLogs(t, c, m.ID, "second") {
		if l.Stream != "system" {
			t.Fatalf("a process of second wrote %q: no process may start", l.Text)
		}
	}
}
