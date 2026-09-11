//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/testutil/gitserver"
)

type gitSourceOut struct {
	ID            string `json:"id"`
	WebhookURL    string `json:"webhook_url"`
	LastSyncedSha string `json:"last_synced_sha"`
	LastError     string `json:"last_error"`
}

type gitRun struct {
	Status           string   `json:"status"`
	Sha              string   `json:"sha"`
	Error            string   `json:"error"`
	Warnings         []string `json:"warnings"`
	SnapshotsCreated int      `json:"snapshots_created"`
}

func gitFlow(id string) string {
	return "id: " + id + "\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"
}

// gitEnv returns the server environment with the env secrets of the fixture credentials.
func gitEnv(db string, g *gitserver.Server) map[string]string {
	return map[string]string{
		"SLUICE_DATABASE_URL":         db,
		"SLUICE_SECRET_GIT_TOKEN":     gitserver.Token,
		"SLUICE_SECRET_GIT_SSH_KEY":   string(g.ClientKey),
		"SLUICE_SECRET_GIT_SSH_OTHER": string(g.OtherKey),
		"SLUICE_SECRET_GIT_HOOK":      "hook-secret",
	}
}

func envSecrets(t testing.TB, c *client, keys ...string) {
	t.Helper()
	for _, k := range keys {
		c.do(t, http.MethodPut, "/api/v1/secrets/"+k, map[string]any{"provider": "env"}, http.StatusOK, nil)
	}
}

func createGitSource(t testing.TB, c *client, body map[string]any) gitSourceOut {
	t.Helper()
	var out gitSourceOut
	c.do(t, http.MethodPost, "/api/v1/git-sources", body, http.StatusCreated, &out)
	return out
}

