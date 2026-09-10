package namespace

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/snapshot"
	"github.com/alternayte/sluice/internal/storage"
)

// EnsureBundle returns the storage key of the tar.gz bundle of a manifest. It builds
// and stores the bundle when it does not exist (D-05).
func (s *Service) EnsureBundle(ctx context.Context, m snapshot.Manifest) (string, error) {
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

func (s *Service) writeBundle(ctx context.Context, w io.Writer, m snapshot.Manifest) error {
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
