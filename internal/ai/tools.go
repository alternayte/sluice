package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/pmezard/go-difflib/difflib"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/masking"
)

// Issue is one validation error of a proposed file.
type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

// NamespaceInfo is one namespace.
type NamespaceInfo struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
}

// FlowInfo is one flow.
type FlowInfo struct {
	Namespace   string `json:"namespace"`
	FlowID      string `json:"flow_id"`
	Path        string `json:"path"`
	Valid       bool   `json:"valid"`
	Disabled    bool   `json:"disabled"`
	Description string `json:"description,omitempty"`
	LastState   string `json:"last_state,omitempty"`
}

// FileInfo is one namespace file.
type FileInfo struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable,omitempty"`
}

// LogLine is one masked log line.
type LogLine struct {
	TaskKey string `json:"task"`
	Attempt int    `json:"attempt"`
	Line    int64  `json:"line"`
	Stream  string `json:"stream"`
	Text    string `json:"text"`
}

// FileChange is one file of a proposed change. Delete removes the file.
type FileChange struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Delete  bool   `json:"delete,omitempty"`
}

// ExecutionFilter filters list_executions.
type ExecutionFilter struct {
	Namespace string
	Flow      string
	State     string
	Limit     int
}

// Data reads and changes Sluice objects for the tools and triage. internal/app adapts the
// namespace, execution and git features. Every method uses the principal of ctx.
type Data interface {
	Namespaces(ctx context.Context) ([]NamespaceInfo, error)
	Flows(ctx context.Context, namespace string) ([]FlowInfo, error)
	Flow(ctx context.Context, namespace, flowID string) (FlowInfo, string, error)
	Files(ctx context.Context, namespace string) ([]FileInfo, error)
	// ReadFile returns the head content of a file, and false when the file does not exist.
	ReadFile(ctx context.Context, namespace, path string) (string, bool, error)
	Validate(ctx context.Context, namespace, path, content string) ([]Issue, error)
	Executions(ctx context.Context, f ExecutionFilter) (any, error)
	Execution(ctx context.Context, id uuid.UUID) (any, error)
	// ExecutionState returns the state of an execution, or a not found error.
	ExecutionState(ctx context.Context, id uuid.UUID) (string, error)
	Logs(ctx context.Context, id uuid.UUID, task string) ([]LogLine, error)
	Metrics(ctx context.Context, id uuid.UUID) (any, error)
	// Masker masks the secret values of all task runs of an execution (SI-01).
	Masker(ctx context.Context, id uuid.UUID) (*masking.Masker, error)
	Trigger(ctx context.Context, namespace, flowID string, inputs map[string]any, labels map[string]string) (any, error)
	Cancel(ctx context.Context, id uuid.UUID) error
	// SourceType returns managed or git.
	SourceType(ctx context.Context, namespace string) (string, error)
	// Save creates a managed version and returns its number.
	Save(ctx context.Context, namespace string, changes []FileChange, message string) (int, error)
	// Push pushes a git branch and returns the branch and the commit SHA.
	Push(ctx context.Context, namespace string, changes []FileChange, message string) (string, string, error)
	// Triage returns the context of a failure triage (REQ-AI-007).
	Triage(ctx context.Context, id uuid.UUID) (TriageData, error)
}

// Tool is one entry of the tool registry (Appendix D).
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Role        kernel.Role
	Mutating    bool
	// AssistantOnly tools are not served over MCP.
	AssistantOnly bool
	run           func(ctx context.Context, s *Service, in json.RawMessage) (any, error)
}

// maxToolResultChars limits one tool result in the model context.
const maxToolResultChars = 20000

// logTailDefault and logTailMax limit get_logs.
const (
	logTailDefault = 200
	logTailMax     = 1000
)

func decode[T any](in json.RawMessage) (T, error) {
	var v T
	if len(in) == 0 {
		in = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(in, &v); err != nil {
		return v, httpx.Errorf(http.StatusUnprocessableEntity, "validation_failed", "invalid tool arguments: %v", err)
	}
	return v, nil
}

func required(fields map[string]string) error {
	var fe []httpx.FieldError
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.TrimSpace(fields[k]) == "" {
			fe = append(fe, httpx.FieldError{Field: k, Message: "is required"})
		}
	}
	if len(fe) > 0 {
		return httpx.Validation(fe...)
	}
	return nil
}

func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, httpx.Validation(httpx.FieldError{Field: "execution_id", Message: "use an execution UUID"})
	}
	return id, nil
}

