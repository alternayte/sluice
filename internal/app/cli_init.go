package app

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/skills"
)

const (
	agentsBegin = "<!-- sluice:begin -->"
	agentsEnd   = "<!-- sluice:end -->"
)

// agentsSection is the Sluice section that `sluice init` keeps in AGENTS.md.
const agentsSection = agentsBegin + `
## Sluice

This repository holds Sluice flows. Read ` + "`.claude/skills/sluice/SKILL.md`" + ` before you change a ` + "`*.flow.yaml`" + ` file or ` + "`namespace.yaml`" + `.

- Validate a namespace directory: ` + "`sluice validate <dir> --json`" + `.
- Deploy it as a new version: ` + "`sluice namespaces push <dir> --namespace <name>`" + `.
- Run a flow and wait for the end: ` + "`sluice run <namespace>/<flow> --wait`" + `.
- The client commands read SLUICE_URL and SLUICE_TOKEN.
` + agentsEnd + "\n"

// runInit implements `sluice init [dir] [--force]`. It writes the skill of this binary and
// the Sluice section of AGENTS.md. It keeps a file that someone changed, unless --force.
func runInit(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "replace the skill and the AGENTS.md section also when they were changed")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: sluice init [dir] [--force]\n\nWrites .claude/skills/sluice/SKILL.md and a Sluice section in AGENTS.md.\n\nFlags:")
		fs.PrintDefaults()
	}
	var dirs []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return exitOK
			}
			return exitConfig
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		dirs = append(dirs, rest[0])
		rest = rest[1:]
	}
	if len(dirs) > 1 {
		fs.Usage()
		return exitConfig
	}
	dir := "."
	if len(dirs) == 1 {
		dir = dirs[0]
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		fmt.Fprintf(stderr, "error: %s is not a directory\n", dir)
		return exitConfig
	}

	skillPath := filepath.Join(dir, ".claude", "skills", "sluice", "SKILL.md")
	status, err := writeManaged(skillPath, skills.Sluice, *force)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitFail
	}
	report(stdout, status, skillPath)

	agentsPath := filepath.Join(dir, "AGENTS.md")
	status, err = writeAgentsSection(agentsPath, *force)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitFail
	}
	report(stdout, status, agentsPath)

	fmt.Fprintf(stdout, "\nStart each flow file with this line, so editors and agents validate it:\n# yaml-language-server: $schema=%s\n", flow.SchemaID)
	return exitOK
}

type writeStatus int

const (
	statusWritten writeStatus = iota
	statusUnchanged
	statusKept
)

func report(w io.Writer, s writeStatus, path string) {
	switch s {
	case statusWritten:
		fmt.Fprintln(w, "wrote     "+path)
	case statusUnchanged:
		fmt.Fprintln(w, "unchanged "+path)
	case statusKept:
		fmt.Fprintln(w, "kept      "+path+" (it differs from this version; use --force to replace it)")
	}
}

// writeManaged writes content to path. An existing file with other content stays, unless force.
func writeManaged(path string, content []byte, force bool) (writeStatus, error) {
	old, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(old, content):
		return statusUnchanged, nil
	case err == nil && !force:
		return statusKept, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	return statusWritten, os.WriteFile(path, content, 0o644)
}

// writeAgentsSection adds the Sluice section to AGENTS.md, or creates the file. A section
// with other content stays, unless force. The rest of the file never changes.
func writeAgentsSection(path string, force bool) (writeStatus, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return statusWritten, os.WriteFile(path, []byte("# AGENTS.md\n\n"+agentsSection), 0o644)
	}
	if err != nil {
		return 0, err
	}
	text := string(b)
	start := strings.Index(text, agentsBegin)
	end := strings.Index(text, agentsEnd)
	if start < 0 || end < start {
		sep := "\n"
		if !strings.HasSuffix(text, "\n") {
			sep = "\n\n"
		}
		return statusWritten, os.WriteFile(path, []byte(text+sep+agentsSection), 0o644)
	}
	end += len(agentsEnd)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	if text[start:end] == agentsSection {
		return statusUnchanged, nil
	}
	if !force {
		return statusKept, nil
	}
	return statusWritten, os.WriteFile(path, []byte(text[:start]+agentsSection+text[end:]), 0o644)
}
