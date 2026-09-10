package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const ledgerPath = "docs/build/ledger.md"

// Ledger is the parsed docs/build/ledger.md.
type Ledger struct {
	Header  map[string]string
	Items   []LedgerItem
	Blocked []BlockedEntry
}

// LedgerItem is one row of the items table.
type LedgerItem struct {
	ID, Status, Slice, Evidence string
	Line                        int
}

// BlockedEntry is one B-entry. It is open until its line says "status: closed".
type BlockedEntry struct {
	ID   string
	Open bool
}

var (
	headerRe  = regexp.MustCompile(`^([a-z_0-9]+): (.*)$`)
	itemRe    = regexp.MustCompile(`^\| ([A-Z]+-[A-Z0-9-]+) \| ([A-Z_]+) \| (S\d+) \| (.*?) ?\|$`)
	blockedRe = regexp.MustCompile(`^(?:- |### )(B-\d+)\b(.*)$`)
)

func parseLedger(src string) (*Ledger, error) {
	l := &Ledger{Header: map[string]string{}}
	section := ""
	for i, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimPrefix(line, "## ")
			continue
		}
		switch section {
		case "":
			if m := headerRe.FindStringSubmatch(line); m != nil {
				l.Header[m[1]] = strings.TrimSpace(m[2])
			}
		case "Blocked":
			if m := blockedRe.FindStringSubmatch(line); m != nil {
				l.Blocked = append(l.Blocked, BlockedEntry{ID: m[1], Open: !strings.Contains(strings.ToLower(m[2]), "status: closed")})
			}
		case "Items":
			if m := itemRe.FindStringSubmatch(line); m != nil {
				l.Items = append(l.Items, LedgerItem{ID: m[1], Status: m[2], Slice: m[3], Evidence: strings.TrimSpace(m[4]), Line: i + 1})
			}
		}
	}
	if len(l.Items) == 0 {
		return nil, errors.New("ledger has no items")
	}
	return l, nil
}

// evidenceRef is "path::TestName" (Go) or "path::title" (Playwright).
func parseEvidence(ev string) (path, name string, ok bool) {
	path, name, ok = strings.Cut(ev, "::")
	path, name = strings.Trim(strings.TrimSpace(path), "`"), strings.Trim(strings.TrimSpace(name), "`")
	return path, name, ok && path != "" && name != ""
}

func checkLedger(l *Ledger, s *SDD, readFile func(string) ([]byte, error)) []string {
	var errs []string
	// Rule 1: header fields.
	valid := map[string]func(string) bool{
		"status":                 oneOf("IN_PROGRESS", "REVIEW", "BLOCKED", "DONE"),
		"sdd_sha256":             regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString,
		"current_slice":          regexp.MustCompile(`^S(\d|1[0-2])$`).MatchString,
		"review_round":           oneOf("0", "1", "2"),
		"review_complete":        oneOf("true", "false"),
		"blocking_findings_open": func(v string) bool { n, err := strconv.Atoi(v); return err == nil && n >= 0 },
	}
	for k, ok := range valid {
		v, present := l.Header[k]
		if !present {
			errs = append(errs, fmt.Sprintf("rule 1: header %s missing", k))
		} else if !ok(v) {
			errs = append(errs, fmt.Sprintf("rule 1: header %s has invalid value %q", k, v))
		}
	}
	// Rule 2: every SDD ID exactly once, no other IDs.
	all := s.All()
	count := map[string]int{}
	byID := map[string]LedgerItem{}
	for _, it := range l.Items {
		count[it.ID]++
		byID[it.ID] = it
		if !all[it.ID] {
			errs = append(errs, fmt.Sprintf("rule 2: line %d: %s is not an SDD ID", it.Line, it.ID))
		}
		if !oneOf("OPEN", "IN_PROGRESS", "PASS", "BLOCKED")(it.Status) {
			errs = append(errs, fmt.Sprintf("rule 2: line %d: invalid status %s", it.Line, it.Status))
		}
	}
	for id := range all {
		if count[id] != 1 {
			errs = append(errs, fmt.Sprintf("rule 2: %s appears %d times", id, count[id]))
		}
	}
	// Rule 3: PASS scenarios name an existing test that contains the ID.
	for _, it := range l.Items {
		if it.Status != "PASS" || !strings.HasPrefix(it.ID, "SCN-") {
			continue
		}
		refs := strings.Split(it.Evidence, ";")
		okAny := false
		for _, ref := range refs {
			path, name, ok := parseEvidence(ref)
			if !ok {
				continue
			}
			if !nameHasID(name, it.ID) {
				errs = append(errs, fmt.Sprintf("rule 3: %s evidence test %q does not contain the ID", it.ID, name))
				continue
			}
			b, err := readFile(path)
			if err != nil {
				errs = append(errs, fmt.Sprintf("rule 3: %s evidence file %s: %v", it.ID, path, err))
				continue
			}
			if !strings.Contains(string(b), name) {
				errs = append(errs, fmt.Sprintf("rule 3: %s evidence test %q not found in %s", it.ID, name, path))
				continue
			}
			okAny = true
		}
		if !okAny {
			errs = append(errs, fmt.Sprintf("rule 3: %s is PASS without valid evidence", it.ID))
		}
	}
	// Rule 4: PASS requirement has all mapped scenarios PASS.
	for _, d := range s.Defs {
		it, ok := byID[d]
		if !ok || it.Status != "PASS" {
			continue
		}
		for _, scn := range s.ScenariosFor(d) {
			if byID[scn].Status != "PASS" {
				errs = append(errs, fmt.Sprintf("rule 4: %s is PASS but %s is %s", d, scn, byID[scn].Status))
			}
		}
	}
	// Rule 5: DONE conditions.
	if l.Header["status"] == "DONE" {
		for _, it := range l.Items {
			if it.Status != "PASS" {
				errs = append(errs, fmt.Sprintf("rule 5: status DONE but %s is %s", it.ID, it.Status))
			}
		}
		if l.Header["review_complete"] != "true" {
			errs = append(errs, "rule 5: status DONE but review_complete is not true")
		}
		if l.Header["blocking_findings_open"] != "0" {
			errs = append(errs, "rule 5: status DONE but blocking findings are open")
		}
		for _, b := range l.Blocked {
			if b.Open {
				errs = append(errs, fmt.Sprintf("rule 5: status DONE but %s is open", b.ID))
			}
		}
	}
	// Rule 6: SDD hash.
	if l.Header["sdd_sha256"] != s.Hash {
		errs = append(errs, fmt.Sprintf("rule 6: sdd_sha256 %s differs from current %s", l.Header["sdd_sha256"], s.Hash))
	}
	return errs
}

func oneOf(vals ...string) func(string) bool {
	return func(v string) bool {
		for _, x := range vals {
			if v == x {
				return true
			}
		}
		return false
	}
}

func cmdLedgerCheck() error {
	s, err := loadSDD()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(ledgerPath)
	if err != nil {
		return err
	}
	l, err := parseLedger(string(b))
	if err != nil {
		return err
	}
	errs := checkLedger(l, s, os.ReadFile)
	for _, e := range errs {
		fmt.Println(e)
	}
	pass := 0
	for _, it := range l.Items {
		if it.Status == "PASS" {
			pass++
		}
	}
	fmt.Printf("ledger-check: %d/%d items PASS, %d errors\n", pass, len(l.Items), len(errs))
	if len(errs) > 0 {
		return errors.New("ledger-check failed")
	}
	return nil
}
