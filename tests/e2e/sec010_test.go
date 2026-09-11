//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/platform/masking"
	"github.com/alternayte/sluice/internal/testutil/llmserver"
)

// leakOf returns the first form of the canary that s contains, or "".
func leakOf(s string, forms []string) string {
	for _, f := range forms {
		if strings.Contains(s, f) {
			return f
		}
	}
	return ""
}

// leakInBytes also looks into gzip data: log chunks, bundles and archives are compressed.
func leakInBytes(b []byte, forms []string) string {
	if f := leakOf(string(b), forms); f != "" {
		return f
	}
	if len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b {
		if zr, err := gzip.NewReader(bytes.NewReader(b)); err == nil {
			d, _ := io.ReadAll(zr)
			return leakOf(string(d), forms)
		}
	}
	return ""
}

// TestSCN_SEC_010_Canary uses one canary secret in a process task, a docker task, an http
// header and an AI triage. Afterwards no stored or sent text contains a form of it (SI-01).
func TestSCN_SEC_010_Canary(t *testing.T) {
	requireImages(t)
	const canary = "Canary-Sec010-7f3a9Q"
	forms := masking.Forms(canary)

	var mu sync.Mutex
	var gotAuth string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer hook.Close()

	llm := llmserver.Start(t)
	dbURL := newDatabase(t)
	fsRoot := t.TempDir()
	port := freePort(t)
	p := startServerOnPort(t, port, map[string]string{
		"SLUICE_DATABASE_URL":           dbURL,
		"SLUICE_LISTEN_ADDR":            fmt.Sprintf("0.0.0.0:%d", port),
		"SLUICE_EXECUTORS":              "process,docker",
		"SLUICE_RUNNER_IMAGE":           imageSluice,
		"SLUICE_DOCKER_API_URL":         fmt.Sprintf("http://host.docker.internal:%d", port),
		"SLUICE_DOCKER_KEEP_CONTAINERS": "true",
		"SLUICE_MASTER_KEYS":            masterKey("k1", 1),
		"SLUICE_STORAGE_TYPE":           "fs",
		"SLUICE_FS_ROOT":                fsRoot,
	})
	c := adminClient(t, p)
	c.do(t, http.MethodPut, "/api/v1/secrets/CANARY", map[string]string{"value": canary}, http.StatusOK, nil)
	c.do(t, http.MethodPut, "/api/v1/secrets/LLM_KEY", map[string]string{"value": llmKey}, http.StatusOK, nil)
	setProvider(t, c, llm, true)
	env := "env:\n  S: \"${{ secret('CANARY') }}\"\n"
	saveFiles(t, c, "canary", map[string]string{
		"use.sh":      "echo \"value $S\"\nprintf '{\"type\":\"output\",\"key\":\"v\",\"value\":\"%s\"}\\n' \"$S\" >> \"$SLUICE_OUTPUTS\"\n",
		"fail.sh":     "echo \"failing with $S\" >&2\nexit 1\n",
		"p.flow.yaml": "id: p\n" + env + "tasks:\n  - {id: t, type: script, file: use.sh}\n",
		"d.flow.yaml": "id: d\n" + env + "tasks:\n  - id: t\n    type: script\n    file: use.sh\n    executor: {type: docker, image: " + imageSluiceUV + "}\n",
		"h.flow.yaml": "id: h\ntasks:\n  - id: t\n    type: http\n    url: \"" + hook.URL + "/hook\"\n    headers:\n      Authorization: \"Bearer ${{ secret('CANARY') }}\"\n",
		"f.flow.yaml": "id: f\n" + env + "tasks:\n  - {id: t, type: script, file: fail.sh}\n",
	})
	llm.Push(llmserver.Reply{JSON: `{"summary":"The task failed.","probable_cause":"exit 1","evidence":[],"suggested_fix":"none","confidence":"low"}`})

	ids := map[string]string{}
	for _, f := range []string{"p", "d", "h", "f"} {
		d := waitTerminal(t, c, triggerFlow(t, c, "canary", f, nil, nil).ID, 3*time.Minute)
		want := "SUCCESS"
		if f == "f" {
			want = "FAILED"
		}
		if d.State != want {
			t.Fatalf("flow %s: %s %q\n%s", f, d.State, d.last("t").Error, p.Logs())
		}
		ids[f] = d.ID
	}
	if in := waitInsight(t, c, ids["f"], time.Minute); in.Status != "done" {
		t.Fatalf("triage %+v", in)
	}
	mu.Lock()
	if gotAuth != "Bearer "+canary {
		t.Fatalf("the http task did not send the secret header: %q", gotAuth)
	}
	mu.Unlock()
	if !strings.Contains(logText(allLogs(t, c, ids["d"], "")), "value ***") {
		t.Fatalf("the docker task did not use the secret: %s", logText(allLogs(t, c, ids["d"], "")))
	}

	t.Run("SCN-SEC-010 a text pg_dump has no canary", func(t *testing.T) {
		u, err := url.Parse(dbURL)
		if err != nil {
			t.Fatal(err)
		}
		u.Host = "host.docker.internal:" + u.Port()
		out, err := exec.Command("docker", "run", "--rm", "--add-host=host.docker.internal:host-gateway", "postgres:17-alpine",
			"pg_dump", "--no-owner", u.String()).Output()
		if err != nil || !bytes.Contains(out, []byte("CREATE TABLE public.executions")) {
			t.Fatalf("pg_dump: %v\n%s", err, out)
		}
		if f := leakOf(string(out), forms); f != "" {
			t.Fatalf("the dump contains the form %q", f)
		}
		conn, err := pgx.Connect(context.Background(), dbURL)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(context.Background()) }()
		rows, err := conn.Query(context.Background(), "SELECT data FROM log_chunks")
		if err != nil {
			t.Fatal(err)
		}
		// Ended tasks archive their chunks to storage, so the table can be empty. The storage
		// check reads the archives.
		chunks, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range chunks {
			if f := leakInBytes(b, forms); f != "" {
				t.Fatalf("a log chunk contains the form %q", f)
			}
		}
	})

	t.Run("SCN-SEC-010 no storage object has the canary", func(t *testing.T) {
		n := 0
		err := filepath.WalkDir(fsRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			n++
			if f := leakInBytes(b, forms); f != "" {
				return fmt.Errorf("%s contains the form %q", path, f)
			}
			return nil
		})
		if err != nil || n == 0 {
			t.Fatalf("%d objects: %v", n, err)
		}
	})

	t.Run("SCN-SEC-010 docker inspect has no canary", func(t *testing.T) {
		ids := strings.Fields(docker(t, "ps", "-a", "-q", "--filter", "label=sluice.dev/execution-id="+ids["d"]))
		if len(ids) == 0 {
			t.Fatal("the kept task container is missing")
		}
		t.Cleanup(func() { _ = exec.Command("docker", append([]string{"rm", "-f"}, ids...)...).Run() })
		if f := leakOf(docker(t, append([]string{"inspect"}, ids...)...), forms); f != "" {
			t.Fatalf("docker inspect contains the form %q", f)
		}
	})

	t.Run("SCN-SEC-010 the recorded LLM requests have no canary", func(t *testing.T) {
		reqs := llm.Requests()
		if len(reqs) == 0 {
			t.Fatal("no LLM request was recorded")
		}
		for _, r := range reqs {
			if f := leakOf(string(r.Body), forms); f != "" {
				t.Fatalf("an LLM request contains the form %q", f)
			}
		}
	})

	t.Run("SCN-SEC-010 API responses have no canary", func(t *testing.T) {
		for _, id := range ids {
			b, _ := json.Marshal(getExec(t, c, id))
			if f := leakOf(string(b)+logText(allLogs(t, c, id, "")), forms); f != "" {
				t.Fatalf("execution %s shows the form %q", id, f)
			}
		}
	})
}
