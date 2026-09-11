//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

type webhookKey struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

// rotateKey creates a new webhook key for a trigger.
func rotateKey(t testing.TB, c *client, ns, flowID, trigger string) webhookKey {
	t.Helper()
	var k webhookKey
	c.do(t, http.MethodPost, fmt.Sprintf("/api/v1/flows/%s/%s/triggers/%s/webhook-key", ns, flowID, trigger), nil, http.StatusOK, &k)
	return k
}

// postHook posts body to a webhook URL without credentials of a Sluice user.
func postHook(t testing.TB, u string, body []byte, headers map[string]string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// flowExecs lists the executions of one flow, newest first. triggerType can be empty.
func flowExecs(t testing.TB, c *client, ns, flowID, triggerType string) execList {
	t.Helper()
	q := url.Values{"flow": {ns + "/" + flowID}, "limit": {"200"}}
	if triggerType != "" {
		q.Set("trigger_type", triggerType)
	}
	var l execList
	c.do(t, http.MethodGet, "/api/v1/executions?"+q.Encode(), nil, http.StatusOK, &l)
	return l
}

// TestSCN_TRG_005_Webhook checks the webhook key, rotation, the body limit and the
// trigger payload (REQ-TRG-004, SI-05).
func TestSCN_TRG_005_Webhook(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	saveFiles(t, c, "hooks", map[string]string{
		"h.flow.yaml": "id: h\ninputs:\n  - {id: name, type: string, required: true}\n" +
			"triggers:\n  - {id: in, type: webhook, inputs: {name: \"${{ trigger.body.name }}\"}}\n" +
			"tasks:\n  - {id: t, type: command, command: [\"echo\", \"input ${{ inputs.name }} body ${{ trigger.body.name }}\"]}\n",
	})
	k := rotateKey(t, c, "hooks", "h", "in")
	if k.URL != p.URL+"/hooks/"+k.Key {
		t.Fatalf("webhook URL %q", k.URL)
	}
	code, body := postHook(t, k.URL, []byte(`{"name":"sluice"}`), map[string]string{"X-Source": "e2e", "Authorization": "Bearer caller-credential"})
	if code != http.StatusAccepted {
		t.Fatalf("valid key: status %d %s", code, body)
	}
	var acc struct {
		ExecutionID string `json:"execution_id"`
	}
	if err := json.Unmarshal(body, &acc); err != nil || acc.ExecutionID == "" {
		t.Fatalf("response %s", body)
	}
	d := waitTerminal(t, c, acc.ExecutionID, 60*time.Second)
	if d.State != "SUCCESS" || d.TriggerType != "webhook" || d.Inputs["name"] != "sluice" {
		t.Fatalf("execution %s trigger %s inputs %v: %s", d.State, d.TriggerType, d.Inputs, d.Error)
	}
	if logs := logText(allLogs(t, c, d.ID, "t")); !strings.Contains(logs, "input sluice body sluice") {
		t.Fatalf("trigger.body is not available to the flow:\n%s", logs)
	}
	headers, _ := d.TriggerPayload["headers"].(map[string]any)
	if headers["x-source"] != "e2e" || headers["authorization"] != nil {
		t.Fatalf("trigger headers %v", headers)
	}

	if code, _ := postHook(t, p.URL+"/hooks/not-a-key", []byte(`{}`), nil); code != http.StatusNotFound {
		t.Fatalf("wrong key: status %d, want 404", code)
	}
	k2 := rotateKey(t, c, "hooks", "h", "in")
	if code, _ := postHook(t, k.URL, []byte(`{"name":"old"}`), nil); code != http.StatusNotFound {
		t.Fatalf("old key after rotation: status %d, want 404", code)
	}
	if code, b := postHook(t, k2.URL, []byte(`{"name":"new"}`), nil); code != http.StatusAccepted {
		t.Fatalf("new key: status %d %s", code, b)
	}
	if code, _ := postHook(t, k2.URL, bytes.Repeat([]byte("a"), 2<<20), nil); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("2 MiB body: status %d, want 413", code)
	}
}

