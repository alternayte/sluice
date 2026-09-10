package snapshot_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alternayte/sluice/internal/snapshot"
)

func tarGz(t *testing.T, entries []tar.Header, content string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(content))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(content))
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return bytes.NewReader(buf.Bytes())
}

// TestBundleExtractionRejectsUnsafeEntries guards SI-08: ExtractBundle rejects
// entries that escape the root, absolute paths, links and special files, and
// extracts a normal file with its mode preserved.
func TestBundleExtractionRejectsUnsafeEntries(t *testing.T) {
	bad := [][]tar.Header{
		{{Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0o644}},
		{{Name: "/abs.txt", Typeflag: tar.TypeReg, Mode: 0o644}},
		{{Name: "a/../../b.txt", Typeflag: tar.TypeReg, Mode: 0o644}},
		{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		{{Name: "hard", Typeflag: tar.TypeLink, Linkname: "x"}},
	}
	for _, entries := range bad {
		dir := t.TempDir()
		err := snapshot.ExtractBundle(tarGz(t, entries, "x"), dir, 0)
		if !errors.Is(err, snapshot.ErrUnsafeEntry) {
			t.Errorf("%s: %v", entries[0].Name, err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
			t.Fatal("file written outside the root")
		}
	}
	dir := t.TempDir()
	if err := snapshot.ExtractBundle(tarGz(t, []tar.Header{{Name: "ok/run.sh", Typeflag: tar.TypeReg, Mode: 0o755}}, "echo ok"), dir, 0); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "ok", "run.sh"))
	if err != nil || st.Mode()&0o100 == 0 {
		t.Fatalf("extracted file: %v %v", st, err)
	}
}
