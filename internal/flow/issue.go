package flow

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Issue codes of structural and semantic validation (REQ-FLOW-002, REQ-FLOW-003).
const (
	CodeYAMLSyntax          = "yaml_syntax"
	CodeUnknownField        = "unknown_field"
	CodeMissingField        = "missing_field"
	CodeInvalidType         = "invalid_type"
	CodeInvalidValue        = "invalid_value"
	CodeInvalidFormat       = "invalid_format"
	CodeOutOfRange          = "out_of_range"
	CodeDuplicateTaskID     = "duplicate_task_id"
	CodeDuplicateFlowID     = "duplicate_flow_id"
	CodeDuplicateTriggerID  = "duplicate_trigger_id"
	CodeDuplicateInputID    = "duplicate_input_id"
	CodeUnknownDependency   = "unknown_dependency"
	CodeDependencyCycle     = "dependency_cycle"
	CodeFileNotFound        = "file_not_found"
	CodeInvalidCron         = "invalid_cron"
	CodeUnknownTimezone     = "unknown_timezone"
	CodeInvalidDuration     = "invalid_duration"
	CodeInvalidTemplate     = "invalid_template"
	CodeTemplateNotAllowed  = "template_not_allowed"
	CodeSecretNotAllowed    = "secret_not_allowed"
	CodeUnknownInput        = "unknown_input"
	CodeUnknownTask         = "unknown_task"
	CodeOutputNotDependency = "output_reference_not_dependency"
	CodeExecutorNotAllowed  = "executor_not_allowed"
	CodeFieldNotAllowed     = "field_not_allowed"
	CodeImageRequired       = "image_required"
	CodeInvalidReference    = "invalid_reference"
	CodeInvalidInputDefault = "invalid_input_default"
	CodeInvalidPath         = "invalid_path"
	CodeUnknownRuntime      = "unknown_runtime"
)

// Issue is one validation error with its YAML path and position.
type Issue struct {
	Code    string `json:"code" jsonschema:"required" jsonschema_description:"Error code."`
	Path    string `json:"path" jsonschema:"required" jsonschema_description:"YAML path, for example tasks[1].depends_on[0]. Empty for the document root."`
	Line    int    `json:"line" jsonschema:"required" jsonschema_description:"1-based line. 0 when unknown."`
	Column  int    `json:"column" jsonschema:"required" jsonschema_description:"1-based column. 0 when unknown."`
	Message string `json:"message" jsonschema:"required" jsonschema_description:"Human-readable message."`
}

func (i Issue) String() string {
	return fmt.Sprintf("%d:%d %s %s: %s", i.Line, i.Column, i.Path, i.Code, i.Message)
}

// SortIssues orders issues by line, column and path.
func SortIssues(is []Issue) {
	sort.SliceStable(is, func(a, b int) bool {
		if is[a].Line != is[b].Line {
			return is[a].Line < is[b].Line
		}
		if is[a].Column != is[b].Column {
			return is[a].Column < is[b].Column
		}
		return is[a].Path < is[b].Path
	})
}

// Pos is a YAML position.
type Pos struct{ Line, Column int }

// Positions maps YAML paths to positions. A path element is ".key" or "[i]".
type Positions map[string]Pos

// indexPositions walks a YAML document node and records each value position.
func indexPositions(doc *yaml.Node) Positions {
	p := Positions{}
	var walk func(n *yaml.Node, path string)
	walk = func(n *yaml.Node, path string) {
		if n == nil {
			return
		}
		if n.Kind == yaml.AliasNode && n.Alias != nil {
			n = n.Alias
		}
		p[path] = Pos{n.Line, n.Column}
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, path)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				child := JoinPath(path, k.Value)
				p[child+"#key"] = Pos{k.Line, k.Column}
				walk(v, child)
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, path+"["+strconv.Itoa(i)+"]")
			}
		}
	}
	walk(doc, "")
	return p
}

// JoinPath appends a mapping key to a path.
func JoinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// IndexPath appends a sequence index to a path.
func IndexPath(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }

// At returns the position of path, or of its nearest known parent.
func (p Positions) At(path string) Pos {
	for {
		if pos, ok := p[path]; ok {
			return pos
		}
		if path == "" {
			return Pos{}
		}
		if i := strings.LastIndexAny(path, ".["); i >= 0 {
			path = path[:i]
		} else {
			path = ""
		}
	}
}

// KeyAt returns the position of the key of path (for unknown fields).
func (p Positions) KeyAt(path string) Pos {
	if pos, ok := p[path+"#key"]; ok {
		return pos
	}
	return p.At(path)
}

// issue builds an issue at path.
func (p Positions) issue(code, path, format string, args ...any) Issue {
	pos := p.At(path)
	return Issue{Code: code, Path: path, Line: pos.Line, Column: pos.Column, Message: fmt.Sprintf(format, args...)}
}

// pointerToPath converts JSON pointer tokens into a YAML path.
func pointerToPath(tokens []string) string {
	path := ""
	for _, t := range tokens {
		if n, err := strconv.Atoi(t); err == nil {
			path += "[" + strconv.Itoa(n) + "]"
			continue
		}
		path = JoinPath(path, t)
	}
	return path
}
