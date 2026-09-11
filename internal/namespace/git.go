package namespace

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/namespace/namespacedb"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/snapshot"
)

// GitFile is one regular file of a git tree.
type GitFile struct {
	Path       string
	Content    []byte
	Executable bool
}

// CreateGitNamespace creates a git namespace that a git source owns, in tx (REQ-GIT-001, D-06).
func (s *Service) CreateGitNamespace(ctx context.Context, tx pgx.Tx, name string, sourceID uuid.UUID) (uuid.UUID, error) {
	if !flow.ValidNamespaceName(name) {
		return uuid.Nil, httpx.Validation(httpx.FieldError{Field: "mappings.namespace",
			Message: "lower case letters, digits and hyphens in dot-separated parts, at most 128 characters"})
	}
	id := newID()
	if err := namespacedb.New(tx).InsertNamespace(ctx, namespacedb.InsertNamespaceParams{ID: id, Name: name, SourceType: "git",
		GitSourceID: &sourceID, CreatedAt: s.Clock.Now()}); err != nil {
		return uuid.Nil, err
	}
	return id, s.Audit.Record(ctx, tx, audit.Event{Action: "namespace.create", TargetType: "namespace", TargetID: name,
		Details: map[string]any{"source_type": "git"}})
}

// CommitGit creates a snapshot of a git namespace from the files of one commit, moves the
// head and refreshes flows and triggers. It returns false and changes nothing when the
// manifest equals the head (REQ-GIT-002).
func (s *Service) CommitGit(ctx context.Context, nsID uuid.UUID, sha, message string, files []GitFile) (bool, error) {
	uploads := map[string][]byte{}
	next := snapshot.Manifest{}
	for _, f := range files {
		if int64(len(f.Content)) > s.MaxFileBytes {
			return false, fmt.Errorf("%s has %d bytes, the limit is %d", f.Path, len(f.Content), s.MaxFileBytes)
		}
		h := ContentHash(f.Content)
		uploads[h] = f.Content
		next[f.Path] = snapshot.Entry{Path: f.Path, Hash: h, Size: int64(len(f.Content)), Executable: f.Executable}
	}
	if total := next.Total(); total > s.MaxBundleBytes {
		return false, fmt.Errorf("the snapshot has %d bytes, the limit is %d", total, s.MaxBundleBytes)
	}
	if err := s.uploadBlobs(ctx, uploads); err != nil {
		return false, err
	}
	created := false
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := namespacedb.New(tx)
		locked, err := q.LockNamespace(ctx, nsID)
		if err != nil {
			return err
		}
		if locked.DeletedAt != nil || locked.SourceType != "git" {
			return ErrNotFound
		}
		if locked.HeadSnapshotID != nil {
			sn, err := q.GetSnapshot(ctx, *locked.HeadSnapshotID)
			if err != nil {
				return err
			}
			if sn.ManifestHash == next.Hash() {
				return nil
			}
		}
		if err := s.lockFileObjects(ctx, q, next); err != nil {
			return err
		}
		snap := namespacedb.Snapshot{ID: newID(), NamespaceID: nsID, GitSha: sha, ManifestHash: next.Hash(), Message: message, CreatedAt: s.Clock.Now()}
		if err := s.insertSnapshot(ctx, tx, snap, next); err != nil {
			return err
		}
		flowSrc, err := s.flowFiles(ctx, next, uploads)
		if err != nil {
			return err
		}
		if err := s.SyncFlows(ctx, tx, nsID, snap.ID, flowSrc); err != nil {
			return err
		}
		created = true
		return s.Audit.Record(ctx, tx, audit.Event{Action: "file.sync", TargetType: "namespace", TargetID: locked.Name,
			Details: map[string]any{"git_sha": sha}})
	})
	return created, err
}

// lockFileObjects takes a share lock on the file objects of a manifest, so that storage GC
// cannot delete content that the new snapshot references (DI-17).
func (s *Service) lockFileObjects(ctx context.Context, q *namespacedb.Queries, m snapshot.Manifest) error {
	hashes := map[string]bool{}
	for _, e := range m {
		hashes[e.Hash] = true
	}
	list := make([]string, 0, len(hashes))
	for h := range hashes {
		list = append(list, h)
	}
	locks, err := q.ShareLockFileObjects(ctx, list)
	if err != nil {
		return err
	}
	if len(locks) != len(list) {
		return fmt.Errorf("file objects are missing, retry the save")
	}
	return nil
}
