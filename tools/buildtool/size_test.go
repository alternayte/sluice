package main

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDist writes JS files into a temporary dist directory. Random bytes do not compress,
// so the gzip size of each file is a little above its length.
func writeDist(t *testing.T, files map[string]int) string {
	t.Helper()
	dir := t.TempDir()
	for name, size := range files {
		b := make([]byte, size)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const sizeManifest = `{
  "index.html": {"file": "assets/index.js", "isEntry": true, "imports": ["_vendor.js"], "dynamicImports": ["src/charts.tsx"]},
  "_vendor.js": {"file": "assets/vendor.js"},
  "src/charts.tsx": {"file": "assets/charts.js", "isDynamicEntry": true, "imports": ["_vendor.js"]},
  "index.css": {"file": "assets/index.css"}
}`

func TestSizeCheck(t *testing.T) {
	t.Run("SCN-NFR-003 the check counts the entry and its static imports and passes below the limit", func(t *testing.T) {
		dir := writeDist(t, map[string]int{"assets/index.js": 10_000, "assets/vendor.js": 20_000, "assets/charts.js": 500_000})
		total, files, err := checkSize(dir, []byte(sizeManifest), 350*1024)
		if err != nil {
			t.Fatalf("check failed: %v", err)
		}
		if strings.Join(files, ",") != "assets/index.js,assets/vendor.js" {
			t.Fatalf("initial files = %v, want the entry and the static import without the lazy chunk", files)
		}
		if total < 30_000 || total > 31_000 {
			t.Fatalf("total = %d, want the gzip size of 30 000 random bytes", total)
		}
	})

	t.Run("SCN-NFR-003 the check fails above the limit", func(t *testing.T) {
		dir := writeDist(t, map[string]int{"assets/index.js": 200 * 1024, "assets/vendor.js": 200 * 1024, "assets/charts.js": 10})
		_, _, err := checkSize(dir, []byte(sizeManifest), 350*1024)
		if err == nil || !strings.Contains(err.Error(), "limit 358400") {
			t.Fatalf("err = %v, want a limit error", err)
		}
	})

	t.Run("SCN-NFR-003 the check fails without an entry chunk or with an invalid manifest", func(t *testing.T) {
		dir := writeDist(t, map[string]int{"assets/vendor.js": 10})
		if _, _, err := checkSize(dir, []byte(`{"_vendor.js": {"file": "assets/vendor.js"}}`), 350*1024); err == nil {
			t.Fatal("a manifest without an entry passed")
		}
		if _, _, err := checkSize(dir, []byte(`{`), 350*1024); err == nil {
			t.Fatal("an invalid manifest passed")
		}
	})
}
