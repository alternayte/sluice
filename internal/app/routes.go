package app

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/instance"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/secret"
	"github.com/alternayte/sluice/internal/trigger"
	"github.com/alternayte/sluice/internal/variable"
)

// services holds what the routes call. `sluice openapi` passes the zero value:
// route registration must not use a service.
type services struct {
	Auth       *auth.Service
	Audit      *audit.Writer
	Instances  *instance.Registry
	Clock      clock.Clock
	Namespaces *namespace.Service
	Engine     *execution.Engine
	Triggers   *trigger.Service
	Secrets    *secret.Service
	Variables  *variable.Service
}

// registerRoutes registers every API operation on api, and the streamed routes on r.
func registerRoutes(api huma.API, r chi.Router, s services) {
	// The execution and namespace packages each have an ExecutionRef type with the same JSON
	// form. Both use the one ExecutionRef schema of api/openapi.yaml.
	api.OpenAPI().Components.Schemas.RegisterTypeAlias(reflect.TypeFor[execution.ExecutionRef](), reflect.TypeFor[namespace.ExecutionRef]())
	auth.Routes(api, s.Auth)
	audit.Routes(api, s.Audit)
	instance.Routes(api, s.Instances, s.Clock)
	namespace.Routes(api, r, s.Namespaces)
	execution.Routes(api, r, s.Engine)
	execution.RunnerRoutes(api, r, s.Engine)
	trigger.Routes(api, r, s.Triggers)
	secret.Routes(api, s.Secrets)
	variable.Routes(api, s.Variables)
}

func (s *Server) services() services {
	return services{Auth: s.Auth, Audit: s.Audit, Instances: s.Registry, Clock: s.Clock, Namespaces: s.Namespaces,
		Engine: s.Engine, Triggers: s.Triggers, Secrets: s.Secrets, Variables: s.Variables}
}
