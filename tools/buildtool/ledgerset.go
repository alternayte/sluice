package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// cmdLedgerSet updates ledger rows and header fields.
//
//	ledger-set item <STATUS> <evidence or -> <ID>...
//	ledger-set slice <STATUS> <slice>          (all rows of a slice that are not PASS)
//	ledger-set header <key> <value>
func cmdLedgerSet(args []string) error {
	if len(args) < 3 {
		return errors.New("usage: ledger-set item|slice|header <args>")
	}
	b, err := os.ReadFile(ledgerPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	switch args[0] {
	case "header":
		re := regexp.MustCompile(`^` + regexp.QuoteMeta(args[1]) + `: `)
		found := false
		for i, l := range lines {
			if re.MatchString(l) {
				lines[i] = args[1] + ": " + args[2]
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("header %s not found", args[1])
		}
	case "item":
		if len(args) < 4 {
			return errors.New("usage: ledger-set item <STATUS> <evidence or -> <IDs>")
		}
		status, ev := args[1], args[2]
		for _, id := range args[3:] {
			found := false
			for i, l := range lines {
				m := itemRe.FindStringSubmatch(l)
				if m == nil || m[1] != id {
					continue
				}
				e := m[4]
				if ev != "-" {
					e = ev
				}
				lines[i] = fmt.Sprintf("| %s | %s | %s | %s |", id, status, m[3], e)
				found = true
			}
			if !found {
				return fmt.Errorf("item %s not found", id)
			}
		}
	case "slice":
		status, slice := args[1], args[2]
		for i, l := range lines {
			m := itemRe.FindStringSubmatch(l)
			if m == nil || m[3] != slice || m[2] == "PASS" {
				continue
			}
			lines[i] = fmt.Sprintf("| %s | %s | %s | %s |", m[1], status, m[3], m[4])
		}
	default:
		return fmt.Errorf("unknown ledger-set mode %q", args[0])
	}
	return os.WriteFile(ledgerPath, []byte(strings.Join(lines, "\n")), 0o644)
}
