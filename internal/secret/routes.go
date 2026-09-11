package secret

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// SecretInfo is one secret. The API never returns a value (REQ-SEC-002, SI-01).
type SecretInfo struct {
	Key            string     `json:"key"`
	Scope          string     `json:"scope" doc:"global or the namespace name that defines the secret."`
	Provider       string     `json:"provider"`
	ProviderType   string     `json:"provider_type" enum:"builtin,env,kubernetes,azure_key_vault,vault"`
	Ref            string     `json:"ref,omitempty" doc:"Reference of an external secret. Builtin secrets have none."`
	Description    string     `json:"description"`
	UpdatedBy      string     `json:"updated_by"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastResolvedAt *time.Time `json:"last_resolved_at,omitempty" nullable:"true"`
	Inherited      bool       `json:"inherited" doc:"True when a parent namespace or the global scope defines the secret."`
}

// SecretList is a list of secrets sorted by key.
type SecretList struct {
	Items []SecretInfo `json:"items"`
}

// SecretPut is the body of a secret write.
type SecretPut struct {
	Provider    string  `json:"provider,omitempty" maxLength:"63" doc:"Provider name. Default builtin, or the provider of the existing secret."`
	Value       *string `json:"value,omitempty" minLength:"1" maxLength:"65536" writeOnly:"true" doc:"Value of a builtin secret. Write-only."`
	Ref         string  `json:"ref,omitempty" maxLength:"512" doc:"Reference of an external secret, for example path#field for vault."`
	Description *string `json:"description,omitempty" maxLength:"500"`
}

// CheckResult is the result of a secret check. It never holds the value (REQ-SEC-004).
type CheckResult struct {
	Status  string `json:"status" enum:"ok,not_found,access_denied,provider_error"`
	Message string `json:"message,omitempty"`
}

// ProviderOut is one secret provider. Its configuration holds no credentials.
type ProviderOut struct {
	Name      string         `json:"name"`
	Type      string         `json:"type" enum:"builtin,env,kubernetes,azure_key_vault,vault"`
	Config    map[string]any `json:"config"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// ProviderList is the list of providers.
type ProviderList struct {
	Items []ProviderOut `json:"items"`
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

type providerIn struct {
	Name string `path:"name" pattern:"^[a-z0-9][a-z0-9_-]{0,62}$"`
}

func toOut(i Info) SecretInfo { return SecretInfo(i) }

func providerOut(p ProviderInfo) ProviderOut { return ProviderOut(p) }

type secretOut struct{ Body SecretInfo }

type noBody struct{}

// Routes registers the secret and provider operations. Global secrets and providers need
// admin, namespace secrets need editor, and every role lists keys and metadata (Appendix B).
func Routes(api huma.API, s *Service) {
	viewer, editor, admin := httpx.MinRole(kernel.Viewer), httpx.MinRole(kernel.Editor), httpx.MinRole(kernel.Admin)

	list := func(ctx context.Context, ns string) (*struct{ Body SecretList }, error) {
		items, err := s.List(ctx, ns)
		if err != nil {
			return nil, err
		}
		out := &struct{ Body SecretList }{Body: SecretList{Items: []SecretInfo{}}}
		for _, i := range items {
			out.Body.Items = append(out.Body.Items, toOut(i))
		}
		return out, nil
	}
	put := func(ctx context.Context, scope, key string, body SecretPut) (*secretOut, error) {
		if _, err := s.Put(ctx, scope, key, PutInput(body)); err != nil {
			return nil, err
		}
		i, err := s.Get(ctx, scope, key)
		if err != nil {
			return nil, err
		}
		return &secretOut{Body: toOut(i)}, nil
	}
	check := func(ctx context.Context, scope, key string) (*struct{ Body CheckResult }, error) {
		st, msg, err := s.Check(ctx, scope, key)
		if err != nil {
			return nil, err
		}
		return &struct{ Body CheckResult }{Body: CheckResult{Status: st, Message: msg}}, nil
	}

	huma.Register(api, httpx.Op("listGlobalSecrets", http.MethodGet, "/api/v1/secrets", viewer),
		func(ctx context.Context, _ *struct{}) (*struct{ Body SecretList }, error) { return list(ctx, "") })
	huma.Register(api, httpx.Op("putGlobalSecret", http.MethodPut, "/api/v1/secrets/{key}", admin),
		func(ctx context.Context, in *struct {
			Key  string `path:"key" pattern:"^[A-Za-z_][A-Za-z0-9_]{0,127}$"`
			Body SecretPut
		}) (*secretOut, error) {
			return put(ctx, "", in.Key, in.Body)
		})
	del := httpx.Op("deleteGlobalSecret", http.MethodDelete, "/api/v1/secrets/{key}", admin)
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *globalKeyIn) (*noBody, error) { return nil, s.Delete(ctx, "", in.Key) })
	huma.Register(api, httpx.Op("checkGlobalSecret", http.MethodPost, "/api/v1/secrets/{key}/check", admin),
		func(ctx context.Context, in *globalKeyIn) (*struct{ Body CheckResult }, error) {
			return check(ctx, "", in.Key)
		})

	huma.Register(api, httpx.Op("listNamespaceSecrets", http.MethodGet, "/api/v1/namespaces/{namespace}/secrets", viewer),
		func(ctx context.Context, in *nsIn) (*struct{ Body SecretList }, error) {
			return list(ctx, in.Namespace)
		})
	huma.Register(api, httpx.Op("putNamespaceSecret", http.MethodPut, "/api/v1/namespaces/{namespace}/secrets/{key}", editor),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			Key       string `path:"key" pattern:"^[A-Za-z_][A-Za-z0-9_]{0,127}$"`
			Body      SecretPut
		}) (*secretOut, error) {
			return put(ctx, in.Namespace, in.Key, in.Body)
		})
	nsDel := httpx.Op("deleteNamespaceSecret", http.MethodDelete, "/api/v1/namespaces/{namespace}/secrets/{key}", editor)
	nsDel.DefaultStatus = http.StatusNoContent
	huma.Register(api, nsDel, func(ctx context.Context, in *nsKeyIn) (*noBody, error) {
		return nil, s.Delete(ctx, in.Namespace, in.Key)
	})
	huma.Register(api, httpx.Op("checkNamespaceSecret", http.MethodPost, "/api/v1/namespaces/{namespace}/secrets/{key}/check", editor),
		func(ctx context.Context, in *nsKeyIn) (*struct{ Body CheckResult }, error) {
			return check(ctx, in.Namespace, in.Key)
		})

	// Editors list providers to choose one in the secret form. Changes need admin (DI-30).
	huma.Register(api, httpx.Op("listSecretProviders", http.MethodGet, "/api/v1/secret-providers", editor),
		func(ctx context.Context, _ *struct{}) (*struct{ Body ProviderList }, error) {
			items, err := s.Providers(ctx)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body ProviderList }{Body: ProviderList{Items: []ProviderOut{}}}
			for _, p := range items {
				out.Body.Items = append(out.Body.Items, providerOut(p))
			}
			return out, nil
		})
	create := httpx.Op("createSecretProvider", http.MethodPost, "/api/v1/secret-providers", admin)
	create.DefaultStatus = http.StatusCreated
	huma.Register(api, create, func(ctx context.Context, in *struct {
		Body struct {
			Name   string         `json:"name" pattern:"^[a-z0-9][a-z0-9_-]{0,62}$"`
			Type   string         `json:"type" enum:"kubernetes,azure_key_vault,vault"`
			Config map[string]any `json:"config,omitempty"`
		}
	}) (*struct{ Body ProviderOut }, error) {
		p, err := s.CreateProvider(ctx, in.Body.Name, in.Body.Type, in.Body.Config)
		if err != nil {
			return nil, err
		}
		return &struct{ Body ProviderOut }{Body: providerOut(p)}, nil
	})
	huma.Register(api, httpx.Op("updateSecretProvider", http.MethodPut, "/api/v1/secret-providers/{name}", admin),
		func(ctx context.Context, in *struct {
			Name string `path:"name" pattern:"^[a-z0-9][a-z0-9_-]{0,62}$"`
			Body struct {
				Config map[string]any `json:"config"`
			}
		}) (*struct{ Body ProviderOut }, error) {
			p, err := s.UpdateProvider(ctx, in.Name, in.Body.Config)
			if err != nil {
				return nil, err
			}
			return &struct{ Body ProviderOut }{Body: providerOut(p)}, nil
		})
	pdel := httpx.Op("deleteSecretProvider", http.MethodDelete, "/api/v1/secret-providers/{name}", admin)
	pdel.DefaultStatus = http.StatusNoContent
	huma.Register(api, pdel, func(ctx context.Context, in *providerIn) (*noBody, error) { return nil, s.DeleteProvider(ctx, in.Name) })
	huma.Register(api, httpx.Op("checkSecretProvider", http.MethodPost, "/api/v1/secret-providers/{name}/check", admin),
		func(ctx context.Context, in *struct {
			Name string `path:"name" pattern:"^[a-z0-9][a-z0-9_-]{0,62}$"`
			Body struct {
				Ref string `json:"ref" minLength:"1" maxLength:"512"`
			}
		}) (*struct{ Body CheckResult }, error) {
			st, msg, err := s.CheckProvider(ctx, in.Name, in.Body.Ref)
			if err != nil {
				return nil, err
			}
			return &struct{ Body CheckResult }{Body: CheckResult{Status: st, Message: msg}}, nil
		})
}
