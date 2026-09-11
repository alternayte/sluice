package ai

import (
	"context"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// MCPPath is the MCP endpoint (REQ-AI-004).
const MCPPath = "/mcp"

const mcpInstructions = "Sluice orchestrates flows of tasks. Read tools list namespaces, flows, files and executions. " +
	"Mutating tools run with the role of the API token."

// mcpHandler serves MCP over streamable HTTP. Only bearer API tokens authenticate. Each
// request gets its own stateless server whose tools run with the principal of the request.
// MCP does not need an AI provider (REQ-AI-002).
func mcpHandler(s *Service) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s.mcpServer(r.Context())
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := kernel.FromContext(r.Context()); p == nil || p.Kind != "token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sluice"`)
			httpx.WriteError(w, r, httpx.Errorf(http.StatusUnauthorized, "unauthenticated", "use a bearer API token"))
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Service) mcpServer(reqCtx context.Context) *mcp.Server {
	p := kernel.FromContext(reqCtx)
	actor := audit.ActorFrom(reqCtx)
	srv := mcp.NewServer(&mcp.Implementation{Name: "sluice", Version: s.Version}, &mcp.ServerOptions{Instructions: mcpInstructions})
	for _, t := range Tools() {
		if t.AssistantOnly {
			continue
		}
		destructive := t.Mutating
		srv.AddTool(&mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.Schema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !t.Mutating, DestructiveHint: &destructive}},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				ctx = audit.WithActor(kernel.WithPrincipal(ctx, p), actor)
				out, err := s.CallTool(ctx, t, req.Params.Arguments)
				if err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: s.toolError(t, err)}}}, nil
				}
				if t.Mutating {
					if err := s.Audit.Record(ctx, nil, audit.Event{Action: "ai.tool.call", TargetType: "tool", TargetID: t.Name,
						Details: map[string]any{"via": "mcp"}}); err != nil {
						s.Log.Warn("audit mcp tool call", "tool", t.Name, "err", err)
					}
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil
			})
	}
	return srv
}

// toolError returns the text of a tool error for the model or client. API errors keep
// their code and message. Other errors are logged and hidden.
func (s *Service) toolError(t Tool, err error) string {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Code + ": " + he.Message
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled: " + err.Error()
	}
	s.Log.Warn("tool failed", "tool", t.Name, "err", err)
	return "internal: the tool failed"
}
