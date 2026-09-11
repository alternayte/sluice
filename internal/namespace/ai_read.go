package namespace

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/snapshot"
)

// FileAt reads one file of a snapshot. It returns false when the file does not exist.
func (s *Service) FileAt(ctx context.Context, snapshotID uuid.UUID, path string) (string, bool, error) {
	m, err := s.Manifest(ctx, s.Pool, snapshotID)
	if err != nil {
		return "", false, err
	}
	e, ok := m[path]
	if !ok {
		return "", false, nil
	}
	b, err := s.readBlob(ctx, e.Hash)
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// HeadFiles lists the files of the head version of a namespace, sorted by path.
func (s *Service) HeadFiles(ctx context.Context, name string) ([]snapshot.Entry, error) {
	ns, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	_, m, err := s.Resolve(ctx, ns, nil)
	if err != nil {
		return nil, err
	}
	out := make([]snapshot.Entry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// DiffSnapshots returns the unified diff of the files of two snapshots. Binary files get
// one line each.
func (s *Service) DiffSnapshots(ctx context.Context, from, to uuid.UUID) (string, error) {
	a, err := s.Manifest(ctx, s.Pool, from)
	if err != nil {
		return "", err
	}
	b, err := s.Manifest(ctx, s.Pool, to)
	if err != nil {
		return "", err
	}
	paths := map[string]bool{}
	for p := range a {
		paths[p] = true
	}
	for p := range b {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	var out strings.Builder
	for _, p := range sorted {
		ea, inA := a[p]
		eb, inB := b[p]
		if inA && inB && ea.Hash == eb.Hash {
			continue
		}
		var oldB, newB []byte
		if inA {
			if oldB, err = s.readBlob(ctx, ea.Hash); err != nil {
				return "", err
			}
		}
		if inB {
			if newB, err = s.readBlob(ctx, eb.Hash); err != nil {
				return "", err
			}
		}
		if !isText(oldB) || !isText(newB) {
			fmt.Fprintf(&out, "Binary file %s changed\n", p)
			continue
		}
		out.WriteString(UnifiedDiff(p, "a", "b", string(oldB), string(newB)))
	}
	return out.String(), nil
}
