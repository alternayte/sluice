//go:build integration

package secret

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startDevVault starts a Vault dev server with root token "root" and returns its address.
func startDevVault(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "hashicorp/vault:2.1.0",
			ExposedPorts: []string{"8200/tcp"},
			Env:          map[string]string{"VAULT_DEV_ROOT_TOKEN_ID": "root", "VAULT_DEV_LISTEN_ADDRESS": "0.0.0.0:8200", "SKIP_SETCAP": "true"},
			WaitingFor:   wait.ForHTTP("/v1/sys/health").WithPort("8200/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start vault: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "8200/tcp")
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

// TestVaultLoginRenewal checks that a provider with login auth logs in again after its token
// is revoked and before its lease ends, and that static-token auth does not (REQ-SEC-001).
// The login function replaces the Kubernetes auth method, which needs a Kubernetes API.
func TestVaultLoginRenewal(t *testing.T) {
	addr := startDevVault(t)
	ctx := context.Background()
	root, err := vault.NewClient(&vault.Config{Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	root.SetToken("root")
	if _, err := root.KVv2("secret").Put(ctx, "app/db", map[string]any{"password": "vault-value"}); err != nil {
		t.Fatal(err)
	}
	if err := root.Sys().PutPolicy("app-read", `path "secret/data/app/*" { capabilities = ["read"] }`); err != nil {
		t.Fatal(err)
	}
	var logins atomic.Int32
	var last atomic.Value
	login := func(ctx context.Context, _ *vault.Client) (*vault.Secret, error) {
		logins.Add(1)
		s, err := root.Auth().Token().CreateWithContext(ctx, &vault.TokenCreateRequest{Policies: []string{"app-read"}, TTL: "1h", NoParent: true})
		if err == nil {
			last.Store(s.Auth.ClientToken)
		}
		return s, err
	}
	resolve := func(p *vaultProvider) {
		t.Helper()
		if v, err := p.Resolve(ctx, "app/db#password"); err != nil || v != "vault-value" {
			t.Fatalf("resolve: %q %v", v, err)
		}
	}

	t.Run("revoked token", func(t *testing.T) {
		logins.Store(0)
		p, err := newVaultProviderLogin(ctx, ProviderConfig{}, addr, login)
		if err != nil {
			t.Fatal(err)
		}
		resolve(p)
		if err := root.Auth().Token().RevokeOrphanWithContext(ctx, last.Load().(string)); err != nil {
			t.Fatal(err)
		}
		resolve(p)
		if n := logins.Load(); n != 2 {
			t.Fatalf("%d logins, want 2", n)
		}
	})

	t.Run("lease end", func(t *testing.T) {
		logins.Store(0)
		now := time.Now()
		p, err := newVaultProviderLogin(ctx, ProviderConfig{}, addr, login)
		if err != nil {
			t.Fatal(err)
		}
		p.now = func() time.Time { return now }
		resolve(p)
		now = now.Add(46 * time.Minute) // after 3/4 of the 1h lease
		resolve(p)
		if n := logins.Load(); n != 2 {
			t.Fatalf("%d logins, want 2", n)
		}
	})

	t.Run("failed login", func(t *testing.T) {
		p, err := newVaultProviderLogin(ctx, ProviderConfig{}, addr, login)
		if err != nil {
			t.Fatal(err)
		}
		p.login = func(context.Context, *vault.Client) (*vault.Secret, error) { return nil, errors.New("no jwt") }
		if err := root.Auth().Token().RevokeOrphanWithContext(ctx, last.Load().(string)); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Resolve(ctx, "app/db#password"); err == nil {
			t.Fatal("resolve with a revoked token and a failed login succeeded")
		}
	})

	t.Run("static token", func(t *testing.T) {
		s, err := root.Auth().Token().CreateWithContext(ctx, &vault.TokenCreateRequest{Policies: []string{"app-read"}, NoParent: true})
		if err != nil {
			t.Fatal(err)
		}
		p, err := newVaultProvider(ctx, ProviderConfig{}, VaultAuth{Addr: addr, Token: s.Auth.ClientToken})
		if err != nil {
			t.Fatal(err)
		}
		resolve(p)
		if err := root.Auth().Token().RevokeOrphanWithContext(ctx, s.Auth.ClientToken); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Resolve(ctx, "app/db#password"); !errors.Is(err, ErrAccessDenied) {
			t.Fatalf("static token after revoke: %v, want access denied", err)
		}
	})
}
