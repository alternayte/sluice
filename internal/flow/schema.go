package flow

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/invopop/jsonschema"
)

// SchemaID is the $id of the flow schema.
const SchemaID = "https://sluice.dev/schemas/flow.json"

// ValidateResultSchemaID is the $id of the `sluice validate --json` result schema.
const ValidateResultSchemaID = "https://sluice.dev/schemas/validate-result.json"

func reflector() *jsonschema.Reflector {
	return &jsonschema.Reflector{RequiredFromJSONSchemaTags: true, AllowAdditionalProperties: false, DoNotReference: false}
}

// FlowSchema returns the JSON Schema of flow files, generated from the Go types (REQ-FLOW-002).
func FlowSchema() ([]byte, error) {
	s := reflector().Reflect(&Flow{})
	s.ID = SchemaID
	s.Title = "Sluice flow"
	s.Description = "A flow file (*.flow.yaml) of a Sluice namespace."
	return marshalSchema(s)
}

// NamespaceSchema returns the JSON Schema of namespace.yaml.
func NamespaceSchema() ([]byte, error) {
	s := reflector().Reflect(&NamespaceFile{})
	s.ID = "https://sluice.dev/schemas/namespace.json"
	s.Title = "Sluice namespace.yaml"
	return marshalSchema(s)
}

// ValidateResultSchema returns the JSON Schema of `sluice validate --json` (REQ-FLOW-007).
func ValidateResultSchema() ([]byte, error) {
	s := reflector().Reflect(&ValidateResult{})
	s.ID = ValidateResultSchemaID
	s.Title = "Sluice validate result"
	return marshalSchema(s)
}

func marshalSchema(s *jsonschema.Schema) ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ReferenceDoc renders docs/reference/flow.md from the schema descriptions (REQ-DOC-002).
// It fails when a property has no description.
func ReferenceDoc() (string, error) {
	var b strings.Builder
	b.WriteString("# Flow reference\n\n")
	b.WriteString("Generated from `internal/flow/model.go` by `just gen`. Do not edit.\n\n")
	b.WriteString("A flow file matches `*.flow.yaml` or `*.flow.yml`. `namespace.yaml` at the namespace root sets defaults.\n")
	var missing []string
	seen := map[reflect.Type]bool{}
	var section func(title string, t reflect.Type)
	section = func(title string, t reflect.Type) {
		if seen[t] {
			return
		}
		seen[t] = true
		fmt.Fprintf(&b, "\n## %s\n\n| Field | Type | Description |\n|---|---|---|\n", title)
		var nested []reflect.Type
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			desc := f.Tag.Get("jsonschema_description")
			if desc == "" {
				missing = append(missing, t.Name()+"."+name)
			}
			req := ""
			if strings.Contains(f.Tag.Get("jsonschema"), "required") {
				req = " Required."
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s%s |\n", name, typeName(f.Type), desc, req)
			if nt := structOf(f.Type); nt != nil {
				nested = append(nested, nt)
			}
		}
		for _, nt := range nested {
			section(nt.Name(), nt)
		}
	}
	section("Flow", reflect.TypeOf(Flow{}))
	section("NamespaceFile (namespace.yaml)", reflect.TypeOf(NamespaceFile{}))
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("schema properties without description: %s", strings.Join(missing, ", "))
	}
	return b.String(), nil
}

func structOf(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		return t
	}
	return nil
}

func typeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Pointer:
		return typeName(t.Elem())
	case reflect.Slice:
		return "list of " + typeName(t.Elem())
	case reflect.Map:
		return "map of " + typeName(t.Elem())
	case reflect.Struct:
		return t.Name()
	case reflect.Interface:
		return "any"
	case reflect.Int, reflect.Int64:
		return "int"
	case reflect.Bool:
		return "boolean"
	}
	if t.Name() == "Duration" {
		return "duration"
	}
	return "string"
}
