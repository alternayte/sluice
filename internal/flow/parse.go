package flow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

var (
	schemaOnce   sync.Once
	flowSchema   *jsonschema.Schema
	nsSchema     *jsonschema.Schema
	schemaErr    error
	printer      = message.NewPrinter(language.English)
	yamlLineRe   = regexp.MustCompile(`line (\d+)`)
	namespaceURL = "https://sluice.dev/schemas/namespace.json"
)

func compiledSchemas() (*jsonschema.Schema, *jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		for url, gen := range map[string]func() ([]byte, error){SchemaID: FlowSchema, namespaceURL: NamespaceSchema} {
			b, err := gen()
			if err != nil {
				schemaErr = err
				return
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
			if err != nil {
				schemaErr = err
				return
			}
			if err := c.AddResource(url, doc); err != nil {
				schemaErr = err
				return
			}
		}
		if flowSchema, schemaErr = c.Compile(SchemaID); schemaErr != nil {
			return
		}
		nsSchema, schemaErr = c.Compile(namespaceURL)
	})
	return flowSchema, nsSchema, schemaErr
}

// ParseFlow parses and structurally validates one flow file (REQ-FLOW-002).
func ParseFlow(p string, src []byte) *ParsedFlow {
	pf := &ParsedFlow{Path: p, Source: string(src), SourceHash: SourceHash(src), Positions: Positions{}}
	fs, _, err := compiledSchemas()
	if err != nil {
		pf.Issues = []Issue{{Code: CodeInvalidValue, Message: "flow schema: " + err.Error()}}
		return pf
	}
	root, pos, issues := parseYAML(src)
	pf.Positions = pos
	if len(issues) > 0 {
		pf.Issues = issues
		return pf
	}
	pf.Issues = validateStructure(fs, root, pos)
	var f Flow
	if err := root.Decode(&f); err != nil {
		if len(pf.Issues) == 0 {
			pf.Issues = []Issue{decodeIssue(err, pos)}
		}
		return pf
	}
	pf.Flow = &f
	return pf
}

// ParseNamespaceFile parses and validates namespace.yaml (REQ-NS-006).
func ParseNamespaceFile(src []byte) (*NamespaceFile, []Issue) {
	_, ns, err := compiledSchemas()
	if err != nil {
		return nil, []Issue{{Code: CodeInvalidValue, Message: "namespace schema: " + err.Error()}}
	}
	if len(bytes.TrimSpace(src)) == 0 {
		return &NamespaceFile{}, nil
	}
	root, pos, issues := parseYAML(src)
	if len(issues) > 0 {
		return nil, issues
	}
	if issues := validateStructure(ns, root, pos); len(issues) > 0 {
		return nil, issues
	}
	var nf NamespaceFile
	if err := root.Decode(&nf); err != nil {
		return nil, []Issue{decodeIssue(err, pos)}
	}
	var out []Issue
	if nf.Defaults != nil {
		v := &validation{f: &Flow{}, pos: pos}
		if nf.Defaults.Executor != nil {
			v.executorFields("defaults.executor", nf.Defaults.Executor)
		}
		if nf.Defaults.Timeout != "" {
			if d, err := time.ParseDuration(string(nf.Defaults.Timeout)); err != nil || d <= 0 {
				v.add(CodeInvalidDuration, "defaults.timeout", "invalid duration %q", nf.Defaults.Timeout)
			}
		}
		for k, val := range nf.Defaults.Env {
			if _, err := ParseTemplate(val); err != nil {
				v.add(CodeInvalidTemplate, "defaults.env."+k, "%v", err)
			}
		}
		out = v.issues
	}
	if len(out) > 0 {
		return nil, out
	}
	return &nf, nil
}

