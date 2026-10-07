//go:build e2e

package e2e

import (
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
