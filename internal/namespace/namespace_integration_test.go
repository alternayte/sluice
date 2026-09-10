//go:build integration

package namespace_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/logging"
	"github.com/alternayte/sluice/internal/storage"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

func newService(t *testing.T) (*namespace.Service, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool, _ := pgtest.Shared(t).NewPool(t)
	clk := clock.Real{}
	svc := &namespace.Service{Pool: pool, Store: &storage.Postgres{Pool: pool}, Clock: clk, Audit: &audit.Writer{Pool: pool, Clock: clk},
		Log: logging.New(io.Discard, "error", "text"), MaxFileBytes: 1 << 20, MaxBundleBytes: 4 << 20}
	ctx := audit.WithActor(context.Background(), audit.Actor{Type: audit.ActorSystem})
	uid := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, role) VALUES ($1, 'editor@example.com', 'x', 'editor')`, uid); err != nil {
		t.Fatal(err)
	}
	ctx = auth.WithPrincipal(ctx, &auth.Principal{UserID: uid, Email: "editor@example.com", Role: auth.Editor})
	return svc, pool, ctx
}

func put(path, content string) namespace.Change {
	return namespace.Change{Op: "put", Path: path, Content: []byte(content)}
}

func TestSCN_STO_004_DeduplicatedFileObjects(t *testing.T) {
	svc, pool, ctx := newService(t)
	for _, ns := range []string{"team.a", "team.b"} {
		if _, err := svc.Create(ctx, ns, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Save(ctx, ns, []namespace.Change{put("lib/shared.py", "print('same content')\n")}, "add shared", nil); err != nil {
			t.Fatal(err)
		}
	}
	var rows, objects int
	hash := namespace.ContentHash([]byte("print('same content')\n"))
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM file_objects WHERE hash = $1", hash).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM storage_objects WHERE key = $1", storage.FileKey(hash)).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	var refs int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM snapshot_files WHERE hash = $1", hash).Scan(&refs)
	if rows != 1 || objects != 1 || refs != 2 {
		t.Fatalf("file_objects %d, storage objects %d, references %d", rows, objects, refs)
	}
}

func TestSaveVersionsDiffRevert(t *testing.T) {
	svc, _, ctx := newService(t)
	if _, err := svc.Create(ctx, "data", ""); err != nil {
		t.Fatal(err)
	}
	s1, err := svc.Save(ctx, "data", []namespace.Change{put("a.txt", "one\n")}, "v1", nil)
	if err != nil || s1.Version == nil || *s1.Version != 1 || s1.Author != "editor@example.com" {
		t.Fatalf("v1: %+v %v", s1, err)
	}
	if _, err := svc.Save(ctx, "data", []namespace.Change{put("a.txt", "two\n"), put("b.txt", "b\n")}, "v2", nil); err != nil {
		t.Fatal(err)
	}
	base := 1
	if _, err := svc.Save(ctx, "data", []namespace.Change{put("c.txt", "c")}, "stale", &base); !errors.Is(err, namespace.ErrVersionConflict) {
		t.Fatalf("stale base version: %v", err)
	}
	if _, err := svc.Save(ctx, "data", []namespace.Change{{Op: "rename", Path: "b.txt", NewPath: "dir/b.txt"}}, "v3", nil); err != nil {
		t.Fatal(err)
	}
	diffs, err := svc.Diff(ctx, "data", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 2 || diffs[0].Path != "a.txt" || diffs[0].Status != "modified" || diffs[1].Path != "dir/b.txt" || diffs[1].Status != "added" {
		t.Fatalf("diff: %+v", diffs)
	}
	s4, err := svc.Revert(ctx, "data", 1, "")
	if err != nil || *s4.Version != 4 {
		t.Fatalf("revert: %+v %v", s4, err)
	}
	r, _, err := svc.ReadFile(ctx, "data", "a.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if string(b) != "one\n" {
		t.Fatalf("after revert: %q", b)
	}
	if _, _, err := svc.ReadFile(ctx, "data", "dir/b.txt", nil); !errors.Is(err, namespace.ErrFileNotFound) {
		t.Fatalf("file of v3 still in head: %v", err)
	}
	for _, bad := range []string{"../x", "/etc/passwd", "a/../../b"} {
		_, err := svc.Save(ctx, "data", []namespace.Change{put(bad, "x")}, "bad", nil)
		var he *httpx.Error
		if !errors.As(err, &he) || he.Status != 422 {
			t.Errorf("path %q: %v", bad, err)
		}
	}
	_, err = svc.Save(ctx, "data", []namespace.Change{put("big.bin", string(make([]byte, 2<<20)))}, "big", nil)
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 413 {
		t.Fatalf("file above limit: %v", err)
	}
}

func tarGz(t *testing.T, entries []tar.Header, content string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(content))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(content))
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return bytes.NewReader(buf.Bytes())
}

func TestBundleExtractionRejectsUnsafeEntries(t *testing.T) {
	bad := [][]tar.Header{
		{{Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0o644}},
		{{Name: "/abs.txt", Typeflag: tar.TypeReg, Mode: 0o644}},
		{{Name: "a/../../b.txt", Typeflag: tar.TypeReg, Mode: 0o644}},
		{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		{{Name: "hard", Typeflag: tar.TypeLink, Linkname: "x"}},
	}
	for _, entries := range bad {
		dir := t.TempDir()
		err := namespace.ExtractBundle(tarGz(t, entries, "x"), dir, 0)
		if !errors.Is(err, namespace.ErrUnsafeEntry) {
			t.Errorf("%s: %v", entries[0].Name, err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
			t.Fatal("file written outside the root")
		}
	}
	dir := t.TempDir()
	if err := namespace.ExtractBundle(tarGz(t, []tar.Header{{Name: "ok/run.sh", Typeflag: tar.TypeReg, Mode: 0o755}}, "echo ok"), dir, 0); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "ok", "run.sh"))
	if err != nil || st.Mode()&0o100 == 0 {
		t.Fatalf("extracted file: %v %v", st, err)
	}
}

func TestBundleRoundTrip(t *testing.T) {
	svc, _, ctx := newService(t)
	if _, err := svc.Create(ctx, "bundle", ""); err != nil {
		t.Fatal(err)
	}
	exec := true
	if _, err := svc.Save(ctx, "bundle", []namespace.Change{put("x.flow.yaml", "id: x\ntasks:\n  - {id: a, type: command, command: [\"true\"]}\n"),
		{Op: "put", Path: "bin/run.sh", Content: []byte("#!/bin/sh\necho hi\n"), Executable: &exec}}, "files", nil); err != nil {
		t.Fatal(err)
	}
	ns, err := svc.Get(ctx, "bundle")
	if err != nil {
		t.Fatal(err)
	}
	_, m, err := svc.Resolve(ctx, ns, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := svc.EnsureBundle(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if key2, err := svc.EnsureBundle(ctx, m); err != nil || key2 != key {
		t.Fatalf("second bundle: %s %v", key2, err)
	}
	r, err := svc.Store.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := namespace.ExtractBundle(r, dir, 0); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	b, err := os.ReadFile(filepath.Join(dir, "bin", "run.sh"))
	if err != nil || string(b) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("extracted: %q %v", b, err)
	}
}
