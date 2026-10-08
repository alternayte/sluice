//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// emitData writes data.parquet and emits it as an artifact.
const emitData = `["sh", "-c", "echo rows-42 > data.parquet && echo '{\"type\":\"artifact\",\"path\":\"data.parquet\",\"name\":\"data.parquet\"}' >> $SLUICE_OUTPUTS"]`

// TestArtifactInputs pins the artifact inputs of a task: the runner writes the artifact of a
// dependency into the workdir, a restart reads the artifact of a reused task run, and a
// missing artifact fails the task before its command starts.
func TestArtifactInputs(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	gate := filepath.Join(t.TempDir(), "gate")
	saveFiles(t, c, "arts", map[string]string{
		"pipe.flow.yaml": "id: pipe\ntasks:\n  - id: extract\n    type: command\n    command: " + emitData + "\n" +
			"  - id: render\n    type: command\n    depends_on: [extract]\n    artifacts:\n      - {from: extract, name: data.parquet, path: data/in.parquet}\n" +
			"    command: [\"sh\", \"-c\", \"cat data/in.parquet && test -f " + gate + "\"]\n",
		"missing.flow.yaml": "id: missing\ntasks:\n  - {id: extract, type: command, command: [\"true\"]}\n" +
			"  - id: render\n    type: command\n    depends_on: [extract]\n    artifacts:\n      - {from: extract, name: nope.bin}\n" +
			"    command: [\"echo\", \"must not run\"]\n",
	})

	first := waitTerminal(t, c, triggerFlow(t, c, "arts", "pipe", nil, nil).ID, 60*time.Second)
	if first.State != "FAILED" || !strings.Contains(logText(allLogs(t, c, first.ID, "render")), "rows-42") {
		t.Fatalf("first run: %s %s", first.State, first.Error)
	}
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var restarted execDetail
	c.do(t, http.MethodPost, "/api/v1/executions/"+first.ID+"/restart", nil, http.StatusCreated, &restarted)
	second := waitTerminal(t, c, restarted.ID, 60*time.Second)
	if second.State != "SUCCESS" || second.last("extract").ReusedFromID == nil {
		t.Fatalf("restart: %s %s, extract %+v", second.State, second.Error, second.last("extract"))
	}
	var arts struct {
		Items []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			TaskKey string `json:"task_key"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/executions/"+second.ID+"/artifacts", nil, http.StatusOK, &arts)
	if len(arts.Items) != 1 || arts.Items[0].Name != "data.parquet" || arts.Items[0].TaskKey != "extract" {
		t.Fatalf("restart: the new execution does not list the artifact of the reused task run: %+v", arts.Items)
	}
	if r := c.do(t, http.MethodGet, "/api/v1/executions/"+second.ID+"/artifacts/"+arts.Items[0].ID, nil, http.StatusOK, nil); !strings.Contains(string(r.Body), "rows-42") {
		t.Fatalf("restart: artifact download: %s", r.Body)
	}
	if logs := logText(allLogs(t, c, second.ID, "render")); !strings.Contains(logs, "rows-42") {
		t.Fatalf("restart: render did not read the artifact of the reused task run:\n%s", logs)
	}

	m := waitTerminal(t, c, triggerFlow(t, c, "arts", "missing", nil, nil).ID, 60*time.Second)
	tr := m.last("render")
	if m.State != "FAILED" || tr.Reason != "artifact_not_found" || !strings.Contains(tr.Error, "extract") || !strings.Contains(tr.Error, "nope.bin") {
		t.Fatalf("missing artifact: %s, render %s %q", m.State, tr.Reason, tr.Error)
	}
	if logs := logText(allLogs(t, c, m.ID, "render")); strings.Contains(logs, "must not run") {
		t.Fatalf("the command started without its artifact:\n%s", logs)
	}
}

// TestArtifactInputsDocker runs the same hand-over in containers: the runner in the
// container has no storage access and gets the artifact from the runner API.
func TestArtifactInputsDocker(t *testing.T) {
	p := dockerServer(t)
	c := adminClient(t, p)
	saveFiles(t, c, "dockarts", map[string]string{
		"pipe.flow.yaml": "id: pipe\nexecutor: {type: docker, image: \"" + imageSluiceUV + "\", inject_runner: false, pull: never}\n" +
			"tasks:\n  - id: extract\n    type: command\n    command: " + emitData + "\n" +
			"  - id: render\n    type: command\n    depends_on: [extract]\n    artifacts:\n      - {from: extract, name: data.parquet}\n" +
			"    command: [\"cat\", \"data.parquet\"]\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "dockarts", "pipe", nil, nil).ID, 180*time.Second)
	if d.State != "SUCCESS" || !strings.Contains(logText(allLogs(t, c, d.ID, "render")), "rows-42") {
		t.Fatalf("docker: %s %s", d.State, d.Error)
	}
}

// TestRunnerProtocolLevel calls the spec route as a runner of the first release, which sends
// no protocol level. A task without new spec features gets its spec. A task with artifact
// inputs fails with runner_too_old, because that runner would start the command without the files.
func TestRunnerProtocolLevel(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	// The script prints the run token of its own runner, then waits for the test.
	const leak = "ps eww -o command= -p $PPID | tr ' ' '\\n' | grep -E '^SLUICE_(RUN_TOKEN|TASK_RUN_ID)='\nsleep 20\n"
	saveFiles(t, c, "level", map[string]string{
		"leak.sh": leak,
		"f.flow.yaml": "id: f\ntasks:\n  - id: extract\n    type: command\n    command: " + emitData + "\n" +
			"  - {id: plain, type: script, file: leak.sh, depends_on: [extract]}\n" +
			"  - id: reads\n    type: script\n    file: leak.sh\n    depends_on: [extract]\n    artifacts:\n      - {from: extract, name: data.parquet}\n",
	})
	d := triggerFlow(t, c, "level", "f", nil, nil)
	credentials := func(task string) (token, id string) {
		for deadline := time.Now().Add(30 * time.Second); token == "" || id == ""; time.Sleep(200 * time.Millisecond) {
			for _, l := range allLogs(t, c, d.ID, task) {
				if v, ok := strings.CutPrefix(l.Text, "SLUICE_RUN_TOKEN="); ok {
					token = v
				}
				if v, ok := strings.CutPrefix(l.Text, "SLUICE_TASK_RUN_ID="); ok {
					id = v
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("no run token in the logs of %s", task)
			}
		}
		return token, id
	}
	spec := func(task, level string) (int, string) {
		token, id := credentials(task)
		req, _ := http.NewRequest(http.MethodGet, p.URL+"/api/runner/v1/task-runs/"+id+"/spec", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if level != "" {
			req.Header.Set("X-Sluice-Runner-Protocol", level)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	if status, body := spec("plain", ""); status != http.StatusOK {
		t.Fatalf("a task without new features, old runner: %d %s", status, body)
	}
	if status, body := spec("reads", "2"); status != http.StatusOK {
		t.Fatalf("a task with artifacts, runner of level 2: %d %s", status, body)
	}
	if status, body := spec("reads", ""); status != http.StatusConflict || !strings.Contains(body, "runner_too_old") {
		t.Fatalf("a task with artifacts, old runner: %d %s", status, body)
	}
	d = waitExec(t, c, d.ID, 30*time.Second, func(x execDetail) bool { return terminal[x.last("reads").State] })
	if tr := d.last("reads"); tr.State != "FAILED" || tr.Reason != "runner_too_old" || !strings.Contains(tr.Error, "level 1") || !strings.Contains(tr.Error, "level 2") {
		t.Fatalf("reads: %s %s %q", tr.State, tr.Reason, tr.Error)
	}
}
