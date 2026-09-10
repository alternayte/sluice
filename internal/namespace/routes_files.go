package namespace

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/page"
)

// FileEntry is one file of a version.
type FileEntry struct {
	Path       string `json:"path"`
	Size       int64  `json:"size" format:"int64"`
	Hash       string `json:"hash"`
	Executable bool   `json:"executable"`
}

// FileList is the file list of a version.
type FileList struct {
	Version    *int        `json:"version,omitempty" nullable:"true"`
	SnapshotID *uuid.UUID  `json:"snapshot_id,omitempty" nullable:"true"`
	GitSha     *string     `json:"git_sha,omitempty"`
	Items      []FileEntry `json:"items"`
}

// Snapshot is one version of a namespace.
type Snapshot struct {
	ID           uuid.UUID `json:"id"`
	Version      *int      `json:"version,omitempty" nullable:"true"`
	GitSha       *string   `json:"git_sha,omitempty"`
	Message      string    `json:"message"`
	Author       string    `json:"author"`
	CreatedAt    time.Time `json:"created_at"`
	ManifestHash string    `json:"manifest_hash"`
	FileCount    int       `json:"file_count"`
}

// SnapshotList is a list of versions, newest first.
type SnapshotList struct {
	Items []Snapshot `json:"items"`
}

// FileDiff is the diff of one file.
type FileDiff struct {
	Path   string `json:"path"`
	Status string `json:"status" enum:"added,removed,modified"`
	Binary bool   `json:"binary"`
	Diff   string `json:"diff" doc:"Unified diff for text files."`
}

// VersionDiff is the diff of two versions.
type VersionDiff struct {
	From  int        `json:"from"`
	To    int        `json:"to"`
	Files []FileDiff `json:"files"`
}

// Issue is one validation problem in a file.
type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

// ValidateFileResult is the result of a validation of proposed content.
type ValidateFileResult struct {
	Valid  bool    `json:"valid"`
	Kind   string  `json:"kind" enum:"flow,namespace,other"`
	FlowID *string `json:"flow_id,omitempty"`
	Errors []Issue `json:"errors"`
}

// FileChange is one change of a save.
type FileChange struct {
	Op            string  `json:"op" enum:"put,delete,rename"`
	Path          string  `json:"path" maxLength:"512"`
	NewPath       *string `json:"new_path,omitempty" maxLength:"512" doc:"rename only."`
	Content       *string `json:"content,omitempty" doc:"put only. UTF-8 text content."`
	ContentBase64 *string `json:"content_base64,omitempty" doc:"put only. Binary content as base64. Used instead of content."`
	Executable    *bool   `json:"executable,omitempty"`
}

// RevertRequest is the body of revertVersion.
type RevertRequest struct {
	Version int    `json:"version" minimum:"1"`
	Message string `json:"message,omitempty" maxLength:"500"`
}

type snapshotOut struct{ Body Snapshot }

func toSnapshotOp(s *SnapshotInfo) Snapshot {
	return Snapshot{ID: s.ID, Version: optInt32(s.Version), GitSha: optStr(s.GitSha), Message: s.Message, Author: s.Author,
		CreatedAt: s.CreatedAt, ManifestHash: s.ManifestHash, FileCount: s.FileCount}
}

func toIssuesOp(is []flow.Issue) []Issue {
	out := make([]Issue, 0, len(is))
	for _, i := range is {
		out = append(out, Issue{Code: i.Code, Path: i.Path, Line: i.Line, Column: i.Column, Message: i.Message})
	}
	return out
}

