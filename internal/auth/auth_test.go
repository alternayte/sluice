package auth

import (
	"strings"
	"testing"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/token"
)

func TestPasswordHashArgon2idParameters(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash parameters: %s", h)
	}
	ok, err := VerifyPassword(h, "correct horse battery")
	if err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword(h, "wrong"); ok {
		t.Fatal("wrong password verified")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("salt is not random")
	}
}

func TestAPITokenFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s, prefix, hash := NewAPIToken()
		if !LooksLikeAPIToken(s) || len(s) != 47 || !strings.HasPrefix(s, prefix) || len(hash) != 32 {
			t.Fatalf("token %q prefix %q", s, prefix)
		}
		if seen[s] {
			t.Fatal("duplicate token")
		}
		seen[s] = true
		if !EqualHash(hash, token.HashSecret(s)) {
			t.Fatal("hash mismatch")
		}
	}
	if LooksLikeAPIToken("slu_short") || LooksLikeAPIToken("abc_"+strings.Repeat("a", 43)) {
		t.Fatal("bad token accepted")
	}
}

func TestBase62Width(t *testing.T) {
	zero := make([]byte, 32)
	if got := Base62(zero, 43); got != strings.Repeat("0", 43) {
		t.Fatalf("zero: %s", got)
	}
	max := make([]byte, 32)
	for i := range max {
		max[i] = 0xff
	}
	if got := Base62(max, 43); len(got) != 43 {
		t.Fatalf("max width %d", len(got))
	}
}

func TestRoles(t *testing.T) {
	for _, r := range kernel.AllRoles {
		p, ok := kernel.ParseRole(r.String())
		if !ok || p != r {
			t.Fatalf("round trip %v", r)
		}
	}
	if kernel.Viewer >= kernel.Operator || kernel.Operator >= kernel.Editor || kernel.Editor >= kernel.Admin {
		t.Fatal("role order")
	}
	if _, ok := kernel.ParseRole("root"); ok {
		t.Fatal("unknown role parsed")
	}
	if kernel.MinRole(kernel.Admin, kernel.Operator) != kernel.Operator {
		t.Fatal("min role")
	}
}
