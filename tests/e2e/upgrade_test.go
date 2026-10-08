//go:build e2e && upgrade

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestUpgradeFromLastRelease pins the upgrade promise of a rolling update. The last release
// runs with data and a running execution. The new migrations apply under it. The last
// release must still serve and end that execution, and the new binary must then read the
// same database. scripts/upgrade-test.sh downloads the release and sets the variables.
func TestUpgradeFromLastRelease(t *testing.T) {
	oldBin := os.Getenv("SLUICE_UPGRADE_FROM_BINARY")
	if oldBin == "" {
		t.Fatal("SLUICE_UPGRADE_FROM_BINARY is empty: run scripts/upgrade-test.sh")
	}
	from := os.Getenv("SLUICE_UPGRADE_FROM_VERSION")
	dbURL := newDatabase(t)
	env := func(port int) map[string]string {
		return map[string]string{
			"SLUICE_DATABASE_URL":   dbURL,
			"SLUICE_LISTEN_ADDR":    fmt.Sprintf("127.0.0.1:%d", port),
			"SLUICE_PUBLIC_URL":     fmt.Sprintf("http://127.0.0.1:%d", port),
			"SLUICE_LOG_FORMAT":     "text",
			"SLUICE_EXECUTORS":      "process",
			"SLUICE_SHUTDOWN_GRACE": "10s",
			"SLUICE_MASTER_KEYS":    masterKey("k1", 1),

			"SLUICE_BOOTSTRAP_ADMIN_EMAIL":    adminEmail,
			"SLUICE_BOOTSTRAP_ADMIN_PASSWORD": adminPassword,
		}
	}

	oldPort := freePort(t)
	old := launchBinary(t, oldBin, oldPort, env(oldPort), true)
	c := adminClient(t, old)
	saveFiles(t, c, "up", map[string]string{
		"quick.flow.yaml": "id: quick\ntasks:\n  - id: emit\n    type: command\n    command: " + emitData + "\n",
		"long.flow.yaml":  "id: long\ntasks:\n  - {id: wait, type: command, command: [\"sh\", \"-c\", \"sleep 15 && echo long-done\"]}\n",
	})
	c.do(t, http.MethodPut, "/api/v1/secrets/UP_KEY", map[string]any{"value": "upgrade-secret"}, http.StatusOK, nil)
	before := waitTerminal(t, c, triggerFlow(t, c, "up", "quick", nil, nil).ID, 60*time.Second)
	if before.State != "SUCCESS" {
		t.Fatalf("%s: quick before the upgrade: %s %s", from, before.State, before.Error)
	}
	long := triggerFlow(t, c, "up", "long", nil, nil)
	waitExec(t, c, long.ID, 30*time.Second, func(x execDetail) bool { return x.last("wait").State == "RUNNING" })

	// The new migrations apply while the last release runs.
	migrate := exec.Command(binary(t), "migrate")
	migrate.Env = baseEnv(map[string]string{"SLUICE_DATABASE_URL": dbURL})
	if out, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("migrate with the new binary: %v\n%s", err, out)
	}

	// The last release still works on the new schema.
	if d := waitTerminal(t, c, long.ID, 60*time.Second); d.State != "SUCCESS" {
		t.Fatalf("%s on the new schema: the running execution ended %s %s", from, d.State, d.Error)
	}
	mid := waitTerminal(t, c, triggerFlow(t, c, "up", "quick", nil, nil).ID, 60*time.Second)
	if mid.State != "SUCCESS" {
		t.Fatalf("%s on the new schema: a new execution ended %s %s", from, mid.State, mid.Error)
	}
	saveFiles(t, c, "up", map[string]string{"note.txt": "saved by the last release on the new schema\n"})
	if err := old.Stop(syscall.SIGTERM); err != nil {
		t.Logf("stop of %s: %v", from, err)
	}

	// The new binary reads what the last release wrote, and works.
	newPort := freePort(t)
	p := launchBinary(t, binary(t), newPort, env(newPort), true)
	c = tokenClient(p.URL, c.token)
	for _, id := range []string{before.ID, long.ID, mid.ID} {
		if d := getExec(t, c, id); d.State != "SUCCESS" {
			t.Fatalf("new binary: execution %s of %s is %s", id, from, d.State)
		}
	}
	if logs := logText(allLogs(t, c, long.ID, "wait")); !strings.Contains(logs, "long-done") {
		t.Fatalf("new binary: logs of the execution of %s:\n%s", from, logs)
	}
	var arts struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/executions/"+before.ID+"/artifacts", nil, http.StatusOK, &arts)
	if len(arts.Items) != 1 {
		t.Fatalf("new binary: artifacts of the execution of %s: %+v", from, arts.Items)
	}
	if r := c.do(t, http.MethodGet, "/api/v1/executions/"+before.ID+"/artifacts/"+arts.Items[0].ID, nil, http.StatusOK, nil); !strings.Contains(string(r.Body), "rows-42") {
		t.Fatalf("new binary: artifact of %s: %s", from, r.Body)
	}
	var check struct {
		Status string `json:"status"`
	}
	c.do(t, http.MethodPost, "/api/v1/secrets/UP_KEY/check", nil, http.StatusOK, &check)
	if check.Status != "ok" {
		t.Fatalf("new binary: secret of %s: %s", from, check.Status)
	}
	after := waitTerminal(t, c, triggerFlow(t, c, "up", "quick", nil, nil).ID, 60*time.Second)
	if after.State != "SUCCESS" {
		t.Fatalf("new binary: a new execution ended %s %s", after.State, after.Error)
	}
}