// masked marshals v and masks the secret values of the execution id.
func (s *Service) masked(ctx context.Context, id uuid.UUID, v any) (any, error) {
	m, err := s.Data.Masker(ctx, id)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(m.Bytes(b)), nil
}

const (
	schemaNone      = `{"type":"object","properties":{}}`
	schemaNamespace = `{"type":"object","properties":{"namespace":{"type":"string","description":"Namespace name."}},"required":["namespace"]}`
	schemaExecution = `{"type":"object","properties":{"execution_id":{"type":"string","description":"Execution UUID."}},"required":["execution_id"]}`
	schemaChange    = `{"type":"object","properties":{
"namespace":{"type":"string"},
"message":{"type":"string","description":"Version or commit message."},
"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"delete":{"type":"boolean"}},"required":["path"]}}},
"required":["namespace","message","files"]}`
)

type changeIn struct {
	Namespace string       `json:"namespace"`
	Message   string       `json:"message"`
	Files     []FileChange `json:"files"`
}

// FileProposal is the validation result and diff of one proposed file.
type FileProposal struct {
	Path   string  `json:"path"`
	Status string  `json:"status" enum:"added,modified,removed"`
	Diff   string  `json:"diff"`
	Issues []Issue `json:"issues"`
}

// Proposal is the result of propose_change and the first step of apply_change.
type Proposal struct {
	Namespace string         `json:"namespace"`
	Source    string         `json:"source" enum:"managed,git"`
	Message   string         `json:"message"`
	Valid     bool           `json:"valid"`
	Files     []FileProposal `json:"files"`
}

// propose validates proposed files with the flow validator and builds the diffs (REQ-AI-006).
func (s *Service) propose(ctx context.Context, in changeIn) (Proposal, error) {
	if err := required(map[string]string{"namespace": in.Namespace, "message": in.Message}); err != nil {
		return Proposal{}, err
	}
	if len(in.Files) == 0 || len(in.Files) > 50 {
		return Proposal{}, httpx.Validation(httpx.FieldError{Field: "files", Message: "use 1 to 50 files"})
	}
	src, err := s.Data.SourceType(ctx, in.Namespace)
	if err != nil {
		return Proposal{}, err
	}
	out := Proposal{Namespace: in.Namespace, Source: src, Message: in.Message, Valid: true, Files: []FileProposal{}}
	for _, f := range in.Files {
		old, exists, err := s.Data.ReadFile(ctx, in.Namespace, f.Path)
		if err != nil {
			return Proposal{}, err
		}
		fp := FileProposal{Path: f.Path, Status: "modified", Issues: []Issue{}}
		switch {
		case f.Delete && !exists:
			return Proposal{}, httpx.Validation(httpx.FieldError{Field: "files", Message: f.Path + " does not exist"})
		case f.Delete:
			fp.Status = "removed"
		case !exists:
			fp.Status = "added"
		}
		if !f.Delete {
			if fp.Issues, err = s.Data.Validate(ctx, in.Namespace, f.Path, f.Content); err != nil {
				return Proposal{}, err
			}
			if fp.Issues == nil {
				fp.Issues = []Issue{}
			}
			if len(fp.Issues) > 0 {
				out.Valid = false
			}
		}
		fp.Diff, _ = difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(old), B: difflib.SplitLines(f.Content),
			FromFile: "a/" + f.Path, ToFile: "b/" + f.Path, Context: 3})
		out.Files = append(out.Files, fp)
	}
	return out, nil
}

