// Package gitsync syncs git sources into git namespaces and pushes edits to new branches
// (REQ-GIT-001 to REQ-GIT-007).
package gitsync

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/gitsync/gitsyncdb"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Limits of the git feature.
const (
	// MinPollInterval is the shortest poll interval in seconds (REQ-GIT-001).
	MinPollInterval = 15
	// DefaultPollInterval is the default poll interval in seconds.
	DefaultPollInterval = 60
	// MaxWebhookBody is the git webhook body limit.
	MaxWebhookBody = 1 << 20
	// RunsShown is the number of sync runs that the API returns (REQ-GIT-004).
	RunsShown = 50
)

// Errors of the git feature.
var (
	ErrSourceNotFound    = httpx.Errorf(http.StatusNotFound, "git_source_not_found", "git source not found")
	ErrSourceExists      = httpx.Errorf(http.StatusConflict, "git_source_exists", "a git source with this name exists")
	ErrNamespaceManaged  = httpx.Errorf(http.StatusConflict, "namespace_managed", "a managed namespace cannot be mapped to a git source")
	ErrNamespaceMapped   = httpx.Errorf(http.StatusConflict, "namespace_mapped", "another git source maps this namespace")
	ErrNotGitNamespace   = httpx.Errorf(http.StatusNotFound, "git_source_not_found", "no git source maps this namespace")
	ErrNotSynced         = httpx.Errorf(http.StatusConflict, "not_synced", "the git source has not synced yet")
	ErrInvalidSignature  = httpx.Errorf(http.StatusUnauthorized, "invalid_signature", "the webhook signature or token is not valid")
	ErrWebhookBodyTooBig = httpx.Errorf(http.StatusRequestEntityTooLarge, "body_too_large", "the webhook body is larger than 1 MiB")
)

// File is one regular file of a git tree.
type File struct {
	Path       string
	Content    []byte
	Executable bool
}

// Namespaces is what gitsync needs from the namespace feature. internal/app adapts it.
type Namespaces interface {
	CreateGitNamespace(ctx context.Context, tx pgx.Tx, name string, sourceID uuid.UUID) (uuid.UUID, error)
	CommitGit(ctx context.Context, nsID uuid.UUID, sha, message string, files []File) (bool, error)
}

// Secrets resolves global secret keys: credentials and webhook secrets (REQ-GIT-001).
type Secrets interface {
	Global(ctx context.Context, key string) (string, error)
}

// Service manages git sources, syncs them and pushes edits.
type Service struct {
	Pool       *pgxpool.Pool
	Clock      clock.Clock
	Audit      *audit.Writer
	Log        *slog.Logger
	Namespaces Namespaces
	Secrets    Secrets
	// Holder is the lease holder name of this instance.
	Holder string
	// PublicURL is the external base URL for webhook URLs.
	PublicURL string
	// TempRoot holds the temporary clone directories. Default <os temp>/sluice-git.
	TempRoot string
	// MaxFileBytes is the file limit (SLUICE_MAX_FILE_BYTES).
	MaxFileBytes int64
}

func (s *Service) tempRoot() (string, error) {
	root := s.TempRoot
	if root == "" {
		root = filepath.Join(os.TempDir(), "sluice-git")
	}
	return root, os.MkdirAll(root, 0o700)
}

func (s *Service) maxFile() int64 {
	if s.MaxFileBytes > 0 {
		return s.MaxFileBytes
	}
	return 10 << 20
}

// Mapping maps a repository path to a namespace.
type Mapping struct {
	RepoPath  string
	Namespace string
}

// SourceInput is a git source write.
type SourceInput struct {
	Name                string
	RepoURL             string
	Branch              string
	AuthType            string
	CredentialSecretKey string
	KnownHosts          string
	PollInterval        int
	WebhookSecretKey    string
	Mappings            []Mapping
}

// Source is a git source with its mappings.
type Source struct {
	gitsyncdb.GitSource
	Mappings []Mapping
}

