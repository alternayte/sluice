package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const verifyOut = "build/reports/verify.json"

// verifyGates is the gate order of `just verify` (SDD §10.2).
var verifyGates = []string{"gen-check", "lint", "test", "test-int", "build", "e2e", "e2e-k8s", "perf", "trace", "ledger-check"}

// GateResult is one gate of a verify run.
type GateResult struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Log        string `json:"log"`
}

// VerifyReport is build/reports/verify.json.
type VerifyReport struct {
	SchemaVersion int          `json:"schema_version"`
	Head          string       `json:"head"`
	TreeClean     bool         `json:"tree_clean"`
	StartedAt     time.Time    `json:"started_at"`
	EndedAt       time.Time    `json:"ended_at"`
	OK            bool         `json:"ok"`
	Gates         []GateResult `json:"gates"`
	JUnit         []TestCase   `json:"junit"`
}

func gitHead() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	return strings.TrimSpace(string(out)), err
}

func treeClean() (bool, error) {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "", nil
}

func cmdVerify(args []string) error {
	gates := verifyGates
	if len(args) > 0 {
		gates = args
	}
	head, err := gitHead()
	if err != nil {
		return err
	}
	clean, err := treeClean()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(junitDir); err != nil {
		return err
	}
	logDir := "build/reports/logs"
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(junitDir, 0o755); err != nil {
		return err
	}
	rep := &VerifyReport{SchemaVersion: 1, Head: head, TreeClean: clean, StartedAt: time.Now().UTC(), OK: true}
	for _, g := range gates {
		logPath := filepath.Join(logDir, g+".log")
		f, err := os.Create(logPath)
		if err != nil {
			return err
		}
		fmt.Printf("==> %s\n", g)
		start := time.Now()
		cmd := exec.Command("just", g)
		cmd.Stdout = io.MultiWriter(os.Stdout, f)
		cmd.Stderr = io.MultiWriter(os.Stderr, f)
		runErr := cmd.Run()
		_ = f.Close()
		gr := GateResult{Name: g, OK: runErr == nil, DurationMS: time.Since(start).Milliseconds(), Log: logPath}
		rep.Gates = append(rep.Gates, gr)
		if !gr.OK {
			rep.OK = false
			fmt.Printf("<== %s FAILED (%s)\n", g, time.Since(start).Round(time.Second))
		} else {
			fmt.Printf("<== %s ok (%s)\n", g, time.Since(start).Round(time.Second))
		}
	}
	rep.EndedAt = time.Now().UTC()
	rep.JUnit, err = readJUnit(junitDir)
	if err != nil {
		return err
	}
	if err := writeJSON(verifyOut, rep); err != nil {
		return err
	}
	if !rep.OK {
		var failed []string
		for _, g := range rep.Gates {
			if !g.OK {
				failed = append(failed, g.Name)
			}
		}
		return fmt.Errorf("verify failed: %s", strings.Join(failed, ", "))
	}
	fmt.Println("verify: all gates passed")
	return nil
}
