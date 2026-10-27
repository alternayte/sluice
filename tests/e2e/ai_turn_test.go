//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/testutil/llmserver"
)

type convBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ToolName string `json:"tool_name"`
	IsError  bool   `json:"is_error"`
	Stopped  bool   `json:"stopped"`
}

type convDetail struct {
	ID         string `json:"id"`
	AutoAccept bool   `json:"auto_accept"`
	TurnStatus string `json:"turn_status"`
	TurnStop   string `json:"turn_stop"`
	TurnError  string `json:"turn_error"`
	DraftText  string `json:"draft_text"`
	Messages   []struct {
		Role    string      `json:"role"`
		Content []convBlock `json:"content"`
	} `json:"messages"`
	Actions []struct {
		Tool   string `json:"tool"`
		Status string `json:"status"`
	} `json:"actions"`
}

// count returns the blocks of a type in all messages.
func (d convDetail) count(blockType string) int {
	n := 0
	for _, m := range d.Messages {
		for _, b := range m.Content {
			if b.Type == blockType {
				n++
			}
		}
	}
	return n
}

// last returns the last block of the last message.
func (d convDetail) last() convBlock {
	if len(d.Messages) == 0 {
		return convBlock{}
	}
	c := d.Messages[len(d.Messages)-1].Content
	if len(c) == 0 {
		return convBlock{}
	}
	return c[len(c)-1]
}

func getConv(t testing.TB, c *client, id string) convDetail {
	t.Helper()
	var d convDetail
	c.do(t, http.MethodGet, "/api/v1/ai/conversations/"+id, nil, http.StatusOK, &d)
	return d
}

// waitTurn polls until the turn of a conversation no longer runs.
func waitTurn(t testing.TB, c *client, id string, timeout time.Duration) convDetail {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(100 * time.Millisecond) {
		d := getConv(t, c, id)
		if d.TurnStatus != "running" {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("the turn of %s still runs after %s", id, timeout)
		}
	}
}

func newConv(t testing.TB, c *client) string {
	t.Helper()
	var conv struct {
		ID string `json:"id"`
	}
	c.do(t, http.MethodPost, "/api/v1/ai/conversations", map[string]any{}, http.StatusCreated, &conv)
	return conv.ID
}

func say(t testing.TB, c *client, id, text string) {
	t.Helper()
	c.do(t, http.MethodPost, "/api/v1/ai/conversations/"+id+"/messages", map[string]string{"text": text}, http.StatusAccepted, nil)
}

// TestAssistantBackgroundTurn checks that a turn runs without a connected client, that the
// events stream attaches to it with the streamed text, and that stop keeps the partial answer.
func TestAssistantBackgroundTurn(t *testing.T) {
	_, admin, llm := aiServer(t, nil)
	setProvider(t, admin, llm, false)

	answer := "The namespaces are listed below, one by one, with their sources and owners."
	llm.Push(llmserver.Reply{Text: answer, DelayMs: 60})
	id := newConv(t, admin)
	say(t, admin, id, "Which namespaces exist?")
	// The POST returned before the answer. A new stream attaches to the running turn.
	r := admin.raw(t, http.MethodGet, "/api/v1/ai/conversations/"+id+"/events", nil, nil)
	stream := string(r.Body)
	if r.Status != http.StatusOK || !strings.Contains(stream, `"turn_status":"running"`) || !strings.Contains(stream, "event: end") {
		t.Fatalf("events stream %d:\n%s", r.Status, stream)
	}
	if !strings.Contains(stream, `"draft_text":"The name`) {
		t.Fatalf("the stream shows no streamed text:\n%s", stream)
	}
	d := waitTurn(t, admin, id, 30*time.Second)
	if d.TurnStop != "end_turn" || d.last().Text != answer || d.DraftText != "" {
		t.Fatalf("after the turn: %+v", d)
	}
	if r := admin.raw(t, http.MethodPost, "/api/v1/ai/conversations/"+id+"/stop", nil, nil); r.Status != http.StatusConflict ||
		errCode(r.Body) != "turn_not_running" {
		t.Fatalf("stop without a turn: %d %s", r.Status, r.Body)
	}

	llm.Push(llmserver.Reply{Text: strings.Repeat("slow text ", 40), DelayMs: 100}, llmserver.Reply{Text: "Next answer."})
	say(t, admin, id, "Write a long answer.")
	if r := admin.raw(t, http.MethodPost, "/api/v1/ai/conversations/"+id+"/messages", map[string]string{"text": "again"}, nil); r.Status != http.StatusConflict ||
		errCode(r.Body) != "turn_running" {
		t.Fatalf("a second message during a turn: %d %s", r.Status, r.Body)
	}
	for deadline := time.Now().Add(10 * time.Second); getConv(t, admin, id).DraftText == ""; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no streamed text within 10 s")
		}
	}
	admin.do(t, http.MethodPost, "/api/v1/ai/conversations/"+id+"/stop", nil, http.StatusAccepted, nil)
	d = waitTurn(t, admin, id, 10*time.Second)
	if d.TurnStatus != "idle" || d.TurnStop != "stopped" || !d.last().Stopped || !strings.HasPrefix(d.last().Text, "slow text") ||
		len(d.last().Text) >= 400 {
		t.Fatalf("after stop: status %s stop %s last %+v", d.TurnStatus, d.TurnStop, d.last())
	}
	say(t, admin, id, "Answer briefly.")
	if d = waitTurn(t, admin, id, 30*time.Second); d.last().Text != "Next answer." {
		t.Fatalf("the message after a stop: %+v", d.last())
	}
}