func gitRuns(t testing.TB, c *client, id string) []gitRun {
	t.Helper()
	var l struct {
		Items []gitRun `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/git-sources/"+id+"/runs", nil, http.StatusOK, &l)
	return l.Items
}

// waitRuns waits until a source has n ended sync runs.
func waitRuns(t testing.TB, c *client, id string, n int, timeout time.Duration) []gitRun {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(250 * time.Millisecond) {
		runs := gitRuns(t, c, id)
		if len(runs) >= n && runs[0].Status != "running" {
			return runs
		}
		if time.Now().After(deadline) {
			t.Fatalf("source %s has %d runs after %s, want %d: %+v", id, len(runs), timeout, n, runs)
		}
	}
}

func headSha(t testing.TB, c *client, ns string) string {
	t.Helper()
	var out struct {
		HeadGitSha *string `json:"head_git_sha"`
	}
	c.do(t, http.MethodGet, "/api/v1/namespaces/"+ns, nil, http.StatusOK, &out)
	if out.HeadGitSha == nil {
		return ""
	}
	return *out.HeadGitSha
}

func tokenSource(name, repoURL string, maps ...map[string]string) map[string]any {
	return map[string]any{"name": name, "repo_url": repoURL, "branch": "main", "auth_type": "https_token",
		"credential_secret_key": "GIT_TOKEN", "mappings": maps}
}

// TestSCN_GIT_001_TokenSource syncs an https-token source that maps pipelines/elt to
// data.elt. The snapshot has the commit SHA and the flows appear (REQ-GIT-001, REQ-GIT-002).
func TestSCN_GIT_001_TokenSource(t *testing.T) {
	g := gitserver.Start(t)
	repoURL, _ := g.Repo("elt")
	sha := g.Commit("elt", "main", "first", []gitserver.File{
		{Path: "pipelines/elt/sync.flow.yaml", Content: gitFlow("sync")},
		{Path: "other/readme.md", Content: "not mapped"},
	})
	p := startServer(t, gitEnv(newDatabase(t), g))
	c := adminClient(t, p)
	envSecrets(t, c, "GIT_TOKEN")
	src := createGitSource(t, c, tokenSource("elt", repoURL, map[string]string{"repo_path": "pipelines/elt", "namespace": "data.elt"}))
	runs := waitRuns(t, c, src.ID, 1, 30*time.Second)
	if runs[0].Status != "success" || runs[0].Sha != sha || runs[0].SnapshotsCreated != 1 {
		t.Fatalf("run %+v", runs[0])
	}
	if got := headSha(t, c, "data.elt"); got != sha {
		t.Fatalf("data.elt head %q, want %s", got, sha)
	}
	var flows struct {
		Items []struct {
			FlowID string `json:"flow_id"`
			Path   string `json:"path"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/flows?namespace=data.elt", nil, http.StatusOK, &flows)
	if len(flows.Items) != 1 || flows.Items[0].FlowID != "sync" || flows.Items[0].Path != "sync.flow.yaml" {
		t.Fatalf("flows %+v", flows.Items)
	}
}

// TestSCN_GIT_002_SSHSource syncs an ssh-key source. A wrong key records an error and the
// previous snapshot stays active (REQ-GIT-001, REQ-GIT-002).
func TestSCN_GIT_002_SSHSource(t *testing.T) {
	g := gitserver.Start(t)
	_, sshURL := g.Repo("ssh")
	sha1 := g.Commit("ssh", "main", "first", []gitserver.File{{Path: "s.flow.yaml", Content: gitFlow("s")}})
	p := startServer(t, gitEnv(newDatabase(t), g))
	c := adminClient(t, p)
	envSecrets(t, c, "GIT_SSH_KEY", "GIT_SSH_OTHER")
	body := map[string]any{"name": "ssh", "repo_url": sshURL, "branch": "main", "auth_type": "ssh_key", "credential_secret_key": "GIT_SSH_KEY",
		"known_hosts": g.KnownHosts, "mappings": []map[string]string{{"repo_path": "", "namespace": "sshns"}}}
	src := createGitSource(t, c, body)
	if runs := waitRuns(t, c, src.ID, 1, 30*time.Second); runs[0].Status != "success" {
		t.Fatalf("ssh sync: %+v", runs[0])
	}
	if got := headSha(t, c, "sshns"); got != sha1 {
		t.Fatalf("head %q, want %s", got, sha1)
	}
	g.Commit("ssh", "main", "second", []gitserver.File{{Path: "s.flow.yaml", Content: gitFlow("s") + "# changed\n"}})
	body["credential_secret_key"] = "GIT_SSH_OTHER"
	delete(body, "name")
	c.do(t, http.MethodPut, "/api/v1/git-sources/"+src.ID, body, http.StatusOK, nil)
	c.do(t, http.MethodPost, "/api/v1/git-sources/"+src.ID+"/sync", nil, http.StatusAccepted, nil)
	runs := waitRuns(t, c, src.ID, 2, 30*time.Second)
	if runs[0].Status != "failed" || runs[0].Error == "" {
		t.Fatalf("sync with a wrong key: %+v", runs[0])
	}
	if got := headSha(t, c, "sshns"); got != sha1 {
		t.Fatalf("after the failed sync the head is %q, want the previous %s", got, sha1)
	}
	var out gitSourceOut
	c.do(t, http.MethodGet, "/api/v1/git-sources/"+src.ID, nil, http.StatusOK, &out)
	if out.LastError == "" || out.LastSyncedSha != sha1 {
		t.Fatalf("source %+v", out)
	}
}

// TestSCN_GIT_003_PollOneMapping changes one mapping in a new commit. Polling creates a
// snapshot for that mapping only (REQ-GIT-002).
func TestSCN_GIT_003_PollOneMapping(t *testing.T) {
	g := gitserver.Start(t)
	repoURL, _ := g.Repo("poll")
	sha1 := g.Commit("poll", "main", "first", []gitserver.File{{Path: "a/x.txt", Content: "1"}, {Path: "b/y.txt", Content: "1"}})
	p := startServer(t, gitEnv(newDatabase(t), g))
	c := adminClient(t, p)
	envSecrets(t, c, "GIT_TOKEN")
	body := tokenSource("poll", repoURL, map[string]string{"repo_path": "a", "namespace": "polla"}, map[string]string{"repo_path": "b", "namespace": "pollb"})
	body["poll_interval"] = 15
	src := createGitSource(t, c, body)
	if runs := waitRuns(t, c, src.ID, 1, 30*time.Second); runs[0].SnapshotsCreated != 2 {
		t.Fatalf("first sync: %+v", runs[0])
	}
	sha2 := g.Commit("poll", "main", "second", []gitserver.File{{Path: "a/x.txt", Content: "1"}, {Path: "b/y.txt", Content: "2"}})
	runs := waitRuns(t, c, src.ID, 2, 45*time.Second)
	if runs[0].Status != "success" || runs[0].Sha != sha2 || runs[0].SnapshotsCreated != 1 {
		t.Fatalf("poll sync: %+v", runs[0])
	}
	if a, b := headSha(t, c, "polla"), headSha(t, c, "pollb"); a != sha1 || b != sha2 {
		t.Fatalf("heads polla %s pollb %s, want %s and %s", a, b, sha1, sha2)
	}
}

func signed(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestSCN_GIT_004_Webhook syncs within 5 s after a valid HMAC webhook. An invalid signature
// returns 401 and a push to another branch does not sync (REQ-GIT-003, SI-05).
func TestSCN_GIT_004_Webhook(t *testing.T) {
	g := gitserver.Start(t)
	repoURL, _ := g.Repo("hook")
	g.Commit("hook", "main", "first", []gitserver.File{{Path: "a.txt", Content: "1"}})
	p := startServer(t, gitEnv(newDatabase(t), g))
	c := adminClient(t, p)
	envSecrets(t, c, "GIT_TOKEN", "GIT_HOOK")
	body := tokenSource("hook", repoURL, map[string]string{"repo_path": "", "namespace": "hookns"})
	body["poll_interval"] = 3600
	body["webhook_secret_key"] = "GIT_HOOK"
	src := createGitSource(t, c, body)
	waitRuns(t, c, src.ID, 1, 30*time.Second)

	sha2 := g.Commit("hook", "main", "second", []gitserver.File{{Path: "a.txt", Content: "2"}})
	push := []byte(`{"ref":"refs/heads/main"}`)
	start := time.Now()
	if code, b := postHook(t, src.WebhookURL, push, map[string]string{"X-Hub-Signature-256": signed("hook-secret", push)}); code != http.StatusAccepted {
		t.Fatalf("valid webhook: %d %s", code, b)
	}
	for headSha(t, c, "hookns") != sha2 {
		if time.Since(start) > 5*time.Second {
			t.Fatal("the webhook did not sync within 5 s")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code, _ := postHook(t, src.WebhookURL, push, map[string]string{"X-Hub-Signature-256": "sha256=" + hex.EncodeToString(make([]byte, 32))}); code != http.StatusUnauthorized {
		t.Fatalf("invalid signature: %d, want 401", code)
	}
	g.Commit("hook", "main", "third", []gitserver.File{{Path: "a.txt", Content: "3"}})
	other := []byte(`{"ref":"refs/heads/feature"}`)
	code, b := postHook(t, src.WebhookURL, other, map[string]string{"X-Hub-Signature-256": signed("hook-secret", other)})
	if code != http.StatusAccepted || !bytes.Contains(b, []byte(`"queued":false`)) {
		t.Fatalf("push to another branch: %d %s", code, b)
	}
	time.Sleep(3 * time.Second)
	if n := len(gitRuns(t, c, src.ID)); n != 2 {
		t.Fatalf("%d sync runs after a push to another branch, want 2", n)
	}
	if got := headSha(t, c, "hookns"); got != sha2 {
		t.Fatalf("head %s, want %s", got, sha2)
	}
}

// TestSCN_GIT_006_RemovedFlow removes a flow file in git. The flow is deleted, its
// triggers are inactive and old executions stay visible (REQ-GIT-006).
func TestSCN_GIT_006_RemovedFlow(t *testing.T) {
	g := gitserver.Start(t)
	repoURL, _ := g.Repo("gone")
	flowSrc := "id: gflow\ntriggers:\n  - {id: yearly, type: schedule, cron: \"0 0 1 1 *\"}\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n"
	g.Commit("gone", "main", "first", []gitserver.File{{Path: "g.flow.yaml", Content: flowSrc}, {Path: "keep.txt", Content: "k"}})
	db := newDatabase(t)
	p := startServer(t, gitEnv(db, g))
	c := adminClient(t, p)
	envSecrets(t, c, "GIT_TOKEN")
	src := createGitSource(t, c, tokenSource("gone", repoURL, map[string]string{"repo_path": "", "namespace": "gone"}))
	waitRuns(t, c, src.ID, 1, 30*time.Second)
	d := waitTerminal(t, c, triggerFlow(t, c, "gone", "gflow", nil, nil).ID, 60*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("execution %s", d.State)
	}

	g.Commit("gone", "main", "remove the flow", []gitserver.File{{Path: "keep.txt", Content: "k"}})
	c.do(t, http.MethodPost, "/api/v1/git-sources/"+src.ID+"/sync", nil, http.StatusAccepted, nil)
	if runs := waitRuns(t, c, src.ID, 2, 30*time.Second); runs[0].Status != "success" {
		t.Fatalf("sync: %+v", runs[0])
	}
	var flows struct {
		Items []map[string]any `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/flows?namespace=gone", nil, http.StatusOK, &flows)
	if len(flows.Items) != 0 {
		t.Fatalf("flows after removal: %+v", flows.Items)
	}
	if r := c.raw(t, http.MethodGet, "/api/v1/flows/gone/gflow", nil, nil); r.Status != http.StatusNotFound {
		t.Fatalf("removed flow: %d %s", r.Status, r.Body)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var deleted bool
	var active int
	if err := conn.QueryRow(ctx, `SELECT f.deleted_at IS NOT NULL, (SELECT count(*) FROM triggers t WHERE t.flow_id = f.id AND t.active)
		FROM flows f WHERE f.flow_key = 'gflow'`).Scan(&deleted, &active); err != nil {
		t.Fatal(err)
	}
	if !deleted || active != 0 {
		t.Fatalf("flow deleted %v, active triggers %d", deleted, active)
	}
	if old := getExec(t, c, d.ID); old.State != "SUCCESS" {
		t.Fatalf("old execution %s", old.State)
	}
}

// TestSCN_GIT_007_MappingConflicts maps a managed namespace and a namespace of another
// source. Both return 409 (REQ-GIT-007).
func TestSCN_GIT_007_MappingConflicts(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	createNamespace(t, c, "managed-one")
	repo := "https://git.example.com/acme/repo.git"
	body := map[string]any{"name": "a", "repo_url": repo, "branch": "main", "auth_type": "none",
		"mappings": []map[string]string{{"repo_path": "x", "namespace": "managed-one"}}}
	if r := c.raw(t, http.MethodPost, "/api/v1/git-sources", body, nil); r.Status != http.StatusConflict || errCode(r.Body) != "namespace_managed" {
		t.Fatalf("mapping a managed namespace: %d %s", r.Status, r.Body)
	}
	body["mappings"] = []map[string]string{{"repo_path": "x", "namespace": "shared-ns"}}
	createGitSource(t, c, body)
	body["name"] = "b"
	if r := c.raw(t, http.MethodPost, "/api/v1/git-sources", body, nil); r.Status != http.StatusConflict || errCode(r.Body) != "namespace_mapped" {
		t.Fatalf("mapping a namespace of another source: %d %s", r.Status, r.Body)
	}
}

// TestSCN_NS_004_GitNamespaceReadOnly writes a file to a git namespace: 409 namespace_read_only (REQ-NS-004).
func TestSCN_NS_004_GitNamespaceReadOnly(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	createGitSource(t, c, map[string]any{"name": "ro", "repo_url": "https://git.example.com/acme/ro.git", "branch": "main", "auth_type": "none",
		"mappings": []map[string]string{{"repo_path": "", "namespace": "ro-ns"}}})
	r := c.raw(t, http.MethodPost, "/api/v1/namespaces/ro-ns/changes",
		map[string]any{"message": "edit", "changes": []map[string]any{{"op": "put", "path": "a.txt", "content": "x"}}}, nil)
	if r.Status != http.StatusConflict || errCode(r.Body) != "namespace_read_only" {
		t.Fatalf("file write to a git namespace: %d %s", r.Status, r.Body)
	}
}
