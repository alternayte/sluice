// Package secret manages secret providers and secrets and resolves secret references
// (REQ-SEC-001 to REQ-SEC-006, REQ-SEC-009, REQ-SEC-010).
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"

	"github.com/alternayte/sluice/internal/platform/token"
)

// Key is one master key: a key ID and 32 key bytes.
type Key struct {
	ID  string
	Key []byte
}

// Keyring holds the master keys. The first key is active (REQ-SEC-006).
type Keyring struct {
	keys []Key
}

// NewKeyring returns a keyring. Each key must have 32 bytes.
func NewKeyring(keys []Key) (*Keyring, error) {
	for _, k := range keys {
		if k.ID == "" || len(k.Key) != 32 {
			return nil, fmt.Errorf("master key %q must have an ID and 32 bytes", k.ID)
		}
	}
	return &Keyring{keys: keys}, nil
}

// Enabled reports whether a master key exists (REQ-SEC-010, SI-09).
func (k *Keyring) Enabled() bool { return k != nil && len(k.keys) > 0 }

// Has reports whether the keyring has the key ID.
func (k *Keyring) Has(id string) bool {
	_, ok := k.find(id)
	return ok
}

func (k *Keyring) find(id string) ([]byte, bool) {
	if k == nil {
		return nil, false
	}
	for _, x := range k.keys {
		if x.ID == id {
			return x.Key, true
		}
	}
	return nil, false
}

// ErrNoMasterKey is returned when no master key can encrypt or decrypt a value.
var ErrNoMasterKey = errors.New("master_key_missing")

// AAD returns the associated data of a builtin secret: "<scope>/<key>" with scope
// "global" or the namespace name (REQ-SEC-002).
func AAD(scope, key string) string {
	if scope == "" {
		scope = "global"
	}
	return scope + "/" + key
}

// Encrypt seals plain with the active key and AES-256-GCM. The result is nonce || ciphertext.
func (k *Keyring) Encrypt(plain []byte, aad string) ([]byte, string, error) {
	if !k.Enabled() {
		return nil, "", ErrNoMasterKey
	}
	active := k.keys[0]
	g, err := gcm(active.Key)
	if err != nil {
		return nil, "", err
	}
	nonce := token.RandomBytes(g.NonceSize())
	return g.Seal(nonce, nonce, plain, []byte(aad)), active.ID, nil
}

// Decrypt opens a value sealed by Encrypt with the key keyID.
func (k *Keyring) Decrypt(keyID string, sealed []byte, aad string) ([]byte, error) {
	key, ok := k.find(keyID)
	if !ok {
		return nil, fmt.Errorf("%w: no key for key ID %q", ErrNoMasterKey, keyID)
	}
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], []byte(aad))
}

// ActiveID returns the ID of the active key, or "" without keys.
func (k *Keyring) ActiveID() string {
	if !k.Enabled() {
		return ""
	}
	return k.keys[0].ID
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
