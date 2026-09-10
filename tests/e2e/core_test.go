//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// sdcCommands are the commands of SDD §4.2.
var sddCommands = []string{"server", "exec", "runner-install", "migrate", "user create", "user reset-password", "secrets rekey", "validate", "version"}

func TestSCN_CORE_001_VersionAndConfigErrors(t *testing.T) {
	out, _, code := runCLI(t, nil, "version")
	if code != 0 {
		t.Fatalf("version exit %d", code)
	}
	for _, f := range []string{"version:", "commit:", "build_date:"} {
		if !strings.Contains(out, f) {
			t.Errorf("version output misses %s: %q", f, out)
		}
	}
	_, stderr, code := runCLI(t, map[string]string{"SLUICE_WORKER_SLOTS": "-1"}, "server")
	if code != 2 {
		t.Fatalf("server with invalid config: exit %d, want 2\n%s", code, stderr)
	}
	for _, v := range []string{"SLUICE_DATABASE_URL", "SLUICE_WORKER_SLOTS"} {
		if !strings.Contains(stderr, v) {
			t.Errorf("error output does not name %s:\n%s", v, stderr)
		}
	}
	help, _, _ := runCLI(t, nil, "help")
	for _, c := range sddCommands {
		if !strings.Contains(help, "\n  "+c+" ") {
			t.Errorf("help does not list command %q", c)
		}
	}
}
