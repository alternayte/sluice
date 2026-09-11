// Package llmserver is the scripted LLM substitute of SDD §10.4. One server speaks the
// Anthropic Messages API (POST /v1/messages) and the OpenAI Chat Completions API
// (POST /v1/chat/completions), streams scripted replies as server-sent events and
// records every request.
package llmserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// T is the part of testing.TB that Start uses.
type T interface {
	Helper()
	Cleanup(func())
}

// ToolCall is one scripted tool call. Input is a JSON object.
type ToolCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// Reply is one scripted model answer. A Status other than 0 or 200 gives an error answer.
// JSON is the structured answer of a request with a JSON schema.
type Reply struct {
	Status    int        `json:"status,omitempty"`
	Text      string     `json:"text,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	JSON      string     `json:"json,omitempty"`
}

// Request is one recorded request.
type Request struct {
	API    string          `json:"api"` // anthropic or openai
	Header http.Header     `json:"header"`
	Body   json.RawMessage `json:"body"`
}

// Server is a running scripted LLM server.
type Server struct {
	// URL is the Anthropic base URL. OpenAIURL is the OpenAI-compatible base URL.
	URL, OpenAIURL string

	mu        sync.Mutex
	script    []Reply
	requests  []Request
	responder func(api string, body []byte) Reply
	srv       *httptest.Server
}

// Start starts a server that stops at the end of the test.
func Start(t T) *Server {
	t.Helper()
	s := New()
	t.Cleanup(s.Close)
	return s
}

// New starts a server. Close stops it.
func New() *Server {
	s := &Server{}
	s.srv = httptest.NewServer(s.Handler())
	s.URL = s.srv.URL
	s.OpenAIURL = s.srv.URL + "/v1"
	return s
}

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// Push appends replies to the script.
func (s *Server) Push(r ...Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script = append(s.script, r...)
}

// SetResponder sets a function that answers requests when the script is empty.
func (s *Server) SetResponder(fn func(api string, body []byte) Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responder = fn
}

// Reset removes the script, the responder and the recorded requests.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script, s.requests, s.responder = nil, nil, nil
}

// Requests returns a copy of the recorded requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Handler serves both APIs.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, "anthropic") })
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, "openai") })
	return mux
}

func (s *Server) next(api string, body []byte) Reply {
	s.mu.Lock()
	s.requests = append(s.requests, Request{API: api, Body: json.RawMessage(body)})
	if len(s.script) > 0 {
		r := s.script[0]
		s.script = s.script[1:]
		s.mu.Unlock()
		return r
	}
	fn := s.responder
	s.mu.Unlock()
	if fn != nil {
		return fn(api, body)
	}
	return Reply{Status: http.StatusInternalServerError, Text: "no scripted reply"}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request, api string) {
	body, _ := io.ReadAll(r.Body)
	reply := s.next(api, body)
	s.mu.Lock()
	s.requests[len(s.requests)-1].Header = r.Header.Clone()
	s.mu.Unlock()
	if reply.Status != 0 && reply.Status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.Status)
		_, _ = fmt.Fprintf(w, `{"error":{"type":"scripted","message":%q}}`, reply.Text)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if api == "anthropic" {
		var req struct {
			ToolChoice struct {
				Name string `json:"name"`
			} `json:"tool_choice"`
		}
		_ = json.Unmarshal(body, &req)
		writeAnthropic(w, reply, req.ToolChoice.Name)
	} else {
		writeOpenAI(w, reply)
	}
}

// chunks splits s into parts of at most n bytes, so that clients see several deltas.
func chunks(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func event(w io.Writer, name string, v any) {
	b, _ := json.Marshal(v)
	if name != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", name)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeAnthropic(w io.Writer, r Reply, jsonTool string) {
	event(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_1", "role": "assistant", "content": []any{}}})
	i := 0
	if r.Text != "" {
		event(w, "content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "text", "text": ""}})
		for _, c := range chunks(r.Text, 8) {
			event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": c}})
		}
		event(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
		i++
	}
	calls := r.ToolCalls
	if r.JSON != "" {
		calls = append(calls, ToolCall{ID: "toolu_json", Name: jsonTool, Input: r.JSON})
	}
	for _, tc := range calls {
		event(w, "content_block_start", map[string]any{"type": "content_block_start", "index": i,
			"content_block": map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": map[string]any{}}})
		for _, c := range chunks(tc.Input, 16) {
			event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": c}})
		}
		event(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
		i++
	}
	stop := "end_turn"
	if len(calls) > 0 {
		stop = "tool_use"
	}
	event(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}})
	event(w, "message_stop", map[string]any{"type": "message_stop"})
}

func writeOpenAI(w io.Writer, r Reply) {
	chunk := func(delta map[string]any, finish any) {
		event(w, "", map[string]any{"id": "chatcmpl-1", "object": "chat.completion.chunk",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	}
	chunk(map[string]any{"role": "assistant"}, nil)
	text := r.Text
	if r.JSON != "" {
		text = r.JSON
	}
	for _, c := range chunks(text, 8) {
		chunk(map[string]any{"content": c}, nil)
	}
	for i, tc := range r.ToolCalls {
		parts := chunks(tc.Input, 16)
		if len(parts) == 0 {
			parts = []string{"{}"}
		}
		for j, p := range parts {
			call := map[string]any{"index": i, "function": map[string]any{"arguments": p}}
			if j == 0 {
				call["id"], call["type"] = tc.ID, "function"
				call["function"].(map[string]any)["name"] = tc.Name
			}
			chunk(map[string]any{"tool_calls": []any{call}}, nil)
		}
	}
	finish := "stop"
	if len(r.ToolCalls) > 0 {
		finish = "tool_calls"
	}
	chunk(map[string]any{}, finish)
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// Contains reports whether any recorded request body contains s.
func (s *Server) Contains(sub string) bool {
	for _, r := range s.Requests() {
		if strings.Contains(string(r.Body), sub) {
			return true
		}
	}
	return false
}
