//go:build integration

package storage_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/storage"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
	"github.com/alternayte/sluice/internal/testutil/storetest"
)

func roundTrip(t *testing.T, s storage.Store) {
	t.Helper()
	if err := storage.RoundTrip(context.Background(), s, "health/test"); err != nil {
		t.Fatal(err)
	}
}

func TestSCN_STO_003_AzblobConnectionStringAndTokenCredential(t *testing.T) {
	ctx := context.Background()
	plain := storetest.Azurite(t)
	s, err := storage.OpenAzblob(ctx, storage.AzblobConfig{ConnectionString: plain.ConnectionString, Container: "conn", Prefix: "p1", ClientOptions: plain.ClientOptions()})
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, s)
	_ = s.Close()

	oauth := storetest.AzuriteOAuth(t)
	calls := 0
	factory := func() (azcore.TokenCredential, error) {
		calls++
		return storetest.StaticTokenCredential{}, nil
	}
	opts := oauth.ClientOptions()
	s, err = storage.OpenAzblob(ctx, storage.AzblobConfig{AccountURL: oauth.AccountURL, Container: "oauth", ClientOptions: opts, CredentialFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	roundTrip(t, s)
	if calls != 1 {
		t.Fatalf("credential factory called %d times", calls)
	}
	// A request without a valid token is rejected, so the token path was used.
	noAuth := func() (azcore.TokenCredential, error) { return failingCredential{}, nil }
	bad, err := storage.OpenAzblob(ctx, storage.AzblobConfig{AccountURL: oauth.AccountURL, Container: "oauth", ClientOptions: opts, CredentialFactory: noAuth})
	if err == nil {
		defer func() { _ = bad.Close() }()
		if _, err := bad.Put(ctx, "health/x", strings.NewReader("x"), ""); err == nil {
			t.Fatal("put without a token succeeded")
		}
	}
}

type failingCredential struct{}

func (failingCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, errors.New("no token")
}

func TestSCN_STO_005_GarbageCollection(t *testing.T) {
	pool, _ := pgtest.Shared(t).NewPool(t)
	ctx := context.Background()
	store := &storage.Postgres{Pool: pool}
	fc := clock.NewFake(time.Now().Add(48 * time.Hour))
	put := func(key string) {
		if _, err := store.Put(ctx, key, strings.NewReader(key), ""); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(key string) bool {
		_, err := store.Stat(ctx, key)
		return err == nil
	}
	exec := func(q string, args ...any) {
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	nsID, snapID, keepExec := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO namespaces (id, name, source_type) VALUES ($1, 'gc', 'managed')`, nsID)
	for _, h := range []string{"referenced", "unreferenced"} {
		put(storage.FileKey(h))
		exec(`INSERT INTO file_objects (hash, size, created_at) VALUES ($1, 1, now() - interval '2 hours')`, h)
	}
	put(storage.FileKey("orphan-without-row"))
	exec(`INSERT INTO snapshots (id, namespace_id, version, manifest_hash) VALUES ($1, $2, 1, 'm')`, snapID, nsID)
	exec(`INSERT INTO snapshot_files (snapshot_id, path, hash, size) VALUES ($1, 'a.py', 'referenced', 1)`, snapID)
	put(storage.BundleKey("old"))
	put(storage.BundleKey("recent"))
	exec(`INSERT INTO bundles (manifest_hash, storage_key, size, last_used_at) VALUES ('old', $1, 1, $2), ('recent', $3, 1, $4)`,
		storage.BundleKey("old"), fc.Now().Add(-8*24*time.Hour), storage.BundleKey("recent"), fc.Now().Add(-24*time.Hour))
	exec(`INSERT INTO executions (id, namespace_id, snapshot_id, state, trigger_type, created_at) VALUES ($1, $2, $3, 'SUCCESS', 'manual', now())`, keepExec, nsID, snapID)
	purged := uuid.New()
	for _, k := range []string{
		storage.LogKey(keepExec.String(), uuid.NewString()), storage.ArtifactKey(keepExec.String(), uuid.NewString(), "a.txt"),
		storage.LogKey(purged.String(), uuid.NewString()), storage.ArtifactKey(purged.String(), uuid.NewString(), "b.txt"),
	} {
		put(k)
	}

	gc := &storage.GC{Pool: pool, Store: store, Clock: fc}
	res, err := gc.RunIfDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Bundles != 1 || res.FileObjects != 1 || res.Orphans != 1 || res.Logs != 1 || res.Artifacts != 1 {
		t.Fatalf("gc result %+v", res)
	}
	for k, want := range map[string]bool{
		storage.FileKey("referenced"): true, storage.FileKey("unreferenced"): false, storage.FileKey("orphan-without-row"): false,
		storage.BundleKey("old"): false, storage.BundleKey("recent"): true,
	} {
		if exists(k) != want {
			t.Errorf("%s exists=%v, want %v", k, !want, want)
		}
	}
	var logs, arts []string
	_ = store.List(ctx, "logs/", func(i storage.Info) error { logs = append(logs, i.Key); return nil })
	_ = store.List(ctx, "artifacts/", func(i storage.Info) error { arts = append(arts, i.Key); return nil })
	if len(logs) != 1 || !strings.Contains(logs[0], keepExec.String()) || len(arts) != 1 || !strings.Contains(arts[0], keepExec.String()) {
		t.Fatalf("logs %v artifacts %v", logs, arts)
	}
	var rows int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM file_objects").Scan(&rows)
	if rows != 1 {
		t.Fatalf("file_objects rows %d", rows)
	}
	// A second run within 24 h does nothing.
	if res, err := gc.RunIfDue(ctx); err != nil || res != nil {
		t.Fatalf("second run: %+v %v", res, err)
	}
	fc.Advance(25 * time.Hour)
	if res, err := gc.RunIfDue(ctx); err != nil || res == nil {
		t.Fatalf("run after 25 h: %+v %v", res, err)
	}
	_ = io.EOF
}
