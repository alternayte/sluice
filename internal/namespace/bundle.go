package namespace

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/storage"
)

// EnsureBundle returns the storage key of the tar.gz bundle of a manifest. It builds
// and stores the bundle when it does not exist (D-05).
func (s *Service) EnsureBundle(ctx context.Context, m Manifest) (string, error) {
	hash := m.Hash()
	key := storage.BundleKey(hash)
	q := dbq.New(s.Pool)
	now := s.Clock.Now()
	if b, err := q.GetBundle(ctx, hash); err == nil {
		if _, err := s.Store.Stat(ctx, b.StorageKey); err == nil {
			_ = q.TouchBundle(ctx, dbq.TouchBundleParams{ManifestHash: hash, LastUsedAt: now})
			return b.StorageKey, nil
		}
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(s.writeBundle(ctx, pw, m)) }()
	n, err := s.Store.Put(ctx, key, pr, "application/gzip")
	_ = pr.Close()
	if err != nil {
		return "", err
	}
	if err := q.InsertBundle(ctx, dbq.InsertBundleParams{ManifestHash: hash, StorageKey: key, Size: n, LastUsedAt: now}); err != nil {
		return "", err
	}
	return key, nil
}

func (s *Service) writeBundle(ctx context.Context, w io.Writer, m Manifest) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	mtime := time.Unix(0, 0).UTC()
	for _, p := range m.Paths() {
		e := m[p]
		mode := int64(0o644)
		if e.Executable {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: mode, Size: e.Size, ModTime: mtime, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return err
		}
		r, err := s.Store.Get(ctx, storage.FileKey(e.Hash))
		if err != nil {
			return fmt.Errorf("bundle %s: %w", p, err)
		}
		_, err = io.Copy(tw, r)
		_ = r.Close()
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

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
