//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIClient pins the contract of the client commands that CI jobs and agents depend on:
// the exit code of each end state, the API JSON on stdout, and a push that creates a version
// only when a file changed.
func TestCLIClient(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_MASTER_KEYS": masterKey("k1", 1)})
	admin := adminClient(t, p)
	saveFiles(t, admin, "cli", map[string]string{
		"ok.flow.yaml":   "id: ok\ntasks:\n  - {id: say, type: command, command: [\"echo\", \"hello cli\"]}\n",
		"fail.flow.yaml": "id: fail\ntasks:\n  - {id: boom, type: command, command: [\"sh\", \"-c\", \"exit 3\"]}\n",
		"sel.flow.yaml":  "id: sel\ninputs:\n  - {id: space, type: select, values: [\"all\", \"1\"]}\n  - {id: n, type: int, default: 1}\ntasks:\n  - {id: say, type: command, command: [\"echo\", \"space=${{ inputs.space }} n=${{ inputs.n }}\"]}\n",
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
		// The text output names the reason of each task run, for example exit_code.
		out, _, code = runCLI(t, env, "executions", "get", d.ID)
		if code != 0 || !strings.Contains(out, "REASON") || !strings.Contains(out, "exit_code") {
			t.Fatalf("get exit %d:\n%s", code, out)
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

	t.Run("run --input keeps the text of a select input and the JSON type of an int input", func(t *testing.T) {
		_, stderr, code := runCLI(t, env, "run", "cli/sel", "--input", "space=1", "--input", "n=7", "--wait")
		if code != 0 || !strings.Contains(stderr, "space=1 n=7") {
			t.Fatalf("exit %d: %s", code, stderr)
		}
	})

	t.Run("a validation error prints each detail, and the envelope with --output json", func(t *testing.T) {
		stdout, stderr, code := runCLI(t, env, "run", "cli/sel", "--input", "space=zzz", "--output", "json")
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Details []struct {
					Field string `json:"field"`
				} `json:"details"`
			} `json:"error"`
		}
		if code != 1 || !strings.Contains(stderr, "  inputs.space: must be one of") {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if json.Unmarshal([]byte(stdout), &env) != nil || env.Error.Code != "validation_failed" || len(env.Error.Details) != 1 || env.Error.Details[0].Field != "inputs.space" {
			t.Fatalf("stdout: %s", stdout)
		}
	})

	t.Run("namespaces push skips ignored files and refuses a file that looks like a secret", func(t *testing.T) {
		dir := t.TempDir()
		for p, content := range map[string]string{
			"hi.flow.yaml":              "id: hi\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n",
			"scripts/__pycache__/x.pyc": "x",
			"out/report.html":           "x",
			".env":                      "TOKEN=abc\n",
			".env.example":              "TOKEN=\n",
			".sluiceignore":             "out/\n!.env.example\n",
		} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, p), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		_, stderr, code := runCLI(t, env, "namespaces", "push", dir, "--namespace", "ignored", "--create")
		if code != 1 || !strings.Contains(stderr, "  .env\n") || strings.Contains(stderr, ".env.example") {
			t.Fatalf("push with .env: exit %d: %s", code, stderr)
		}
		// The refused push created no namespace.
		admin.do(t, http.MethodGet, "/api/v1/namespaces/ignored", nil, http.StatusNotFound, nil)
		if err := os.WriteFile(filepath.Join(dir, ".sluiceignore"), []byte("out/\n!.env.example\n.env\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := runCLI(t, env, "namespaces", "push", dir, "--namespace", "ignored", "--create", "-o", "json")
		var out struct {
			Added []string `json:"added"`
		}
		if code != 0 || json.Unmarshal([]byte(stdout), &out) != nil || strings.Join(out.Added, " ") != ".env.example hi.flow.yaml" {
			t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
	})

	t.Run("secrets set reads the value from a file, and list, check and delete work on it", func(t *testing.T) {
		adminEnv := map[string]string{"SLUICE_URL": p.URL, "SLUICE_TOKEN": admin.token}
		file := filepath.Join(t.TempDir(), "key.json")
		if err := os.WriteFile(file, []byte("{\"k\": \"cli-secret-value\"}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, stderr, code := runCLI(t, adminEnv, "secrets", "set", "CLI_KEY", "--from-file", file); code != 0 {
			t.Fatalf("set: exit %d: %s", code, stderr)
		}
		stdout, _, code := runCLI(t, adminEnv, "secrets", "list")
		if code != 0 || !strings.Contains(stdout, "CLI_KEY") || strings.Contains(stdout, "cli-secret-value") {
			t.Fatalf("list: exit %d: %s", code, stdout)
		}
		if stdout, _, code := runCLI(t, adminEnv, "secrets", "check", "CLI_KEY"); code != 0 || !strings.Contains(stdout, "ok") {
			t.Fatalf("check: exit %d: %s", code, stdout)
		}
		if _, stderr, code := runCLI(t, adminEnv, "secrets", "delete", "CLI_KEY"); code != 0 {
			t.Fatalf("delete: exit %d: %s", code, stderr)
		}
		if _, _, code := runCLI(t, adminEnv, "secrets", "check", "CLI_KEY"); code != 1 {
			t.Fatalf("check after delete: exit %d", code)
		}
		if _, stderr, code := runCLI(t, adminEnv, "secrets", "set", "CLI_KEY"); code != 2 {
			t.Fatalf("set without a source: exit %d: %s", code, stderr)
		}
	})
}