func registerFiles(api huma.API, r chi.Router, s *Service) {
	viewer := httpx.MinRole(kernel.Viewer)
	editor := httpx.MinRole(kernel.Editor)

	huma.Register(api, httpx.Op("listFiles", http.MethodGet, "/api/v1/namespaces/{namespace}/files", viewer),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			Version   int    `query:"version" minimum:"1" doc:"Version number. Default is the head version."`
		}) (*struct{ Body FileList }, error) {
			ns, err := s.Get(ctx, in.Namespace)
			if err != nil {
				return nil, err
			}
			info, m, err := s.Resolve(ctx, ns, optInt(in.Version))
			if err != nil {
				return nil, err
			}
			out := &struct{ Body FileList }{Body: FileList{Items: []FileEntry{}}}
			if info != nil {
				out.Body.Version = optInt32(info.Version)
				id := info.ID
				out.Body.SnapshotID = &id
				out.Body.GitSha = optStr(info.GitSha)
			}
			for _, p := range m.Paths() {
				e := m[p]
				out.Body.Items = append(out.Body.Items, FileEntry{Path: e.Path, Size: e.Size, Hash: e.Hash, Executable: e.Executable})
			}
			return out, nil
		})

	registerFileTransfer(api, r, s, viewer, editor)

	huma.Register(api, unlimitedBody(withStatus(httpx.Op("saveChanges", http.MethodPost, "/api/v1/namespaces/{namespace}/changes", editor), http.StatusCreated)),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			Body      struct {
				Message     string       `json:"message" minLength:"1" maxLength:"500"`
				BaseVersion *int         `json:"base_version,omitempty" doc:"Fails with 409 version_conflict when the head version differs."`
				Changes     []FileChange `json:"changes" minItems:"1" maxItems:"1000"`
			}
		}) (*snapshotOut, error) {
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
						return nil, httpx.Validation(httpx.FieldError{Field: fmt.Sprintf("changes[%d].content_base64", i), Message: "invalid base64"})
					}
					ch.Content = b
				case c.Content != nil:
					ch.Content = []byte(*c.Content)
				}
				changes = append(changes, ch)
			}
			snap, err := s.Save(ctx, in.Namespace, changes, in.Body.Message, in.Body.BaseVersion)
			if err != nil {
				return nil, err
			}
			return &snapshotOut{Body: toSnapshotOp(snap)}, nil
		})

	huma.Register(api, httpx.Op("listVersions", http.MethodGet, "/api/v1/namespaces/{namespace}/versions", viewer),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			Limit     int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
		}) (*struct{ Body SnapshotList }, error) {
			list, err := s.Versions(ctx, in.Namespace, page.Limit(in.Limit))
			if err != nil {
				return nil, err
			}
			out := &struct{ Body SnapshotList }{Body: SnapshotList{Items: []Snapshot{}}}
			for i := range list {
				out.Body.Items = append(out.Body.Items, toSnapshotOp(&list[i]))
			}
			return out, nil
		})

	huma.Register(api, httpx.Op("diffVersions", http.MethodGet, "/api/v1/namespaces/{namespace}/diff", viewer),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			From      int    `query:"from" required:"true" minimum:"1"`
			To        int    `query:"to" required:"true" minimum:"1"`
		}) (*struct{ Body VersionDiff }, error) {
			diffs, err := s.Diff(ctx, in.Namespace, in.From, in.To)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body VersionDiff }{Body: VersionDiff{From: in.From, To: in.To, Files: []FileDiff{}}}
			for _, d := range diffs {
				out.Body.Files = append(out.Body.Files, FileDiff(d))
			}
			return out, nil
		})

	huma.Register(api, unlimitedBody(withStatus(httpx.Op("revertVersion", http.MethodPost, "/api/v1/namespaces/{namespace}/revert", editor), http.StatusCreated)),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			Body      RevertRequest
		}) (*snapshotOut, error) {
			snap, err := s.Revert(ctx, in.Namespace, in.Body.Version, in.Body.Message)
			if err != nil {
				return nil, err
			}
			return &snapshotOut{Body: toSnapshotOp(snap)}, nil
		})

	huma.Register(api, unlimitedBody(httpx.Op("validateFile", http.MethodPost, "/api/v1/namespaces/{namespace}/validate", viewer)),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			Body      struct {
				Path    string `json:"path" maxLength:"512"`
				Content string `json:"content"`
			}
		}) (*struct{ Body ValidateFileResult }, error) {
			kind, id, issues, err := s.ValidateFile(ctx, in.Namespace, in.Body.Path, in.Body.Content)
			if err != nil {
				return nil, err
			}
			return &struct{ Body ValidateFileResult }{Body: ValidateFileResult{Valid: len(issues) == 0, Kind: kind, FlowID: optStr(id),
				Errors: toIssuesOp(issues)}}, nil
		})
}

func optInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// fileParams are the parameters of the two Raw file operations. The old validator checked
// them against the spec, so parseFileParams checks the same keywords (REQ-API-002).
type fileParams struct {
	Namespace  string
	Path       string
	Version    *int
	Message    *string
	Executable *bool
}