// TestSCN_TRG_006_FlowTriggers fires a downstream flow on upstream FAILED and stops an
// A→B→A chain at depth 10 with an audit event (REQ-TRG-005).
func TestSCN_TRG_006_FlowTriggers(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	task := "tasks:\n  - {id: t, type: command, command: [\"true\"]}\n"
	saveFiles(t, c, "chain", map[string]string{
		"up.flow.yaml": "id: up\ninputs:\n  - {id: fail, type: boolean, default: false}\n" +
			"tasks:\n  - {id: t, type: command, command: [\"sh\", \"-c\", \"test ${{ inputs.fail }} = false\"]}\n",
		"down.flow.yaml": "id: down\ninputs:\n  - {id: upstream, type: string}\n" +
			"triggers:\n  - {id: on_fail, type: flow, flow: chain/up, states: [FAILED], inputs: {upstream: \"${{ trigger.execution_id }}\"}}\n" + task,
		"ping.flow.yaml": "id: ping\ntriggers:\n  - {id: back, type: flow, flow: chain/pong, states: [SUCCESS]}\n" + task,
		"pong.flow.yaml": "id: pong\ntriggers:\n  - {id: forth, type: flow, flow: chain/ping, states: [SUCCESS]}\n" + task,
	})

	failed := triggerFlow(t, c, "chain", "up", map[string]any{"fail": true}, nil)
	if d := waitTerminal(t, c, failed.ID, 60*time.Second); d.State != "FAILED" {
		t.Fatalf("upstream %s", d.State)
	}
	var downID string
	for deadline := time.Now().Add(30 * time.Second); downID == ""; time.Sleep(200 * time.Millisecond) {
		if l := flowExecs(t, c, "chain", "down", ""); len(l.Items) > 0 {
			downID = l.Items[0].ID
		} else if time.Now().After(deadline) {
			t.Fatal("the downstream flow did not fire on FAILED")
		}
	}
	down := waitTerminal(t, c, downID, 60*time.Second)
	if down.TriggerType != "flow" || down.Inputs["upstream"] != failed.ID || down.TriggerPayload["execution_id"] != failed.ID || down.ChainDepth != 1 {
		t.Fatalf("downstream trigger %s inputs %v payload %v depth %d", down.TriggerType, down.Inputs, down.TriggerPayload, down.ChainDepth)
	}
	ok := triggerFlow(t, c, "chain", "up", nil, nil)
	if d := waitTerminal(t, c, ok.ID, 60*time.Second); d.State != "SUCCESS" {
		t.Fatalf("upstream %s", d.State)
	}
	time.Sleep(3 * time.Second)
	if n := len(flowExecs(t, c, "chain", "down", "").Items); n != 1 {
		t.Fatalf("downstream fired %d times, want 1: SUCCESS is not a listed state", n)
	}

	triggerFlow(t, c, "chain", "ping", nil, nil)
	chain := func() []string {
		var ids []string
		for _, f := range []string{"ping", "pong"} {
			for _, it := range flowExecs(t, c, "chain", f, "").Items {
				if !terminal[it.State] {
					return nil
				}
				ids = append(ids, it.ID)
			}
		}
		return ids
	}
	var ids []string
	for deadline := time.Now().Add(180 * time.Second); len(ids) < 11; time.Sleep(time.Second) {
		ids = chain()
		if time.Now().After(deadline) {
			t.Fatalf("chain has %d ended executions, want 11", len(ids))
		}
	}
	time.Sleep(3 * time.Second)
	if ids = chain(); len(ids) != 11 {
		t.Fatalf("chain has %d executions after it stopped, want 11", len(ids))
	}
	depth := 0
	for _, id := range ids {
		depth = max(depth, getExec(t, c, id).ChainDepth)
	}
	if depth != 10 {
		t.Fatalf("deepest chain_depth %d, want 10", depth)
	}
	var events struct {
		Items []map[string]any `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/audit?action=trigger.chain_depth_exceeded", nil, http.StatusOK, &events)
	if len(events.Items) != 1 {
		t.Fatalf("%d chain depth audit events, want 1", len(events.Items))
	}
}

// TestSCN_TRG_007_Upcoming lists the next fire times sorted and omits disabled flows (REQ-TRG-006).
func TestSCN_TRG_007_Upcoming(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	task := "tasks:\n  - {id: t, type: command, command: [\"true\"]}\n"
	saveFiles(t, c, "soon", map[string]string{
		"a.flow.yaml": "id: a\ntriggers:\n  - {id: s, type: schedule, cron: \"0 3 * * *\"}\n" + task,
		"b.flow.yaml": "id: b\ntriggers:\n  - {id: s, type: schedule, cron: \"30 1 * * *\", timezone: Europe/Zurich}\n" + task,
		"c.flow.yaml": "id: c\ntriggers:\n  - {id: s, type: schedule, cron: \"*/5 * * * *\"}\n" + task,
		"d.flow.yaml": "id: d\ntriggers:\n  - {id: s, type: schedule, cron: \"0 2 * * *\"}\n" + task,
	})
	c.do(t, http.MethodPatch, "/api/v1/flows/soon/d", map[string]any{"disabled": true}, http.StatusOK, nil)
	var l struct {
		Items []struct {
			Namespace  string    `json:"namespace"`
			FlowID     string    `json:"flow_id"`
			TriggerID  string    `json:"trigger_id"`
			NextFireAt time.Time `json:"next_fire_at"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/schedules/upcoming?namespace=soon&limit=50", nil, http.StatusOK, &l)
	var flows []string
	now := time.Now()
	for i, it := range l.Items {
		flows = append(flows, it.FlowID)
		if !it.NextFireAt.After(now.Add(-2*time.Second)) || it.NextFireAt.After(now.Add(25*time.Hour)) {
			t.Errorf("%s next fire time %s is not within the next day", it.FlowID, it.NextFireAt)
		}
		if i > 0 && it.NextFireAt.Before(l.Items[i-1].NextFireAt) {
			t.Errorf("items are not sorted: %s before %s", l.Items[i-1].NextFireAt, it.NextFireAt)
		}
	}
	sort.Strings(flows)
	if strings.Join(flows, ",") != "a,b,c" {
		t.Fatalf("upcoming flows %v, want a, b and c without the disabled flow d", flows)
	}
	if l.Items[0].FlowID != "c" {
		t.Fatalf("first item %s, want c (every 5 minutes)", l.Items[0].FlowID)
	}
}
