//go:build integration

package db_test

import (
	"context"
	"sync"
	"testing"

	"github.com/alternayte/sluice/internal/platform/db"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

func TestSCN_CORE_002_ConcurrentMigrations(t *testing.T) {
	srv := pgtest.Shared(t)
	url := srv.NewDatabase(t)
	ctx := context.Background()
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	const n = 3
	var wg sync.WaitGroup
	applied := make([][]string, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pool, err := db.Open(ctx, url)
			if err != nil {
				errs[i] = err
				return
			}
			defer pool.Close()
			<-start
			applied[i], errs[i] = db.Migrate(ctx, pool)
			if errs[i] == nil {
				errs[i] = db.MigrationsCurrent(ctx, pool)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	total := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("instance %d: %v", i, errs[i])
		}
		total += len(applied[i])
	}
	if total != len(migs) {
		t.Fatalf("migrations applied %d times in total, want %d (each once)", total, len(migs))
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var rows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != len(migs) {
		t.Fatalf("schema_migrations has %d rows, want %d", rows, len(migs))
	}
}

func TestRejectsUnknownStateValue(t *testing.T) {
	pool, _ := pgtest.Shared(t).NewPool(t)
	_, err := pool.Exec(context.Background(), `INSERT INTO leases (name, holder, expires_at) VALUES ('a', 'b', now())`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO users (id, email, password_hash, role) VALUES (gen_random_uuid(), 'a@b.c', 'x', 'root')`)
	if err == nil {
		t.Fatal("unknown role was accepted")
	}
}
