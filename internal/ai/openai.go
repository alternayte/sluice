package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// openAIModel speaks the OpenAI Chat Completions API with streaming. It also serves
// compatible endpoints, so it sends no provider-specific fields.
type openAIModel struct {
	base, model, key string
	hc               *http.Client
}

func (m *openAIModel) Name() string { return m.model }

type openAIToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type openAIRequest struct {
	Model          string          `json:"model"`
	Messages       []openAIMessage `json:"messages"`
	Tools          []openAITool    `json:"tools,omitempty"`
	ResponseFormat map[string]any  `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
}

func strPtr(s string) *string { return &s }

func (m *openAIModel) body(req Request) openAIRequest {
	out := openAIRequest{Model: m.model, Stream: true}
	if req.System != "" {
		out.Messages = append(out.Messages, openAIMessage{Role: "system", Content: strPtr(req.System)})
	}
	for _, msg := range req.Messages {
		var text strings.Builder
		var calls []openAIToolCall
		for _, b := range msg.Content {
			switch b.Type {
			case BlockText:
				text.WriteString(b.Text)
			case BlockToolUse:
				c := openAIToolCall{ID: b.ToolUseID, Type: "function"}
				c.Function.Name = b.ToolName
				c.Function.Arguments = string(b.Input)
				if c.Function.Arguments == "" {
					c.Function.Arguments = "{}"
				}
				calls = append(calls, c)
			case BlockToolResult:
				// Each tool result is its own tool message.
				out.Messages = append(out.Messages, openAIMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: strPtr(b.Text)})
			}
		}
		if text.Len() == 0 && len(calls) == 0 {
			continue
		}
		om := openAIMessage{Role: msg.Role, ToolCalls: calls}
		if text.Len() > 0 || len(calls) == 0 {
			om.Content = strPtr(text.String())
		}
		out.Messages = append(out.Messages, om)
	}
	for _, t := range req.Tools {
		ot := openAITool{Type: "function"}
		ot.Function.Name, ot.Function.Description, ot.Function.Parameters = t.Name, t.Description, t.InputSchema
		out.Tools = append(out.Tools, ot)
	}
	if len(req.JSONSchema) > 0 {
		out.ResponseFormat = map[string]any{"type": "json_schema",
			"json_schema": map[string]any{"name": req.JSONName, "schema": req.JSONSchema}}
	}
	return out
}

func (m *openAIModel) Complete(ctx context.Context, req Request, onText func(string)) (Response, error) {
	resp, err := post(ctx, m.hc, m.base+"/chat/completions", map[string]string{"Authorization": "Bearer " + m.key}, m.body(req))
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	type callState struct {
		id, name string
		args     strings.Builder
	}
	calls := map[int]*callState{}
	var text strings.Builder
	var out Response
	err = readSSE(resp.Body, func(ev sseEvent) error {
		if ev.Data == "[DONE]" {
			return errStreamEnd
		}
		if ev.Data == "" {
			return nil
		}
		var d struct {
			Choices []struct {
				Delta struct {
					Content   string           `json:"content"`
					ToolCalls []openAIToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &d); err != nil {
			return fmt.Errorf("decode openai event: %w", err)
		}
		if d.Error != nil {
			return &ProviderError{Status: http.StatusOK, Attempts: 1, Detail: d.Error.Message}
		}
		for _, ch := range d.Choices {
			if ch.Delta.Content != "" {
				text.WriteString(ch.Delta.Content)
				if onText != nil && len(req.JSONSchema) == 0 {
					onText(ch.Delta.Content)
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				i := 0
				if tc.Index != nil {
					i = *tc.Index
				}
				c := calls[i]
				if c == nil {
					c = &callState{}
					calls[i] = c
				}
				if tc.ID != "" {
					c.id = tc.ID
				}
				if tc.Function.Name != "" {
					c.name = tc.Function.Name
				}
				c.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				out.StopReason = *ch.FinishReason
			}
		}
		return nil
	})
	if err != nil && err != errStreamEnd {
		return Response{}, err
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		c := calls[i]
		in := json.RawMessage(c.args.String())
		if len(in) == 0 {
			in = json.RawMessage(`{}`)
		}
		if !json.Valid(in) {
			return Response{}, fmt.Errorf("the model sent invalid JSON for tool %s", c.name)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: c.id, Name: c.name, Input: in})
	}
	if len(req.JSONSchema) > 0 {
		raw := strings.TrimSpace(text.String())
		if !json.Valid([]byte(raw)) {
			return Response{}, fmt.Errorf("the model did not return valid JSON")
		}
		out.JSON = json.RawMessage(raw)
		return out, nil
	}
	out.Text = text.String()
	return out, nil
}
