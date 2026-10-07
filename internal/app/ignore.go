package app

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// ignoreFile is the ignore file at the root of a namespace directory. It has gitignore
// syntax. `sluice namespaces push` and `sluice validate` read it and never upload it.
const ignoreFile = ".sluiceignore"

// builtinIgnores apply to every namespace directory. A negation line in the ignore file
// overrides one.
var builtinIgnores = []string{".git/", "__pycache__/", "*.pyc", ".venv/", "node_modules/", ".DS_Store"}

// secretNames are the file names that look like a secret. `sluice namespaces push` refuses
// such a file unless a rule of the ignore file covers it.
var secretNames = []string{".env", ".env.*", "*.pem", "*.key", "*.p12"}

// starterIgnore is the ignore file that `sluice init` writes.
const starterIgnore = `# Files that sluice namespaces push and sluice validate skip. The syntax is that of .gitignore.
# Keep this file at the root of the namespace directory.
# Sluice always skips: .git/ __pycache__/ *.pyc .venv/ node_modules/ .DS_Store
# A line that starts with ! permits a file, for example !certs/ca.pem

.env
.env.*
*.pem
*.key
*.p12
out/
dist/
*.log
`

// ignoreRules are the builtin rules followed by the rules of the ignore file.
type ignoreRules []gitignore.Pattern

// loadIgnore reads the rules of a namespace directory.
func loadIgnore(dir string) (ignoreRules, error) {
	var rules ignoreRules
	for _, l := range builtinIgnores {
		rules = append(rules, gitignore.ParsePattern(l, nil))
	}
	b, err := os.ReadFile(filepath.Join(dir, ignoreFile))
	if errors.Is(err, fs.ErrNotExist) {
		return rules, nil
	}
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), " \t\r")
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		rules = append(rules, gitignore.ParsePattern(l, nil))
	}
	return rules, sc.Err()
}

// match returns the result of the last rule that matches the path, as git does.
func (r ignoreRules) match(rel string, isDir bool) gitignore.MatchResult {
	segs := strings.Split(rel, "/")
	for i := len(r) - 1; i >= 0; i-- {
		if m := r[i].Match(segs, isDir); m != gitignore.NoMatch {
			return m
		}
	}
	return gitignore.NoMatch
}

// looksSecret reports whether the file name is one that usually holds a credential.
func looksSecret(rel string) bool {
	for _, p := range secretNames {
		if ok, _ := path.Match(p, path.Base(rel)); ok {
			return true
		}
	}
	return false
}
