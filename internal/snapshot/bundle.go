package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alternayte/sluice/internal/flow"
)

// ErrUnsafeEntry is returned for tar entries outside the root, links or special files (SI-08).
var ErrUnsafeEntry = errors.New("bundle entry is not allowed")

// ExtractBundle extracts a tar.gz bundle into dir. It rejects absolute paths, ".."
// segments, links and special files, and limits the total size (SI-08).
func ExtractBundle(r io.Reader, dir string, maxBytes int64) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag == tar.TypeDir {
			if err := flow.ValidPath(strings.TrimSuffix(name, "/")); err != nil {
				return fmt.Errorf("%w: %q: %v", ErrUnsafeEntry, h.Name, err)
			}
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("%w: %q has type %c", ErrUnsafeEntry, h.Name, h.Typeflag)
		}
		if err := flow.ValidPath(name); err != nil {
			return fmt.Errorf("%w: %q: %v", ErrUnsafeEntry, h.Name, err)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if rel, err := filepath.Rel(root, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: %q leaves the root", ErrUnsafeEntry, h.Name)
		}
		total += h.Size
		if maxBytes > 0 && total > maxBytes {
			return fmt.Errorf("bundle exceeds %d bytes", maxBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			mode = 0o755
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, h.Size))
		cerr := f.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
	}
}
