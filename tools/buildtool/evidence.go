package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	evidenceDir  = "build/evidence/v1"
	evidenceFile = evidenceDir + "/evidence.json"
	perfReport   = "build/reports/perf.json"
)

// Evidence is build/evidence/v1/evidence.json (SDD §12.3).
type Evidence struct {
	SchemaVersion int               `json:"schema_version"`
	Head          string            `json:"head"`
	SDDHash       string            `json:"sdd_sha256"`
	GeneratedAt   time.Time         `json:"generated_at"`
	ToolVersions  map[string]string `json:"tool_versions"`
	Gates         []GateResult      `json:"gates"`
	Scenarios     EvidenceScenarios `json:"scenarios"`
	Trace         *TraceResult      `json:"trace"`
	Review        EvidenceReview    `json:"review"`
	NFR           json.RawMessage   `json:"nfr_measurements"`
}

// EvidenceScenarios holds scenario totals.
type EvidenceScenarios struct {
	Total    int      `json:"total"`
	Passed   int      `json:"passed"`
	Failures []string `json:"failures"`
}

// EvidenceReview holds review data from the ledger.
type EvidenceReview struct {
	Rounds               int    `json:"rounds"`
	Complete             bool   `json:"complete"`
	BlockingFindingsOpen int    `json:"blocking_findings_open"`
	LedgerStatus         string `json:"ledger_status"`
}

func readVerify() (*VerifyReport, error) {
	b, err := os.ReadFile(verifyOut)
	if err != nil {
		return nil, err
	}
	var v VerifyReport
	return &v, json.Unmarshal(b, &v)
}

func toolVersions() map[string]string {
	cmds := map[string][]string{
		"go":            {"go", "version"},
		"bun":           {"bun", "--version"},
		"docker":        {"docker", "version", "--format", "{{.Server.Version}}"},
		"kind":          {"kind", "version"},
		"kubectl":       {"kubectl", "version", "--client", "-o", "yaml"},
		"helm":          {"helm", "version", "--short"},
		"just":          {"just", "--version"},
		"golangci-lint": {"golangci-lint", "version"},
		"uv":            {"uv", "--version"},
	}
	out := map[string]string{}
	for k, c := range cmds {
		b, err := exec.Command(c[0], c[1:]...).Output()
		if err != nil {
			out[k] = "unavailable"
			continue
		}
		line := strings.TrimSpace(string(b))
		if k == "kubectl" {
			for _, l := range strings.Split(line, "\n") {
				if strings.Contains(l, "gitVersion") {
					line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "gitVersion:"))
					break
				}
			}
		}
		if i := strings.Index(line, "\n"); i > 0 {
			line = line[:i]
		}
		out[k] = line
	}
	return out
}

func cmdEvidence() error {
	head, err := gitHead()
	if err != nil {
		return err
	}
	clean, err := treeClean()
	if err != nil {
		return err
	}
	if !clean {
		return errors.New("evidence: working tree is dirty")
	}
	v, err := readVerify()
	if err != nil {
		return fmt.Errorf("evidence: read verify report: %w", err)
	}
	if v.Head != head {
		return fmt.Errorf("evidence: verify.json is for %s, HEAD is %s", v.Head, head)
	}
	if !v.OK || !v.TreeClean {
		return errors.New("evidence: verify.json has a failed gate or a dirty tree")
	}
	s, err := loadSDD()
	if err != nil {
		return err
	}
	tr := computeTrace(s, v.JUnit)
	lb, err := os.ReadFile(ledgerPath)
	if err != nil {
		return err
	}
	l, err := parseLedger(string(lb))
	if err != nil {
		return err
	}
	ev := &Evidence{SchemaVersion: 1, Head: head, SDDHash: s.Hash, GeneratedAt: time.Now().UTC(), ToolVersions: toolVersions(), Gates: v.Gates, Trace: tr}
	ev.Scenarios.Total = tr.Totals["total"]
	ev.Scenarios.Passed = tr.Totals["passed"]
	for id, st := range tr.Scenarios {
		if st.Status != "passed" {
			ev.Scenarios.Failures = append(ev.Scenarios.Failures, id)
		}
	}
	ev.Review.Rounds = atoi(l.Header["review_round"])
	ev.Review.Complete = l.Header["review_complete"] == "true"
	ev.Review.BlockingFindingsOpen = atoi(l.Header["blocking_findings_open"])
	ev.Review.LedgerStatus = l.Header["status"]
	if b, err := os.ReadFile(perfReport); err == nil {
		ev.NFR = b
	} else {
		ev.NFR = json.RawMessage("null")
	}
	if err := os.RemoveAll(evidenceDir); err != nil {
		return err
	}
	if err := writeJSON(evidenceFile, ev); err != nil {
		return err
	}
	for _, f := range []string{verifyOut, traceOut, perfReport} {
		if _, err := os.Stat(f); err == nil {
			if err := copyFile(f, filepath.Join(evidenceDir, filepath.Base(f))); err != nil {
				return err
			}
		}
	}
	files, _ := filepath.Glob(filepath.Join(junitDir, "*.xml"))
	for _, f := range files {
		if err := copyFile(f, filepath.Join(evidenceDir, "junit", filepath.Base(f))); err != nil {
			return err
		}
	}
	fmt.Printf("evidence: wrote %s\n", evidenceFile)
	return nil
}

func cmdEvidenceCheck() error {
	b, err := os.ReadFile(evidenceFile)
	if err != nil {
		return fmt.Errorf("evidence-check: %w", err)
	}
	var ev Evidence
	if err := json.Unmarshal(b, &ev); err != nil {
		return err
	}
	head, err := gitHead()
	if err != nil {
		return err
	}
	var errs []string
	if ev.Head != head {
		errs = append(errs, fmt.Sprintf("evidence HEAD %s differs from current HEAD %s", ev.Head, head))
	}
	if clean, _ := treeClean(); !clean {
		errs = append(errs, "working tree is dirty")
	}
	for _, g := range ev.Gates {
		if !g.OK {
			errs = append(errs, "gate failed: "+g.Name)
		}
	}
	if len(ev.Gates) < len(verifyGates) {
		errs = append(errs, "evidence does not hold all gates")
	}
	if ev.Scenarios.Total == 0 || ev.Scenarios.Passed != ev.Scenarios.Total {
		errs = append(errs, fmt.Sprintf("scenarios passed %d/%d", ev.Scenarios.Passed, ev.Scenarios.Total))
	}
	out, err := exec.Command("git", "show", "HEAD:"+ledgerPath).Output()
	if err != nil {
		errs = append(errs, "ledger not at HEAD")
	} else if l, err := parseLedger(string(out)); err != nil || l.Header["status"] != "DONE" {
		errs = append(errs, "ledger at HEAD does not have status: DONE")
	}
	for _, e := range errs {
		fmt.Println(e)
	}
	if len(errs) > 0 {
		return errors.New("evidence-check failed")
	}
	fmt.Println("evidence-check: ok")
	return nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
