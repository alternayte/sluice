// Package token creates random secrets and their SHA-256 hashes.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// RandomBytes returns n random bytes. It panics when the system random source fails.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// HashSecret returns the SHA-256 hash of a session ID, token, run token or webhook key (SI-02).
func HashSecret(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// NewSecret returns a random URL-safe secret of 256 bits and its hash. Session IDs,
// run tokens and webhook keys use it.
func NewSecret() (secret string, hash []byte) {
	secret = base64.RawURLEncoding.EncodeToString(RandomBytes(32))
	return secret, HashSecret(secret)
}
