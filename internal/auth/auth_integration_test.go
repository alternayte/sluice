//go:build integration

package auth_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/logging"
	"github.com/alternayte/sluice/internal/platform/token"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

// TestSCN_AUTH_012_SecretsStoredAsHashes inspects the database: password hashes are
// argon2id with the stated parameters, and session IDs and tokens exist only as hashes (SI-02).
func TestSCN_AUTH_012_SecretsStoredAsHashes(t *testing.T) {
	pool, _ := pgtest.Shared(t).NewPool(t)
	ctx := audit.WithActor(context.Background(), audit.Actor{Type: audit.ActorSystem})
	clk := clock.Real{}
	svc := &auth.Service{Pool: pool, Clock: clk, Audit: &audit.Writer{Pool: pool, Clock: clk},
		Log: logging.New(io.Discard, "error", "text"), SessionTTL: time.Hour}
	u, err := svc.CreateUser(ctx, "db@example.com", "", kernel.Editor, "db-password-123", false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Login(ctx, "db@example.com", "db-password-123", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := svc.CreateToken(ctx, res.Principal, "t", kernel.Viewer, nil)
	if err != nil {
		t.Fatal(err)
	}

	var hash string
	if err := pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", u.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("password hash parameters: %s", hash)
	}
	var sidHash []byte
	if err := pool.QueryRow(ctx, "SELECT id_hash FROM sessions WHERE user_id = $1", u.ID).Scan(&sidHash); err != nil {
		t.Fatal(err)
	}
	if !auth.EqualHash(sidHash, token.HashSecret(res.SessionID)) || len(sidHash) != 32 {
		t.Fatal("session is not stored as its SHA-256")
	}
	var tokHash []byte
	if err := pool.QueryRow(ctx, "SELECT token_hash FROM api_tokens WHERE user_id = $1", u.ID).Scan(&tokHash); err != nil {
		t.Fatal(err)
	}
	if !auth.EqualHash(tokHash, token.HashSecret(secret)) {
		t.Fatal("token is not stored as its SHA-256")
	}
	// No table row contains the plaintext session ID, token or password.
	var n int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT row_to_json(x)::text AS j FROM users x
		UNION ALL SELECT row_to_json(x)::text FROM sessions x
		UNION ALL SELECT row_to_json(x)::text FROM api_tokens x
		UNION ALL SELECT row_to_json(x)::text FROM audit_events x
		UNION ALL SELECT row_to_json(x)::text FROM login_attempts x) r
		WHERE strpos(j, $1) > 0 OR strpos(j, $2) > 0 OR strpos(j, $3) > 0`,
		res.SessionID, secret, "db-password-123").Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d rows contain a plaintext secret", n)
	}
}

func TestLoginRateLimitIsSharedAcrossInstances(t *testing.T) {
	pool, _ := pgtest.Shared(t).NewPool(t)
	ctx := audit.WithActor(context.Background(), audit.Actor{Type: audit.ActorSystem})
	clk := clock.Real{}
	mk := func() *auth.Service {
		return &auth.Service{Pool: pool, Clock: clk, Audit: &audit.Writer{Pool: pool, Clock: clk},
			Log: logging.New(io.Discard, "error", "text"), SessionTTL: time.Hour}
	}
	a, b := mk(), mk()
	for i := 0; i < 50; i++ {
		svc := a
		if i%2 == 1 {
			svc = b
		}
		_, _ = svc.Login(ctx, "user"+string(rune('a'+i%26))+"@example.com", "x", "10.0.0.9", "t")
	}
	_, err := b.Login(ctx, "fresh@example.com", "x", "10.0.0.9", "t")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("51st failure from one IP across instances: %v", err)
	}
}