// Tools returns the tool registry (Appendix D, REQ-AI-003).
func Tools() []Tool {
	return []Tool{
		{Name: "list_namespaces", Description: "List all namespaces with their source type.", Schema: json.RawMessage(schemaNone), Role: kernel.Viewer,
			run: func(ctx context.Context, s *Service, _ json.RawMessage) (any, error) { return s.Data.Namespaces(ctx) }},
		{Name: "list_flows", Description: "List the flows of a namespace and its children. An empty namespace lists all flows.",
			Schema: json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"}}}`), Role: kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct{ Namespace string }](raw)
				if err != nil {
					return nil, err
				}
				return s.Data.Flows(ctx, in.Namespace)
			}},
		{Name: "get_flow", Description: "Read one flow with its YAML source.",
			Schema: json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"},"flow_id":{"type":"string"}},"required":["namespace","flow_id"]}`),
			Role:   kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct {
					Namespace string `json:"namespace"`
					FlowID    string `json:"flow_id"`
				}](raw)
				if err != nil {
					return nil, err
				}
				if err := required(map[string]string{"namespace": in.Namespace, "flow_id": in.FlowID}); err != nil {
					return nil, err
				}
				f, src, err := s.Data.Flow(ctx, in.Namespace, in.FlowID)
				if err != nil {
					return nil, err
				}
				return map[string]any{"flow": f, "source": src}, nil
			}},
		{Name: "validate_flow", Description: "Validate the content of a flow or namespace file against the head version of the namespace.",
			Schema: json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"},"path":{"type":"string"},"content":{"type":"string"}},"required":["namespace","path","content"]}`),
			Role:   kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct{ Namespace, Path, Content string }](raw)
				if err != nil {
					return nil, err
				}
				if err := required(map[string]string{"namespace": in.Namespace, "path": in.Path}); err != nil {
					return nil, err
				}
				issues, err := s.Data.Validate(ctx, in.Namespace, in.Path, in.Content)
				if err != nil {
					return nil, err
				}
				if issues == nil {
					issues = []Issue{}
				}
				return map[string]any{"valid": len(issues) == 0, "issues": issues}, nil
			}},
		{Name: "list_files", Description: "List the files of the head version of a namespace.", Schema: json.RawMessage(schemaNamespace), Role: kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct{ Namespace string }](raw)
				if err != nil {
					return nil, err
				}
				if err := required(map[string]string{"namespace": in.Namespace}); err != nil {
					return nil, err
				}
				return s.Data.Files(ctx, in.Namespace)
			}},
		{Name: "read_file", Description: "Read one file of the head version of a namespace.",
			Schema: json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"},"path":{"type":"string"}},"required":["namespace","path"]}`),
			Role:   kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct{ Namespace, Path string }](raw)
				if err != nil {
					return nil, err
				}
				if err := required(map[string]string{"namespace": in.Namespace, "path": in.Path}); err != nil {
					return nil, err
				}
				content, ok, err := s.Data.ReadFile(ctx, in.Namespace, in.Path)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, httpx.Errorf(http.StatusNotFound, "file_not_found", "the file %s does not exist", in.Path)
				}
				return map[string]any{"path": in.Path, "content": content}, nil
			}},
		{Name: "list_executions", Description: "List recent executions, newest first. Filters: namespace, flow as <namespace>/<flow_id>, comma-separated states.",
			Schema: json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"},"flow":{"type":"string"},"state":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":50}}}`),
			Role:   kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct {
					Namespace, Flow, State string
					Limit                  int
				}](raw)
				if err != nil {
					return nil, err
				}
				if in.Limit <= 0 || in.Limit > 50 {
					in.Limit = 20
				}
				return s.Data.Executions(ctx, ExecutionFilter{Namespace: in.Namespace, Flow: in.Flow, State: in.State, Limit: in.Limit})
			}},
		{Name: "get_execution", Description: "Read one execution with its task runs.", Schema: json.RawMessage(schemaExecution), Role: kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct {
					ExecutionID string `json:"execution_id"`
				}](raw)
				if err != nil {
					return nil, err
				}
				id, err := parseID(in.ExecutionID)
				if err != nil {
					return nil, err
				}
				d, err := s.Data.Execution(ctx, id)
				if err != nil {
					return nil, err
				}
				return s.masked(ctx, id, d)
			}},
		{Name: "get_logs", Description: "Read the last log lines of an execution, optionally of one task. tail is 1 to 1000, default 200.",
			Schema: json.RawMessage(`{"type":"object","properties":{"execution_id":{"type":"string"},"task":{"type":"string"},"tail":{"type":"integer","minimum":1,"maximum":1000}},"required":["execution_id"]}`),
			Role:   kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct {
					ExecutionID string `json:"execution_id"`
					Task        string `json:"task"`
					Tail        int    `json:"tail"`
				}](raw)
				if err != nil {
					return nil, err
				}
				id, err := parseID(in.ExecutionID)
				if err != nil {
					return nil, err
				}
				if in.Tail <= 0 || in.Tail > logTailMax {
					in.Tail = logTailDefault
				}
				lines, err := s.Data.Logs(ctx, id, in.Task)
				if err != nil {
					return nil, err
				}
				total := len(lines)
				if total > in.Tail {
					lines = lines[total-in.Tail:]
				}
				return s.masked(ctx, id, map[string]any{"total_lines": total, "lines": lines})
			}},
		{Name: "get_metrics", Description: "Read the metrics of an execution.", Schema: json.RawMessage(schemaExecution), Role: kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct {
					ExecutionID string `json:"execution_id"`
				}](raw)
				if err != nil {
					return nil, err
				}
				id, err := parseID(in.ExecutionID)
				if err != nil {
					return nil, err
				}
				m, err := s.Data.Metrics(ctx, id)
				if err != nil {
					return nil, err
				}
				return s.masked(ctx, id, m)
			}},
		{Name: "get_insight", Description: "Read the latest failure triage of an execution.", Schema: json.RawMessage(schemaExecution), Role: kernel.Viewer,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct {
					ExecutionID string `json:"execution_id"`
				}](raw)
				if err != nil {
					return nil, err
				}
				id, err := parseID(in.ExecutionID)
				if err != nil {
					return nil, err
				}
				list, err := s.Insights(ctx, id)
				if err != nil {
					return nil, err
				}
				if len(list) == 0 {
					return map[string]any{"insight": nil}, nil
				}
				return s.masked(ctx, id, map[string]any{"insight": list[0]})
			}},
		{Name: "trigger_execution", Description: "Start a flow with inputs and labels.", Role: kernel.Operator, Mutating: true,
			Schema: json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"},"flow_id":{"type":"string"},"inputs":{"type":"object"},"labels":{"type":"object","additionalProperties":{"type":"string"}}},"required":["namespace","flow_id"]}`),
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct {
					Namespace string            `json:"namespace"`
					FlowID    string            `json:"flow_id"`
					Inputs    map[string]any    `json:"inputs"`
					Labels    map[string]string `json:"labels"`
				}](raw)
				if err != nil {
					return nil, err
				}
				if err := required(map[string]string{"namespace": in.Namespace, "flow_id": in.FlowID}); err != nil {
					return nil, err
				}
				return s.Data.Trigger(ctx, in.Namespace, in.FlowID, in.Inputs, in.Labels)
			}},
		{Name: "cancel_execution", Description: "Cancel a running execution.", Schema: json.RawMessage(schemaExecution), Role: kernel.Operator, Mutating: true,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[struct {
					ExecutionID string `json:"execution_id"`
				}](raw)
				if err != nil {
					return nil, err
				}
				id, err := parseID(in.ExecutionID)
				if err != nil {
					return nil, err
				}
				if err := s.Data.Cancel(ctx, id); err != nil {
					return nil, err
				}
				return map[string]any{"execution_id": id, "cancel_requested": true}, nil
			}},
		{Name: "propose_change", Description: "Propose file changes of a namespace. The result has the validation issues and a diff. Nothing is written.",
			Schema: json.RawMessage(schemaChange), Role: kernel.Editor, AssistantOnly: true,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[changeIn](raw)
				if err != nil {
					return nil, err
				}
				return s.propose(ctx, in)
			}},
		{Name: "apply_change", Description: "Apply file changes: a new version of a managed namespace, or a new branch of a git namespace. Invalid flows are refused.",
			Schema: json.RawMessage(schemaChange), Role: kernel.Editor, Mutating: true,
			run: func(ctx context.Context, s *Service, raw json.RawMessage) (any, error) {
				in, err := decode[changeIn](raw)
				if err != nil {
					return nil, err
				}
				p, err := s.propose(ctx, in)
				if err != nil {
					return nil, err
				}
				if !p.Valid {
					return nil, httpx.Errorf(http.StatusUnprocessableEntity, "flow_invalid", "the change has validation issues; use propose_change first").WithDetails(p)
				}
				if p.Source == "git" {
					branch, sha, err := s.Data.Push(ctx, in.Namespace, in.Files, in.Message)
					if err != nil {
						return nil, err
					}
					return map[string]any{"namespace": in.Namespace, "branch": branch, "sha": sha}, nil
				}
				v, err := s.Data.Save(ctx, in.Namespace, in.Files, in.Message)
				if err != nil {
					return nil, err
				}
				return map[string]any{"namespace": in.Namespace, "version": v}, nil
			}},
	}
}

// ToolByName returns one tool of the registry.
func ToolByName(name string) (Tool, bool) {
	for _, t := range Tools() {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// ErrToolPermission is the answer to a tool call without the role of the tool.
func errToolPermission(t Tool) error {
	return httpx.Errorf(http.StatusForbidden, "forbidden", "the tool %s needs the %s role", t.Name, t.Role)
}

// CallTool checks the role of the principal of ctx and runs a tool. The result is JSON
// text of at most maxToolResultChars characters.
func (s *Service) CallTool(ctx context.Context, t Tool, input json.RawMessage) (string, error) {
	p := kernel.FromContext(ctx)
	if p == nil || !p.Can(t.Role) {
		return "", errToolPermission(t)
	}
	v, err := t.run(ctx, s, input)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode %s result: %w", t.Name, err)
	}
	if len(b) > maxToolResultChars {
		return string(b[:maxToolResultChars]) + " …(truncated)", nil
	}
	return string(b), nil
}
