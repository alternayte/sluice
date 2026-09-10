package app

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/instance"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/clock"
)

// services holds what the routes call. `sluice openapi` passes the zero value:
// route registration must not use a service.
type services struct {
	Auth             *auth.Service
	Audit            *audit.Writer
	Instances        *instance.Registry
	Clock            clock.Clock
	Namespaces       *namespace.Service
	Engine           *execution.Engine
	MaxArtifactBytes int64
}

// registerRoutes registers every API operation on api, and the streamed routes on r.
func registerRoutes(api huma.API, r chi.Router, s services) {
	auth.Routes(api, s.Auth)
	audit.Routes(api, s.Audit)
	instance.Routes(api, s.Instances, s.Clock)
	namespace.Routes(api, r, s.Namespaces)
}

func (s *Server) services() services {
	return services{Auth: s.Auth, Audit: s.Audit, Instances: s.Registry, Clock: s.Clock, Namespaces: s.Namespaces,
		Engine: s.Engine, MaxArtifactBytes: int64(s.Cfg.MaxArtifactBytes)}
}
