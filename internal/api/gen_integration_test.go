//go:build integration

package api_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSCN_API_001_RegenerateNoDiff regenerates the Go server and the TS schema from
// api/openapi.yaml into a temporary directory and compares them with the committed files.
func TestSCN_API_001_RegenerateNoDiff(t *testing.T) {
	root, _ := filepath.Abs("../..")
	tmp := t.TempDir()

	cfg := filepath.Join(tmp, "cfg.yaml")
	goOut := filepath.Join(tmp, "apigen.gen.go")
	raw, err := os.ReadFile(filepath.Join(root, "api/oapi-codegen.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfgText := string(raw)
	cfgText = replaceLine(cfgText, "output: ", "output: "+goOut)
	if err := os.WriteFile(cfg, []byte(cfgText), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "tool", "oapi-codegen", "-config", cfg, "api/openapi.yaml")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("oapi-codegen: %v\n%s", err, out)
	}
	same(t, goOut, filepath.Join(root, "internal/api/apigen/apigen.gen.go"))

	tsOut := filepath.Join(tmp, "schema.d.ts")
	cmd = exec.Command("bunx", "openapi-typescript", "../api/openapi.yaml", "-o", tsOut)
	cmd.Dir = filepath.Join(root, "ui")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("openapi-typescript: %v\n%s", err, out)
	}
	cmd = exec.Command("bunx", "prettier", "--write", tsOut)
	cmd.Dir = filepath.Join(root, "ui")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prettier: %v\n%s", err, out)
	}
	same(t, tsOut, filepath.Join(root, "ui/src/api/schema.d.ts"))
}

func replaceLine(src, prefix, repl string) string {
	lines := splitLines(src)
	for i, l := range lines {
		if len(l) >= len(prefix) && l[:len(prefix)] == prefix {
			lines[i] = repl
		}
	}
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func same(t *testing.T, gotPath, wantPath string) {
	t.Helper()
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s differs from regenerated output: run `just gen`", wantPath)
	}
}
