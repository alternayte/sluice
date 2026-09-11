package gitsync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// MappingIO is one mapping of a repository path to a namespace.
type MappingIO struct {
	RepoPath  string `json:"repo_path" maxLength:"512" doc:"Directory in the repository. Empty is the repository root."`
	Namespace string `json:"namespace" maxLength:"128"`
}

// SourceBody is the body of a git source write.
type SourceBody struct {
	RepoURL             string      `json:"repo_url" minLength:"1" maxLength:"2048" doc:"https or ssh URL."`
	Branch              string      `json:"branch" minLength:"1" maxLength:"255"`
	AuthType            string      `json:"auth_type" enum:"none,https_token,ssh_key"`
	CredentialSecretKey string      `json:"credential_secret_key,omitempty" maxLength:"128" doc:"Global secret key with the token or the private key."`
	KnownHosts          string      `json:"known_hosts,omitempty" maxLength:"65536"`
	PollInterval        int         `json:"poll_interval,omitempty" minimum:"15" maximum:"86400" doc:"Seconds between polls. Default 60."`
	WebhookSecretKey    string      `json:"webhook_secret_key,omitempty" maxLength:"128" doc:"Global secret key with the webhook secret."`
	Mappings            []MappingIO `json:"mappings" minItems:"1" maxItems:"100"`
}

// SourceOut is one git source. It holds secret key names, never secret values.
type SourceOut struct {
	ID                  uuid.UUID   `json:"id"`
	Name                string      `json:"name"`
	RepoURL             string      `json:"repo_url"`
	Branch              string      `json:"branch"`
	AuthType            string      `json:"auth_type"`
	CredentialSecretKey string      `json:"credential_secret_key"`
	KnownHosts          string      `json:"known_hosts"`
	PollInterval        int         `json:"poll_interval"`
	WebhookSecretKey    string      `json:"webhook_secret_key"`
	WebhookURL          string      `json:"webhook_url"`
	LastSyncedSha       string      `json:"last_synced_sha"`
	LastSyncAt          *time.Time  `json:"last_sync_at,omitempty" nullable:"true"`
	LastSyncStatus      string      `json:"last_sync_status"`
	LastError           string      `json:"last_error"`
	Mappings            []MappingIO `json:"mappings"`
}

// SourceList is the list of git sources.
type SourceList struct {
	Items []SourceOut `json:"items"`
}

// RunOut is one sync run.
type RunOut struct {
	ID               uuid.UUID  `json:"id"`
	StartedAt        time.Time  `json:"started_at"`
	EndedAt          *time.Time `json:"ended_at,omitempty" nullable:"true"`
	Sha              string     `json:"sha"`
	Status           string     `json:"status" enum:"running,success,failed"`
	Error            string     `json:"error"`
	Warnings         []string   `json:"warnings"`
	SnapshotsCreated int        `json:"snapshots_created"`
}

// RunList is the list of the last 50 sync runs, newest first.
type RunList struct {
	Items []RunOut `json:"items"`
}

// NamespaceGit is the git source of one namespace.
type NamespaceGit struct {
	SourceID       uuid.UUID  `json:"source_id"`
	SourceName     string     `json:"source_name"`
	RepoURL        string     `json:"repo_url"`
	Branch         string     `json:"branch"`
	RepoPath       string     `json:"repo_path"`
	LastSyncedSha  string     `json:"last_synced_sha"`
	LastSyncAt     *time.Time `json:"last_sync_at,omitempty" nullable:"true"`
	LastSyncStatus string     `json:"last_sync_status"`
	LastError      string     `json:"last_error"`
}

// PushChange is one file change of a push.
type PushChange struct {
	Op            string  `json:"op" enum:"put,delete,rename"`
	Path          string  `json:"path" maxLength:"512"`
	NewPath       *string `json:"new_path,omitempty" maxLength:"512" doc:"rename only."`
	Content       *string `json:"content,omitempty" doc:"put only. UTF-8 text content."`
	ContentBase64 *string `json:"content_base64,omitempty" doc:"put only. Binary content as base64."`
	Executable    *bool   `json:"executable,omitempty"`
}

// PushResult is the branch that a push created.
type PushResult struct {
	Branch string `json:"branch"`
	Sha    string `json:"sha"`
}

