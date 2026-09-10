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
	"strings"

	"github.com/alternayte/sluice/internal/flow"
)

// runValidate implements `sluice validate <dir> [--json]` (REQ-FLOW-007). It exits
// 0 when the namespace is valid and 1 when it is invalid.
func runValidate(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the result as JSON (schemas/validate-result.schema.json)")
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
		fmt.Fprintln(stderr, "usage: sluice validate <dir> [--json]")
		return exitConfig
	}
	files, warnings, err := readNamespaceDir(dirs[0])
	if err != nil {
		fmt.Fprintln(stderr, "validate:", err)
		return exitConfig
	}
	for _, w := range warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	res := flow.ValidateNamespace(files).Result()
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

// readNamespaceDir reads a namespace directory. Symlinks and paths that fail
// REQ-NS-005 are skipped with a warning. .git directories are skipped.
func readNamespaceDir(dir string) (map[string][]byte, []string, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, nil, err
	}
	if !st.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", dir)
	}
	files := map[string][]byte{}
	var warnings []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			warnings = append(warnings, "skipped symlink "+rel)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if err := flow.ValidPath(rel); err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %v", rel, err))
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return files, warnings, nil
	}
	_ = strings.TrimSpace
	return files, warnings, nil
}
