//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// userToken creates a user with role and returns a token client with that role.
func userToken(t testing.TB, admin *client, email, role string) *client {
	t.Helper()
	_, c := createUser(t, admin, email, role, "user-password-1")
	secret, _ := createToken(t, c, role)
	return tokenClient(admin.base, secret)
}

type nsItem struct {
	Name       string `json:"name"`
	Implicit   bool   `json:"implicit"`
	Parent     string `json:"parent"`
	SourceType string `json:"source_type"`
}

func TestSCN_NS_001_NamespaceNames(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	editor := userToken(t, adminClient(t, p), "ns-editor@example.com", "editor")
	for _, bad := range []string{"Data", "a..b", "-a", "a-", "data.", strings.Repeat("a", 129)} {
		r := editor.raw(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": bad}, nil)
		if r.Status != http.StatusUnprocessableEntity || errCode(r.Body) != "validation_failed" {
			t.Errorf("name %q: %d %s", bad, r.Status, r.Body)
		}
	}
	editor.do(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": "data.elt", "description": "ELT"}, http.StatusCreated, nil)
	if r := editor.raw(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": "data.elt"}, nil); r.Status != http.StatusConflict {
		t.Fatalf("duplicate name: %d", r.Status)
	}
	var list struct {
		Items []nsItem `json:"items"`
	}
	editor.do(t, http.MethodGet, "/api/v1/namespaces", nil, http.StatusOK, &list)
	byName := map[string]nsItem{}
	for _, n := range list.Items {
		byName[n.Name] = n
	}
	if n, ok := byName["data"]; !ok || !n.Implicit || n.SourceType != "implicit" {
		t.Fatalf("parent data: %+v (all: %+v)", n, list.Items)
	}
	if n := byName["data.elt"]; n.Parent != "data" || n.Implicit || n.SourceType != "managed" {
		t.Fatalf("data.elt: %+v", n)
	}
	// A viewer cannot create namespaces.
	viewer := userToken(t, adminClient(t, p), "ns-viewer@example.com", "viewer")
	if r := viewer.raw(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": "x"}, nil); r.Status != http.StatusForbidden {
		t.Fatalf("viewer create: %d", r.Status)
	}
}

func TestSCN_NS_009_FileAndSnapshotLimits(t *testing.T) {
	p := startServer(t, map[string]string{
		"SLUICE_DATABASE_URL":     newDatabase(t),
		"SLUICE_MAX_FILE_BYTES":   "1KiB",
		"SLUICE_MAX_BUNDLE_BYTES": "4KiB",
	})
	editor := userToken(t, adminClient(t, p), "limits@example.com", "editor")
	editor.do(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": "limits"}, http.StatusCreated, nil)

	big := strings.Repeat("x", 2048)
	r := editor.raw(t, http.MethodPut, "/api/v1/namespaces/limits/file?path=big.txt", []byte(big),
		map[string]string{"Content-Type": "application/octet-stream"})
	if r.Status != http.StatusRequestEntityTooLarge || errCode(r.Body) != "file_too_large" {
		t.Fatalf("upload above the file limit: %d %s", r.Status, r.Body)
	}
	ok := editor.raw(t, http.MethodPut, "/api/v1/namespaces/limits/file?path=small.txt", []byte("small"),
		map[string]string{"Content-Type": "application/octet-stream"})
	if ok.Status != http.StatusCreated {
		t.Fatalf("small upload: %d %s", ok.Status, ok.Body)
	}

	var changes []map[string]any
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		changes = append(changes, map[string]any{"op": "put", "path": name + ".txt", "content": strings.Repeat(name, 1000)})
	}
	r = editor.raw(t, http.MethodPost, "/api/v1/namespaces/limits/changes", map[string]any{"message": "too much", "changes": changes}, nil)
	if r.Status != http.StatusRequestEntityTooLarge || errCode(r.Body) != "snapshot_too_large" {
		t.Fatalf("save above the snapshot limit: %d %s", r.Status, r.Body)
	}
	r = editor.raw(t, http.MethodPost, "/api/v1/namespaces/limits/changes", map[string]any{"message": "big file", "changes": []map[string]any{
		{"op": "put", "path": "big.txt", "content": big}}}, nil)
	if r.Status != http.StatusRequestEntityTooLarge || errCode(r.Body) != "file_too_large" {
		t.Fatalf("save of a file above the file limit: %d %s", r.Status, r.Body)
	}
}

type snapshotOut struct {
	Version *int   `json:"version"`
	Author  string `json:"author"`
}

type fileDiff struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type versionDiff struct {
	Files []fileDiff `json:"files"`
}

// TestSaveVersionsDiffRevert saves changes over HTTP, lists versions, reads a diff between
// versions, reverts to an earlier version, and checks that a stale base version is refused.
func TestSaveVersionsDiffRevert(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	editor := userToken(t, adminClient(t, p), "svr-editor@example.com", "editor")
	editor.do(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": "data"}, http.StatusCreated, nil)

	var s1 snapshotOut
	editor.do(t, http.MethodPost, "/api/v1/namespaces/data/changes",
		map[string]any{"message": "v1", "changes": []map[string]any{{"op": "put", "path": "a.txt", "content": "one\n"}}},
		http.StatusCreated, &s1)
	if s1.Version == nil || *s1.Version != 1 || s1.Author != "svr-editor@example.com" {
		t.Fatalf("v1: %+v", s1)
	}
	editor.do(t, http.MethodPost, "/api/v1/namespaces/data/changes",
		map[string]any{"message": "v2", "changes": []map[string]any{
			{"op": "put", "path": "a.txt", "content": "two\n"}, {"op": "put", "path": "b.txt", "content": "b\n"}}},
		http.StatusCreated, nil)

	base := 1
	r := editor.raw(t, http.MethodPost, "/api/v1/namespaces/data/changes",
		map[string]any{"message": "stale", "base_version": base, "changes": []map[string]any{{"op": "put", "path": "c.txt", "content": "c"}}}, nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("stale base version: %d %s", r.Status, r.Body)
	}

	editor.do(t, http.MethodPost, "/api/v1/namespaces/data/changes",
		map[string]any{"message": "v3", "changes": []map[string]any{{"op": "rename", "path": "b.txt", "new_path": "dir/b.txt"}}},
		http.StatusCreated, nil)

	var vlist struct {
		Items []snapshotOut `json:"items"`
	}
	editor.do(t, http.MethodGet, "/api/v1/namespaces/data/versions", nil, http.StatusOK, &vlist)
	if len(vlist.Items) != 3 {
		t.Fatalf("versions: %+v", vlist.Items)
	}

	var diff versionDiff
	editor.do(t, http.MethodGet, "/api/v1/namespaces/data/diff?from=1&to=3", nil, http.StatusOK, &diff)
	if len(diff.Files) != 2 || diff.Files[0].Path != "a.txt" || diff.Files[0].Status != "modified" ||
		diff.Files[1].Path != "dir/b.txt" || diff.Files[1].Status != "added" {
		t.Fatalf("diff: %+v", diff.Files)
	}

	var s4 snapshotOut
	editor.do(t, http.MethodPost, "/api/v1/namespaces/data/revert", map[string]any{"version": 1}, http.StatusCreated, &s4)
	if s4.Version == nil || *s4.Version != 4 {
		t.Fatalf("revert: %+v", s4)
	}
	ok := editor.raw(t, http.MethodGet, "/api/v1/namespaces/data/file?path=a.txt", nil, nil)
	if ok.Status != http.StatusOK || string(ok.Body) != "one\n" {
		t.Fatalf("after revert: %d %q", ok.Status, ok.Body)
	}
	missing := editor.raw(t, http.MethodGet, "/api/v1/namespaces/data/file?path=dir/b.txt", nil, nil)
	if missing.Status != http.StatusNotFound {
		t.Fatalf("file of v3 still in head: %d", missing.Status)
	}
}
