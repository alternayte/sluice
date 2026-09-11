//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearer adds an API token to each MCP request.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// mcpSession connects an MCP client with a bearer API token.
func mcpSession(t testing.TB, base, token string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := mcp.NewClient(&mcp.Implementation{Name: "sluice-e2e", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}},
		DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("connect MCP: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool calls an MCP tool and returns the text of the result and the error flag.
func callTool(t testing.TB, cs *mcp.ClientSession, name string, args any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestSCN_AI_001_Disabled(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	admin := adminClient(t, p)
	saveFiles(t, admin, "noai", map[string]string{
		"bad.flow.yaml": "id: bad\ntasks:\n  - {id: t, type: command, command: [\"false\"]}\n",
	})
	d := waitTerminal(t, admin, triggerFlow(t, admin, "noai", "bad", nil, nil).ID, time.Minute)
	if d.State != "FAILED" {
		t.Fatalf("state %s", d.State)
	}

	var st struct {
		Enabled bool `json:"enabled"`
	}
	admin.do(t, http.MethodGet, "/api/v1/ai/status", nil, http.StatusOK, &st)
	if st.Enabled {
		t.Fatal("AI status is enabled without a provider; the UI must hide AI actions")
	}
	for _, call := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/executions/" + d.ID + "/insights"},
		{http.MethodGet, "/api/v1/executions/" + d.ID + "/insights"},
		{http.MethodPost, "/api/v1/ai/provider/test"},
	} {
		r := admin.raw(t, call.method, call.path, nil, nil)
		if r.Status != http.StatusConflict || errCode(r.Body) != "ai_disabled" {
			t.Fatalf("%s %s: %d %s, want 409 ai_disabled", call.method, call.path, r.Status, r.Body)
		}
	}

	viewer, _ := createToken(t, login(t, p.URL, adminEmail, adminPassword), "viewer")
	text, isErr := callTool(t, mcpSession(t, p.URL, viewer), "list_flows", map[string]any{"namespace": "noai"})
	if isErr || !strings.Contains(text, `"flow_id":"bad"`) {
		t.Fatalf("MCP list_flows without a provider: error %v: %s", isErr, text)
	}
}

func TestSCN_AI_003_MCP(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_MASTER_KEYS": masterKey("k1", 1)})
	admin := adminClient(t, p)
	const canary = "canary-value-4711"
	admin.do(t, http.MethodPut, "/api/v1/secrets/MCP_CANARY", map[string]string{"value": canary}, http.StatusOK, nil)
	saveFiles(t, admin, "mcp", map[string]string{
		"hello.flow.yaml": "id: hello\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n",
		"leak.sh":         "echo \"leak:$S\"\n",
		"leak.flow.yaml":  "id: leak\nenv:\n  S: \"${{ secret('MCP_CANARY') }}\"\ntasks:\n  - {id: t, type: script, file: leak.sh}\n",
	})
	session := login(t, p.URL, adminEmail, adminPassword)
	viewerToken, _ := createToken(t, session, "viewer")
	editorToken, _ := createToken(t, session, "editor")
	viewer := mcpSession(t, p.URL, viewerToken)
	editor := mcpSession(t, p.URL, editorToken)

	t.Run("SCN-AI-003 /mcp without a bearer token answers 401 with the API code", func(t *testing.T) {
		ping := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}
		for name, c := range map[string]*client{"no token": tokenClient(p.URL, ""), "session cookie": session} {
			r := c.raw(t, http.MethodPost, "/mcp", ping, map[string]string{"Accept": "application/json, text/event-stream"})
			if r.Status != http.StatusUnauthorized || errCode(r.Body) != "unauthorized" {
				t.Fatalf("%s: status %d: %s", name, r.Status, r.Body)
			}
			if h := r.Header.Get("WWW-Authenticate"); !strings.HasPrefix(h, "Bearer") {
				t.Fatalf("%s: WWW-Authenticate %q", name, h)
			}
		}
	})

	t.Run("SCN-AI-003 an MCP client lists the tools", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := viewer.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, tool := range res.Tools {
			names[tool.Name] = true
		}
		for _, want := range []string{"list_namespaces", "list_flows", "get_execution", "get_logs", "trigger_execution", "cancel_execution", "apply_change"} {
			if !names[want] {
				t.Fatalf("tool %s is missing: %v", want, names)
			}
		}
		if names["propose_change"] {
			t.Fatal("propose_change is an assistant tool and must not be served over MCP")
		}
	})

	t.Run("SCN-AI-003 a viewer token gets a permission error for trigger_execution", func(t *testing.T) {
		text, isErr := callTool(t, viewer, "trigger_execution", map[string]any{"namespace": "mcp", "flow_id": "hello"})
		if !isErr || !strings.Contains(text, "forbidden") {
			t.Fatalf("error %v: %s", isErr, text)
		}
		var list struct {
			Items []json.RawMessage `json:"items"`
		}
		admin.do(t, http.MethodGet, "/api/v1/executions?flow=mcp/hello", nil, http.StatusOK, &list)
		if len(list.Items) != 0 {
			t.Fatalf("the refused call created %d executions", len(list.Items))
		}
	})

	t.Run("SCN-AI-003 an editor token apply_change on a managed namespace creates a version", func(t *testing.T) {
		text, isErr := callTool(t, editor, "apply_change", map[string]any{"namespace": "mcp", "message": "From MCP",
			"files": []map[string]any{{"path": "added.flow.yaml", "content": "id: added\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"}}})
		if isErr || !strings.Contains(text, `"version":2`) {
			t.Fatalf("error %v: %s", isErr, text)
		}
		admin.do(t, http.MethodGet, "/api/v1/flows/mcp/added", nil, http.StatusOK, nil)
		var audit struct {
			Items []struct {
				Action   string `json:"action"`
				TargetID string `json:"target_id"`
			} `json:"items"`
		}
		admin.do(t, http.MethodGet, "/api/v1/audit?action=ai.tool.call", nil, http.StatusOK, &audit)
		if len(audit.Items) == 0 || audit.Items[0].TargetID != "apply_change" {
			t.Fatalf("audit %+v", audit.Items)
		}
	})

	t.Run("SCN-AI-003 a log tool result is masked", func(t *testing.T) {
		d := waitTerminal(t, admin, triggerFlow(t, admin, "mcp", "leak", nil, nil).ID, time.Minute)
		if d.State != "SUCCESS" {
			t.Fatalf("state %s", d.State)
		}
		text, isErr := callTool(t, viewer, "get_logs", map[string]any{"execution_id": d.ID})
		if isErr || !strings.Contains(text, "leak:***") || strings.Contains(text, canary) {
			t.Fatalf("error %v: %s", isErr, text)
		}
	})
}
