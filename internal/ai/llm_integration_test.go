//go:build integration

package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/testutil/llmserver"
)

func adapters(srv *llmserver.Server) map[string]Model {
	a, _ := NewModel(TypeAnthropic, srv.URL, "claude-test", "anthropic-key", nil)
	o, _ := NewModel(TypeOpenAICompatible, srv.OpenAIURL, "gpt-test", "openai-key", nil)
	return map[string]Model{TypeAnthropic: a, TypeOpenAICompatible: o}
}

func lastBody(t *testing.T, srv *llmserver.Server) map[string]any {
	t.Helper()
	reqs := srv.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request recorded")
	}
	var m map[string]any
	if err := json.Unmarshal(reqs[len(reqs)-1].Body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAdapters(t *testing.T) {
	old := retryBase
	retryBase = time.Millisecond
	t.Cleanup(func() { retryBase = old })
	ctx := context.Background()

	for _, typ := range []string{TypeAnthropic, TypeOpenAICompatible} {
		t.Run("SCN-AI-002 "+typ+" streams text", func(t *testing.T) {
			srv := llmserver.Start(t)
			m := adapters(srv)[typ]
			srv.Push(llmserver.Reply{Text: "Hello from the scripted model."})
			var deltas []string
			resp, err := m.Complete(ctx, Request{System: "Be brief.", Messages: []Message{{Role: "user", Content: []Block{{Type: BlockText, Text: "Hi"}}}}},
				func(s string) { deltas = append(deltas, s) })
			if err != nil {
				t.Fatal(err)
			}
			if resp.Text != "Hello from the scripted model." || strings.Join(deltas, "") != resp.Text || len(deltas) < 2 {
				t.Fatalf("text %q, deltas %q", resp.Text, deltas)
			}
			h := srv.Requests()[0].Header
			if typ == TypeAnthropic && (h.Get("x-api-key") != "anthropic-key" || h.Get("anthropic-version") == "") {
				t.Fatalf("anthropic headers %v", h)
			}
			if typ == TypeOpenAICompatible && h.Get("Authorization") != "Bearer openai-key" {
				t.Fatalf("openai headers %v", h)
			}
			if b := lastBody(t, srv); b["stream"] != true {
				t.Fatalf("request does not stream: %v", b)
			}
		})

		t.Run("SCN-AI-002 "+typ+" completes a tool call round trip", func(t *testing.T) {
			srv := llmserver.Start(t)
			m := adapters(srv)[typ]
			srv.Push(llmserver.Reply{Text: "I check it.", ToolCalls: []llmserver.ToolCall{{ID: "call_1", Name: "get_execution", Input: `{"execution_id":"e-1"}`}}},
				llmserver.Reply{Text: "The execution failed."})
			tools := []ToolDef{{Name: "get_execution", Description: "Read one execution.", InputSchema: json.RawMessage(`{"type":"object","properties":{"execution_id":{"type":"string"}}}`)}}
			msgs := []Message{{Role: "user", Content: []Block{{Type: BlockText, Text: "Why did e-1 fail?"}}}}
			first, err := m.Complete(ctx, Request{Messages: msgs, Tools: tools}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "get_execution" || string(first.ToolCalls[0].Input) != `{"execution_id":"e-1"}` {
				t.Fatalf("tool calls %+v", first.ToolCalls)
			}
			msgs = append(msgs,
				Message{Role: "assistant", Content: []Block{{Type: BlockText, Text: first.Text},
					{Type: BlockToolUse, ToolUseID: first.ToolCalls[0].ID, ToolName: "get_execution", Input: first.ToolCalls[0].Input}}},
				Message{Role: "user", Content: []Block{{Type: BlockToolResult, ToolUseID: first.ToolCalls[0].ID, Text: `{"state":"FAILED"}`}}})
			second, err := m.Complete(ctx, Request{Messages: msgs, Tools: tools}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if second.Text != "The execution failed." {
				t.Fatalf("second text %q", second.Text)
			}
			body := string(srv.Requests()[1].Body)
			for _, want := range []string{`call_1`, `\"state\":\"FAILED\"`, `get_execution`} {
				if !strings.Contains(body, want) {
					t.Fatalf("second request lacks %s: %s", want, body)
				}
			}
		})

		t.Run("SCN-AI-002 "+typ+" returns structured JSON output", func(t *testing.T) {
			srv := llmserver.Start(t)
			m := adapters(srv)[typ]
			srv.Push(llmserver.Reply{JSON: `{"summary":"Disk full","confidence":"high"}`})
			schema := json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"confidence":{"type":"string"}},"required":["summary","confidence"]}`)
			resp, err := m.Complete(ctx, Request{Messages: []Message{{Role: "user", Content: []Block{{Type: BlockText, Text: "Triage."}}}},
				JSONSchema: schema, JSONName: "triage"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var got struct{ Summary, Confidence string }
			if err := json.Unmarshal(resp.JSON, &got); err != nil || got.Summary != "Disk full" || got.Confidence != "high" {
				t.Fatalf("json %s: %v", resp.JSON, err)
			}
			b := lastBody(t, srv)
			if typ == TypeAnthropic && b["tool_choice"] == nil {
				t.Fatalf("anthropic request has no forced tool: %v", b)
			}
			if typ == TypeOpenAICompatible && b["response_format"] == nil {
				t.Fatalf("openai request has no response format: %v", b)
			}
		})

		t.Run("SCN-AI-002 "+typ+" retries 429 and then succeeds", func(t *testing.T) {
			srv := llmserver.Start(t)
			m := adapters(srv)[typ]
			srv.Push(llmserver.Reply{Status: 429, Text: "slow down"}, llmserver.Reply{Text: "ok"})
			resp, err := m.Complete(ctx, Request{Messages: []Message{{Role: "user", Content: []Block{{Type: BlockText, Text: "Hi"}}}}}, nil)
			if err != nil || resp.Text != "ok" {
				t.Fatalf("resp %+v, err %v", resp, err)
			}
			if n := len(srv.Requests()); n != 2 {
				t.Fatalf("%d requests, want 2", n)
			}
		})

		t.Run("SCN-AI-002 "+typ+" gives an error after a persistent 500", func(t *testing.T) {
			srv := llmserver.Start(t)
			m := adapters(srv)[typ]
			for range 3 {
				srv.Push(llmserver.Reply{Status: 500, Text: "broken"})
			}
			_, err := m.Complete(ctx, Request{Messages: []Message{{Role: "user", Content: []Block{{Type: BlockText, Text: "Hi"}}}}}, nil)
			var pe *ProviderError
			if !errors.As(err, &pe) || pe.Status != 500 || pe.Attempts != 3 || !strings.Contains(err.Error(), "status 500 after 3 attempts") {
				t.Fatalf("err %v", err)
			}
			if n := len(srv.Requests()); n != 3 {
				t.Fatalf("%d requests, want 3", n)
			}
		})
	}
}
