//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/alternayte/sluice/internal/platform/masking"
)

// masterKey returns a SLUICE_MASTER_KEYS entry with a fixed test key.
func masterKey(id string, b byte) string {
	return id + ":" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// hashScript prints the SHA-256 of $S, so that a test can compare a secret value without
// a masked form in the logs.
const hashScript = "printf '%s' \"$S\" | { if command -v sha256sum >/dev/null; then sha256sum; else shasum -a 256; fi; } | cut -c1-64\n"

// secretFlow is a flow that prints the hash of secret key.
func secretFlow(id, key string) string {
	return "id: " + id + "\nenv:\n  S: \"${{ secret('" + key + "') }}\"\ntasks:\n  - {id: t, type: script, file: hash.sh}\n"
}

// runHash triggers a flow of secretFlow and returns the printed hash.
func runHash(t testing.TB, c *client, ns, flowID string) string {
	t.Helper()
	d := waitTerminal(t, c, triggerFlow(t, c, ns, flowID, nil, nil).ID, 60*time.Second)
	if d.State != "SUCCESS" {
		t.Fatalf("%s/%s: %s: %s", ns, flowID, d.State, d.Error)
	}
	for _, l := range allLogs(t, c, d.ID, "t") {
		if l.Stream == "stdout" && len(l.Text) == 64 {
			return l.Text
		}
	}
	t.Fatalf("no hash in the logs of %s", d.ID)
	return ""
}

func createNamespace(t testing.TB, c *client, name string) {
	t.Helper()
	r := c.raw(t, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": name}, nil)
	if r.Status != http.StatusCreated && r.Status != http.StatusConflict {
		t.Fatalf("create namespace %s: %d %s", name, r.Status, r.Body)
	}
}

// startVaultE2E starts a Vault dev server with root token "root", stores app/db
// password=vault-value and returns the address and a root client.
func startVaultE2E(t testing.TB) (string, *vault.Client) {
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
	addr := fmt.Sprintf("http://%s:%s", host, port.Port())
	vc, err := vault.NewClient(&vault.Config{Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	vc.SetToken("root")
	if _, err := vc.KVv2("secret").Put(ctx, "app/db", map[string]any{"password": "vault-value"}); err != nil {
		t.Fatal(err)
	}
	return addr, vc
}

// TestSCN_SEC_003_NearestScope defines PG_URL globally, in data and in data.elt. A flow in
// data.elt.x gets the data.elt value, and after its deletion the data value (REQ-SEC-003).
func TestSCN_SEC_003_NearestScope(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_MASTER_KEYS": masterKey("k1", 1)})
	c := adminClient(t, p)
	createNamespace(t, c, "data")
	createNamespace(t, c, "data.elt")
	saveFiles(t, c, "data.elt.x", map[string]string{"hash.sh": hashScript, "s.flow.yaml": secretFlow("s", "PG_URL")})
	c.do(t, http.MethodPut, "/api/v1/secrets/PG_URL", map[string]any{"value": "global-url"}, http.StatusOK, nil)
	c.do(t, http.MethodPut, "/api/v1/namespaces/data/secrets/PG_URL", map[string]any{"value": "data-url"}, http.StatusOK, nil)
	c.do(t, http.MethodPut, "/api/v1/namespaces/data.elt/secrets/PG_URL", map[string]any{"value": "elt-url"}, http.StatusOK, nil)
	if got := runHash(t, c, "data.elt.x", "s"); got != sha("elt-url") {
		t.Fatalf("the flow did not get the data.elt value")
	}
	var list struct {
		Items []struct {
			Key       string `json:"key"`
			Scope     string `json:"scope"`
			Inherited bool   `json:"inherited"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/namespaces/data.elt.x/secrets", nil, http.StatusOK, &list)
	if len(list.Items) != 1 || list.Items[0].Scope != "data.elt" || !list.Items[0].Inherited {
		t.Fatalf("effective secrets of data.elt.x: %+v", list.Items)
	}
	c.do(t, http.MethodDelete, "/api/v1/namespaces/data.elt/secrets/PG_URL", nil, http.StatusNoContent, nil)
	if got := runHash(t, c, "data.elt.x", "s"); got != sha("data-url") {
		t.Fatalf("after the deletion the flow did not get the data value")
	}
}

// TestSCN_SEC_004_VaultCheck checks a Vault reference without returning the value (REQ-SEC-004).
func TestSCN_SEC_004_VaultCheck(t *testing.T) {
	addr, _ := startVaultE2E(t)
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_VAULT_ADDR": addr, "SLUICE_VAULT_TOKEN": "root"})
	c := adminClient(t, p)
	c.do(t, http.MethodPost, "/api/v1/secret-providers", map[string]any{"name": "hv", "type": "vault"}, http.StatusCreated, nil)
	for key, ref := range map[string]string{"DB_PASS": "app/db#password", "NOPE": "app/none#password"} {
		b := c.do(t, http.MethodPut, "/api/v1/secrets/"+key, map[string]any{"provider": "hv", "ref": ref}, http.StatusOK, nil)
		if bytes.Contains(b.Body, []byte("vault-value")) {
			t.Fatalf("the write response contains the value: %s", b.Body)
		}
	}
	for key, want := range map[string]string{"DB_PASS": "ok", "NOPE": "not_found"} {
		var res struct {
			Status string `json:"status"`
		}
		r := c.do(t, http.MethodPost, "/api/v1/secrets/"+key+"/check", nil, http.StatusOK, &res)
		if res.Status != want || bytes.Contains(r.Body, []byte("vault-value")) {
			t.Fatalf("check %s: %s, want %s without the value", key, r.Body, want)
		}
	}
}

// TestSCN_SEC_005_CacheTTL changes a Vault value. With a cache lifetime of 1 s the next
// execution after 2 s gets the new value without a restart (REQ-SEC-005).
func TestSCN_SEC_005_CacheTTL(t *testing.T) {
	addr, vc := startVaultE2E(t)
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_VAULT_ADDR": addr, "SLUICE_VAULT_TOKEN": "root",
		"SLUICE_SECRET_CACHE_TTL": "1s"})
	c := adminClient(t, p)
	c.do(t, http.MethodPost, "/api/v1/secret-providers", map[string]any{"name": "hv", "type": "vault"}, http.StatusCreated, nil)
	c.do(t, http.MethodPut, "/api/v1/secrets/DB_PASS", map[string]any{"provider": "hv", "ref": "app/db#password"}, http.StatusOK, nil)
	saveFiles(t, c, "cache", map[string]string{"hash.sh": hashScript, "s.flow.yaml": secretFlow("s", "DB_PASS")})
	if got := runHash(t, c, "cache", "s"); got != sha("vault-value") {
		t.Fatal("first execution did not get the Vault value")
	}
	if _, err := vc.KVv2("secret").Put(context.Background(), "app/db", map[string]any{"password": "vault-value-2"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if got := runHash(t, c, "cache", "s"); got != sha("vault-value-2") {
		t.Fatal("the execution after the cache lifetime did not get the new value")
	}
}

// TestSCN_SEC_006_Rekey adds a new key, runs rekey, removes the old key and restarts:
// secrets resolve. Removing the old key before rekey makes /readyz fail with
// master_key_missing (REQ-SEC-006).
func TestSCN_SEC_006_Rekey(t *testing.T) {
	db := newDatabase(t)
	k1, k2 := masterKey("k1", 1), masterKey("k2", 2)
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": db, "SLUICE_MASTER_KEYS": k1})
	c := adminClient(t, p)
	saveFiles(t, c, "rekey", map[string]string{"hash.sh": hashScript, "s.flow.yaml": secretFlow("s", "TOKEN")})
	c.do(t, http.MethodPut, "/api/v1/secrets/TOKEN", map[string]any{"value": "rekey-value"}, http.StatusOK, nil)
	if err := p.Stop(syscall.SIGTERM); err != nil {
		t.Logf("stop: %v", err)
	}
	out, stderr, code := runCLI(t, map[string]string{"SLUICE_DATABASE_URL": db, "SLUICE_MASTER_KEYS": k2 + "," + k1}, "secrets", "rekey")
	if code != 0 || !strings.Contains(out, "re-encrypted 1 secrets with key k2") {
		t.Fatalf("rekey: exit %d %q %q", code, out, stderr)
	}
	p = startServer(t, map[string]string{"SLUICE_DATABASE_URL": db, "SLUICE_MASTER_KEYS": k2})
	c = tokenClient(p.URL, c.token)
	if got := runHash(t, c, "rekey", "s"); got != sha("rekey-value") {
		t.Fatal("after rekey and the removal of the old key the secret does not resolve")
	}

	db2 := newDatabase(t)
	p = startServer(t, map[string]string{"SLUICE_DATABASE_URL": db2, "SLUICE_MASTER_KEYS": k1})
	c = adminClient(t, p)
	c.do(t, http.MethodPut, "/api/v1/secrets/TOKEN", map[string]any{"value": "rekey-value"}, http.StatusOK, nil)
	if err := p.Stop(syscall.SIGTERM); err != nil {
		t.Logf("stop: %v", err)
	}
	p = startServerNoReady(t, map[string]string{"SLUICE_DATABASE_URL": db2, "SLUICE_MASTER_KEYS": k2})
	resp, err := http.Get(p.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "master_key_missing") {
		t.Fatalf("/readyz without the old key: %d %s", resp.StatusCode, body)
	}
}

// TestSCN_SEC_008_SecretKeysUsed lists the used secret keys on the execution without values (REQ-SEC-008).
func TestSCN_SEC_008_SecretKeysUsed(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_MASTER_KEYS": masterKey("k1", 1)})
	c := adminClient(t, p)
	saveFiles(t, c, "used", map[string]string{"hash.sh": hashScript, "s.flow.yaml": secretFlow("s", "API_KEY")})
	c.do(t, http.MethodPut, "/api/v1/secrets/API_KEY", map[string]any{"value": "used-value"}, http.StatusOK, nil)
	d := waitTerminal(t, c, triggerFlow(t, c, "used", "s", nil, nil).ID, 60*time.Second)
	if d.State != "SUCCESS" || len(d.SecretKeysUsed) != 1 || d.SecretKeysUsed[0] != "API_KEY" {
		t.Fatalf("execution %s secret_keys_used %v", d.State, d.SecretKeysUsed)
	}
	r := c.do(t, http.MethodGet, "/api/v1/executions/"+d.ID, nil, http.StatusOK, nil)
	if bytes.Contains(r.Body, []byte("used-value")) {
		t.Fatal("the execution API returns the secret value")
	}
}

// TestSCN_SEC_009_SecretNotFound fails the task with secret_not_found, the key and the scopes searched (REQ-SEC-009).
func TestSCN_SEC_009_SecretNotFound(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_MASTER_KEYS": masterKey("k1", 1)})
	c := adminClient(t, p)
	saveFiles(t, c, "miss.deep", map[string]string{"hash.sh": hashScript, "s.flow.yaml": secretFlow("s", "NOT_THERE")})
	d := waitTerminal(t, c, triggerFlow(t, c, "miss.deep", "s", nil, nil).ID, 60*time.Second)
	tr := d.last("t")
	if d.State != "FAILED" || tr.Reason != "secret_not_found" || !strings.Contains(tr.Error, "NOT_THERE") ||
		!strings.Contains(tr.Error, "miss.deep, miss, global") {
		t.Fatalf("execution %s task %s %q", d.State, tr.Reason, tr.Error)
	}
}

// TestSCN_SEC_011_NoMasterKeys refuses builtin writes with 409 builtin_provider_disabled.
// An env secret resolves (REQ-SEC-010, SI-09).
func TestSCN_SEC_011_NoMasterKeys(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_SECRET_TOKEN_X": "env-token-value"})
	c := adminClient(t, p)
	r := c.raw(t, http.MethodPut, "/api/v1/secrets/BUILTIN", map[string]any{"value": "builtin-value"}, nil)
	if r.Status != http.StatusConflict || errCode(r.Body) != "builtin_provider_disabled" {
		t.Fatalf("builtin write without master keys: %d %s", r.Status, r.Body)
	}
	c.do(t, http.MethodPut, "/api/v1/secrets/TOKEN_X", map[string]any{"provider": "env"}, http.StatusOK, nil)
	saveFiles(t, c, "envp", map[string]string{"hash.sh": hashScript, "s.flow.yaml": secretFlow("s", "TOKEN_X")})
	if got := runHash(t, c, "envp", "s"); got != sha("env-token-value") {
		t.Fatal("the env secret did not resolve")
	}
}

// maskScript prints a secret in raw, base64 standard, base64 URL, URL-encoded and
// JSON-escaped form, writes it to an output and fails with it in its error text.
const maskScript = `v="$S"
urlenc() { local s="$1" out="" c h i; for ((i=0; i<${#s}; i++)); do c="${s:i:1}"; case "$c" in [a-zA-Z0-9._~-]) out+="$c";; *) printf -v h '%%%02X' "'$c"; out+="$h";; esac; done; printf '%s' "$out"; }
j=${v//\\/\\\\}; j=${j//\"/\\\"}
echo "raw: $v"
echo "b64: $(printf '%s' "$v" | base64)"
echo "b64url: $(printf '%s' "$v" | base64 | tr '+/' '-_')"
echo "url: $(urlenc "$v")"
echo "json: $j"
printf '{"type":"output","key":"leak","value":"%s"}\n' "$j" >> "$SLUICE_OUTPUTS"
echo "failing with $v" >&2
exit 1
`

// TestSCN_RUN_005_Masking checks that every stored form of a secret shows *** (REQ-RUN-005, SI-10).
func TestSCN_RUN_005_Masking(t *testing.T) {
	const value = `Canary+/Val?&"q\z9`
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t), "SLUICE_MASTER_KEYS": masterKey("k1", 1)})
	c := adminClient(t, p)
	c.do(t, http.MethodPut, "/api/v1/secrets/CANARY", map[string]any{"value": value}, http.StatusOK, nil)
	saveFiles(t, c, "mask", map[string]string{
		"mask.sh":     maskScript,
		"m.flow.yaml": "id: m\nenv:\n  S: \"${{ secret('CANARY') }}\"\ntasks:\n  - {id: t, type: script, file: mask.sh}\n",
	})
	d := waitTerminal(t, c, triggerFlow(t, c, "mask", "m", nil, nil).ID, 60*time.Second)
	if d.State != "FAILED" {
		t.Fatalf("execution %s", d.State)
	}
	lines := map[string]string{}
	for _, l := range allLogs(t, c, d.ID, "t") {
		if label, rest, ok := strings.Cut(l.Text, ": "); ok && l.Stream == "stdout" {
			lines[label] = rest
		}
	}
	for _, label := range []string{"raw", "b64", "b64url", "url", "json"} {
		if lines[label] != "***" {
			t.Errorf("%s line stored as %q, want ***", label, lines[label])
		}
	}
	tr := d.last("t")
	if tr.Outputs["leak"] != "***" {
		t.Errorf("output stored as %v, want ***", tr.Outputs["leak"])
	}
	if !strings.Contains(tr.Error, "failing with ***") {
		t.Errorf("error text %q does not show ***", tr.Error)
	}
	stored := logText(allLogs(t, c, d.ID, "")) + tr.Error + fmt.Sprint(tr.Outputs)
	for _, f := range masking.Forms(value) {
		if strings.Contains(stored, f) {
			t.Errorf("a stored text contains the form %q", f)
		}
	}
}
