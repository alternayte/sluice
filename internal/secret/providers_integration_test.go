//go:build integration

package secret_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	vault "github.com/hashicorp/vault/api"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/secret"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
	"github.com/alternayte/sluice/internal/testutil/storetest"
)

// newService returns a secret service on a fresh database with one master key.
func newService(t *testing.T, env map[string]string, v secret.VaultAuth, az secret.AzureFactory) *secret.Service {
	t.Helper()
	pool, _ := pgtest.Shared(t).NewPool(t)
	keys, err := secret.NewKeyring([]secret.Key{{ID: "k1", Key: bytes.Repeat([]byte{7}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	s := &secret.Service{Pool: pool, Clock: clock.Real{}, Audit: &audit.Writer{Pool: pool, Clock: clock.Real{}}, Keys: keys,
		Lookup: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, Vault: v, Azure: az}
	if err := s.EnsureDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func put(t *testing.T, s *secret.Service, key string, in secret.PutInput) {
	t.Helper()
	if _, err := s.Put(context.Background(), "", key, in); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func wantValue(t *testing.T, s *secret.Service, key, want string) {
	t.Helper()
	got, err := s.Resolve(context.Background(), "any.namespace", key)
	if err != nil || got != want {
		t.Fatalf("resolve %s: %q %v, want %q", key, got, err, want)
	}
}

func wantProviderStatus(t *testing.T, s *secret.Service, provider, ref, want string) {
	t.Helper()
	st, msg, err := s.CheckProvider(context.Background(), provider, ref)
	if err != nil || st != want {
		t.Fatalf("check %s %s: %s %q %v, want %s", provider, ref, st, msg, err, want)
	}
}

func str(s string) *string { return &s }

// TestSCN_SEC_001_ProviderConformance resolves values, missing references and denied
// access where the provider supports it, for builtin, env, azure_key_vault (lowkey-vault)
// and vault (dev server, token auth) (REQ-SEC-001).
func TestSCN_SEC_001_ProviderConformance(t *testing.T) {
	t.Run("builtin", func(t *testing.T) {
		s := newService(t, nil, secret.VaultAuth{}, nil)
		put(t, s, "BUILTIN_KEY", secret.PutInput{Value: str("builtin-value")})
		wantValue(t, s, "BUILTIN_KEY", "builtin-value")
		if st, _, err := s.Check(context.Background(), "", "BUILTIN_KEY"); err != nil || st != "ok" {
			t.Fatalf("check: %s %v", st, err)
		}
		var nf *secret.NotFoundError
		if _, err := s.Resolve(context.Background(), "any.namespace", "MISSING"); !errors.As(err, &nf) {
			t.Fatalf("missing key: %v", err)
		}
	})

	t.Run("env", func(t *testing.T) {
		s := newService(t, map[string]string{"SLUICE_SECRET_ENV_KEY": "env-value"}, secret.VaultAuth{}, nil)
		put(t, s, "ENV_KEY", secret.PutInput{Provider: "env"})
		wantValue(t, s, "ENV_KEY", "env-value")
		wantProviderStatus(t, s, "env", "ENV_KEY", "ok")
		wantProviderStatus(t, s, "env", "NOT_SET", "not_found")
	})

	t.Run("vault", func(t *testing.T) {
		addr := startVault(t)
		root, err := vault.NewClient(&vault.Config{Address: addr})
		if err != nil {
			t.Fatal(err)
		}
		root.SetToken("root")
		ctx := context.Background()
		if _, err := root.KVv2("secret").Put(ctx, "app/db", map[string]any{"password": "vault-value"}); err != nil {
			t.Fatal(err)
		}
		if err := root.Sys().PutPolicy("other-only", `path "secret/data/other/*" { capabilities = ["read"] }`); err != nil {
			t.Fatal(err)
		}
		limited, err := root.Auth().Token().Create(&vault.TokenCreateRequest{Policies: []string{"other-only"}})
		if err != nil {
			t.Fatal(err)
		}

		s := newService(t, nil, secret.VaultAuth{Addr: addr, Token: "root"}, nil)
		if _, err := s.CreateProvider(ctx, "hv", secret.TypeVault, nil); err != nil {
			t.Fatal(err)
		}
		put(t, s, "VAULT_KEY", secret.PutInput{Provider: "hv", Ref: "app/db#password"})
		wantValue(t, s, "VAULT_KEY", "vault-value")
		wantProviderStatus(t, s, "hv", "app/db#password", "ok")
		wantProviderStatus(t, s, "hv", "app/missing#password", "not_found")
		wantProviderStatus(t, s, "hv", "app/db#nofield", "not_found")

		denied := newService(t, nil, secret.VaultAuth{Addr: addr, Token: limited.Auth.ClientToken}, nil)
		if _, err := denied.CreateProvider(ctx, "hv", secret.TypeVault, nil); err != nil {
			t.Fatal(err)
		}
		wantProviderStatus(t, denied, "hv", "app/db#password", "access_denied")
	})

	t.Run("azure_key_vault", func(t *testing.T) {
		vaultURL := startLowkeyVault(t)
		az := func() (azcore.TokenCredential, *azsecrets.ClientOptions, error) {
			return storetest.StaticTokenCredential{}, lowkeyOptions(), nil
		}
		ctx := context.Background()
		client, err := azsecrets.NewClient(vaultURL, storetest.StaticTokenCredential{}, lowkeyOptions())
		if err != nil {
			t.Fatal(err)
		}
		var set azsecrets.SetSecretResponse
		for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(time.Second) {
			if set, err = client.SetSecret(ctx, "app-db", azsecrets.SetSecretParameters{Value: str("akv-value")}, nil); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("set secret in lowkey-vault: %v", err)
			}
		}

		s := newService(t, nil, secret.VaultAuth{}, az)
		if _, err := s.CreateProvider(ctx, "akv", secret.TypeAzureKeyVault, map[string]any{"vault_url": vaultURL}); err != nil {
			t.Fatal(err)
		}
		put(t, s, "AKV_KEY", secret.PutInput{Provider: "akv", Ref: "app-db"})
		wantValue(t, s, "AKV_KEY", "akv-value")
		wantProviderStatus(t, s, "akv", "app-db/"+set.ID.Version(), "ok")
		wantProviderStatus(t, s, "akv", "no-such-secret", "not_found")
	})
}

// startVault starts a Vault dev server with root token "root" and returns its address.
func startVault(t *testing.T) string {
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

// startLowkeyVault starts lowkey-vault and returns the URL of its default vault.
func startLowkeyVault(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "nagyesta/lowkey-vault:7.3.74",
			ExposedPorts: []string{"8443/tcp"},
			Env:          map[string]string{"LOWKEY_ARGS": "--LOWKEY_VAULT_RELAXED_PORTS=true"},
			WaitingFor:   wait.ForListeningPort("8443/tcp").WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start lowkey-vault: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	port, _ := c.MappedPort(ctx, "8443/tcp")
	return fmt.Sprintf("https://localhost:%s", port.Port())
}

// lowkeyOptions trusts the self-signed certificate of lowkey-vault and skips the
// challenge resource check, which needs a real Key Vault host name. Test use only.
func lowkeyOptions() *azsecrets.ClientOptions {
	transport := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // test substitute
	return &azsecrets.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
		DisableChallengeResourceVerification: true}
}