func parseFileParams(req *http.Request, extra ...httpx.FieldError) (fileParams, error) {
	var p fileParams
	fields := extra
	q := req.URL.Query()
	p.Namespace = chi.URLParam(req, "namespace")
	if len(p.Namespace) > 128 || !nameRE.MatchString(p.Namespace) {
		fields = append(fields, httpx.FieldError{Field: "namespace", Message: "invalid namespace name"})
	}
	p.Path = q.Get("path")
	switch {
	case !q.Has("path"):
		fields = append(fields, httpx.FieldError{Field: "path", Message: "value is required but missing"})
	case len(p.Path) < 1 || len(p.Path) > 512:
		fields = append(fields, httpx.FieldError{Field: "path", Message: "length must be between 1 and 512"})
	}
	if v := q.Get("version"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fields = append(fields, httpx.FieldError{Field: "version", Message: "must be an integer of 1 or more"})
		}
		p.Version = &n
	}
	if q.Has("message") {
		m := q.Get("message")
		if len(m) > 500 {
			fields = append(fields, httpx.FieldError{Field: "message", Message: "maximum string length is 500"})
		}
		p.Message = &m
	}
	if v := q.Get("executable"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			fields = append(fields, httpx.FieldError{Field: "executable", Message: "must be a boolean"})
		}
		p.Executable = &b
	}
	if len(fields) > 0 {
		return p, httpx.Validation(fields...)
	}
	return p, nil
}

// registerFileTransfer registers getFile and uploadFile as Raw routes: the body is a stream (DI-23).
func registerFileTransfer(api huma.API, r chi.Router, s *Service, viewer, editor httpx.Access) {
	pathQuery := &huma.Param{Name: "path", In: "query", Required: true, Description: "File path relative to the namespace root.",
		Schema: &huma.Schema{Type: huma.TypeString, MinLength: ptr(1), MaxLength: ptr(512)}}

	getFile := httpx.Op("getFile", http.MethodGet, "/api/v1/namespaces/{namespace}/file", viewer)
	getFile.Parameters = append(httpx.PathParams("namespace"), pathQuery,
		&huma.Param{Name: "version", In: "query", Description: "Version number. Default is the head version.",
			Schema: &huma.Schema{Type: huma.TypeInteger, Minimum: ptrF(1)}})
	getFile.Responses = httpx.RawResponse(http.StatusOK, "application/octet-stream", "File content.")
	httpx.Raw(api, r, getFile, func(w http.ResponseWriter, req *http.Request) {
		p, err := parseFileParams(req)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		rc, e, err := s.ReadFile(req.Context(), p.Namespace, p.Path, p.Version)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		defer func() { _ = rc.Close() }()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(e.Size, 10))
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(e.Path)))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
	})

	uploadFile := httpx.Op("uploadFile", http.MethodPut, "/api/v1/namespaces/{namespace}/file", editor)
	uploadFile.Parameters = append(httpx.PathParams("namespace"), pathQuery,
		&huma.Param{Name: "message", In: "query", Schema: &huma.Schema{Type: huma.TypeString, MaxLength: ptr(500)}},
		&huma.Param{Name: "executable", In: "query", Schema: &huma.Schema{Type: huma.TypeBoolean}})
	uploadFile.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
		"application/octet-stream": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}}}
	uploadFile.Responses = httpx.RawResponse(http.StatusCreated, "application/json", "New version.")
	httpx.Raw(api, r, uploadFile, func(w http.ResponseWriter, req *http.Request) {
		p, err := parseFileParams(req, uploadBodyErrors(req)...)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		// Read one byte more than the limit to detect a file above SLUICE_MAX_FILE_BYTES.
		b, err := io.ReadAll(io.LimitReader(req.Body, s.MaxFileBytes+1))
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		if int64(len(b)) > s.MaxFileBytes {
			httpx.WriteError(w, req, errTooLarge("file_too_large", "the file is larger than %d bytes", s.MaxFileBytes))
			return
		}
		msg := "Upload " + p.Path
		if p.Message != nil && *p.Message != "" {
			msg = *p.Message
		}
		snap, err := s.Save(req.Context(), p.Namespace, []Change{{Op: "put", Path: p.Path, Content: b, Executable: p.Executable}}, msg, nil)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, toSnapshotOp(snap))
	})
}

// uploadBodyErrors checks the uploadFile body as the old validator did (the deleted internal/api/server.go).
// The old validator skipped only a non-empty body with a Content-Type that is not JSON. In each
// other case it checked the required application/octet-stream body: an empty body, no
// Content-Type and a JSON Content-Type each gave 422 validation_failed with the field "body".
// The messages are the kin-openapi messages.
func uploadBodyErrors(req *http.Request) []httpx.FieldError {
	ct := req.Header.Get("Content-Type")
	switch {
	case req.Body == nil || req.ContentLength == 0:
		return []httpx.FieldError{{Field: "body", Message: "value is required but missing"}}
	case ct == "" || strings.HasPrefix(ct, "application/json"):
		return []httpx.FieldError{{Field: "body", Message: fmt.Sprintf("header Content-Type has unexpected value %q", ct)}}
	}
	return nil
}

func ptr(v int) *int { return &v }

func ptrF(v float64) *float64 { return &v }
