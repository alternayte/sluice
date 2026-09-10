package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

const traceOut = "build/reports/trace.json"

// TraceResult is written to build/reports/trace.json.
type TraceResult struct {
	OK           bool                     `json:"ok"`
	SDDHash      string                   `json:"sdd_sha256"`
	Errors       []string                 `json:"errors"`
	Scenarios    map[string]ScenarioTrace `json:"scenarios"`
	Requirements map[string][]string      `json:"requirements"`
	Totals       map[string]int           `json:"totals"`
}

// ScenarioTrace lists the tests of one scenario.
type ScenarioTrace struct {
	Layer    string   `json:"layer"`
	Verifies []string `json:"verifies"`
	Tests    []string `json:"tests"`
	Status   string   `json:"status"` // passed, failed, missing
}

var findingRe = regexp.MustCompile(`F[-_]\d+[-_]\d+`)

func computeTrace(s *SDD, cases []TestCase) *TraceResult {
	res := &TraceResult{SDDHash: s.Hash, Scenarios: map[string]ScenarioTrace{}, Requirements: map[string][]string{}, Totals: map[string]int{}}
	// Rule 1: every REQ, NFR and SI ID is listed by a scenario.
	for _, d := range s.Defs {
		scns := s.ScenariosFor(d)
		res.Requirements[d] = scns
		if len(scns) == 0 {
			res.Errors = append(res.Errors, fmt.Sprintf("rule 1: %s is not listed by any scenario", d))
		}
	}
	// Rules 2 and 3: each scenario has tests and all of them passed.
	for _, scn := range s.Scenarios {
		st := ScenarioTrace{Layer: s.Layer[scn], Verifies: s.ScenarioOf[scn], Status: "missing"}
		failed := false
		for _, c := range cases {
			if !nameHasID(c.Name, scn) {
				continue
			}
			st.Tests = append(st.Tests, c.Classname+" "+c.Name+" ["+c.Status+"]")
			if c.Status != "passed" {
				failed = true
			}
		}
		switch {
		case len(st.Tests) == 0:
			res.Errors = append(res.Errors, fmt.Sprintf("rule 2: %s has no test in the JUnit reports", scn))
		case failed:
			st.Status = "failed"
			res.Errors = append(res.Errors, fmt.Sprintf("rule 3: %s has a failed or skipped test", scn))
		default:
			st.Status = "passed"
		}
		res.Totals[st.Status]++
		res.Scenarios[scn] = st
	}
	res.Totals["total"] = len(s.Scenarios)
	// Rule 4: no test references an unknown ID. Review tests use finding IDs.
	known := s.All()
	for _, c := range cases {
		if strings.Contains(c.Classname, "tests/review") || findingRe.MatchString(c.Name) {
			continue
		}
		for _, m := range idInName.FindAllStringSubmatch(c.Name, -1) {
			if id := normID(m[1]); !known[id] {
				res.Errors = append(res.Errors, fmt.Sprintf("rule 4: test %q references unknown ID %s", c.Name, id))
			}
		}
	}
	sort.Strings(res.Errors)
	res.Errors = dedupe(res.Errors)
	res.OK = len(res.Errors) == 0
	return res
}

func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func cmdTrace() error {
	s, err := loadSDD()
	if err != nil {
		return err
	}
	cases, err := readJUnit(junitDir)
	if err != nil {
		return err
	}
	res := computeTrace(s, cases)
	if err := writeJSON(traceOut, res); err != nil {
		return err
	}
	for _, e := range res.Errors {
		fmt.Println(e)
	}
	fmt.Printf("trace: %d/%d scenarios passed, %d errors\n", res.Totals["passed"], res.Totals["total"], len(res.Errors))
	if !res.OK {
		return fmt.Errorf("trace failed")
	}
	return nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func dirOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "."
	}
	return p[:i]
}
