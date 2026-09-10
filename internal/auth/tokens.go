package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"math/big"
	"strings"
)

// TokenPrefix starts every API token (REQ-AUTH-005).
const TokenPrefix = "slu_"

// tokenBodyLen is the number of base62 characters for 256 bits.
const tokenBodyLen = 43

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// RandomBytes returns n bytes from crypto/rand.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// Base62 encodes b as a fixed-width base62 string.
func Base62(b []byte, width int) string {
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(62)
	mod := new(big.Int)
	out := make([]byte, 0, width)
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, base62Alphabet[mod.Int64()])
	}
	for len(out) < width {
		out = append(out, '0')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// HashSecret returns the SHA-256 of a session ID, token, run token or webhook key (SI-02).
func HashSecret(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// EqualHash compares two hashes in constant time.
func EqualHash(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// NewAPIToken returns a token "slu_" + 43 base62 chars, its display prefix and its hash.
func NewAPIToken() (secret, prefix string, hash []byte) {
	secret = TokenPrefix + Base62(RandomBytes(32), tokenBodyLen)
	return secret, secret[:len(TokenPrefix)+6], HashSecret(secret)
}

// LooksLikeAPIToken reports whether s has the API token format.
func LooksLikeAPIToken(s string) bool {
	if !strings.HasPrefix(s, TokenPrefix) || len(s) != len(TokenPrefix)+tokenBodyLen {
		return false
	}
	for _, r := range s[len(TokenPrefix):] {
		if !strings.ContainsRune(base62Alphabet, r) {
			return false
		}
	}
	return true
}

// NewSecret returns a random 256-bit URL-safe secret and its hash. It is used for
// session IDs, run tokens and webhook keys.
func NewSecret() (secret string, hash []byte) {
	secret = base64.RawURLEncoding.EncodeToString(RandomBytes(32))
	return secret, HashSecret(secret)
}
