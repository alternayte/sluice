//go:build integration

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/gitsync"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/lease"
	"github.com/alternayte/sluice/internal/secret"
	"github.com/alternayte/sluice/internal/snapshot"
	"github.com/alternayte/sluice/internal/testutil/gitserver"
)

// gitTestSource creates a git source with a token credential from the env provider and
// makes s the git-sync leader.
func gitTestSource(t *testing.T, s *Server, name, repoURL string, maps ...gitsync.Mapping) gitsync.Source {
	t.Helper()
	ctx := context.Background()
	s.Secrets.Lookup = func(k string) (string, bool) {
		if k == "SLUICE_SECRET_GIT_TOKEN" {
			return gitserver.Token, true
		}
		return "", false
	}
	if _, err := s.Secrets.Put(ctx, "", "GIT_TOKEN", secret.PutInput{Provider: "env"}); err != nil {
		t.Fatal(err)
	}
	src, err := s.Git.Create(ctx, gitsync.SourceInput{Name: name, RepoURL: repoURL, Branch: "main", AuthType: "https_token",
		CredentialSecretKey: "GIT_TOKEN", Mappings: maps})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if ok, err := s.Leases.TryAcquire(ctx, lease.GitSync); err != nil || !ok {
		t.Fatalf("git-sync lease: %v %v", ok, err)
	}
	return src
}

// TestSCN_NS_005_UnsafePaths rejects traversal and absolute paths in the file API, syncs a
// git tree without its symlink and traversal entries with warnings, and rejects bundle
// entries outside the root (REQ-NS-005, SI-08).
func TestSCN_NS_005_UnsafePaths(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	if _, err := s.Namespaces.Create(ctx, "paths", ""); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../x", "/etc/passwd", "a/../../b"} {
		_, err := s.Namespaces.Save(ctx, "paths", []namespace.Change{{Op: "put", Path: p, Content: []byte("x")}}, "unsafe", nil)
		var he *httpx.Error
		if !errors.As(err, &he) || he.Code != "validation_failed" {
			t.Fatalf("file API accepted %q: %v", p, err)
		}
	}

	g := gitserver.Start(t)
	repoURL, _ := g.Repo("unsafe")
	g.Commit("unsafe", "main", "unsafe entries", []gitserver.File{
		{Path: "ok.txt", Content: "ok"},
		{Path: "link", Content: "/etc/passwd", Mode: filemode.Symlink},
		{Path: "a/../../evil.txt", Content: "evil"},
	})
	src := gitTestSource(t, s, "unsafe", repoURL, gitsync.Mapping{Namespace: "unsafe"})
	s.Git.TempRoot = t.TempDir()
	if err := s.Git.SyncSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	runs, err := s.Git.Runs(ctx, src.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != "success" {
		t.Fatalf("runs %+v %v", runs, err)
	}
	warnings := string(runs[0].Warnings)
	if !strings.Contains(warnings, "skipped symlink link") || !strings.Contains(warnings, "skipped a/..") {
		t.Fatalf("warnings %s", warnings)
	}
	ns, err := s.Namespaces.Get(ctx, "unsafe")
	if err != nil {
		t.Fatal(err)
	}
	_, m, err := s.Namespaces.Resolve(ctx, ns, nil)
	if err != nil {
		t.Fatal(err)
	}
	if paths := m.Paths(); len(paths) != 1 || paths[0] != "ok.txt" {
		t.Fatalf("snapshot files %v, want only ok.txt", paths)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../evil.txt", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()
	_ = gz.Close()
	parent := t.TempDir()
	dst := filepath.Join(parent, "root")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.ExtractBundle(&buf, dst, 1<<20); err == nil {
		t.Fatal("bundle extraction accepted an entry outside the root")
	}
	if _, err := os.Stat(filepath.Join(parent, "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("bundle extraction wrote a file outside the root")
	}
}

// TestSCN_GIT_008_TempRootAndHistory syncs twice. After each sync the temporary root is
// empty, and the run history keeps a record per sync (REQ-GIT-002).
func TestSCN_GIT_008_TempRootAndHistory(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	g := gitserver.Start(t)
	repoURL, _ := g.Repo("hist")
	g.Commit("hist", "main", "first", []gitserver.File{{Path: "a.txt", Content: "one"}})
	src := gitTestSource(t, s, "hist", repoURL, gitsync.Mapping{Namespace: "hist"})
	s.Git.TempRoot = t.TempDir()
	empty := func() {
		t.Helper()
		entries, err := os.ReadDir(s.Git.TempRoot)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("the temporary root holds %d entries after the sync", len(entries))
		}
	}
	if err := s.Git.SyncSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	empty()
	second := g.Commit("hist", "main", "second", []gitserver.File{{Path: "a.txt", Content: "two"}})
	if err := s.Git.SyncSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	empty()
	runs, err := s.Git.Runs(ctx, src.ID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs %+v %v", runs, err)
	}
	for _, r := range runs {
		if r.Status != "success" || r.SnapshotsCreated != 1 || r.GitSourceID != src.ID || r.EndedAt == nil {
			t.Fatalf("run %+v", r)
		}
	}
	if runs[0].Sha != second {
		t.Fatalf("newest run sha %s, want %s", runs[0].Sha, second)
	}
	other := gitTestSourceOther(t, s, g)
	if rs, err := s.Git.Runs(ctx, other); err != nil || len(rs) != 0 {
		t.Fatalf("runs of another source: %+v %v", rs, err)
	}
}

// gitTestSourceOther creates a second source without syncing it, to check that runs are per source.
func gitTestSourceOther(t *testing.T, s *Server, g *gitserver.Server) uuid.UUID {
	t.Helper()
	repoURL, _ := g.Repo("other")
	src, err := s.Git.Create(context.Background(), gitsync.SourceInput{Name: "other", RepoURL: repoURL, Branch: "main", AuthType: "https_token",
		CredentialSecretKey: "GIT_TOKEN", Mappings: []gitsync.Mapping{{Namespace: "other"}}})
	if err != nil {
		t.Fatal(err)
	}
	return src.ID
}