// TestAssistantRetry checks that a failed turn retries from the saved messages without a
// second user message.
func TestAssistantRetry(t *testing.T) {
	_, admin, llm := aiServer(t, nil)
	setProvider(t, admin, llm, false)
	llm.Push(llmserver.Reply{Status: 500, Text: "down"}, llmserver.Reply{Status: 500, Text: "down"}, llmserver.Reply{Status: 500, Text: "down"})
	id := newConv(t, admin)
	if r := admin.raw(t, http.MethodPost, "/api/v1/ai/conversations/"+id+"/retry", nil, nil); r.Status != http.StatusConflict ||
		errCode(r.Body) != "turn_not_failed" {
		t.Fatalf("retry of an idle conversation: %d %s", r.Status, r.Body)
	}
	say(t, admin, id, "Hello?")
	d := waitTurn(t, admin, id, time.Minute)
	if d.TurnStatus != "failed" || !strings.Contains(d.TurnError, "status 500") {
		t.Fatalf("turn after provider errors: %s %q", d.TurnStatus, d.TurnError)
	}
	llm.Push(llmserver.Reply{Text: "Hello, I am back."})
	admin.do(t, http.MethodPost, "/api/v1/ai/conversations/"+id+"/retry", nil, http.StatusAccepted, nil)
	d = waitTurn(t, admin, id, 30*time.Second)
	users := 0
	for _, m := range d.Messages {
		if m.Role == "user" {
			users++
		}
	}
	if d.TurnStatus != "idle" || d.last().Text != "Hello, I am back." || users != 1 {
		t.Fatalf("after retry: status %s, last %+v, %d user messages", d.TurnStatus, d.last(), users)
	}
}

// TestAssistantAcceptAllAndWebAccess checks that accept-all runs a mutating call with an
// audit event, that a fetch_url call in the turn brings the confirm back, and that
// fetch_url exists only with web access and refuses internal addresses.
func TestAssistantAcceptAllAndWebAccess(t *testing.T) {
	_, admin, llm := aiServer(t, nil)
	setProvider(t, admin, llm, false)
	saveFiles(t, admin, "auto", map[string]string{"job.flow.yaml": "id: job\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"})
	trigger := llmserver.ToolCall{ID: "t1", Name: "trigger_execution", Input: `{"namespace":"auto","flow_id":"job"}`}
	executions := func() int {
		var l struct {
			Items []any `json:"items"`
		}
		admin.do(t, http.MethodGet, "/api/v1/executions?flow=auto/job", nil, http.StatusOK, &l)
		return len(l.Items)
	}

	id := newConv(t, admin)
	var conv convDetail
	admin.do(t, http.MethodPatch, "/api/v1/ai/conversations/"+id, map[string]bool{"auto_accept": true}, http.StatusOK, &conv)
	if !conv.AutoAccept {
		t.Fatal("PATCH auto_accept=true did not turn on accept-all")
	}
	llm.Push(llmserver.Reply{ToolCalls: []llmserver.ToolCall{trigger}}, llmserver.Reply{Text: "The job started."})
	say(t, admin, id, "Run the job.")
	d := waitTurn(t, admin, id, 30*time.Second)
	if d.TurnStop != "end_turn" || executions() != 1 || len(d.Actions) != 1 || d.Actions[0].Status != "auto_confirmed" {
		t.Fatalf("accept-all: stop %s, %d executions, actions %+v", d.TurnStop, executions(), d.Actions)
	}
	var audit struct {
		Items []struct {
			TargetID string `json:"target_id"`
		} `json:"items"`
	}
	admin.do(t, http.MethodGet, "/api/v1/audit?action=ai.action.auto_confirmed", nil, http.StatusOK, &audit)
	if len(audit.Items) != 1 || audit.Items[0].TargetID != "trigger_execution" {
		t.Fatalf("audit: %+v", audit.Items)
	}
	if strings.Contains(string(llm.Requests()[0].Body), `"name":"fetch_url"`) {
		t.Fatal("the model got fetch_url without web access")
	}

	admin.do(t, http.MethodPut, "/api/v1/ai/provider", map[string]any{"type": "anthropic", "base_url": llm.URL, "model": "claude-test",
		"api_key_secret_key": "LLM_KEY", "web_access": true}, http.StatusOK, nil)
	llm.Reset()
	llm.Push(llmserver.Reply{ToolCalls: []llmserver.ToolCall{
		{ID: "f1", Name: "fetch_url", Input: `{"url":"http://127.0.0.1:1/"}`},
		{ID: "f2", Name: "fetch_url", Input: `{"url":"http://169.254.169.254/latest/meta-data/"}`},
		{ID: "t2", Name: "trigger_execution", Input: `{"namespace":"auto","flow_id":"job"}`},
	}})
	say(t, admin, id, "Read the page, then run the job.")
	d = waitTurn(t, admin, id, 30*time.Second)
	if !strings.Contains(string(llm.Requests()[0].Body), `"name":"fetch_url"`) {
		t.Fatal("the model did not get fetch_url with web access")
	}
	var results []convBlock
	for _, m := range d.Messages {
		for _, b := range m.Content {
			if b.Type == "tool_result" && b.ToolName == "fetch_url" {
				results = append(results, b)
			}
		}
	}
	if len(results) != 2 || !results[0].IsError || !strings.Contains(results[0].Text, "not public") ||
		!results[1].IsError || !strings.Contains(results[1].Text, "not public") {
		t.Fatalf("fetch_url results: %+v", results)
	}
	if d.TurnStop != "pending_action" || executions() != 1 {
		t.Fatalf("after a fetch, trigger_execution ran without a confirm: stop %s, %d executions", d.TurnStop, executions())
	}
	b, _ := json.Marshal(d.Actions)
	if !strings.Contains(string(b), `"status":"pending"`) {
		t.Fatalf("actions %s", b)
	}
}
