package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// anthropicModel speaks the Anthropic Messages API with streaming.
type anthropicModel struct {
	base, model, key string
	hc               *http.Client
}

func (m *anthropicModel) Name() string { return m.model }

type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicRequest struct {
	Model      string             `json:"model"`
	MaxTokens  int                `json:"max_tokens"`
	System     string             `json:"system,omitempty"`
	Messages   []anthropicMessage `json:"messages"`
	Tools      []anthropicTool    `json:"tools,omitempty"`
	ToolChoice map[string]string  `json:"tool_choice,omitempty"`
	Stream     bool               `json:"stream"`
}

func (m *anthropicModel) body(req Request) anthropicRequest {
	out := anthropicRequest{Model: m.model, MaxTokens: req.MaxTokens, System: req.System, Stream: true}
	if out.MaxTokens == 0 {
		out.MaxTokens = 4096
	}
	for _, msg := range req.Messages {
		am := anthropicMessage{Role: msg.Role}
		for _, b := range msg.Content {
			switch b.Type {
			case BlockText:
				am.Content = append(am.Content, anthropicBlock{Type: "text", Text: b.Text})
			case BlockToolUse:
				in := b.Input
				if len(in) == 0 {
					in = json.RawMessage(`{}`)
				}
				am.Content = append(am.Content, anthropicBlock{Type: "tool_use", ID: b.ToolUseID, Name: b.ToolName, Input: in})
			case BlockToolResult:
				am.Content = append(am.Content, anthropicBlock{Type: "tool_result", ToolUseID: b.ToolUseID, Content: b.Text, IsError: b.IsError})
			}
		}
		out.Messages = append(out.Messages, am)
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	// A forced tool call gives the structured answer.
	if len(req.JSONSchema) > 0 {
		out.Tools = append(out.Tools, anthropicTool{Name: req.JSONName, Description: "Return the answer.", InputSchema: req.JSONSchema})
		out.ToolChoice = map[string]string{"type": "tool", "name": req.JSONName}
	}
	return out
}

func (m *anthropicModel) Complete(ctx context.Context, req Request, onText func(string)) (Response, error) {
	resp, err := post(ctx, m.hc, m.base+"/v1/messages",
		map[string]string{"x-api-key": m.key, "anthropic-version": "2023-06-01"}, m.body(req))
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	type blockState struct {
		typ, id, name string
		text, input   strings.Builder
	}
	var blocks []*blockState
	var out Response
	err = readSSE(resp.Body, func(ev sseEvent) error {
		var d struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Text string `json:"text"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if ev.Data == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(ev.Data), &d); err != nil {
			return fmt.Errorf("decode anthropic event: %w", err)
		}
		switch d.Type {
		case "content_block_start":
			for len(blocks) <= d.Index {
				blocks = append(blocks, &blockState{})
			}
			b := blocks[d.Index]
			b.typ, b.id, b.name = d.ContentBlock.Type, d.ContentBlock.ID, d.ContentBlock.Name
			b.text.WriteString(d.ContentBlock.Text)
		case "content_block_delta":
			if d.Index >= len(blocks) {
				return fmt.Errorf("anthropic delta for unknown block %d", d.Index)
			}
			b := blocks[d.Index]
			switch d.Delta.Type {
			case "text_delta":
				b.text.WriteString(d.Delta.Text)
				if onText != nil && d.Delta.Text != "" {
					onText(d.Delta.Text)
				}
			case "input_json_delta":
				b.input.WriteString(d.Delta.PartialJSON)
			}
		case "message_delta":
			if d.Delta.StopReason != "" {
				out.StopReason = d.Delta.StopReason
			}
		case "message_stop":
			return errStreamEnd
		case "error":
			return &ProviderError{Status: http.StatusOK, Attempts: 1, Detail: d.Error.Type + ": " + d.Error.Message}
		}
		return nil
	})
	if err != nil && err != errStreamEnd {
		return Response{}, err
	}
	var text strings.Builder
	for _, b := range blocks {
		switch b.typ {
		case "text":
			text.WriteString(b.text.String())
		case "tool_use":
			in := json.RawMessage(b.input.String())
			if len(in) == 0 {
				in = json.RawMessage(`{}`)
			}
			if !json.Valid(in) {
				return Response{}, fmt.Errorf("the model sent invalid JSON for tool %s", b.name)
			}
			if len(req.JSONSchema) > 0 && b.name == req.JSONName {
				out.JSON = in
				continue
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.id, Name: b.name, Input: in})
		}
	}
	out.Text = text.String()
	return out, nil
}
