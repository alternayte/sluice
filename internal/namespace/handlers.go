package namespace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/api/apigen"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/page"
)

// API serves the namespace, file and flow operations.
type API struct {
	Svc *Service
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intPtr32(v *int32) *int {
	if v == nil {
		return nil
	}
	x := int(*v)
	return &x
}

func toNamespace(n TreeNode) apigen.Namespace {
	out := apigen.Namespace{Name: n.Name, Implicit: n.Implicit, SourceType: apigen.Implicit, Parent: strPtr(n.Parent)}
	if n.Row != nil {
		out.SourceType = apigen.NamespaceSourceType(n.Row.SourceType)
		out.Description = n.Row.Description
		out.HeadVersion = intPtr32(n.Row.HeadVersion)
		if n.Row.HeadGitSha != nil {
			out.HeadGitSha = strPtr(*n.Row.HeadGitSha)
		}
		out.GitSourceId = n.Row.GitSourceID
		ro := n.Row.SourceType == "git"
		out.ReadOnly = &ro
	}
	return out
}

func rowToNode(r dbq.GetNamespaceRow) TreeNode {
	return TreeNode{Name: r.Name, Parent: ParentOf(r.Name), Row: &dbq.ListNamespacesRow{ID: r.ID, Name: r.Name, SourceType: r.SourceType,
		GitSourceID: r.GitSourceID, HeadSnapshotID: r.HeadSnapshotID, Description: r.Description, CreatedAt: r.CreatedAt,
		HeadVersion: r.HeadVersion, HeadGitSha: r.HeadGitSha}}
}

func toSnapshot(s *SnapshotInfo) apigen.Snapshot {
	return apigen.Snapshot{Id: s.ID, Version: intPtr32(s.Version), GitSha: strPtr(s.GitSha), Message: s.Message, Author: s.Author,
		CreatedAt: s.CreatedAt, ManifestHash: s.ManifestHash, FileCount: s.FileCount}
}

func toIssues(is []flow.Issue) []apigen.Issue {
	out := make([]apigen.Issue, 0, len(is))
	for _, i := range is {
		out = append(out, apigen.Issue{Code: i.Code, Path: i.Path, Line: i.Line, Column: i.Column, Message: i.Message})
	}
	return out
}

// ListNamespaces lists namespaces with implicit parents.
func (h API) ListNamespaces(ctx context.Context, _ apigen.ListNamespacesRequestObject) (apigen.ListNamespacesResponseObject, error) {
	nodes, err := h.Svc.Tree(ctx)
	if err != nil {
		return nil, err
	}
	out := apigen.ListNamespaces200JSONResponse{Items: []apigen.Namespace{}}
	for _, n := range nodes {
		out.Items = append(out.Items, toNamespace(n))
	}
	return out, nil
}

// CreateNamespace creates a managed namespace.
func (h API) CreateNamespace(ctx context.Context, req apigen.CreateNamespaceRequestObject) (apigen.CreateNamespaceResponseObject, error) {
	desc := ""
	if req.Body.Description != nil {
		desc = *req.Body.Description
	}
	ns, err := h.Svc.Create(ctx, req.Body.Name, desc)
	if err != nil {
		return nil, err
	}
	return apigen.CreateNamespace201JSONResponse(toNamespace(rowToNode(ns))), nil
}

// GetNamespace returns one namespace.
func (h API) GetNamespace(ctx context.Context, req apigen.GetNamespaceRequestObject) (apigen.GetNamespaceResponseObject, error) {
	ns, err := h.Svc.Get(ctx, req.Namespace)
	if err != nil {
		return nil, err
	}
	return apigen.GetNamespace200JSONResponse(toNamespace(rowToNode(ns))), nil
}

// DeleteNamespace deletes a managed namespace.
func (h API) DeleteNamespace(ctx context.Context, req apigen.DeleteNamespaceRequestObject) (apigen.DeleteNamespaceResponseObject, error) {
	if err := h.Svc.Delete(ctx, req.Namespace); err != nil {
		return nil, err
	}
	return apigen.DeleteNamespace204Response{}, nil
}

// ListFiles lists the files of a version.
func (h API) ListFiles(ctx context.Context, req apigen.ListFilesRequestObject) (apigen.ListFilesResponseObject, error) {
	ns, err := h.Svc.Get(ctx, req.Namespace)
	if err != nil {
		return nil, err
	}
	info, m, err := h.Svc.Resolve(ctx, ns, req.Params.Version)
	if err != nil {
		return nil, err
	}
	out := apigen.ListFiles200JSONResponse{Items: []apigen.FileEntry{}}
	if info != nil {
		out.Version = intPtr32(info.Version)
		id := info.ID
		out.SnapshotId = &id
		out.GitSha = strPtr(info.GitSha)
	}
	for _, p := range m.Paths() {
		e := m[p]
		out.Items = append(out.Items, apigen.FileEntry{Path: e.Path, Size: e.Size, Hash: e.Hash, Executable: e.Executable})
	}
	return out, nil
}

type fileResponse struct {
	r    io.ReadCloser
	size int64
	name string
}

func (f fileResponse) VisitGetFileResponse(w http.ResponseWriter) error {
	defer func() { _ = f.r.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(f.size, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(f.name)))
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, f.r)
	return err
}

