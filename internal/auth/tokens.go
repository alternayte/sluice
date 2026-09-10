package auth

import (
	"crypto/subtle"
	"math/big"
	"strings"

	"github.com/alternayte/sluice/internal/platform/token"
)

// TokenPrefix starts every API token (REQ-AUTH-005).
const TokenPrefix = "slu_"

// tokenBodyLen is the number of base62 characters for 256 bits.
const tokenBodyLen = 43

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

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

// EqualHash compares two hashes in constant time.
func EqualHash(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// NewAPIToken returns a token "slu_" + 43 base62 chars, its display prefix and its hash.
func NewAPIToken() (secret, prefix string, hash []byte) {
	secret = TokenPrefix + Base62(token.RandomBytes(32), tokenBodyLen)
	return secret, secret[:len(TokenPrefix)+6], token.HashSecret(secret)
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
