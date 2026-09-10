// Package storage implements object storage with the drivers postgres, fs, s3 and
// azblob behind one streaming interface (REQ-STO-001, D-11).
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("storage: object not found")

// Info describes one stored object.
type Info struct {
	Key     string
	Size    int64
	ModTime time.Time
}

// Store is the storage interface. All reads and writes stream (REQ-STO-004).
type Store interface {
	// Put writes the object from r and returns the number of bytes written.
	// An existing object is replaced.
	Put(ctx context.Context, key string, r io.Reader, contentType string) (int64, error)
	// Get opens the object. The caller closes the reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Stat returns object metadata or ErrNotFound.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete removes the object. A missing key is not an error.
	Delete(ctx context.Context, key string) error
	// List calls fn for each object whose key starts with prefix.
	List(ctx context.Context, prefix string, fn func(Info) error) error
	// Driver returns the driver name.
	Driver() string
	// Close releases resources.
	Close() error
}

// Key layout of REQ-STO-001.

// FileKey is the key of a content-addressed file object.
func FileKey(sha256hex string) string { return "files/sha256/" + sha256hex }

// BundleKey is the key of a snapshot bundle.
func BundleKey(manifestHash string) string { return "bundles/" + manifestHash + ".tar.gz" }

// LogKey is the key of an archived task run log.
func LogKey(executionID, taskRunID string) string {
	return "logs/" + executionID + "/" + taskRunID + ".ndjson.gz"
}

// ArtifactKey is the key of an artifact.
func ArtifactKey(executionID, taskRunID, name string) string {
	return "artifacts/" + executionID + "/" + taskRunID + "/" + name
}

// ValidKey rejects keys that could escape a prefix or a directory root.
func ValidKey(key string) error {
	if key == "" || len(key) > 1024 || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return fmt.Errorf("storage: invalid key %q", key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("storage: invalid key %q", key)
		}
	}
	return nil
}

// RoundTrip writes, reads and deletes a small object. /readyz uses it (REQ-CORE-004).
func RoundTrip(ctx context.Context, s Store, key string) error {
	want := "sluice-health-" + time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.Put(ctx, key, strings.NewReader(want), "text/plain"); err != nil {
		return fmt.Errorf("put: %w", err)
	}
	r, err := s.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(r, 1024))
	_ = r.Close()
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if string(b) != want {
		return errors.New("read back different content")
	}
	if err := s.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}
