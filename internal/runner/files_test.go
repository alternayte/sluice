package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.ini"), []byte("from bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFiles(dir, map[string]string{"app.ini": "rendered", "conf/deep/x.json": "{}"}); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"app.ini": "rendered", "conf/deep/x.json": "{}"} {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil || string(b) != want {
			t.Errorf("%s: got %q, %v", p, b, err)
		}
	}
	if err := writeFiles(dir, map[string]string{"../escape": "x"}); err == nil {
		t.Error("path outside the workdir: want error")
	}
	if err := writeFiles(dir, map[string]string{"conf": "x"}); err == nil {
		t.Error("path is a directory: want error")
	}
}
