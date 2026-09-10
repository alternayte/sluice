package namespace

import (
	"context"
	"net/http"
	"regexp"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/namespace/namespacedb"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// nameRE is the pattern of a namespace name in a path (api/openapi.yaml NamespacePath). The Raw
// routes use it. The path struct tags repeat it with the backslash escaped for the tag syntax.
var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// Namespace is one namespace. An implicit namespace exists only because of its children.
type Namespace struct {
	Name        string     `json:"name"`
	SourceType  string     `json:"source_type" enum:"managed,git,implicit"`
	Description string     `json:"description"`
	Implicit    bool       `json:"implicit" doc:"True for a parent that exists only because of its children."`
	Parent      *string    `json:"parent,omitempty"`
	HeadVersion *int       `json:"head_version,omitempty" nullable:"true"`
	HeadGitSha  *string    `json:"head_git_sha,omitempty"`
	GitSourceID *uuid.UUID `json:"git_source_id,omitempty" nullable:"true"`
	ReadOnly    *bool      `json:"read_only,omitempty"`
}

// NamespaceList is the list of namespaces in name order.
type NamespaceList struct {
	Items []Namespace `json:"items"`
}

type namespaceOut struct{ Body Namespace }

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optInt32(v *int32) *int {
	if v == nil {
		return nil
	}
	x := int(*v)
	return &x
}

func toNamespaceOp(n TreeNode) Namespace {
	out := Namespace{Name: n.Name, Implicit: n.Implicit, SourceType: "implicit", Parent: optStr(n.Parent)}
	if n.Row != nil {
		out.SourceType = n.Row.SourceType
		out.Description = n.Row.Description
		out.HeadVersion = optInt32(n.Row.HeadVersion)
		if n.Row.HeadGitSha != nil {
			out.HeadGitSha = optStr(*n.Row.HeadGitSha)
		}
		out.GitSourceID = n.Row.GitSourceID
		ro := n.Row.SourceType == "git"
		out.ReadOnly = &ro
	}
	return out
}

func withStatus(op huma.Operation, status int) huma.Operation {
	op.DefaultStatus = status
	return op
}

// unlimitedBody removes the huma body size limit and read timeout. The service size limits
// (file_too_large and snapshot_too_large) then apply, as before.
func unlimitedBody(op huma.Operation) huma.Operation {
	op.MaxBodyBytes = -1
	op.BodyReadTimeout = -1
	return op
}

// Routes registers the namespace, file and flow operations (REQ-NS, REQ-FLOW). The file transfer
// operations are Raw routes on r.
func Routes(api huma.API, r chi.Router, s *Service) {
	registerNamespaces(api, s)
	registerFiles(api, r, s)
	registerFlows(api, s)
}

func registerNamespaces(api huma.API, s *Service) {
	viewer := httpx.MinRole(kernel.Viewer)

	huma.Register(api, httpx.Op("listNamespaces", http.MethodGet, "/api/v1/namespaces", viewer),
		func(ctx context.Context, _ *struct{}) (*struct{ Body NamespaceList }, error) {
			nodes, err := s.Tree(ctx)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body NamespaceList }{Body: NamespaceList{Items: []Namespace{}}}
			for _, n := range nodes {
				out.Body.Items = append(out.Body.Items, toNamespaceOp(n))
			}
			return out, nil
		})

	// The service validates the name syntax (422 validation_failed). The spec has no pattern on the body name.
	huma.Register(api, withStatus(httpx.Op("createNamespace", http.MethodPost, "/api/v1/namespaces", httpx.MinRole(kernel.Editor)), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				Name        string `json:"name" maxLength:"128"`
				Description string `json:"description,omitempty" maxLength:"2000"`
			}
		}) (*namespaceOut, error) {
			ns, err := s.Create(ctx, in.Body.Name, in.Body.Description)
			if err != nil {
				return nil, err
			}
			return &namespaceOut{Body: toNamespaceOp(rowToNode(ns))}, nil
		})

	huma.Register(api, httpx.Op("getNamespace", http.MethodGet, "/api/v1/namespaces/{namespace}", viewer),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
		}) (*namespaceOut, error) {
			ns, err := s.Get(ctx, in.Namespace)
			if err != nil {
				return nil, err
			}
			return &namespaceOut{Body: toNamespaceOp(rowToNode(ns))}, nil
		})

	huma.Register(api, withStatus(httpx.Op("deleteNamespace", http.MethodDelete, "/api/v1/namespaces/{namespace}", httpx.MinRole(kernel.Admin)), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
		}) (*struct{}, error) {
			return nil, s.Delete(ctx, in.Namespace)
		})
}

func rowToNode(r namespacedb.GetNamespaceRow) TreeNode {
	return TreeNode{Name: r.Name, Parent: ParentOf(r.Name), Row: &namespacedb.ListNamespacesRow{ID: r.ID, Name: r.Name, SourceType: r.SourceType,
		GitSourceID: r.GitSourceID, HeadSnapshotID: r.HeadSnapshotID, Description: r.Description, CreatedAt: r.CreatedAt,
		HeadVersion: r.HeadVersion, HeadGitSha: r.HeadGitSha}}
}
