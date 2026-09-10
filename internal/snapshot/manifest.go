// Package snapshot holds the snapshot manifest and the bundle format. The namespace,
// execution and runner features share them.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
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
