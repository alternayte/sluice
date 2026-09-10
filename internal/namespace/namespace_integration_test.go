//go:build integration

package namespace_test

import (
	"context"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/clock"
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
	ctx = kernel.WithPrincipal(ctx, &kernel.Principal{UserID: uid, Email: "editor@example.com", Role: kernel.Editor})
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