func sourceOut(s *Service, src Source) SourceOut {
	out := SourceOut{ID: src.ID, Name: src.Name, RepoURL: src.RepoUrl, Branch: src.Branch, AuthType: src.AuthType,
		CredentialSecretKey: src.CredentialSecretKey, KnownHosts: src.KnownHosts, PollInterval: int(src.PollInterval),
		WebhookSecretKey: src.WebhookSecretKey, WebhookURL: s.WebhookURL(src.ID), LastSyncedSha: src.LastSyncedSha,
		LastSyncAt: src.LastSyncAt, LastSyncStatus: src.LastSyncStatus, LastError: src.LastError, Mappings: []MappingIO{}}
	for _, m := range src.Mappings {
		out.Mappings = append(out.Mappings, MappingIO(m))
	}
	return out
}

func input(name string, b SourceBody) SourceInput {
	in := SourceInput{Name: name, RepoURL: b.RepoURL, Branch: b.Branch, AuthType: b.AuthType, CredentialSecretKey: b.CredentialSecretKey,
		KnownHosts: b.KnownHosts, PollInterval: b.PollInterval, WebhookSecretKey: b.WebhookSecretKey}
	for _, m := range b.Mappings {
		in.Mappings = append(in.Mappings, Mapping(m))
	}
	return in
}

type sourceOutBody struct{ Body SourceOut }

type noBody struct{}