func parseYAML(src []byte) (*yaml.Node, Positions, []Issue) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		line := 0
		if m := yamlLineRe.FindStringSubmatch(err.Error()); m != nil {
			line, _ = strconv.Atoi(m[1])
		}
		return nil, Positions{}, []Issue{{Code: CodeYAMLSyntax, Line: line, Message: strings.TrimPrefix(err.Error(), "yaml: ")}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, Positions{}, []Issue{{Code: CodeMissingField, Line: 1, Column: 1, Message: "the file is empty"}}
	}
	pos := indexPositions(&doc)
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, pos, []Issue{{Code: CodeInvalidType, Line: root.Line, Column: root.Column, Message: "the document must be a mapping"}}
	}
	return root, pos, nil
}

func decodeIssue(err error, pos Positions) Issue {
	var te *yaml.TypeError
	line := 0
	if errors.As(err, &te) && len(te.Errors) > 0 {
		if m := yamlLineRe.FindStringSubmatch(te.Errors[0]); m != nil {
			line, _ = strconv.Atoi(m[1])
		}
	}
	return Issue{Code: CodeInvalidType, Line: line, Message: err.Error()}
}

// toGeneric converts a YAML node into JSON-compatible values for schema validation.
func toGeneric(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return toGeneric(n.Content[0])
	case yaml.AliasNode:
		return toGeneric(n.Alias)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("line %d: mapping keys must be scalars", k.Line)
			}
			v, err := toGeneric(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := toGeneric(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, err
		}
		switch x := v.(type) {
		case int:
			return json.Number(strconv.Itoa(x)), nil
		case int64:
			return json.Number(strconv.FormatInt(x, 10)), nil
		case uint64:
			return json.Number(strconv.FormatUint(x, 10)), nil
		case float64:
			return json.Number(strconv.FormatFloat(x, 'g', -1, 64)), nil
		case time.Time:
			return n.Value, nil
		}
		return v, nil
	}
	return nil, fmt.Errorf("unsupported YAML node")
}

func validateStructure(s *jsonschema.Schema, root *yaml.Node, pos Positions) []Issue {
	generic, err := toGeneric(root)
	if err != nil {
		return []Issue{{Code: CodeInvalidType, Line: root.Line, Column: root.Column, Message: err.Error()}}
	}
	err = s.Validate(generic)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []Issue{{Code: CodeInvalidValue, Message: err.Error()}}
	}
	var out []Issue
	seen := map[string]bool{}
	add := func(is Issue) {
		k := is.Code + "|" + is.Path
		if !seen[k] {
			seen[k] = true
			out = append(out, is)
		}
	}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		loc := pointerToPath(e.InstanceLocation)
		switch k := e.ErrorKind.(type) {
		case *kind.Required:
			for _, m := range k.Missing {
				p := pos.At(loc)
				add(Issue{Code: CodeMissingField, Path: JoinPath(loc, m), Line: p.Line, Column: p.Column, Message: fmt.Sprintf("missing required field %s", m)})
			}
			return
		case *kind.AdditionalProperties:
			for _, name := range k.Properties {
				fp := JoinPath(loc, name)
				p := pos.KeyAt(fp)
				add(Issue{Code: CodeUnknownField, Path: fp, Line: p.Line, Column: p.Column, Message: fmt.Sprintf("unknown field %s", name)})
			}
			return
		}
		p := pos.At(loc)
		add(Issue{Code: codeForKeyword(e.ErrorKind.KeywordPath()), Path: loc, Line: p.Line, Column: p.Column, Message: e.ErrorKind.LocalizedString(printer)})
	}
	walk(ve)
	SortIssues(out)
	return out
}

func codeForKeyword(kw []string) string {
	if len(kw) == 0 {
		return CodeInvalidValue
	}
	switch kw[len(kw)-1] {
	case "type":
		return CodeInvalidType
	case "enum", "const":
		return CodeInvalidValue
	case "pattern", "format":
		return CodeInvalidFormat
	case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "minItems", "maxItems",
		"minLength", "maxLength", "minProperties", "maxProperties", "multipleOf":
		return CodeOutOfRange
	}
	return CodeInvalidValue
}
