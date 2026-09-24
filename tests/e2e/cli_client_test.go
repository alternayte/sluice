//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIClient pins the contract of the client commands that CI jobs and agents depend on:
// the exit code of each end state, the API JSON on stdout, and a push that creates a version
// only when a file changed.
func TestCLIClient(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	admin := adminClient(t, p)
	saveFiles(t, admin, "cli", map[string]string{
		"ok.flow.yaml":   "id: ok\ntasks:\n  - {id: say, type: command, command: [\"echo\", \"hello cli\"]}\n",
		"fail.flow.yaml": "id: fail\ntasks:\n  - {id: boom, type: command, command: [\"sh\", \"-c\", \"exit 3\"]}\n",
	})
	session := login(t, p.URL, adminEmail, adminPassword)
	editorToken, _ := createToken(t, session, "editor")
	env := map[string]string{"SLUICE_URL": p.URL, "SLUICE_TOKEN": editorToken}

	t.Run("a client command without SLUICE_URL or SLUICE_TOKEN exits 2", func(t *testing.T) {
		_, stderr, code := runCLI(t, nil, "run", "cli/ok")
		if code != 2 || !strings.Contains(stderr, "SLUICE_TOKEN") {
			t.Fatalf("exit %d: %s", code, stderr)
		}
	})

	t.Run("run --wait exits 0 on SUCCESS and streams the logs to stderr", func(t *testing.T) {
		stdout, stderr, code := runCLI(t, env, "run", "cli/ok", "--wait")
		if code != 0 || !strings.Contains(stderr, "[say#1] hello cli") || !strings.HasPrefix(stdout, "SUCCESS cli/ok") {
			t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
	})

	t.Run("run --wait exits 10 on FAILED and prints the API JSON with --output json", func(t *testing.T) {
		stdout, stderr, code := runCLI(t, env, "run", "cli/fail", "--wait", "--output", "json")
		var d struct {
			State string `json:"state"`
			ID    string `json:"id"`
		}
		if code != 10 || json.Unmarshal([]byte(stdout), &d) != nil || d.State != "FAILED" || d.ID == "" {
			t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		out, _, code := runCLI(t, env, "executions", "logs", d.ID, "--output", "json")
		var lines []map[string]any
		if code != 0 || json.Unmarshal([]byte(out), &lines) != nil {
			t.Fatalf("logs exit %d: %s", code, out)
		}
	})

	t.Run("an API error exits 1 with its code", func(t *testing.T) {
		_, stderr, code := runCLI(t, env, "run", "cli/missing")
		if code != 1 || !strings.Contains(stderr, "error: ") {
			t.Fatalf("exit %d: %s", code, stderr)
		}
	})

	t.Run("namespaces push creates one version with the changes and none when nothing changed", func(t *testing.T) {
		dir := t.TempDir()
		write := func(p, s string) {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("hi.flow.yaml", "id: hi\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n")
		write("lib/a.txt", "a\n")
		push := func() map[string]any {
			t.Helper()
			stdout, stderr, code := runCLI(t, env, "namespaces", "push", dir, "--namespace", "pushed", "--create", "-o", "json")
			var out map[string]any
			if code != 0 || json.Unmarshal([]byte(stdout), &out) != nil {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			return out
		}
		if out := push(); out["version"] != float64(1) || out["changed"] != true {
			t.Fatalf("first push %v", out)
		}
		if out := push(); out["version"] != float64(1) || out["changed"] != false {
			t.Fatalf("push without a change %v", out)
		}
		write("lib/a.txt", "b\n")
		if err := os.Remove(filepath.Join(dir, "hi.flow.yaml")); err != nil {
			t.Fatal(err)
		}
		out := push()
		if out["version"] != float64(2) || len(out["updated"].([]any)) != 1 || len(out["deleted"].([]any)) != 1 {
			t.Fatalf("push with a change %v", out)
		}
	})
}