var scpLike = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:.+$`)

// validate checks a source write (REQ-GIT-001, DI-33).
func validate(in *SourceInput) error {
	fe := func(field, msg string) error { return httpx.Validation(httpx.FieldError{Field: field, Message: msg}) }
	in.RepoURL = strings.TrimSpace(in.RepoURL)
	isSSH := strings.HasPrefix(in.RepoURL, "ssh://") || scpLike.MatchString(in.RepoURL)
	isHTTP := false
	if u, err := url.Parse(in.RepoURL); err == nil && u.Host != "" {
		switch u.Scheme {
		case "https":
			isHTTP = true
		case "http":
			// Plain http only for loopback hosts, for local development and the test fixture.
			host := u.Hostname()
			ip := net.ParseIP(host)
			if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				return fe("repo_url", "use https or ssh; plain http is allowed only for loopback hosts")
			}
			isHTTP = true
		}
	}
	if !isSSH && !isHTTP {
		return fe("repo_url", "must be an https or ssh repository URL")
	}
	if strings.TrimSpace(in.Branch) == "" || strings.ContainsAny(in.Branch, " ~^:?*[\\") || strings.Contains(in.Branch, "..") {
		return fe("branch", "must be a valid branch name")
	}
	switch in.AuthType {
	case "none":
		in.CredentialSecretKey = ""
	case "https_token":
		if !isHTTP {
			return fe("auth_type", "https_token needs an https repository URL")
		}
	case "ssh_key":
		if !isSSH {
			return fe("auth_type", "ssh_key needs an ssh repository URL")
		}
	default:
		return fe("auth_type", "must be none, https_token or ssh_key")
	}
	if in.AuthType != "none" && in.CredentialSecretKey == "" {
		return fe("credential_secret_key", "a global secret key with the credential is required")
	}
	if in.PollInterval == 0 {
		in.PollInterval = DefaultPollInterval
	}
	if in.PollInterval < MinPollInterval {
		return fe("poll_interval", fmt.Sprintf("must be at least %d seconds", MinPollInterval))
	}
	if len(in.Mappings) == 0 {
		return fe("mappings", "at least one mapping is required")
	}
	seenPath, seenNS := map[string]bool{}, map[string]bool{}
	for i, m := range in.Mappings {
		p := strings.Trim(m.RepoPath, "/")
		if p == "." {
			p = ""
		}
		if p != "" {
			if err := flow.ValidPath(p); err != nil {
				return fe(fmt.Sprintf("mappings[%d].repo_path", i), err.Error())
			}
		}
		if seenPath[p] {
			return fe(fmt.Sprintf("mappings[%d].repo_path", i), "duplicate repo path")
		}
		if seenNS[m.Namespace] {
			return fe(fmt.Sprintf("mappings[%d].namespace", i), "duplicate namespace")
		}
		seenPath[p], seenNS[m.Namespace] = true, true
		in.Mappings[i].RepoPath = p
	}
	return nil
}

// Create adds a git source and its mappings. A mapped namespace that does not exist is
// created as a git namespace (REQ-GIT-001, REQ-GIT-007).
func (s *Service) Create(ctx context.Context, in SourceInput) (Source, error) {
	if err := validate(&in); err != nil {
		return Source{}, err
	}
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := gitsyncdb.New(tx)
		if _, err := q.GetSourceByName(ctx, in.Name); err == nil {
			return ErrSourceExists
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		id, _ = uuid.NewV7()
		if _, err := q.InsertSource(ctx, gitsyncdb.InsertSourceParams{ID: id, Name: in.Name, RepoUrl: in.RepoURL, Branch: in.Branch,
			AuthType: in.AuthType, CredentialSecretKey: in.CredentialSecretKey, KnownHosts: in.KnownHosts, PollInterval: int32(in.PollInterval),
			WebhookSecretKey: in.WebhookSecretKey, Now: s.Clock.Now()}); err != nil {
			return err
		}
		if err := s.setMappings(ctx, tx, id, in.Mappings); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "git_source.create", TargetType: "git_source", TargetID: in.Name,
			Details: map[string]any{"repo_url": in.RepoURL, "branch": in.Branch}})
	})
	if err != nil {
		return Source{}, err
	}
	return s.Get(ctx, id)
}

// Update replaces the settings and mappings of a source. A namespace that loses its
// mapping stays a read-only git namespace without a source.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in SourceInput) (Source, error) {
	if err := validate(&in); err != nil {
		return Source{}, err
	}
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := gitsyncdb.New(tx)
		src, err := q.GetSource(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSourceNotFound
		}
		if err != nil {
			return err
		}
		if _, err := q.UpdateSource(ctx, gitsyncdb.UpdateSourceParams{ID: id, RepoUrl: in.RepoURL, Branch: in.Branch, AuthType: in.AuthType,
			CredentialSecretKey: in.CredentialSecretKey, KnownHosts: in.KnownHosts, PollInterval: int32(in.PollInterval),
			WebhookSecretKey: in.WebhookSecretKey}); err != nil {
			return err
		}
		if err := s.releaseNamespaces(ctx, q, id); err != nil {
			return err
		}
		if err := s.setMappings(ctx, tx, id, in.Mappings); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "git_source.update", TargetType: "git_source", TargetID: src.Name})
	})
	if err != nil {
		return Source{}, err
	}
	return s.Get(ctx, id)
}

// Delete removes a source. Its namespaces stay as read-only git namespaces with their history.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := gitsyncdb.New(tx)
		src, err := q.GetSource(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSourceNotFound
		}
		if err != nil {
			return err
		}
		if err := s.releaseNamespaces(ctx, q, id); err != nil {
			return err
		}
		if err := q.DeleteSource(ctx, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "git_source.delete", TargetType: "git_source", TargetID: src.Name})
	})
}

// releaseNamespaces removes the mappings of a source and clears the source of their namespaces.
func (s *Service) releaseNamespaces(ctx context.Context, q *gitsyncdb.Queries, id uuid.UUID) error {
	maps, err := q.ListMappings(ctx, id)
	if err != nil {
		return err
	}
	for _, m := range maps {
		if err := q.SetNamespaceSource(ctx, gitsyncdb.SetNamespaceSourceParams{ID: m.NamespaceID}); err != nil {
			return err
		}
	}
	return q.DeleteMappings(ctx, id)
}

// setMappings maps each namespace to the source. A managed namespace or a namespace of
// another source is a conflict (REQ-GIT-007).
func (s *Service) setMappings(ctx context.Context, tx pgx.Tx, id uuid.UUID, maps []Mapping) error {
	q := gitsyncdb.New(tx)
	for _, m := range maps {
		ns, err := q.NamespaceByName(ctx, m.Namespace)
		var nsID uuid.UUID
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if nsID, err = s.Namespaces.CreateGitNamespace(ctx, tx, m.Namespace, id); err != nil {
				return err
			}
		case err != nil:
			return err
		case ns.SourceType != "git":
			return ErrNamespaceManaged.WithDetails(map[string]string{"namespace": m.Namespace})
		case ns.MappedBy != nil && *ns.MappedBy != id:
			return ErrNamespaceMapped.WithDetails(map[string]string{"namespace": m.Namespace})
		default:
			nsID = ns.ID
			if err := q.SetNamespaceSource(ctx, gitsyncdb.SetNamespaceSourceParams{GitSourceID: &id, ID: nsID}); err != nil {
				return err
			}
		}
		if err := q.InsertMapping(ctx, gitsyncdb.InsertMappingParams{GitSourceID: id, RepoPath: m.RepoPath, NamespaceID: nsID}); err != nil {
			return err
		}
	}
	return nil
}

// Get returns a source with its mappings.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Source, error) {
	q := gitsyncdb.New(s.Pool)
	src, err := q.GetSource(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, ErrSourceNotFound
	}
	if err != nil {
		return Source{}, err
	}
	return s.withMappings(ctx, q, src)
}

func (s *Service) withMappings(ctx context.Context, q *gitsyncdb.Queries, src gitsyncdb.GitSource) (Source, error) {
	maps, err := q.ListMappings(ctx, src.ID)
	if err != nil {
		return Source{}, err
	}
	out := Source{GitSource: src, Mappings: []Mapping{}}
	for _, m := range maps {
		out.Mappings = append(out.Mappings, Mapping{RepoPath: m.RepoPath, Namespace: m.Namespace})
	}
	return out, nil
}

// List returns all sources.
func (s *Service) List(ctx context.Context) ([]Source, error) {
	q := gitsyncdb.New(s.Pool)
	rows, err := q.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(rows))
	for _, r := range rows {
		src, err := s.withMappings(ctx, q, r)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, nil
}

// WebhookURL returns the webhook URL of a source.
func (s *Service) WebhookURL(id uuid.UUID) string {
	return strings.TrimRight(s.PublicURL, "/") + "/hooks/git/" + id.String()
}

// RequestSync asks the git-sync leader to sync a source soon (REQ-GIT-004).
func (s *Service) RequestSync(ctx context.Context, id uuid.UUID) error {
	now := s.Clock.Now()
	n, err := gitsyncdb.New(s.Pool).RequestSync(ctx, gitsyncdb.RequestSyncParams{Now: &now, ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSourceNotFound
	}
	return s.Audit.Record(ctx, s.Pool, audit.Event{Action: "git_source.sync", TargetType: "git_source", TargetID: id.String()})
}

// Runs returns the last sync runs of a source, newest first.
func (s *Service) Runs(ctx context.Context, id uuid.UUID) ([]gitsyncdb.GitSyncRun, error) {
	if _, err := gitsyncdb.New(s.Pool).GetSource(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSourceNotFound
	} else if err != nil {
		return nil, err
	}
	return gitsyncdb.New(s.Pool).ListRuns(ctx, gitsyncdb.ListRunsParams{GitSourceID: id, MaxRows: RunsShown})
}

// NamespaceSource returns the source that maps a namespace.
func (s *Service) NamespaceSource(ctx context.Context, namespace string) (gitsyncdb.SourceOfNamespaceRow, error) {
	row, err := gitsyncdb.New(s.Pool).SourceOfNamespace(ctx, namespace)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotGitNamespace
	}
	return row, err
}

// Tick syncs the sources that are due. Only the holder of the git-sync lease claims a
// source: the claim checks the lease in the same statement (REQ-CORE-006).
func (s *Service) Tick(ctx context.Context) error {
	now := s.Clock.Now()
	before := now.Add(-30 * time.Minute)
	if err := gitsyncdb.New(s.Pool).ResetStaleRunning(ctx, &before); err != nil {
		return err
	}
	ids, err := gitsyncdb.New(s.Pool).DueSources(ctx, &now)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.SyncSource(ctx, id); err != nil && s.Log != nil {
			s.Log.Warn("git sync", "source", id, "err", err)
		}
	}
	return nil
}

// SyncSource claims and syncs one source and records a sync run (REQ-GIT-002). A failed
// sync keeps the previous snapshots active.
func (s *Service) SyncSource(ctx context.Context, id uuid.UUID) error {
	q := gitsyncdb.New(s.Pool)
	now := s.Clock.Now()
	n, err := q.ClaimSync(ctx, gitsyncdb.ClaimSyncParams{Now: &now, ID: id, Holder: s.Holder})
	if err != nil || n == 0 {
		return err
	}
	src, err := q.GetSource(ctx, id)
	if err != nil {
		return err
	}
	runID, _ := uuid.NewV7()
	if err := q.InsertRun(ctx, gitsyncdb.InsertRunParams{ID: runID, GitSourceID: id, StartedAt: now}); err != nil {
		return err
	}
	sha, created, warnings, syncErr := s.sync(ctx, src)
	status, errText := "success", ""
	if syncErr != nil {
		status, errText = "failed", syncErr.Error()
	}
	if warnings == nil {
		warnings = []string{}
	}
	w, _ := json.Marshal(warnings)
	end := s.Clock.Now()
	ctxEnd := context.WithoutCancel(ctx)
	if err := q.FinishRun(ctxEnd, gitsyncdb.FinishRunParams{EndedAt: &end, Sha: sha, Status: status, Error: errText, Warnings: w,
		SnapshotsCreated: int32(created), ID: runID}); err != nil {
		return err
	}
	return q.FinishSync(ctxEnd, gitsyncdb.FinishSyncParams{Status: status, Error: errText, Now: &end, Sha: sha, ID: id})
}

// sync fetches the branch head into a temporary directory and commits one snapshot per
// mapping whose files changed. The directory is removed at the end.
func (s *Service) sync(ctx context.Context, src gitsyncdb.GitSource) (sha string, created int, warnings []string, err error) {
	root, err := s.tempRoot()
	if err != nil {
		return "", 0, nil, err
	}
	dir, err := os.MkdirTemp(root, "sync-*")
	if err != nil {
		return "", 0, nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	auth, warn, err := s.auth(ctx, src.AuthType, src.CredentialSecretKey, src.KnownHosts, src.RepoUrl, dir)
	if err != nil {
		return "", 0, nil, err
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}
	repo, err := git.PlainCloneContext(ctx, filepath.Join(dir, "repo"), true, &git.CloneOptions{URL: src.RepoUrl, Auth: auth,
		ReferenceName: plumbing.NewBranchReferenceName(src.Branch), SingleBranch: true, Depth: 1, Tags: git.NoTags})
	if err != nil {
		return "", 0, warnings, fmt.Errorf("fetch %s: %w", src.Branch, err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", 0, warnings, err
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return "", 0, warnings, err
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", 0, warnings, err
	}
	sha = head.Hash().String()
	subject, _, _ := strings.Cut(strings.TrimSpace(commit.Message), "\n")
	message := fmt.Sprintf("git %s: %s", sha[:12], subject)
	maps, err := gitsyncdb.New(s.Pool).ListMappings(ctx, src.ID)
	if err != nil {
		return sha, 0, warnings, err
	}
	for _, m := range maps {
		files, warns, err := collect(repo.Storer, tree, m.RepoPath, s.maxFile())
		warnings = append(warnings, warns...)
		if err != nil {
			return sha, created, warnings, fmt.Errorf("mapping %s: %w", m.Namespace, err)
		}
		ok, err := s.Namespaces.CommitGit(ctx, m.NamespaceID, sha, message, files)
		if err != nil {
			return sha, created, warnings, fmt.Errorf("mapping %s: %w", m.Namespace, err)
		}
		if ok {
			created++
		}
	}
	return sha, created, warnings, nil
}

// auth builds the transport credentials of a source. An ssh source without known_hosts
// accepts any host key and returns a warning (DI-33).
func (s *Service) auth(ctx context.Context, authType, key, knownHostsText, repoURL, dir string) (transport.AuthMethod, string, error) {
	switch authType {
	case "none":
		return nil, "", nil
	case "https_token":
		token, err := s.Secrets.Global(ctx, key)
		if err != nil {
			return nil, "", fmt.Errorf("credential: %w", err)
		}
		return &githttp.BasicAuth{Username: "x-access-token", Password: token}, "", nil
	case "ssh_key":
		pemKey, err := s.Secrets.Global(ctx, key)
		if err != nil {
			return nil, "", fmt.Errorf("credential: %w", err)
		}
		user := "git"
		if u, err := url.Parse(repoURL); err == nil && u.User != nil && u.User.Username() != "" {
			user = u.User.Username()
		} else if at := strings.Index(repoURL, "@"); at > 0 && !strings.Contains(repoURL, "://") {
			user = repoURL[:at]
		}
		pk, err := gitssh.NewPublicKeys(user, []byte(pemKey), "")
		if err != nil {
			return nil, "", fmt.Errorf("ssh key: %w", err)
		}
		if strings.TrimSpace(knownHostsText) == "" {
			pk.HostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec // known_hosts is optional (REQ-GIT-001); the run records a warning
			return pk, "no known_hosts: the host key was not checked", nil
		}
		path := filepath.Join(dir, "known_hosts")
		if err := os.WriteFile(path, []byte(knownHostsText+"\n"), 0o600); err != nil {
			return nil, "", err
		}
		cb, err := knownhosts.New(path)
		if err != nil {
			return nil, "", fmt.Errorf("known_hosts: %w", err)
		}
		pk.HostKeyCallback = cb
		return pk, "", nil
	}
	return nil, "", fmt.Errorf("unknown auth type %q", authType)
}

// VerifyWebhook checks a webhook call of a source and requests a sync for a push to the
// tracked branch. It returns whether a sync was queued (REQ-GIT-003, SI-05).
func (s *Service) VerifyWebhook(ctx context.Context, id uuid.UUID, body []byte, headers http.Header) (bool, error) {
	src, err := gitsyncdb.New(s.Pool).GetSource(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrSourceNotFound
	}
	if err != nil {
		return false, err
	}
	if src.WebhookSecretKey == "" {
		return false, ErrInvalidSignature
	}
	secretValue, err := s.Secrets.Global(ctx, src.WebhookSecretKey)
	if err != nil || secretValue == "" {
		return false, ErrInvalidSignature
	}
	valid := false
	if sig := headers.Get("X-Hub-Signature-256"); sig != "" {
		mac := hmac.New(sha256.New, []byte(secretValue))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		valid = hmac.Equal([]byte(sig), []byte(want))
	} else if tok := headers.Get("X-Sluice-Token"); tok != "" {
		valid = subtle.ConstantTimeCompare([]byte(tok), []byte(secretValue)) == 1
	}
	if !valid {
		return false, ErrInvalidSignature
	}
	if headers.Get("X-GitHub-Event") == "ping" {
		return false, nil
	}
	var push struct {
		Ref string `json:"ref"`
	}
	if len(body) > 0 && json.Unmarshal(body, &push) == nil && push.Ref != "" && push.Ref != "refs/heads/"+src.Branch {
		return false, nil
	}
	now := s.Clock.Now()
	if _, err := gitsyncdb.New(s.Pool).RequestSync(ctx, gitsyncdb.RequestSyncParams{Now: &now, ID: id}); err != nil {
		return false, err
	}
	return true, nil
}

// Change is one file change of a push, relative to the namespace root.
type Change struct {
	Op         string // put, delete, rename
	Path       string
	NewPath    string
	Content    []byte
	Executable *bool
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// userSlug returns a branch-safe name of a user from the email local part.
func userSlug(email string) string {
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	slug := strings.Trim(slugRe.ReplaceAllString(local, "-"), "-")
	if slug == "" {
		slug = "user"
	}
	return slug
}

// Push commits changes of a git namespace to a new branch
// sluice/<user-slug>/<yyyymmdd-hhmmss> from the last synced SHA, with the user as author,
// and pushes only that branch. The tracked branch does not change (REQ-GIT-005).
func (s *Service) Push(ctx context.Context, namespace string, changes []Change, message string) (branch, sha string, err error) {
	p := kernel.FromContext(ctx)
	if p == nil {
		return "", "", httpx.Errorf(http.StatusUnauthorized, "unauthorized", "sign in first")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", "", httpx.Validation(httpx.FieldError{Field: "message", Message: "a message is required"})
	}
	if len(changes) == 0 {
		return "", "", httpx.Validation(httpx.FieldError{Field: "changes", Message: "at least one change is required"})
	}
	src, err := s.NamespaceSource(ctx, namespace)
	if err != nil {
		return "", "", err
	}
	if src.LastSyncedSha == "" {
		return "", "", ErrNotSynced
	}
	root, err := s.tempRoot()
	if err != nil {
		return "", "", err
	}
	dir, err := os.MkdirTemp(root, "push-*")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	auth, _, err := s.auth(ctx, src.AuthType, src.CredentialSecretKey, src.KnownHosts, src.RepoUrl, dir)
	if err != nil {
		return "", "", err
	}
	repo, err := git.PlainCloneContext(ctx, filepath.Join(dir, "repo"), true, &git.CloneOptions{URL: src.RepoUrl, Auth: auth,
		ReferenceName: plumbing.NewBranchReferenceName(src.Branch), SingleBranch: true, Tags: git.NoTags})
	if err != nil {
		return "", "", fmt.Errorf("fetch %s: %w", src.Branch, err)
	}
	base, err := repo.CommitObject(plumbing.NewHash(src.LastSyncedSha))
	if err != nil {
		return "", "", fmt.Errorf("the last synced commit %s is not on %s: %w", src.LastSyncedSha, src.Branch, err)
	}
	baseTree, err := base.Tree()
	if err != nil {
		return "", "", err
	}
	leaves := map[string]entry{}
	if err := flatten(repo.Storer, baseTree, "", leaves); err != nil {
		return "", "", err
	}
	prefix := ""
	if src.RepoPath != "" {
		prefix = src.RepoPath + "/"
	}
	for i, c := range changes {
		field := fmt.Sprintf("changes[%d]", i)
		if err := flow.ValidPath(c.Path); err != nil {
			return "", "", httpx.Validation(httpx.FieldError{Field: field + ".path", Message: err.Error()})
		}
		full := prefix + c.Path
		switch c.Op {
		case "put":
			if int64(len(c.Content)) > s.maxFile() {
				return "", "", httpx.Errorf(http.StatusRequestEntityTooLarge, "file_too_large", "%s has %d bytes, the limit is %d", c.Path, len(c.Content), s.maxFile())
			}
			h, err := writeBlob(repo.Storer, c.Content)
			if err != nil {
				return "", "", err
			}
			mode := filemode.Regular
			if old, ok := leaves[full]; ok && old.mode == filemode.Executable {
				mode = filemode.Executable
			}
			if c.Executable != nil {
				mode = filemode.Regular
				if *c.Executable {
					mode = filemode.Executable
				}
			}
			leaves[full] = entry{mode: mode, hash: h}
		case "delete":
			if _, ok := leaves[full]; !ok {
				return "", "", httpx.Validation(httpx.FieldError{Field: field + ".path", Message: "file does not exist"})
			}
			delete(leaves, full)
		case "rename":
			if err := flow.ValidPath(c.NewPath); err != nil {
				return "", "", httpx.Validation(httpx.FieldError{Field: field + ".new_path", Message: err.Error()})
			}
			e, ok := leaves[full]
			if !ok {
				return "", "", httpx.Validation(httpx.FieldError{Field: field + ".path", Message: "file does not exist"})
			}
			if _, exists := leaves[prefix+c.NewPath]; exists {
				return "", "", httpx.Validation(httpx.FieldError{Field: field + ".new_path", Message: "a file with this path exists"})
			}
			delete(leaves, full)
			leaves[prefix+c.NewPath] = e
		default:
			return "", "", httpx.Validation(httpx.FieldError{Field: field + ".op", Message: "must be put, delete or rename"})
		}
	}
	treeHash, err := buildTree(repo.Storer, leaves)
	if err != nil {
		return "", "", err
	}
	if treeHash == baseTree.Hash {
		return "", "", httpx.Validation(httpx.FieldError{Field: "changes", Message: "the changes do not change any file"})
	}
	now := s.Clock.Now()
	sig := object.Signature{Name: userSlug(p.Email), Email: p.Email, When: now}
	commit := &object.Commit{Author: sig, Committer: sig, Message: message + "\n", TreeHash: treeHash, ParentHashes: []plumbing.Hash{base.Hash}}
	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return "", "", err
	}
	commitHash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return "", "", err
	}
	branch = fmt.Sprintf("sluice/%s/%s", userSlug(p.Email), now.UTC().Format("20060102-150405"))
	ref := plumbing.NewBranchReferenceName(branch)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(ref, commitHash)); err != nil {
		return "", "", err
	}
	if err := repo.PushContext(ctx, &git.PushOptions{RemoteName: "origin", Auth: auth,
		RefSpecs: []config.RefSpec{config.RefSpec(ref.String() + ":" + ref.String())}}); err != nil {
		return "", "", fmt.Errorf("push %s: %w", branch, err)
	}
	sha = commitHash.String()
	err = s.Audit.Record(ctx, s.Pool, audit.Event{Action: "git.push", TargetType: "namespace", TargetID: namespace,
		Details: map[string]any{"branch": branch, "sha": sha, "files": len(changes)}})
	return branch, sha, err
}
