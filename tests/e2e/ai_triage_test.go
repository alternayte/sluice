//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/testutil/llmserver"
)

const llmKey = "llm-api-key-4242"

// aiServer starts a server with a builtin secret for the API key and a scripted LLM server.
func aiServer(t *testing.T, extra map[string]string) (*Proc, *client, *llmserver.Server) {
	t.Helper()
	llm := llmserver.Start(t)
	env := map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_MASTER_KEYS": masterKey("k1", 1)}
	for k, v := range extra {
		env[k] = v
	}
	p := startServer(t, env)
	admin := adminClient(t, p)
	admin.do(t, http.MethodPut, "/api/v1/secrets/LLM_KEY", map[string]string{"value": llmKey}, http.StatusOK, nil)
	return p, admin, llm
}

func setProvider(t *testing.T, admin *client, llm *llmserver.Server, autoTriage bool) {
	t.Helper()
	admin.do(t, http.MethodPut, "/api/v1/ai/provider", map[string]any{"type": "anthropic", "base_url": llm.URL, "model": "claude-test",
		"api_key_secret_key": "LLM_KEY", "auto_triage": autoTriage}, http.StatusOK, nil)
}

type insightOut struct {
	Status        string `json:"status"`
	Summary       string `json:"summary"`
	ProbableCause string `json:"probable_cause"`
	SuggestedFix  string `json:"suggested_fix"`
	Confidence    string `json:"confidence"`
	Model         string `json:"model"`
	Error         string `json:"error"`
	Evidence      []struct {
		Task string `json:"task"`
		Line int64  `json:"line"`
		Text string `json:"text"`
	} `json:"evidence"`
}

