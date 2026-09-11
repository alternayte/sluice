// Package namespace implements namespaces, files, snapshots and bundles (REQ-NS-*, D-05).
package namespace

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ContentHash returns the hex SHA-256 of b.
func ContentHash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// isFlowOrNamespace reports whether the file content is needed for flow sync.
func needsContent(p string) bool {
	return p == "namespace.yaml" || strings.HasSuffix(p, ".flow.yaml") || strings.HasSuffix(p, ".flow.yml")
}
