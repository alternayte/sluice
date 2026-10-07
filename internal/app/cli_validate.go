package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"github.com/alternayte/sluice/internal/flow"
)

// runValidate implements `sluice validate <dir> [--json] [--verbose]` (REQ-FLOW-007). It exits
// 0 when the namespace is valid and 1 when it is invalid.
func runValidate(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the result as JSON (schemas/validate-result.schema.json)")
	verbose := fs.Bool("verbose", false, "print each path that an ignore rule skips")
	// Accept the flag before or after the directory.
	var dirs []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return exitConfig
		}
		rest = fs.Args()
		if len(rest) > 0 {
			dirs = append(dirs, rest[0])
			rest = rest[1:]
		}
	}
	if len(dirs) != 1 {
		fmt.Fprintln(stderr, "usage: sluice validate <dir> [--json] [--verbose]")
		return exitConfig
	}
	nd, err := readNamespaceDir(dirs[0])
	if err != nil {
		fmt.Fprintln(stderr, "validate:", err)
		return exitConfig
	}
	nd.report(stderr, *verbose)
	for _, p := range nd.Secrets {
		fmt.Fprintf(stderr, "warning: %s looks like a secret and sluice namespaces push refuses it: %s\n", p, secretHelp)
	}
	res := flow.ValidateNamespace(nd.Files).Result()
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	} else {
		for _, f := range res.Files {
			if f.Valid {
				fmt.Fprintf(stdout, "ok      %s\n", f.Path)
				continue
			}
			fmt.Fprintf(stdout, "invalid %s\n", f.Path)
			for _, is := range f.Errors {
				fmt.Fprintf(stdout, "  %s:%d:%d %s %s: %s\n", f.Path, is.Line, is.Column, is.Code, is.Path, is.Message)
			}
		}
		if len(res.Files) == 0 {
			fmt.Fprintln(stdout, "no flow files found")
		}
	}
	if !res.Valid {
		return exitFail
	}
	return exitOK
}

// namespaceDir is the content of a namespace directory that the CLI validates or uploads.
type namespaceDir struct {
	Files    map[string][]byte
	Warnings []string
	// Skipped are the paths that an ignore rule excludes. A directory ends with a slash.
	Skipped []string
	// Secrets are the files that look like a secret and that no ignore rule covers.
	Secrets []string
}

// readNamespaceDir reads a namespace directory. Paths that the ignore rules exclude are
// skipped. Symlinks and paths that fail REQ-NS-005 are skipped with a warning.
func readNamespaceDir(dir string) (*namespaceDir, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	rules, err := loadIgnore(dir)
	if err != nil {
		return nil, err
	}
	out := &namespaceDir{Files: map[string][]byte{}}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." || rel == ignoreFile {
			return nil
		}
		rule := rules.match(rel, d.IsDir())
		if d.IsDir() {
			if rule == gitignore.Exclude {
				out.Skipped = append(out.Skipped, rel+"/")
				return filepath.SkipDir
			}
			return nil
		}
		if rule == gitignore.Exclude {
			out.Skipped = append(out.Skipped, rel)
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			out.Warnings = append(out.Warnings, "skipped symlink "+rel)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if err := flow.ValidPath(rel); err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("skipped %s: %v", rel, err))
			return nil
		}
		if rule == gitignore.NoMatch && looksSecret(rel) {
			out.Secrets = append(out.Secrets, rel)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out.Files[rel] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// report prints the warnings of the directory and, with verbose, each skipped path.
func (n *namespaceDir) report(stderr io.Writer, verbose bool) {
	for _, w := range n.Warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	if verbose {
		for _, p := range n.Skipped {
			fmt.Fprintln(stderr, "skipped", p)
		}
	}
}

// secretHelp tells how to resolve a file that looks like a secret.
const secretHelp = "add the path to " + ignoreFile + " to skip the file, or add a line with ! before the path to permit it"
