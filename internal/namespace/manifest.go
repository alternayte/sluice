// Package namespace implements namespaces, files, snapshots and bundles (REQ-NS-*, D-05).
package namespace

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Entry is one file of a snapshot.
type Entry struct {
	Path       string
	Hash       string
	Size       int64
	Executable bool
}

// Manifest maps paths to entries.
type Manifest map[string]Entry

// Paths returns the sorted paths.
func (m Manifest) Paths() []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Hash is the SHA-256 of the sorted manifest lines "path\x00hash\x00x|-\n".
func (m Manifest) Hash() string {
	h := sha256.New()
	for _, p := range m.Paths() {
		e := m[p]
		x := "-"
		if e.Executable {
			x = "x"
		}
		h.Write([]byte(p + "\x00" + e.Hash + "\x00" + x + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Total is the sum of all file sizes.
func (m Manifest) Total() int64 {
	var n int64
	for _, e := range m {
		n += e.Size
	}
	return n
}

// Clone returns a copy.
func (m Manifest) Clone() Manifest {
	out := make(Manifest, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ContentHash returns the hex SHA-256 of b.
func ContentHash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// isFlowOrNamespace reports whether the file content is needed for flow sync.
func needsContent(p string) bool {
	return p == "namespace.yaml" || strings.HasSuffix(p, ".flow.yaml") || strings.HasSuffix(p, ".flow.yml")
}
