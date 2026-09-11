//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The images come from `just build-images`, which `just build` runs before `just e2e`.
const (
	imageSluice   = "sluice:dev"
	imageSluiceUV = "sluice-uv:dev"
)

// docker runs the docker command and returns its trimmed output.
func docker(t testing.TB, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func requireImages(t testing.TB) {
	t.Helper()
	for _, img := range []string{imageSluice, imageSluiceUV} {
		if err := exec.Command("docker", "image", "inspect", img).Run(); err != nil {
			t.Fatalf("image %s is missing: run `just build-images` (or `just build`) first", img)
		}
	}
}

// dockerServer starts a server with the docker executor. It listens on all interfaces, so
// that task containers reach it through host.docker.internal on Linux engines as well.
func dockerServer(t testing.TB) *Proc {
	t.Helper()
	requireImages(t)
	port := freePort(t)
	return startServerOnPort(t, port, map[string]string{
		"SLUICE_DATABASE_URL":   newDatabase(t),
		"SLUICE_LISTEN_ADDR":    fmt.Sprintf("0.0.0.0:%d", port),
		"SLUICE_EXECUTORS":      "process,docker",
		"SLUICE_RUNNER_IMAGE":   imageSluice,
		"SLUICE_DOCKER_API_URL": fmt.Sprintf("http://host.docker.internal:%d", port),
	})
}

// containerOf waits until a task container of the execution exists and returns its ID.
func containerOf(t testing.TB, execID string, timeout time.Duration) string {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(200 * time.Millisecond) {
		if id := docker(t, "ps", "-q", "--filter", "label=sluice.dev/execution-id="+execID); id != "" {
			return id
		}
		if time.Now().After(deadline) {
			t.Fatalf("no container of execution %s within %s", execID, timeout)
		}
	}
}

// waitNoContainer waits until no container of the execution exists, running or stopped.
func waitNoContainer(t testing.TB, execID string, timeout time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(250 * time.Millisecond) {
		if docker(t, "ps", "-a", "-q", "--filter", "label=sluice.dev/execution-id="+execID) == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a container of execution %s still exists after %s", execID, timeout)
		}
	}
}

// TestSCN_EXR_003_DockerInjection runs a task in python:3.12-slim with an injected
// runner. It succeeds with logs and outputs, the container has no mounts and it is gone
// after completion (REQ-EXR-004).
func TestSCN_EXR_003_DockerInjection(t *testing.T) {
	p := dockerServer(t)
	c := adminClient(t, p)
	script := `import os, time
print("inside python slim")
open(os.environ["SLUICE_OUTPUTS"], "a").write('{"type":"output","key":"answer","value":42}\n')
time.sleep(3)
`
	saveFiles(t, c, "dock", map[string]string{
		"py.flow.yaml": "id: py\nexecutor: {type: docker, image: \"python:3.12-slim\"}\ntasks:\n  - id: t\n    type: command\n    command: [\"python3\", \"-c\", " + fmt.Sprintf("%q", script) + "]\n",
	})
	d := triggerFlow(t, c, "dock", "py", nil, nil)
	id := containerOf(t, d.ID, 120*time.Second)
	if mounts := docker(t, "inspect", "-f", "{{json .Mounts}}", id); mounts != "[]" {
		t.Fatalf("the task container has mounts: %s", mounts)
	}
	d = waitTerminal(t, c, d.ID, 180*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("execution %s: %s", d.State, d.Error)
	}
	if logs := logText(allLogs(t, c, d.ID, "t")); !strings.Contains(logs, "inside python slim") {
		t.Fatalf("logs:\n%s", logs)
	}
	if v, _ := d.last("t").Outputs["answer"].(float64); v != 42 {
		t.Fatalf("outputs %v", d.last("t").Outputs)
	}
	waitNoContainer(t, d.ID, 15*time.Second)
}

