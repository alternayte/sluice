package flow

import "strings"

// NamespaceChain returns a namespace and its parents, nearest first: "a.b.c" gives
// "a.b.c", "a.b", "a" (REQ-NS-001, REQ-SEC-003).
func NamespaceChain(ns string) []string {
	var out []string
	for n := ns; n != ""; {
		out = append(out, n)
		i := strings.LastIndex(n, ".")
		if i < 0 {
			break
		}
		n = n[:i]
	}
	return out
}
