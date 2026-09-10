// Package masking replaces secret values in text (REQ-RUN-005, SI-10).
package masking

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// Mask is the replacement text.
const Mask = "***"

// MinLength is the shortest value that is masked (SI-10).
const MinLength = 4

// Masker replaces the raw, base64 standard, base64 URL, URL-encoded and JSON-escaped
// forms of each secret value of 4 or more characters.
type Masker struct {
	r       *strings.Replacer
	forms   []string
	maxForm int
}

// Forms returns the encoded forms of one value.
func Forms(v string) []string {
	if len(v) < MinLength {
		return nil
	}
	set := map[string]bool{v: true}
	b := []byte(v)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		set[enc.EncodeToString(b)] = true
	}
	set[url.QueryEscape(v)] = true
	set[url.PathEscape(v)] = true
	if j, err := json.Marshal(v); err == nil {
		set[strings.Trim(string(j), `"`)] = true
	}
	// JSON escaping without HTML escaping (< > &), as json.Encoder with SetEscapeHTML(false) writes.
	set[strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(v)] = true
	out := make([]string, 0, len(set))
	for f := range set {
		if len(f) >= MinLength {
			out = append(out, f)
		}
	}
	return out
}

// New builds a masker for the values.
func New(values []string) *Masker {
	set := map[string]bool{}
	for _, v := range values {
		for _, f := range Forms(v) {
			set[f] = true
		}
	}
	forms := make([]string, 0, len(set))
	for f := range set {
		forms = append(forms, f)
	}
	// Longest forms first, so a form that contains another is replaced whole.
	sort.Slice(forms, func(i, j int) bool {
		if len(forms[i]) != len(forms[j]) {
			return len(forms[i]) > len(forms[j])
		}
		return forms[i] < forms[j]
	})
	m := &Masker{forms: forms}
	if len(forms) > 0 {
		args := make([]string, 0, 2*len(forms))
		for _, f := range forms {
			args = append(args, f, Mask)
			if len(f) > m.maxForm {
				m.maxForm = len(f)
			}
		}
		m.r = strings.NewReplacer(args...)
	}
	return m
}

// Empty reports whether the masker has no values.
func (m *Masker) Empty() bool { return m == nil || m.r == nil }

// String masks s.
func (m *Masker) String(s string) string {
	if m.Empty() {
		return s
	}
	return m.r.Replace(s)
}

// Bytes masks b.
func (m *Masker) Bytes(b []byte) []byte {
	if m.Empty() {
		return b
	}
	return []byte(m.r.Replace(string(b)))
}

// Values returns the raw values count (for tests and diagnostics).
func (m *Masker) Forms() []string { return append([]string(nil), m.forms...) }
