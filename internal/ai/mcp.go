package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// MCPPath is the MCP endpoint (REQ-AI-004).
const MCPPath = "/mcp"

// MCPCardPath is the MCP server card, the discovery document of SEP-2127.
const MCPCardPath = "/.well-known/mcp.json"

// mcpProtocolVersions are the protocol versions of the MCP SDK in go.mod, newest first.
var mcpProtocolVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const mcpInstructions = "Sluice orchestrates flows of tasks. Read tools list namespaces, flows, files and executions. " +
	"To write a flow, read get_flow_schema, then check the file with validate_flow before apply_change. " +
	"To triage a failure, read get_execution, get_insight and get_logs with failed_only. " +
	"rerun_execution and restart_execution reuse the files of the old execution: use them for a cause outside the files. " +
	"After a change to a flow or a script, start a new execution with trigger_execution. " +
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
			httpx.WriteError(w, r, httpx.Errorf(http.StatusUnauthorized, "unauthorized", "use a bearer API token"))
			return
		}
		h.ServeHTTP(w, r)
	})
}

// mcpCardHandler serves the server card. It is public: it names the endpoint and the bearer
// header, and holds no data. The endpoint URL uses SLUICE_PUBLIC_URL, or the request host.
func mcpCardHandler(s *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := ""
		if s != nil {
			base = strings.TrimRight(s.PublicURL, "/")
		}
		if base == "" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			base = scheme + "://" + r.Host
		}
		version := "dev"
		if s != nil && s.Version != "" {
			version = s.Version
		}
		card := map[string]any{
			"$schema":     "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json",
			"name":        "io.github.alternayte/sluice",
			"title":       "Sluice",
			"version":     version,
			"description": "Read, run and triage Sluice flows and executions. Tools run with the role of the API token.",
			"websiteUrl":  "https://sluice-docs.pages.dev",
			"repository":  map[string]string{"url": "https://github.com/alternayte/sluice", "source": "github"},
			"remotes": []map[string]any{{
				"type":                      "streamable-http",
				"url":                       base + MCPPath,
				"supportedProtocolVersions": mcpProtocolVersions,
				"headers": []map[string]any{{
					"name":        "Authorization",
					"description": "Bearer <API token>. Create a token on Settings, API tokens.",
					"isRequired":  true,
					"isSecret":    true,
				}},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(card)
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
