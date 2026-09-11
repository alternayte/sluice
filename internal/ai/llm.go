// Package ai holds the AI provider adapters, the tool registry, the MCP server, the
// assistant and failure triage (SDD §7.14, D-13).
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider types (REQ-AI-001).
const (
	TypeAnthropic        = "anthropic"
	TypeOpenAICompatible = "openai_compatible"
)

// Block types of a message.
const (
	BlockText       = "text"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"
)

// Block is one part of a message: text, a tool call of the model, or a tool result.
type Block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// Message is one turn of a conversation. Role is user or assistant. Tool results are
// blocks of a user message.
type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

// ToolDef describes a tool to the model.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Request is one model call.
type Request struct {
	System   string
	Messages []Message
	Tools    []ToolDef
	// JSONSchema asks for one JSON object that matches the schema. JSONName names it.
	JSONSchema json.RawMessage
	JSONName   string
	MaxTokens  int
}

// ToolCall is one tool call of the model.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Response is the result of one model call. JSON holds the structured answer when the
// request had a JSONSchema.
type Response struct {
	Text       string
	ToolCalls  []ToolCall
	JSON       json.RawMessage
	StopReason string
}

// Model is a provider adapter. Complete streams text deltas to onText, which can be nil.
type Model interface {
	Complete(ctx context.Context, req Request, onText func(string)) (Response, error)
	Name() string
}

// ProviderError is a provider failure after all attempts (REQ-AI-009).
type ProviderError struct {
	Status   int
	Attempts int
	Detail   string
}

func (e *ProviderError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("the AI provider did not answer after %d attempts: %s", e.Attempts, e.Detail)
	}
	return fmt.Sprintf("the AI provider returned status %d after %d attempts: %s", e.Status, e.Attempts, e.Detail)
}

// maxAttempts and retryBase control the retry of 429 and 5xx answers (REQ-AI-009).
// Tests set a small retryBase.
var (
	maxAttempts = 3
	retryBase   = 500 * time.Millisecond
)

// post sends a JSON body and retries 429, 5xx and transport errors with exponential backoff.
// The caller closes the body of the returned response.
func post(ctx context.Context, hc *http.Client, url string, headers map[string]string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var last *ProviderError
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryBase << (attempt - 2)):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = &ProviderError{Attempts: attempt, Detail: err.Error()}
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		_ = resp.Body.Close()
		last = &ProviderError{Status: resp.StatusCode, Attempts: attempt, Detail: strings.TrimSpace(string(detail))}
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return nil, last
		}
	}
	return nil, last
}

// sseEvent is one server-sent event.
type sseEvent struct {
	Event string
	Data  string
}

// readSSE calls fn for each event of r until r ends or fn returns an error.
func readSSE(r io.Reader, fn func(sseEvent) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var ev sseEvent
	var data []string
	flush := func() error {
		if len(data) == 0 && ev.Event == "" {
			return nil
		}
		ev.Data = strings.Join(data, "\n")
		err := fn(ev)
		ev, data = sseEvent{}, nil
		return err
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			ev.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return flush()
}

// errStreamEnd stops readSSE without an error.
var errStreamEnd = errors.New("stream end")

// NewModel returns the adapter of a provider type. A nil client uses a client without
// timeout, because responses stream.
func NewModel(typ, baseURL, model, apiKey string, hc *http.Client) (Model, error) {
	if hc == nil {
		hc = &http.Client{}
	}
	switch typ {
	case TypeAnthropic:
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		return &anthropicModel{base: strings.TrimRight(baseURL, "/"), model: model, key: apiKey, hc: hc}, nil
	case TypeOpenAICompatible:
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return &openAIModel{base: strings.TrimRight(baseURL, "/"), model: model, key: apiKey, hc: hc}, nil
	}
	return nil, fmt.Errorf("unknown AI provider type %q", typ)
}