// GetFile streams one file.
func (h API) GetFile(ctx context.Context, req apigen.GetFileRequestObject) (apigen.GetFileResponseObject, error) {
	r, e, err := h.Svc.ReadFile(ctx, req.Namespace, req.Params.Path, req.Params.Version)
	if err != nil {
		return nil, err
	}
	return fileResponse{r: r, size: e.Size, name: e.Path}, nil
}

// UploadFile saves one file from the raw body.
func (h API) UploadFile(ctx context.Context, req apigen.UploadFileRequestObject) (apigen.UploadFileResponseObject, error) {
	b, err := io.ReadAll(io.LimitReader(req.Body, h.Svc.MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > h.Svc.MaxFileBytes {
		return nil, errTooLarge("file_too_large", "the file is larger than %d bytes", h.Svc.MaxFileBytes)
	}
	msg := "Upload " + req.Params.Path
	if req.Params.Message != nil && *req.Params.Message != "" {
		msg = *req.Params.Message
	}
	snap, err := h.Svc.Save(ctx, req.Namespace, []Change{{Op: "put", Path: req.Params.Path, Content: b, Executable: req.Params.Executable}}, msg, nil)
	if err != nil {
		return nil, err
	}
	return apigen.UploadFile201JSONResponse(toSnapshot(snap)), nil
}

// SaveChanges applies file changes as one version.
func (h API) SaveChanges(ctx context.Context, req apigen.SaveChangesRequestObject) (apigen.SaveChangesResponseObject, error) {
	changes := make([]Change, 0, len(req.Body.Changes))
	for i, c := range req.Body.Changes {
		ch := Change{Op: string(c.Op), Path: c.Path, Executable: c.Executable}
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
	snap, err := h.Svc.Save(ctx, req.Namespace, changes, req.Body.Message, req.Body.BaseVersion)
	if err != nil {
		return nil, err
	}
	return apigen.SaveChanges201JSONResponse(toSnapshot(snap)), nil
}

// ListVersions lists snapshots.
func (h API) ListVersions(ctx context.Context, req apigen.ListVersionsRequestObject) (apigen.ListVersionsResponseObject, error) {
	list, err := h.Svc.Versions(ctx, req.Namespace, page.LimitPtr(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	out := apigen.ListVersions200JSONResponse{Items: []apigen.Snapshot{}}
	for i := range list {
		out.Items = append(out.Items, toSnapshot(&list[i]))
	}
	return out, nil
}

// DiffVersions diffs two versions.
func (h API) DiffVersions(ctx context.Context, req apigen.DiffVersionsRequestObject) (apigen.DiffVersionsResponseObject, error) {
	diffs, err := h.Svc.Diff(ctx, req.Namespace, req.Params.From, req.Params.To)
	if err != nil {
		return nil, err
	}
	out := apigen.DiffVersions200JSONResponse{From: req.Params.From, To: req.Params.To, Files: []apigen.FileDiff{}}
	for _, d := range diffs {
		out.Files = append(out.Files, apigen.FileDiff{Path: d.Path, Status: apigen.FileDiffStatus(d.Status), Binary: d.Binary, Diff: d.Diff})
	}
	return out, nil
}

// RevertVersion creates a version with the content of an old version.
func (h API) RevertVersion(ctx context.Context, req apigen.RevertVersionRequestObject) (apigen.RevertVersionResponseObject, error) {
	msg := ""
	if req.Body.Message != nil {
		msg = *req.Body.Message
	}
	snap, err := h.Svc.Revert(ctx, req.Namespace, req.Body.Version, msg)
	if err != nil {
		return nil, err
	}
	return apigen.RevertVersion201JSONResponse(toSnapshot(snap)), nil
}

// ValidateFile validates proposed content (inline editor validation, REQ-UI-007).
func (h API) ValidateFile(ctx context.Context, req apigen.ValidateFileRequestObject) (apigen.ValidateFileResponseObject, error) {
	kind, id, issues, err := h.Svc.ValidateFile(ctx, req.Namespace, req.Body.Path, req.Body.Content)
	if err != nil {
		return nil, err
	}
	return apigen.ValidateFile200JSONResponse{Valid: len(issues) == 0, Kind: apigen.ValidateFileResultKind(kind), FlowId: strPtr(id), Errors: toIssues(issues)}, nil
}

// ListFlows lists flows with the state of their last execution.
func (h API) ListFlows(ctx context.Context, req apigen.ListFlowsRequestObject) (apigen.ListFlowsResponseObject, error) {
	prefix := ""
	if req.Params.Namespace != nil {
		prefix = *req.Params.Namespace
	}
	var after []string
	if req.Params.Cursor != nil && *req.Params.Cursor != "" {
		var err error
		if after, err = page.DecodeStrings(*req.Params.Cursor, 2); err != nil {
			return nil, err
		}
	}
	limit := page.LimitPtr(req.Params.Limit)
	rows, err := h.Svc.ListFlows(ctx, prefix, after, limit+1)
	if err != nil {
		return nil, err
	}
	out := apigen.ListFlows200JSONResponse{Items: []apigen.FlowSummary{}}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		c := page.EncodeStrings(last.Namespace, last.FlowID)
		out.NextCursor = &c
	}
	for _, r := range rows {
		out.Items = append(out.Items, r.summary())
	}
	return out, nil
}

func (r FlowListRow) summary() apigen.FlowSummary {
	s := apigen.FlowSummary{Id: r.ID, Namespace: r.Namespace, FlowId: r.FlowID, Path: r.Path, Valid: r.Valid, Disabled: r.Disabled,
		Description: r.Description, ErrorCount: r.ErrorCount}
	if len(r.Labels) > 0 {
		l := r.Labels
		s.Labels = &l
	}
	if r.LastID != nil {
		s.LastExecution = &apigen.ExecutionRef{Id: *r.LastID, State: r.LastState, CreatedAt: *r.LastCreated}
	}
	return s
}

func (h API) detail(ctx context.Context, namespace, flowID string) (apigen.FlowDetail, error) {
	f, err := h.Svc.GetFlow(ctx, namespace, flowID)
	if err != nil {
		return apigen.FlowDetail{}, err
	}
	rows, err := h.Svc.ListFlowsByID(ctx, f.ID)
	if err != nil {
		return apigen.FlowDetail{}, err
	}
	if len(rows) == 0 {
		return apigen.FlowDetail{}, ErrFlowNotFound
	}
	sum := rows[0].summary()
	d := apigen.FlowDetail{Id: sum.Id, Namespace: sum.Namespace, FlowId: sum.FlowId, Path: sum.Path, Valid: sum.Valid, Disabled: sum.Disabled,
		Description: sum.Description, ErrorCount: sum.ErrorCount, Labels: sum.Labels, LastExecution: sum.LastExecution, Triggers: []apigen.TriggerInfo{}}
	q := dbq.New(h.Svc.Pool)
	if f.CurrentRevisionID != nil {
		rev, err := h.revision(ctx, f.ID, *f.CurrentRevisionID)
		if err != nil {
			return d, err
		}
		d.Revision = &rev
	}
	triggers, err := q.ListFlowTriggers(ctx, f.ID)
	if err != nil {
		return d, err
	}
	for _, t := range triggers {
		cfg := map[string]any{}
		_ = json.Unmarshal(t.Config, &cfg)
		d.Triggers = append(d.Triggers, apigen.TriggerInfo{Key: t.TriggerKey, Type: apigen.TriggerInfoType(t.Type), Active: t.Active, Config: cfg,
			NextFireAt: t.NextFireAt, LastFiredAt: t.LastFiredAt, HasWebhookKey: len(t.WebhookKeyHash) > 0})
	}
	return d, nil
}

func (h API) revision(ctx context.Context, flowID, revID uuid.UUID) (apigen.Revision, error) {
	q := dbq.New(h.Svc.Pool)
	r, err := q.GetFlowRevision(ctx, revID)
	if err != nil || r.FlowID != flowID {
		return apigen.Revision{}, httpx.Errorf(http.StatusNotFound, "revision_not_found", "revision not found")
	}
	var issues []flow.Issue
	_ = json.Unmarshal(r.Errors, &issues)
	out := apigen.Revision{Id: r.ID, CreatedAt: r.CreatedAt, Path: r.Path, Source: r.Source, Valid: len(issues) == 0, Errors: toIssues(issues)}
	if len(r.Definition) > 0 && string(r.Definition) != "null" {
		def := map[string]any{}
		if json.Unmarshal(r.Definition, &def) == nil {
			out.Definition = &def
		}
	}
	if sn, err := q.GetSnapshot(ctx, r.SnapshotID); err == nil {
		out.SnapshotVersion = intPtr32(sn.Version)
		out.GitSha = strPtr(sn.GitSha)
		out.Message = strPtr(sn.Message)
	}
	return out, nil
}

// GetFlow returns a flow with revision and triggers.
func (h API) GetFlow(ctx context.Context, req apigen.GetFlowRequestObject) (apigen.GetFlowResponseObject, error) {
	d, err := h.detail(ctx, req.Namespace, req.FlowId)
	if err != nil {
		return nil, err
	}
	return apigen.GetFlow200JSONResponse(d), nil
}

// UpdateFlow enables or disables a flow.
func (h API) UpdateFlow(ctx context.Context, req apigen.UpdateFlowRequestObject) (apigen.UpdateFlowResponseObject, error) {
	if err := h.Svc.SetDisabled(ctx, req.Namespace, req.FlowId, req.Body.Disabled); err != nil {
		return nil, err
	}
	d, err := h.detail(ctx, req.Namespace, req.FlowId)
	if err != nil {
		return nil, err
	}
	return apigen.UpdateFlow200JSONResponse(d), nil
}

// ListFlowRevisions lists revisions of a flow.
func (h API) ListFlowRevisions(ctx context.Context, req apigen.ListFlowRevisionsRequestObject) (apigen.ListFlowRevisionsResponseObject, error) {
	f, err := h.Svc.GetFlow(ctx, req.Namespace, req.FlowId)
	if err != nil {
		return nil, err
	}
	rows, err := dbq.New(h.Svc.Pool).ListFlowRevisions(ctx, dbq.ListFlowRevisionsParams{FlowID: f.ID, Limit: int32(page.LimitPtr(req.Params.Limit))})
	if err != nil {
		return nil, err
	}
	out := apigen.ListFlowRevisions200JSONResponse{Items: []apigen.RevisionSummary{}}
	for _, r := range rows {
		var issues []flow.Issue
		_ = json.Unmarshal(r.Errors, &issues)
		out.Items = append(out.Items, apigen.RevisionSummary{Id: r.ID, CreatedAt: r.CreatedAt, SnapshotVersion: intPtr32(r.Version),
			GitSha: strPtr(r.GitSha), Message: strPtr(r.Message), Valid: len(issues) == 0, ErrorCount: len(issues)})
	}
	return out, nil
}

// GetFlowRevision returns one revision.
func (h API) GetFlowRevision(ctx context.Context, req apigen.GetFlowRevisionRequestObject) (apigen.GetFlowRevisionResponseObject, error) {
	f, err := h.Svc.GetFlow(ctx, req.Namespace, req.FlowId)
	if err != nil {
		return nil, err
	}
	rev, err := h.revision(ctx, f.ID, req.RevisionId)
	if err != nil {
		return nil, err
	}
	return apigen.GetFlowRevision200JSONResponse(rev), nil
}

// DiffFlowRevisions diffs two revisions of a flow (REQ-FLOW-005).
func (h API) DiffFlowRevisions(ctx context.Context, req apigen.DiffFlowRevisionsRequestObject) (apigen.DiffFlowRevisionsResponseObject, error) {
	f, err := h.Svc.GetFlow(ctx, req.Namespace, req.FlowId)
	if err != nil {
		return nil, err
	}
	a, err := h.revision(ctx, f.ID, req.Params.From)
	if err != nil {
		return nil, err
	}
	b, err := h.revision(ctx, f.ID, req.Params.To)
	if err != nil {
		return nil, err
	}
	label := func(r apigen.Revision) string {
		if r.SnapshotVersion != nil {
			return fmt.Sprintf("v%d", *r.SnapshotVersion)
		}
		return r.Id.String()[:8]
	}
	return apigen.DiffFlowRevisions200JSONResponse{Diff: UnifiedDiff(b.Path, label(a), label(b), a.Source, b.Source)}, nil
}

// GetFlowSchema returns the flow JSON Schema (REQ-FLOW-002).
func (h API) GetFlowSchema(context.Context, apigen.GetFlowSchemaRequestObject) (apigen.GetFlowSchemaResponseObject, error) {
	b, err := flow.FlowSchema()
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return apigen.GetFlowSchema200JSONResponse(m), nil
}