// TestSCN_EXR_004_DockerImageRunner runs a task in sluice-uv without injection, fails a
// missing image with image_pull_failed and removes the container on cancel
// (REQ-EXR-004, REQ-EXR-007, REQ-EXR-009).
func TestSCN_EXR_004_DockerImageRunner(t *testing.T) {
	p := dockerServer(t)
	c := adminClient(t, p)
	own := "executor: {type: docker, image: \"" + imageSluiceUV + "\", inject_runner: false, pull: never}\n"
	saveFiles(t, c, "dock4", map[string]string{
		"own.flow.yaml":     "id: own\n" + own + "tasks:\n  - {id: t, type: command, command: [\"bash\", \"-c\", \"echo from the image path; command -v sluice\"]}\n",
		"missing.flow.yaml": "id: missing\nexecutor: {type: docker, image: \"sluice-e2e-missing/image:does-not-exist\"}\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n",
		"long.flow.yaml":    "id: long\n" + own + "tasks:\n  - {id: t, type: command, command: [\"sleep\", \"120\"]}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "dock4", "own", nil, nil).ID, 120*time.Second)
	if d.State != "SUCCESS" || !strings.Contains(logText(allLogs(t, c, d.ID, "t")), "/usr/local/bin/sluice") {
		t.Fatalf("sluice-uv without injection: %s %s", d.State, d.Error)
	}
	m := waitTerminal(t, c, triggerFlow(t, c, "dock4", "missing", nil, nil).ID, 120*time.Second)
	if tr := m.last("t"); m.State != "FAILED" || tr.Reason != "image_pull_failed" {
		t.Fatalf("missing image: %s, task %s %q", m.State, tr.Reason, tr.Error)
	}
	l := triggerFlow(t, c, "dock4", "long", nil, nil)
	containerOf(t, l.ID, 60*time.Second)
	waitExec(t, c, l.ID, 60*time.Second, func(x execDetail) bool { return x.last("t").State == "RUNNING" })
	c.do(t, http.MethodPost, "/api/v1/executions/"+l.ID+"/cancel", nil, http.StatusAccepted, nil)
	if l = waitTerminal(t, c, l.ID, 60*time.Second); l.State != "CANCELLED" {
		t.Fatalf("cancel: %s", l.State)
	}
	waitNoContainer(t, l.ID, 30*time.Second)
}

// TestSCN_RUN_007_RuntimeTools runs python, bash and bun scripts in sluice-uv. In sluice,
// which has no bun, a bun script fails with runtime_not_found naming bun (REQ-RUN-007).
func TestSCN_RUN_007_RuntimeTools(t *testing.T) {
	p := dockerServer(t)
	c := adminClient(t, p)
	exe := func(image string) string {
		return "executor: {type: docker, image: \"" + image + "\", inject_runner: false, pull: never}\n"
	}
	saveFiles(t, c, "rt", map[string]string{
		"hello.py":        "print('python ok')\n",
		"hello.sh":        "echo bash ok\n",
		"hello.ts":        "console.log('bun ok');\n",
		"uv.flow.yaml":    "id: uv\n" + exe(imageSluiceUV) + "tasks:\n  - {id: py, type: script, file: hello.py}\n  - {id: sh, type: script, file: hello.sh}\n  - {id: ts, type: script, file: hello.ts}\n",
		"nobun.flow.yaml": "id: nobun\n" + exe(imageSluice) + "tasks:\n  - {id: ts, type: script, file: hello.ts}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "rt", "uv", nil, nil).ID, 300*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("sluice-uv scripts: %s %s", d.State, d.Error)
	}
	for task, want := range map[string]string{"py": "python ok", "sh": "bash ok", "ts": "bun ok"} {
		if logs := logText(allLogs(t, c, d.ID, task)); !strings.Contains(logs, want) {
			t.Fatalf("task %s logs:\n%s", task, logs)
		}
	}
	n := waitTerminal(t, c, triggerFlow(t, c, "rt", "nobun", nil, nil).ID, 120*time.Second)
	if tr := n.last("ts"); n.State != "FAILED" || tr.Reason != "runtime_not_found" || !strings.Contains(tr.Error, "bun") {
		t.Fatalf("bun in sluice: %s, task %s %q", n.State, tr.Reason, tr.Error)
	}
}

// TestSCN_DEP_001_Images builds both images. Both run as non-root and `sluice version`
// works in both (REQ-DEP-001).
func TestSCN_DEP_001_Images(t *testing.T) {
	root := repoRoot()
	for target, tag := range map[string]string{"sluice": imageSluice, "sluice-uv": imageSluiceUV} {
		cmd := exec.Command("docker", "build", "-f", "deploy/docker/Dockerfile", "--target", target, "-t", tag, ".")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", target, err, lastLines(string(out), 40))
		}
		if user := docker(t, "image", "inspect", "-f", "{{.Config.User}}", tag); !strings.HasPrefix(user, "65532") {
			t.Fatalf("%s runs as %q, want the non-root user 65532", tag, user)
		}
		if out := docker(t, "run", "--rm", tag, "version"); !strings.Contains(out, "version:") || !strings.Contains(out, "commit:") {
			t.Fatalf("%s version: %q", tag, out)
		}
	}
	if uid := docker(t, "run", "--rm", "--entrypoint", "id", imageSluiceUV, "-u"); uid != "65532" {
		t.Fatalf("sluice-uv runs with uid %s", uid)
	}
	for _, tool := range []string{"bash", "uv", "bun"} {
		docker(t, "run", "--rm", "--entrypoint", tool, imageSluiceUV, "--version")
	}
}

// TestSCN_DEP_003_SingleContainer runs sluice-uv with only environment variables and no
// volumes. A uv flow reaches SUCCESS, and after a container restart history and logs are
// intact (REQ-DEP-003, C-07).
func TestSCN_DEP_003_SingleContainer(t *testing.T) {
	requireImages(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	net, pg, app := "sluice-dep003-"+suffix, "sluice-dep003-pg-"+suffix, "sluice-dep003-app-"+suffix
	docker(t, "network", "create", net)
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", app, pg).Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
	})
	docker(t, "run", "-d", "--name", pg, "--network", net, "-e", "POSTGRES_USER=sluice", "-e", "POSTGRES_PASSWORD=sluice",
		"-e", "POSTGRES_DB=sluice", "postgres:17-alpine")
	for deadline := time.Now().Add(60 * time.Second); exec.Command("docker", "exec", pg, "pg_isready", "-U", "sluice", "-h", "127.0.0.1").Run() != nil; time.Sleep(500 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("postgres not ready")
		}
	}
	docker(t, "run", "-d", "--name", app, "--network", net, "-p", "127.0.0.1::8080",
		"-e", "SLUICE_DATABASE_URL=postgres://sluice:sluice@"+pg+":5432/sluice?sslmode=disable",
		"-e", "SLUICE_PUBLIC_URL=http://localhost:8080", "-e", "SLUICE_EXECUTORS=process",
		"-e", "SLUICE_BOOTSTRAP_ADMIN_EMAIL="+adminEmail, "-e", "SLUICE_BOOTSTRAP_ADMIN_PASSWORD="+adminPassword,
		imageSluiceUV)
	if mounts := docker(t, "inspect", "-f", "{{len .Mounts}}", app); mounts != "0" {
		t.Fatalf("the sluice container has %s mounts", mounts)
	}
	base := waitContainerReady(t, app)
	c := adminClient(t, &Proc{URL: base})
	saveFiles(t, c, "single", map[string]string{
		"hello.py":    "import sys\nprint('uv flow ok on', sys.version.split()[0])\n",
		"u.flow.yaml": "id: u\ntasks:\n  - {id: t, type: script, file: hello.py}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "single", "u", nil, nil).ID, 300*time.Second)
	if d.State != "SUCCESS" || !strings.Contains(logText(allLogs(t, c, d.ID, "t")), "uv flow ok on 3.12") {
		t.Fatalf("uv flow: %s %s", d.State, d.Error)
	}
	docker(t, "restart", app)
	base = waitContainerReady(t, app)
	c = tokenClient(base, c.token)
	after := getExec(t, c, d.ID)
	if after.State != "SUCCESS" || !strings.Contains(logText(allLogs(t, c, d.ID, "t")), "uv flow ok on 3.12") {
		t.Fatalf("after the restart: %s, logs lost", after.State)
	}
}

// waitContainerReady returns the base URL of a sluice container when /readyz is 200.
func waitContainerReady(t testing.TB, name string) string {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(500 * time.Millisecond) {
		if hostPort, err := exec.Command("docker", "port", name, "8080/tcp").Output(); err == nil {
			base := "http://" + strings.TrimSpace(strings.Split(string(hostPort), "\n")[0])
			if resp, err := http.Get(base + "/readyz"); err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return base
				}
			}
		}
		if time.Now().After(deadline) {
			out, _ := exec.Command("docker", "logs", "--tail", "50", name).CombinedOutput()
			t.Fatalf("container %s not ready:\n%s", name, out)
		}
	}
}

// TestSCN_DEP_005_Compose starts deploy/compose. /readyz is 200 within 60 s (REQ-DEP-005).
func TestSCN_DEP_005_Compose(t *testing.T) {
	requireImages(t)
	port := freePort(t)
	project := fmt.Sprintf("sluice-dep005-%d", time.Now().UnixNano())
	env := append(os.Environ(), "COMPOSE_PROJECT_NAME="+project, fmt.Sprintf("SLUICE_PORT=%d", port),
		"SLUICE_BOOTSTRAP_ADMIN_PASSWORD="+adminPassword, "SLUICE_MASTER_KEYS="+masterKey("k1", 1))
	compose := func(args ...string) ([]byte, error) {
		cmd := exec.Command("docker", append([]string{"compose", "-f", "deploy/compose/compose.yml"}, args...)...)
		cmd.Dir = repoRoot()
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	t.Cleanup(func() { _, _ = compose("down", "-v", "--remove-orphans") })
	start := time.Now()
	if out, err := compose("up", "-d"); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	for {
		if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", port)); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Since(start) > 60*time.Second {
			out, _ := compose("logs", "--tail", "50")
			t.Fatalf("/readyz not 200 within 60 s:\n%s", out)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
