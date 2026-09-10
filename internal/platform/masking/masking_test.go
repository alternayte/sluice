package masking

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestMaskAllForms(t *testing.T) {
	secret := `p@ss/w0rd+"x"?&=`
	m := New([]string{secret, "abc"})
	j, _ := json.Marshal(map[string]string{"k": secret})
	inputs := []string{
		"raw " + secret,
		"b64 " + base64.StdEncoding.EncodeToString([]byte(secret)),
		"b64url " + base64.URLEncoding.EncodeToString([]byte(secret)),
		"b64raw " + base64.RawURLEncoding.EncodeToString([]byte(secret)),
		"url " + url.QueryEscape(secret),
		"path " + url.PathEscape(secret),
		"json " + string(j),
	}
	for _, in := range inputs {
		out := m.String(in)
		if !strings.Contains(out, Mask) {
			t.Errorf("not masked: %q -> %q", in, out)
		}
		for _, f := range Forms(secret) {
			if strings.Contains(out, f) {
				t.Errorf("form %q left in %q", f, out)
			}
		}
	}
	if got := m.String("abc and abcd"); got != "abc and abcd" {
		t.Errorf("short value masked: %q", got)
	}
	if !New(nil).Empty() || New([]string{"ab"}).String("ab") != "ab" {
		t.Error("empty masker changed text")
	}
}
