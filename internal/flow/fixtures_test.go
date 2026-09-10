package flow

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

const fixtureRoot = "../../tests/fixtures/flows"

// loadTree reads all files under dir with paths relative to dir.
func loadTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func fixtureDirs(t *testing.T, kind string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(fixtureRoot, kind))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// TestSCN_FLOW_001_ValidFixtures parses every valid fixture to its expected model.
// Set SLUICE_UPDATE_GOLDEN=1 to rewrite expected.json after a reviewed model change.
func TestSCN_FLOW_001_ValidFixtures(t *testing.T) {
	for _, name := range fixtureDirs(t, "valid") {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(fixtureRoot, "valid", name)
			res := ValidateNamespace(loadTree(t, filepath.Join(dir, "ns")))
			if len(res.NamespaceIssues) > 0 {
				t.Fatalf("namespace.yaml issues: %v", res.NamespaceIssues)
			}
			model := map[string]any{}
			for _, pf := range res.Flows {
				if !pf.Valid() {
					t.Fatalf("%s is invalid: %v", pf.Path, pf.Issues)
				}
				model[pf.Path] = pf.Flow
			}
			if res.Namespace != nil {
				model["namespace.yaml"] = res.Namespace
			}
			got, err := json.MarshalIndent(model, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			golden := filepath.Join(dir, "expected.json")
			if os.Getenv("SLUICE_UPDATE_GOLDEN") != "" {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("model differs from %s:\n%s", golden, got)
			}
		})
	}
}

type invalidExpect struct {
	File       string `json:"file"`
	Code       string `json:"code"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Structural bool   `json:"structural"`
}

func readExpect(t *testing.T, dir string) invalidExpect {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var e invalidExpect
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// TestSCN_FLOW_002_InvalidFixtures returns the expected code, path and line for every invalid fixture.
func TestSCN_FLOW_002_InvalidFixtures(t *testing.T) {
	required := []string{"cycle", "unknown_dependency", "duplicate_task_id", "duplicate_flow_id", "bad_cron", "unknown_timezone",
		"missing_file", "secret_in_args", "output_non_dependency", "executor_on_http", "bad_input_default"}
	have := map[string]bool{}
	for _, name := range fixtureDirs(t, "invalid") {
		have[name] = true
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(fixtureRoot, "invalid", name)
			exp := readExpect(t, dir)
			res := ValidateNamespace(loadTree(t, filepath.Join(dir, "ns")))
			var found []Issue
			for _, pf := range res.Flows {
				if pf.Path != exp.File {
					continue
				}
				found = pf.Issues
				if pf.Valid() {
					t.Fatalf("%s is valid", pf.Path)
				}
			}
			for _, is := range found {
				if is.Code == exp.Code && is.Path == exp.Path && is.Line == exp.Line {
					if is.Column == 0 || is.Message == "" {
						t.Fatalf("issue without column or message: %+v", is)
					}
					return
				}
			}
			t.Fatalf("want %s at %s line %d, got %v", exp.Code, exp.Path, exp.Line, found)
		})
	}
	for _, r := range required {
		if !have[r] {
			t.Errorf("missing required invalid fixture %s", r)
		}
	}
}

// TestSCN_FLOW_007_SchemaGeneratedAndApplied checks that the generated schema equals the
// committed file, accepts all valid fixtures and rejects the structural invalid fixtures.
func TestSCN_FLOW_007_SchemaGeneratedAndApplied(t *testing.T) {
	gen, err := FlowSchema()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../../schemas/flow.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gen, committed) {
		t.Fatal("schemas/flow.schema.json differs from the generated schema: run `just gen`")
	}
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(committed))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource("flow.json", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("flow.json")
	if err != nil {
		t.Fatal(err)
	}
	toValue := func(src []byte) any {
		var n yaml.Node
		if err := yaml.Unmarshal(src, &n); err != nil {
			t.Fatal(err)
		}
		v, err := toGeneric(&n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	valid := 0
	for _, name := range fixtureDirs(t, "valid") {
		for p, src := range loadTree(t, filepath.Join(fixtureRoot, "valid", name, "ns")) {
			if !IsFlowFile(p) {
				continue
			}
			if err := sch.Validate(toValue(src)); err != nil {
				t.Errorf("valid fixture %s/%s rejected: %v", name, p, err)
			}
			valid++
		}
	}
	rejected := 0
	for _, name := range fixtureDirs(t, "invalid") {
		dir := filepath.Join(fixtureRoot, "invalid", name)
		exp := readExpect(t, dir)
		if !exp.Structural {
			continue
		}
		src := loadTree(t, filepath.Join(dir, "ns"))[exp.File]
		if err := sch.Validate(toValue(src)); err == nil {
			t.Errorf("structural invalid fixture %s accepted", name)
		}
		rejected++
	}
	if valid < 3 || rejected < 4 {
		t.Fatalf("too few fixtures: %d valid, %d structural invalid", valid, rejected)
	}
}

// TestSCN_DOC_001_FlowDocGenerated checks docs/reference/flow.md and that every
// schema property has a description.
func TestSCN_DOC_001_FlowDocGenerated(t *testing.T) {
	want, err := ReferenceDoc()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../docs/reference/flow.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatal("docs/reference/flow.md is out of date: run `just gen`")
	}
	for _, s := range []string{"`depends_on`", "`catch_up`", "`inject_runner`", "NamespaceFile"} {
		if !strings.Contains(want, s) {
			t.Errorf("flow.md misses %s", s)
		}
	}
}