// waitInsight polls until the latest insight of an execution is done or failed.
func waitInsight(t *testing.T, c *client, execID string, timeout time.Duration) insightOut {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var list struct {
			Items []insightOut `json:"items"`
		}
		c.do(t, http.MethodGet, "/api/v1/executions/"+execID+"/insights", nil, http.StatusOK, &list)
		if len(list.Items) > 0 && (list.Items[0].Status == "done" || list.Items[0].Status == "failed") {
			return list.Items[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no finished insight within %s: %+v", timeout, list.Items)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// anthropicContext returns the system text and the user text of a recorded request.
func anthropicContext(t *testing.T, body []byte) (string, string) {
	t.Helper()
	var req struct {
		System   string `json:"system"`
		Messages []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, m := range req.Messages {
		for _, c := range m.Content {
			b.WriteString(c.Text)
		}
	}
	return req.System, b.String()
}

func TestSCN_AI_007_AutoTriage(t *testing.T) {
	p, admin, llm := aiServer(t, nil)
	setProvider(t, admin, llm, true)
	saveFiles(t, admin, "tri", map[string]string{
		"load.sh":        "echo loading\necho rows=5\n",
		"etl.flow.yaml":  "id: etl\ntasks:\n  - {id: load, type: script, file: load.sh}\n",
		"note.txt":       "first\n",
		"namespace.yaml": "description: triage test\n",
	})
	if d := waitTerminal(t, admin, triggerFlow(t, admin, "tri", "etl", nil, nil).ID, time.Minute); d.State != "SUCCESS" {
		t.Fatalf("first run %s", d.State)
	}
	if len(llm.Requests()) != 0 {
		t.Fatal("a successful execution got a triage")
	}
	saveFiles(t, admin, "tri", map[string]string{"load.sh": "echo loading\necho 'ERROR: disk full on /data' >&2\nexit 3\n"})
	llm.Push(llmserver.Reply{JSON: `{"summary":"The load task failed.","probable_cause":"The disk /data is full.",` +
		`"evidence":[{"task":"load","line":2,"text":"ERROR: disk full on /data"},{"task":"load","line":7,"text":"invented line"}],` +
		`"suggested_fix":"Free space on /data.","confidence":"high"}`})
	d := waitTerminal(t, admin, triggerFlow(t, admin, "tri", "etl", nil, nil).ID, time.Minute)
	if d.State != "FAILED" {
		t.Fatalf("second run %s", d.State)
	}

	in := waitInsight(t, admin, d.ID, time.Minute)
	if in.Status != "done" || in.Summary == "" || in.ProbableCause == "" || in.SuggestedFix == "" || in.Confidence != "high" || in.Model != "claude-test" {
		t.Fatalf("insight %+v\n%s", in, p.Logs())
	}
	if len(in.Evidence) != 1 || in.Evidence[0].Text != "ERROR: disk full on /data" || in.Evidence[0].Task != "load" {
		t.Fatalf("evidence %+v: the invented line must be removed", in.Evidence)
	}
	reqs := llm.Requests()
	if len(reqs) != 1 || reqs[0].Header.Get("x-api-key") != llmKey {
		t.Fatalf("%d requests", len(reqs))
	}
	_, user := anthropicContext(t, reqs[0].Body)
	for _, want := range []string{"Diff against the last successful execution", "-echo rows=5", "+exit 3", "exit code 3", "ERROR: disk full on /data"} {
		if !strings.Contains(user, want) {
			t.Fatalf("the triage request lacks %q:\n%s", want, user)
		}
	}
}

func TestSCN_AI_008_ContextLimit(t *testing.T) {
	_, admin, llm := aiServer(t, map[string]string{"SLUICE_AI_MAX_CONTEXT_CHARS": "5000"})
	setProvider(t, admin, llm, false)
	saveFiles(t, admin, "big", map[string]string{
		"big.sh":        "for i in $(seq 1 50000); do echo \"line-$i\"; done\nexit 1\n",
		"big.flow.yaml": "id: big\ntasks:\n  - {id: t, type: script, file: big.sh}\n",
	})
	d := waitTerminal(t, admin, triggerFlow(t, admin, "big", "big", nil, nil).ID, 3*time.Minute)
	if d.State != "FAILED" {
		t.Fatalf("state %s", d.State)
	}
	llm.Push(llmserver.Reply{JSON: `{"summary":"s","probable_cause":"c","evidence":[],"suggested_fix":"f","confidence":"low"}`})
	admin.do(t, http.MethodPost, "/api/v1/executions/"+d.ID+"/insights", nil, http.StatusAccepted, nil)
	if in := waitInsight(t, admin, d.ID, time.Minute); in.Status != "done" {
		t.Fatalf("insight %+v", in)
	}
	system, user := anthropicContext(t, llm.Requests()[0].Body)
	if n := len(system) + len(user); n > 5000 {
		t.Fatalf("the model context has %d characters, limit 5000", n)
	}
	if !strings.Contains(user, "] line-1\n") || !strings.Contains(user, "] line-50000\n") {
		t.Fatalf("the context lacks the head or the tail lines:\n%s", user)
	}
}

func TestSCN_AI_009_StepLimit(t *testing.T) {
	_, admin, llm := aiServer(t, nil)
	setProvider(t, admin, llm, false)
	var n atomic.Int64
	llm.SetResponder(func(string, []byte) llmserver.Reply {
		return llmserver.Reply{ToolCalls: []llmserver.ToolCall{{ID: fmt.Sprintf("call_%d", n.Add(1)), Name: "list_namespaces", Input: "{}"}}}
	})
	var conv struct {
		ID string `json:"id"`
	}
	admin.do(t, http.MethodPost, "/api/v1/ai/conversations", map[string]any{}, http.StatusCreated, &conv)
	r := admin.raw(t, http.MethodPost, "/api/v1/ai/conversations/"+conv.ID+"/messages", map[string]string{"text": "List the namespaces again and again."}, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d: %s", r.Status, r.Body)
	}
	stream := string(r.Body)
	if !strings.Contains(stream, "event: error\ndata: {\"code\":\"step_limit_reached\"") || !strings.Contains(stream, `"stop":"step_limit_reached"`) {
		t.Fatalf("stream:\n%s", stream)
	}
	if got := len(llm.Requests()); got != 20 {
		t.Fatalf("%d model calls, want 20", got)
	}
	if calls := strings.Count(stream, "event: tool_result\n"); calls != 20 {
		t.Fatalf("%d tool results, want 20", calls)
	}
}
