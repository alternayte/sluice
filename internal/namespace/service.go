package namespace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pmezard/go-difflib/difflib"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/storage"
)

// Errors of the namespace service.
var (
	ErrNotFound          = httpx.Errorf(http.StatusNotFound, "namespace_not_found", "namespace not found")
	ErrFileNotFound      = httpx.Errorf(http.StatusNotFound, "file_not_found", "file not found")
	ErrVersionNotFound   = httpx.Errorf(http.StatusNotFound, "version_not_found", "version not found")
	ErrReadOnly          = httpx.Errorf(http.StatusConflict, "namespace_read_only", "git namespaces are read-only: push changes to a branch")
	ErrExists            = httpx.Errorf(http.StatusConflict, "namespace_exists", "a namespace with this name exists")
	ErrVersionConflict   = httpx.Errorf(http.StatusConflict, "version_conflict", "the namespace changed since the base version")
	ErrExecutionsRunning = httpx.Errorf(http.StatusConflict, "executions_running", "executions of this namespace are running")
	ErrNoChanges         = httpx.Validation(httpx.FieldError{Field: "changes", Message: "the changes do not change any file"})
)

// Service manages namespaces and their files.
type Service struct {
	Pool           *pgxpool.Pool
	Store          storage.Store
	Clock          clock.Clock
	Audit          *audit.Writer
	Log            *slog.Logger
	MaxFileBytes   int64
	MaxBundleBytes int64
}

func newID() uuid.UUID {
	id, _ := uuid.NewV7()
	return id
}

// Get returns a namespace by name.
func (s *Service) Get(ctx context.Context, name string) (dbq.GetNamespaceRow, error) {
	ns, err := dbq.New(s.Pool).GetNamespace(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return ns, ErrNotFound
	}
	return ns, err
}

// Create creates a managed namespace (REQ-NS-001).
func (s *Service) Create(ctx context.Context, name, description string) (dbq.GetNamespaceRow, error) {
	if !flow.ValidNamespaceName(name) {
		return dbq.GetNamespaceRow{}, httpx.Validation(httpx.FieldError{Field: "name",
			Message: "lower case letters, digits and hyphens in dot-separated parts, at most 128 characters"})
	}
	id := newID()
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := dbq.New(tx).InsertNamespace(ctx, dbq.InsertNamespaceParams{ID: id, Name: name, SourceType: "managed",
			Description: strings.TrimSpace(description), CreatedAt: s.Clock.Now()}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "namespace.create", TargetType: "namespace", TargetID: name})
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return dbq.GetNamespaceRow{}, ErrExists
	}
	if err != nil {
		return dbq.GetNamespaceRow{}, err
	}
	return s.Get(ctx, name)
}

// TreeNode is one namespace in the tree API. Implicit nodes exist only as parents.
type TreeNode struct {
	Name     string
	Parent   string
	Implicit bool
	Row      *dbq.ListNamespacesRow
}

// ParentOf returns the parent namespace name, or "".
func ParentOf(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return ""
}

