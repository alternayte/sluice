package secret_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/alternayte/sluice/internal/secret"
)

func key(id string, b byte) secret.Key { return secret.Key{ID: id, Key: bytes.Repeat([]byte{b}, 32)} }

func TestKeyringRoundTrip(t *testing.T) {
	k, err := secret.NewKeyring([]secret.Key{key("k2", 2), key("k1", 1)})
	if err != nil {
		t.Fatal(err)
	}
	sealed, kid, err := k.Encrypt([]byte("value"), secret.AAD("data", "PG_URL"))
	if err != nil || kid != "k2" {
		t.Fatalf("encrypt: %v, key %s", err, kid)
	}
	if bytes.Contains(sealed, []byte("value")) {
		t.Fatal("the sealed value contains the plain text")
	}
	plain, err := k.Decrypt(kid, sealed, secret.AAD("data", "PG_URL"))
	if err != nil || string(plain) != "value" {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	if _, err := k.Decrypt(kid, sealed, secret.AAD("global", "PG_URL")); err == nil {
		t.Fatal("a value of another scope decrypted: the associated data is not checked")
	}
	if _, err := k.Decrypt("k9", sealed, secret.AAD("data", "PG_URL")); !errors.Is(err, secret.ErrNoMasterKey) {
		t.Fatalf("unknown key ID: %v", err)
	}
}

func TestKeyringWithoutKeys(t *testing.T) {
	k, _ := secret.NewKeyring(nil)
	if k.Enabled() {
		t.Fatal("empty keyring is enabled")
	}
	if _, _, err := k.Encrypt([]byte("v"), "global/K"); !errors.Is(err, secret.ErrNoMasterKey) {
		t.Fatalf("encrypt without keys: %v", err)
	}
	if _, err := secret.NewKeyring([]secret.Key{{ID: "short", Key: []byte("x")}}); err == nil {
		t.Fatal("a short key was accepted")
	}
}