// Routes registers the git source, sync, push and webhook operations. Sources need admin,
// "Sync now" needs operator, push needs editor, runs and namespace source info need
// viewer (Appendix B).
func Routes(api huma.API, r chi.Router, s *Service) {
	viewer, operator := httpx.MinRole(kernel.Viewer), httpx.MinRole(kernel.Operator)
	editor, admin := httpx.MinRole(kernel.Editor), httpx.MinRole(kernel.Admin)

	huma.Register(api, httpx.Op("listGitSources", http.MethodGet, "/api/v1/git-sources", admin),
		func(ctx context.Context, _ *struct{}) (*struct{ Body SourceList }, error) {
			list, err := s.List(ctx)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body SourceList }{Body: SourceList{Items: []SourceOut{}}}
			for _, src := range list {
				out.Body.Items = append(out.Body.Items, sourceOut(s, src))
			}
			return out, nil
		})
	create := httpx.Op("createGitSource", http.MethodPost, "/api/v1/git-sources", admin)
	create.DefaultStatus = http.StatusCreated
	huma.Register(api, create, func(ctx context.Context, in *struct {
		Body struct {
			Name string `json:"name" pattern:"^[a-z0-9][a-z0-9_-]{0,62}$"`
			SourceBody
		}
	}) (*sourceOutBody, error) {
		src, err := s.Create(ctx, input(in.Body.Name, in.Body.SourceBody))
		if err != nil {
			return nil, err
		}
		return &sourceOutBody{Body: sourceOut(s, src)}, nil
	})
	huma.Register(api, httpx.Op("getGitSource", http.MethodGet, "/api/v1/git-sources/{sourceId}", admin),
		func(ctx context.Context, in *struct {
			SourceID uuid.UUID `path:"sourceId"`
		}) (*sourceOutBody, error) {
			src, err := s.Get(ctx, in.SourceID)
			if err != nil {
				return nil, err
			}
			return &sourceOutBody{Body: sourceOut(s, src)}, nil
		})
	huma.Register(api, httpx.Op("updateGitSource", http.MethodPut, "/api/v1/git-sources/{sourceId}", admin),
		func(ctx context.Context, in *struct {
			SourceID uuid.UUID `path:"sourceId"`
			Body     SourceBody
		}) (*sourceOutBody, error) {
			src, err := s.Update(ctx, in.SourceID, input("", in.Body))
			if err != nil {
				return nil, err
			}
			return &sourceOutBody{Body: sourceOut(s, src)}, nil
		})
	del := httpx.Op("deleteGitSource", http.MethodDelete, "/api/v1/git-sources/{sourceId}", admin)
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *struct {
		SourceID uuid.UUID `path:"sourceId"`
	}) (*noBody, error) {
		return nil, s.Delete(ctx, in.SourceID)
	})
	sync := httpx.Op("syncGitSource", http.MethodPost, "/api/v1/git-sources/{sourceId}/sync", operator)
	sync.DefaultStatus = http.StatusAccepted
	huma.Register(api, sync, func(ctx context.Context, in *struct {
		SourceID uuid.UUID `path:"sourceId"`
	}) (*noBody, error) {
		return nil, s.RequestSync(ctx, in.SourceID)
	})
	huma.Register(api, httpx.Op("listGitSyncRuns", http.MethodGet, "/api/v1/git-sources/{sourceId}/runs", viewer),
		func(ctx context.Context, in *struct {
			SourceID uuid.UUID `path:"sourceId"`
		}) (*struct{ Body RunList }, error) {
			runs, err := s.Runs(ctx, in.SourceID)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body RunList }{Body: RunList{Items: []RunOut{}}}
			for _, run := range runs {
				ro := RunOut{ID: run.ID, StartedAt: run.StartedAt, EndedAt: run.EndedAt, Sha: run.Sha, Status: run.Status, Error: run.Error,
					SnapshotsCreated: int(run.SnapshotsCreated), Warnings: []string{}}
				_ = json.Unmarshal(run.Warnings, &ro.Warnings)
				out.Body.Items = append(out.Body.Items, ro)
			}
			return out, nil
		})

	huma.Register(api, httpx.Op("getNamespaceGit", http.MethodGet, "/api/v1/namespaces/{namespace}/git", viewer),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
		}) (*struct{ Body NamespaceGit }, error) {
			row, err := s.NamespaceSource(ctx, in.Namespace)
			if err != nil {
				return nil, err
			}
			return &struct{ Body NamespaceGit }{Body: NamespaceGit{SourceID: row.ID, SourceName: row.Name, RepoURL: row.RepoUrl, Branch: row.Branch,
				RepoPath: row.RepoPath, LastSyncedSha: row.LastSyncedSha, LastSyncAt: row.LastSyncAt, LastSyncStatus: row.LastSyncStatus,
				LastError: row.LastError}}, nil
		})
	push := httpx.Op("pushNamespaceBranch", http.MethodPost, "/api/v1/namespaces/{namespace}/git/push", editor)
	push.DefaultStatus = http.StatusCreated
	push.MaxBodyBytes = 256 << 20
	huma.Register(api, push, func(ctx context.Context, in *struct {
		Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
		Body      struct {
			Message string       `json:"message" minLength:"1" maxLength:"2000"`
			Changes []PushChange `json:"changes" minItems:"1" maxItems:"1000"`
		}
	}) (*struct{ Body PushResult }, error) {
		changes := make([]Change, 0, len(in.Body.Changes))
		for i, c := range in.Body.Changes {
			ch := Change{Op: c.Op, Path: c.Path, Executable: c.Executable}
			if c.NewPath != nil {
				ch.NewPath = *c.NewPath
			}
			switch {
			case c.ContentBase64 != nil:
				b, err := base64.StdEncoding.DecodeString(*c.ContentBase64)
				if err != nil {
					return nil, httpx.Validation(httpx.FieldError{Field: "changes[" + itoa(i) + "].content_base64", Message: "not valid base64"})
				}
				ch.Content = b
			case c.Content != nil:
				ch.Content = []byte(*c.Content)
			}
			changes = append(changes, ch)
		}
		branch, sha, err := s.Push(ctx, in.Namespace, changes, in.Body.Message)
		if err != nil {
			return nil, err
		}
		return &struct{ Body PushResult }{Body: PushResult{Branch: branch, Sha: sha}}, nil
	})

	hook := httpx.Op("gitWebhook", http.MethodPost, "/hooks/git/{sourceId}", httpx.Public)
	hook.Summary = "Request a sync of a git source from a push webhook"
	hook.Parameters = httpx.PathParams("sourceId")
	hook.Responses = httpx.RawResponse(http.StatusAccepted, "application/json", "The call is valid.")
	httpx.Raw(api, r, hook, func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "sourceId"))
		if err != nil {
			httpx.WriteError(w, req, ErrSourceNotFound)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, MaxWebhookBody))
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteError(w, req, ErrWebhookBodyTooBig)
			return
		}
		if err != nil {
			httpx.WriteError(w, req, httpx.Errorf(http.StatusBadRequest, "bad_request", "read body: %v", err))
			return
		}
		queued, err := s.VerifyWebhook(req.Context(), id, body, req.Header)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		httpx.WriteJSON(w, http.StatusAccepted, map[string]bool{"queued": queued})
	})
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