// Tree lists namespaces with their implicit parents (REQ-NS-001).
func (s *Service) Tree(ctx context.Context) ([]TreeNode, error) {
	rows, err := dbq.New(s.Pool).ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	nodes := map[string]*TreeNode{}
	for i := range rows {
		r := rows[i]
		nodes[r.Name] = &TreeNode{Name: r.Name, Parent: ParentOf(r.Name), Row: &r}
		for p := ParentOf(r.Name); p != ""; p = ParentOf(p) {
			if _, ok := nodes[p]; !ok {
				nodes[p] = &TreeNode{Name: p, Parent: ParentOf(p), Implicit: true}
			}
		}
	}
	out := make([]TreeNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SnapshotInfo is a snapshot with its author.
type SnapshotInfo struct {
	dbq.Snapshot
	Author    string
	FileCount int
}

// Manifest loads the files of a snapshot.
func (s *Service) Manifest(ctx context.Context, db dbq.DBTX, snapshotID uuid.UUID) (Manifest, error) {
	files, err := dbq.New(db).ListSnapshotFiles(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	m := make(Manifest, len(files))
	for _, f := range files {
		m[f.Path] = Entry{Path: f.Path, Hash: f.Hash, Size: f.Size, Executable: f.Executable}
	}
	return m, nil
}

// Resolve returns the snapshot of a version (nil: head) and its manifest. A namespace
// without a snapshot has an empty manifest.
func (s *Service) Resolve(ctx context.Context, ns dbq.GetNamespaceRow, version *int) (*SnapshotInfo, Manifest, error) {
	q := dbq.New(s.Pool)
	var info *SnapshotInfo
	switch {
	case version != nil:
		sn, err := q.GetSnapshotByVersion(ctx, dbq.GetSnapshotByVersionParams{NamespaceID: ns.ID, Version: int32ptr(*version)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrVersionNotFound
		}
		if err != nil {
			return nil, nil, err
		}
		info = &SnapshotInfo{Snapshot: dbq.Snapshot{ID: sn.ID, NamespaceID: sn.NamespaceID, Version: sn.Version, GitSha: sn.GitSha,
			ManifestHash: sn.ManifestHash, Message: sn.Message, CreatedBy: sn.CreatedBy, CreatedAt: sn.CreatedAt}, Author: deref(sn.AuthorEmail)}
	case ns.HeadSnapshotID != nil:
		sn, err := q.GetSnapshot(ctx, *ns.HeadSnapshotID)
		if err != nil {
			return nil, nil, err
		}
		info = &SnapshotInfo{Snapshot: dbq.Snapshot{ID: sn.ID, NamespaceID: sn.NamespaceID, Version: sn.Version, GitSha: sn.GitSha,
			ManifestHash: sn.ManifestHash, Message: sn.Message, CreatedBy: sn.CreatedBy, CreatedAt: sn.CreatedAt}, Author: deref(sn.AuthorEmail)}
	default:
		return nil, Manifest{}, nil
	}
	m, err := s.Manifest(ctx, s.Pool, info.ID)
	if err != nil {
		return nil, nil, err
	}
	info.FileCount = len(m)
	return info, m, nil
}

func int32ptr(v int) *int32 {
	x := int32(v)
	return &x
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ReadFile opens one file of a version.
func (s *Service) ReadFile(ctx context.Context, name, path string, version *int) (io.ReadCloser, Entry, error) {
	ns, err := s.Get(ctx, name)
	if err != nil {
		return nil, Entry{}, err
	}
	_, m, err := s.Resolve(ctx, ns, version)
	if err != nil {
		return nil, Entry{}, err
	}
	e, ok := m[path]
	if !ok {
		return nil, Entry{}, ErrFileNotFound
	}
	r, err := s.Store.Get(ctx, storage.FileKey(e.Hash))
	if err != nil {
		return nil, Entry{}, err
	}
	return r, e, nil
}

func (s *Service) readBlob(ctx context.Context, hash string) ([]byte, error) {
	r, err := s.Store.Get(ctx, storage.FileKey(hash))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// Change is one file change of a save (REQ-NS-002).
type Change struct {
	Op         string // put, delete, rename
	Path       string
	NewPath    string
	Content    []byte
	Executable *bool
}

func pathError(field string, err error) error {
	return httpx.Validation(httpx.FieldError{Field: field, Message: err.Error()})
}

// ErrTooLarge returns a 413 error.
func errTooLarge(code, format string, args ...any) error {
	return httpx.Errorf(http.StatusRequestEntityTooLarge, code, format, args...)
}

// Save applies changes and creates a new version with author and message.
func (s *Service) Save(ctx context.Context, name string, changes []Change, message string, baseVersion *int) (*SnapshotInfo, error) {
	ns, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if ns.SourceType != "managed" {
		return nil, ErrReadOnly
	}
	uploads := map[string][]byte{}
	for i, c := range changes {
		f := fmt.Sprintf("changes[%d]", i)
		if err := flow.ValidPath(c.Path); err != nil {
			return nil, pathError(f+".path", err)
		}
		switch c.Op {
		case "put":
			if int64(len(c.Content)) > s.MaxFileBytes {
				return nil, errTooLarge("file_too_large", "%s has %d bytes, the limit is %d", c.Path, len(c.Content), s.MaxFileBytes)
			}
			uploads[ContentHash(c.Content)] = c.Content
		case "rename":
			if err := flow.ValidPath(c.NewPath); err != nil {
				return nil, pathError(f+".new_path", err)
			}
		case "delete":
		default:
			return nil, httpx.Validation(httpx.FieldError{Field: f + ".op", Message: "must be put, delete or rename"})
		}
	}
	mutate := func(head Manifest) (Manifest, error) {
		m := head.Clone()
		for i, c := range changes {
			f := fmt.Sprintf("changes[%d]", i)
			switch c.Op {
			case "put":
				exec := false
				if old, ok := m[c.Path]; ok {
					exec = old.Executable
				}
				if c.Executable != nil {
					exec = *c.Executable
				}
				m[c.Path] = Entry{Path: c.Path, Hash: ContentHash(c.Content), Size: int64(len(c.Content)), Executable: exec}
			case "delete":
				if _, ok := m[c.Path]; !ok {
					return nil, httpx.Validation(httpx.FieldError{Field: f + ".path", Message: "file does not exist"})
				}
				delete(m, c.Path)
			case "rename":
				e, ok := m[c.Path]
				if !ok {
					return nil, httpx.Validation(httpx.FieldError{Field: f + ".path", Message: "file does not exist"})
				}
				if _, exists := m[c.NewPath]; exists {
					return nil, httpx.Validation(httpx.FieldError{Field: f + ".new_path", Message: "a file with this path exists"})
				}
				delete(m, c.Path)
				e.Path = c.NewPath
				if c.Executable != nil {
					e.Executable = *c.Executable
				}
				m[c.NewPath] = e
			}
		}
		return m, nil
	}
	return s.commit(ctx, ns, uploads, mutate, message, baseVersion, auditDetails(changes))
}

func auditDetails(changes []Change) map[string]any {
	var list []string
	for _, c := range changes {
		switch c.Op {
		case "rename":
			list = append(list, c.Op+" "+c.Path+" -> "+c.NewPath)
		default:
			list = append(list, c.Op+" "+c.Path)
		}
	}
	return map[string]any{"changes": list}
}

// Revert creates a new version with the files of an old version (REQ-NS-003).
func (s *Service) Revert(ctx context.Context, name string, version int, message string) (*SnapshotInfo, error) {
	ns, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if ns.SourceType != "managed" {
		return nil, ErrReadOnly
	}
	_, target, err := s.Resolve(ctx, ns, &version)
	if err != nil {
		return nil, err
	}
	if message == "" {
		message = fmt.Sprintf("Revert to version %d", version)
	}
	return s.commit(ctx, ns, nil, func(Manifest) (Manifest, error) { return target.Clone(), nil }, message, nil,
		map[string]any{"revert_to": version})
}

// commit uploads new content, then creates the snapshot, moves the head and syncs flows
// in one transaction.
func (s *Service) commit(ctx context.Context, ns dbq.GetNamespaceRow, uploads map[string][]byte, mutate func(Manifest) (Manifest, error),
	message string, baseVersion *int, details map[string]any) (*SnapshotInfo, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, httpx.Validation(httpx.FieldError{Field: "message", Message: "a message is required"})
	}
	if err := s.uploadBlobs(ctx, uploads); err != nil {
		return nil, err
	}
	var author *uuid.UUID
	authorEmail := ""
	if p := auth.FromContext(ctx); p != nil {
		id := p.UserID
		author = &id
		authorEmail = p.Email
	}
	var out *SnapshotInfo
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbq.New(tx)
		locked, err := q.LockNamespace(ctx, ns.ID)
		if err != nil {
			return err
		}
		if locked.DeletedAt != nil {
			return ErrNotFound
		}
		head := Manifest{}
		var headVersion int
		if locked.HeadSnapshotID != nil {
			sn, err := q.GetSnapshot(ctx, *locked.HeadSnapshotID)
			if err != nil {
				return err
			}
			if sn.Version != nil {
				headVersion = int(*sn.Version)
			}
			if head, err = s.Manifest(ctx, tx, sn.ID); err != nil {
				return err
			}
		}
		if baseVersion != nil && *baseVersion != headVersion {
			return ErrVersionConflict
		}
		next, err := mutate(head)
		if err != nil {
			return err
		}
		if locked.HeadSnapshotID != nil && next.Hash() == head.Hash() {
			return ErrNoChanges
		}
		if total := next.Total(); total > s.MaxBundleBytes {
			return errTooLarge("snapshot_too_large", "the snapshot has %d bytes, the limit is %d", total, s.MaxBundleBytes)
		}
		hashes := map[string]bool{}
		for _, e := range next {
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
		version, err := q.NextSnapshotVersion(ctx, ns.ID)
		if err != nil {
			return err
		}
		snap := dbq.Snapshot{ID: newID(), NamespaceID: ns.ID, Version: &version, ManifestHash: next.Hash(), Message: message,
			CreatedBy: author, CreatedAt: s.Clock.Now()}
		if err := s.insertSnapshot(ctx, tx, snap, next); err != nil {
			return err
		}
		files, err := s.flowFiles(ctx, next, uploads)
		if err != nil {
			return err
		}
		if err := s.SyncFlows(ctx, tx, ns.ID, snap.ID, files); err != nil {
			return err
		}
		d := map[string]any{"version": version, "message": message}
		for k, v := range details {
			d[k] = v
		}
		if err := s.Audit.Record(ctx, tx, audit.Event{Action: "file.save", TargetType: "namespace", TargetID: ns.Name, Details: d}); err != nil {
			return err
		}
		out = &SnapshotInfo{Snapshot: snap, Author: authorEmail, FileCount: len(next)}
		return nil
	})
	return out, err
}

// insertSnapshot writes the snapshot row, its files and moves the namespace head.
func (s *Service) insertSnapshot(ctx context.Context, tx pgx.Tx, snap dbq.Snapshot, m Manifest) error {
	q := dbq.New(tx)
	if err := q.InsertSnapshot(ctx, dbq.InsertSnapshotParams(snap)); err != nil {
		return err
	}
	p := dbq.InsertSnapshotFilesParams{SnapshotID: snap.ID}
	for _, path := range m.Paths() {
		e := m[path]
		p.Paths = append(p.Paths, e.Path)
		p.Hashes = append(p.Hashes, e.Hash)
		p.Sizes = append(p.Sizes, e.Size)
		p.Executables = append(p.Executables, e.Executable)
	}
	if len(p.Paths) > 0 {
		if err := q.InsertSnapshotFiles(ctx, p); err != nil {
			return err
		}
	}
	return q.SetNamespaceHead(ctx, dbq.SetNamespaceHeadParams{ID: snap.NamespaceID, HeadSnapshotID: &snap.ID})
}

// uploadBlobs stores new content objects (deduplicated by hash, REQ-STO-005).
func (s *Service) uploadBlobs(ctx context.Context, uploads map[string][]byte) error {
	q := dbq.New(s.Pool)
	for hash, content := range uploads {
		exists, err := q.FileObjectExists(ctx, hash)
		if err != nil {
			return err
		}
		if exists {
			if _, err := s.Store.Stat(ctx, storage.FileKey(hash)); err == nil {
				continue
			}
		}
		if _, err := s.Store.Put(ctx, storage.FileKey(hash), bytes.NewReader(content), "application/octet-stream"); err != nil {
			return err
		}
		if err := q.InsertFileObject(ctx, dbq.InsertFileObjectParams{Hash: hash, Size: int64(len(content)), CreatedAt: s.Clock.Now()}); err != nil {
			return err
		}
	}
	return nil
}

// flowFiles returns all paths of m with content for flow files and namespace.yaml.
func (s *Service) flowFiles(ctx context.Context, m Manifest, pending map[string][]byte) (map[string][]byte, error) {
	files := make(map[string][]byte, len(m))
	for p, e := range m {
		if !needsContent(p) {
			files[p] = nil
			continue
		}
		if b, ok := pending[e.Hash]; ok {
			files[p] = b
			continue
		}
		b, err := s.readBlob(ctx, e.Hash)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		files[p] = b
	}
	return files, nil
}

// Versions lists snapshots newest first.
func (s *Service) Versions(ctx context.Context, name string, limit int) ([]SnapshotInfo, error) {
	ns, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	rows, err := dbq.New(s.Pool).ListSnapshots(ctx, dbq.ListSnapshotsParams{NamespaceID: ns.ID, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotInfo, 0, len(rows))
	for _, r := range rows {
		var n int
		_ = s.Pool.QueryRow(ctx, "SELECT count(*) FROM snapshot_files WHERE snapshot_id = $1", r.ID).Scan(&n)
		out = append(out, SnapshotInfo{Snapshot: dbq.Snapshot{ID: r.ID, NamespaceID: r.NamespaceID, Version: r.Version, GitSha: r.GitSha,
			ManifestHash: r.ManifestHash, Message: r.Message, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}, Author: deref(r.AuthorEmail), FileCount: n})
	}
	return out, nil
}

// FileDiff is the difference of one file between two versions.
type FileDiff struct {
	Path   string
	Status string // added, removed, modified
	Binary bool
	Diff   string
}

// Diff compares two versions (REQ-NS-003).
func (s *Service) Diff(ctx context.Context, name string, from, to int) ([]FileDiff, error) {
	ns, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	_, a, err := s.Resolve(ctx, ns, &from)
	if err != nil {
		return nil, err
	}
	_, b, err := s.Resolve(ctx, ns, &to)
	if err != nil {
		return nil, err
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
	var out []FileDiff
	for _, p := range sorted {
		ea, inA := a[p]
		eb, inB := b[p]
		if inA && inB && ea.Hash == eb.Hash {
			continue
		}
		fd := FileDiff{Path: p, Status: "modified"}
		switch {
		case !inA:
			fd.Status = "added"
		case !inB:
			fd.Status = "removed"
		}
		var oldB, newB []byte
		if inA {
			if oldB, err = s.readBlob(ctx, ea.Hash); err != nil {
				return nil, err
			}
		}
		if inB {
			if newB, err = s.readBlob(ctx, eb.Hash); err != nil {
				return nil, err
			}
		}
		if !isText(oldB) || !isText(newB) {
			fd.Binary = true
		} else {
			fd.Diff = UnifiedDiff(p, fmt.Sprintf("v%d", from), fmt.Sprintf("v%d", to), string(oldB), string(newB))
		}
		out = append(out, fd)
	}
	return out, nil
}

// UnifiedDiff returns a unified diff of two texts.
func UnifiedDiff(path, fromLabel, toLabel, a, b string) string {
	d, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(a), B: difflib.SplitLines(b),
		FromFile: fromLabel + "/" + path, ToFile: toLabel + "/" + path, Context: 3,
	})
	return d
}

func isText(b []byte) bool {
	return utf8.Valid(b) && !bytes.ContainsRune(b, 0)
}

// Delete deletes a managed namespace. It fails while executions run (REQ-NS-008).
func (s *Service) Delete(ctx context.Context, name string) error {
	ns, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	if ns.SourceType != "managed" {
		return ErrReadOnly
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbq.New(tx)
		if _, err := q.LockNamespace(ctx, ns.ID); err != nil {
			return err
		}
		n, err := q.CountActiveExecutions(ctx, ns.ID)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrExecutionsRunning
		}
		now := s.Clock.Now()
		if err := q.SoftDeleteNamespace(ctx, dbq.SoftDeleteNamespaceParams{ID: ns.ID, DeletedAt: &now}); err != nil {
			return err
		}
		flows, err := q.ListNamespaceFlows(ctx, ns.ID)
		if err != nil {
			return err
		}
		for _, f := range flows {
			if err := q.MarkFlowDeleted(ctx, dbq.MarkFlowDeletedParams{ID: f.ID, DeletedAt: &now}); err != nil {
				return err
			}
			if err := q.DeactivateFlowTriggers(ctx, f.ID); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "namespace.delete", TargetType: "namespace", TargetID: name})
	})
}

// ValidateFile validates proposed content of one file against the head snapshot.
func (s *Service) ValidateFile(ctx context.Context, name, path, content string) (string, string, []flow.Issue, error) {
	if err := flow.ValidPath(path); err != nil {
		return "", "", nil, pathError("path", err)
	}
	ns, err := s.Get(ctx, name)
	if err != nil {
		return "", "", nil, err
	}
	_, m, err := s.Resolve(ctx, ns, nil)
	if err != nil {
		return "", "", nil, err
	}
	files, err := s.flowFiles(ctx, m, nil)
	if err != nil {
		return "", "", nil, err
	}
	files[path] = []byte(content)
	res := flow.ValidateNamespace(files)
	if path == flow.NamespaceFileName {
		return "namespace", "", res.NamespaceIssues, nil
	}
	if !flow.IsFlowFile(path) {
		return "other", "", nil, nil
	}
	for _, pf := range res.Flows {
		if pf.Path == path {
			id := ""
			if pf.Flow != nil {
				id = pf.Flow.ID
			}
			return "flow", id, pf.Issues, nil
		}
	}
	return "flow", "", nil, nil
}
