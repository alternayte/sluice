package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
)

const sddPath = "docs/sluice-sdd.md"

// SDD holds the IDs of the specification.
type SDD struct {
	Hash       string
	Defs       []string            // REQ, NFR and SI IDs in document order
	Scenarios  []string            // SCN IDs in document order
	ScenarioOf map[string][]string // SCN -> verified IDs
	Layer      map[string]string   // SCN -> layer letter
}

var (
	defRe = regexp.MustCompile(`(?m)^\| ((?:REQ-[A-Z]+-\d{3})|(?:NFR-\d{3})|(?:SI-\d{2})) \|`)
	scnRe = regexp.MustCompile(`(?m)^- \*\*(SCN-[A-Z]+-\d{3})\*\* \[([A-Z])\] \(([^)]*)\)`)
	// idInName finds spec IDs in test names, with - or _ separators.
	// Go test names put the ID right after "Test", so no word boundary is required.
	idInName = regexp.MustCompile(`(SCN[-_][A-Z]+[-_]\d{3}|REQ[-_][A-Z]+[-_]\d{3}|NFR[-_]\d{3}|SI[-_]\d{2})(?:[^0-9]|$)`)
)

func loadSDD() (*SDD, error) {
	b, err := os.ReadFile(sddPath)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	s := &SDD{Hash: hex.EncodeToString(sum[:]), ScenarioOf: map[string][]string{}, Layer: map[string]string{}}
	src := string(b)
	// The ledger template in §12.1 repeats REQ-CORE-001 in a table; keep first occurrences only.
	seen := map[string]bool{}
	for _, m := range defRe.FindAllStringSubmatch(src, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			s.Defs = append(s.Defs, m[1])
		}
	}
	for _, m := range scnRe.FindAllStringSubmatch(src, -1) {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		s.Scenarios = append(s.Scenarios, id)
		s.Layer[id] = m[2]
		for _, p := range strings.Split(m[3], ",") {
			if p = strings.TrimSpace(p); p != "" {
				s.ScenarioOf[id] = append(s.ScenarioOf[id], p)
			}
		}
	}
	return s, nil
}

// All returns every ID (definitions and scenarios).
func (s *SDD) All() map[string]bool {
	out := map[string]bool{}
	for _, d := range s.Defs {
		out[d] = true
	}
	for _, d := range s.Scenarios {
		out[d] = true
	}
	return out
}

// ScenariosFor returns the scenarios that list id.
func (s *SDD) ScenariosFor(id string) []string {
	var out []string
	for _, scn := range s.Scenarios {
		for _, x := range s.ScenarioOf[scn] {
			if x == id {
				out = append(out, scn)
				break
			}
		}
	}
	return out
}

// normID turns SCN_EXE_003 into SCN-EXE-003.
func normID(s string) string { return strings.ReplaceAll(s, "_", "-") }

// nameHasID reports whether a test name contains id in either separator form.
func nameHasID(name, id string) bool {
	return strings.Contains(name, id) || strings.Contains(name, strings.ReplaceAll(id, "-", "_"))
}
