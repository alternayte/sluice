//go:build k8s

package k8s

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

// kubectlCmd returns a kubectl command in the test context and namespace.
func kubectlCmd(t testing.TB, args ...string) *exec.Cmd {
	t.Helper()
	return exec.Command("kubectl", append([]string{"--context", env(t, "SLUICE_K8S_CONTEXT"), "-n", env(t, "SLUICE_K8S_TEST_NAMESPACE")}, args...)...)
}

// hostServer starts the host binary with env and a fresh database and returns its base URL.
func hostServer(t *testing.T, extra map[string]string) string {
	t.Helper()
	port := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	vars := map[string]string{
		"SLUICE_DATABASE_URL":             pgtest.Shared(t).NewDatabase(t),
		"SLUICE_LISTEN_ADDR":              fmt.Sprintf("127.0.0.1:%d", port),
		"SLUICE_PUBLIC_URL":               base,
		"SLUICE_BOOTSTRAP_ADMIN_EMAIL":    adminEmail,
		"SLUICE_BOOTSTRAP_ADMIN_PASSWORD": adminPassword,
		"SLUICE_LOG_FORMAT":               "text",
	}
	for k, v := range extra {
		vars[k] = v
	}
	var list []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "SLUICE_") || k == "KUBECONFIG" || (k == "DOCKER_HOST" && extra["DOCKER_HOST"] != "") {
			continue
		}
		list = append(list, kv)
	}
	for k, v := range vars {
		list = append(list, k+"="+v)
	}
	cmd := exec.Command(env(t, "SLUICE_E2E_BINARY"), "server")
	cmd.Env = list
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("server logs:\n%s", logs.String())
		}
	})
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if resp, err := http.Get(base + "/readyz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("host server not ready:\n%s", logs.String())
		}
	}
}

// executorsOf returns the executors that the only instance of a server registered.
func executorsOf(t *testing.T, base string) []string {
	t.Helper()
	c := adminClientAt(t, base)
	var inst struct {
		Items []struct {
			Executors []string `json:"executors"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/instances", nil, http.StatusOK, &inst)
	if len(inst.Items) != 1 {
		t.Fatalf("instances %+v", inst.Items)
	}
	out := inst.Items[0].Executors
	slices.Sort(out)
	return out
}

// TestSCN_EXR_001_Detection starts the binary with SLUICE_EXECUTORS=auto: without Docker
// or a cluster it enables inline and process, with a Docker daemon it adds docker and with
// a kubeconfig to kind it adds kubernetes. SLUICE_EXECUTORS=process enables only inline and
// process (REQ-EXR-001, REQ-EXR-002).
func TestSCN_EXR_001_Detection(t *testing.T) {
	kubeconfig := env(t, "SLUICE_KIND_KUBECONFIG")
	noDocker := "unix:///nonexistent/sluice-e2e/docker.sock"
	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"no docker, no cluster", map[string]string{"SLUICE_EXECUTORS": "auto", "DOCKER_HOST": noDocker}, []string{"inline", "process"}},
		{"docker", map[string]string{"SLUICE_EXECUTORS": "auto"}, []string{"docker", "inline", "process"}},
		{"docker and kind", map[string]string{"SLUICE_EXECUTORS": "auto", "SLUICE_K8S_KUBECONFIG": kubeconfig,
			"SLUICE_K8S_NAMESPACE": env(t, "SLUICE_K8S_TEST_NAMESPACE")}, []string{"docker", "inline", "kubernetes", "process"}},
		{"explicit process", map[string]string{"SLUICE_EXECUTORS": "process", "SLUICE_K8S_KUBECONFIG": kubeconfig}, []string{"inline", "process"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := executorsOf(t, hostServer(t, tc.env))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("executors %v, want %v", got, tc.want)
			}
		})
	}
}
