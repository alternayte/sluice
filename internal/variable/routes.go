package variable

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// VariableInfo is one variable.
type VariableInfo struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Scope     string    `json:"scope" doc:"global or the namespace name that defines the variable."`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
	Inherited bool      `json:"inherited" doc:"True when a parent namespace or the global scope defines the variable."`
}

// VariableList is a list of variables sorted by key.
type VariableList struct {
	Items []VariableInfo `json:"items"`
}

// VariablePut is the body of a variable write.
type VariablePut struct {
	Value string `json:"value" maxLength:"65536"`
}

type globalKeyIn struct {
	Key string `path:"key" pattern:"^[A-Za-z_][A-Za-z0-9_]{0,127}$"`
}

type nsIn struct {
	Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
}

type nsKeyIn struct {
	Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
	Key       string `path:"key" pattern:"^[A-Za-z_][A-Za-z0-9_]{0,127}$"`
}

func toOut(i Info) VariableInfo {
	return VariableInfo(i)
}

type noBody struct{}

// Routes registers the variable operations. Every role reads variables. Global writes
// need admin, namespace writes need editor (Appendix B).
func Routes(api huma.API, s *Service) {
	viewer, editor, admin := httpx.MinRole(kernel.Viewer), httpx.MinRole(kernel.Editor), httpx.MinRole(kernel.Admin)

	list := func(ctx context.Context, ns string) (*struct{ Body VariableList }, error) {
		items, err := s.List(ctx, ns)
		if err != nil {
			return nil, err
		}
		out := &struct{ Body VariableList }{Body: VariableList{Items: []VariableInfo{}}}
		for _, i := range items {
			out.Body.Items = append(out.Body.Items, toOut(i))
		}
		return out, nil
	}
	put := func(ctx context.Context, scope, key, value string) (*struct{ Body VariableInfo }, error) {
		i, err := s.Put(ctx, scope, key, value)
		if err != nil {
			return nil, err
		}
		return &struct{ Body VariableInfo }{Body: toOut(i)}, nil
	}

	huma.Register(api, httpx.Op("listGlobalVariables", http.MethodGet, "/api/v1/variables", viewer),
		func(ctx context.Context, _ *struct{}) (*struct{ Body VariableList }, error) { return list(ctx, "") })
	huma.Register(api, httpx.Op("putGlobalVariable", http.MethodPut, "/api/v1/variables/{key}", admin),
		func(ctx context.Context, in *struct {
			globalKeyIn
			Body VariablePut
		}) (*struct{ Body VariableInfo }, error) {
			return put(ctx, "", in.Key, in.Body.Value)
		})
	del := httpx.Op("deleteGlobalVariable", http.MethodDelete, "/api/v1/variables/{key}", admin)
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *globalKeyIn) (*noBody, error) { return nil, s.Delete(ctx, "", in.Key) })

	huma.Register(api, httpx.Op("listNamespaceVariables", http.MethodGet, "/api/v1/namespaces/{namespace}/variables", viewer),
		func(ctx context.Context, in *nsIn) (*struct{ Body VariableList }, error) {
			return list(ctx, in.Namespace)
		})
	huma.Register(api, httpx.Op("putNamespaceVariable", http.MethodPut, "/api/v1/namespaces/{namespace}/variables/{key}", editor),
		func(ctx context.Context, in *struct {
			nsKeyIn
			Body VariablePut
		}) (*struct{ Body VariableInfo }, error) {
			return put(ctx, in.Namespace, in.Key, in.Body.Value)
		})
	nsDel := httpx.Op("deleteNamespaceVariable", http.MethodDelete, "/api/v1/namespaces/{namespace}/variables/{key}", editor)
	nsDel.DefaultStatus = http.StatusNoContent
	huma.Register(api, nsDel, func(ctx context.Context, in *nsKeyIn) (*noBody, error) {
		return nil, s.Delete(ctx, in.Namespace, in.Key)
	})
}
