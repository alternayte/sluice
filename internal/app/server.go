package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/api"
	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/executor"
	"github.com/alternayte/sluice/internal/instance"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/db"
	"github.com/alternayte/sluice/internal/platform/health"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/lease"
	"github.com/alternayte/sluice/internal/platform/logging"
	"github.com/alternayte/sluice/internal/platform/promx"
	"github.com/alternayte/sluice/internal/runnerapi"
	"github.com/alternayte/sluice/internal/storage"
)

func storageConfig(cfg *Config) storage.Config {
	return storage.Config{
		Type:   cfg.StorageType,
		FSRoot: cfg.FSRoot,
		S3: storage.S3Config{Bucket: cfg.S3Bucket, Region: cfg.S3Region, Endpoint: cfg.S3Endpoint, ForcePathStyle: cfg.S3ForcePathStyle,
			AccessKeyID: cfg.S3AccessKeyID, SecretAccessKey: cfg.S3SecretAccessKey, Prefix: cfg.S3Prefix},
		Azblob: storage.AzblobConfig{AccountURL: cfg.AzblobAccountURL, ConnectionString: cfg.AzblobConnectionString,
			Container: cfg.AzblobContainer, Prefix: cfg.AzblobPrefix},
	}
}

// Server is one running sluice server instance.
type Server struct {
	Cfg        *Config
	Log        *slog.Logger
	Pool       *pgxpool.Pool
	Clock      clock.Clock
	Instance   instance.Info
	Leases     *lease.Store
	Health     *health.Checker
	Metrics    *promx.Metrics
	Registry   *instance.Registry
	Audit      *audit.Writer
	Auth       *auth.Service
	Store      storage.Store
	GC         *storage.GC
	Namespaces *namespace.Service
	Engine     *execution.Engine

	httpServer *http.Server
	listener   net.Listener
	wg         sync.WaitGroup
}

// NewServer opens the database, applies migrations and builds all components.
func NewServer(ctx context.Context, cfg *Config, log *slog.Logger) (*Server, error) {
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if len(applied) > 0 {
		log.Info("migrations applied", "migrations", applied)
	}
	hostname, _ := os.Hostname()
	id, _ := uuid.NewV7()
	clk := clock.Real{}
	s := &Server{
		Cfg:   cfg,
		Log:   log,
		Pool:  pool,
		Clock: clk,
		Instance: instance.Info{
			ID:        id,
			Hostname:  hostname,
			Version:   Version,
			Pools:     cfg.Pools,
			Executors: enabledExecutors(cfg),
			StartedAt: clk.Now(),
		},
		Health:   &health.Checker{},
		Registry: &instance.Registry{Pool: pool, Clock: clk, Log: log},
		Audit:    &audit.Writer{Pool: pool, Clock: clk},
	}
	s.Auth = newAuthService(cfg, pool, clk, s.Audit, log)
	s.Leases = &lease.Store{Pool: pool, Clock: clk, Holder: id.String()}
	s.Metrics = promx.New(pool, log)
	s.Health.Add("database", func(ctx context.Context) error {
		var one int
		return pool.QueryRow(ctx, "SELECT 1").Scan(&one)
	})
	s.Health.Add("migrations", func(ctx context.Context) error { return db.MigrationsCurrent(ctx, pool) })
	s.Store, err = storage.Open(ctx, storageConfig(cfg), pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("open storage: %w", err)
	}
	healthKey := "health/" + id.String()
	s.Health.Add("storage", func(ctx context.Context) error { return storage.RoundTrip(ctx, s.Store, healthKey) })
	s.GC = &storage.GC{Pool: pool, Store: s.Store, Clock: clk, Log: log}
	s.Namespaces = &namespace.Service{Pool: pool, Store: s.Store, Clock: clk, Audit: s.Audit, Log: log,
		MaxFileBytes: int64(cfg.MaxFileBytes), MaxBundleBytes: int64(cfg.MaxBundleBytes)}
	s.Engine = &execution.Engine{Pool: pool, OfflineAfter: instance.OfflineAfter, Clock: clk, Log: log, Audit: s.Audit, Namespaces: s.Namespaces, Store: s.Store,
		Instance: id, Pools: cfg.Pools, Cfg: execution.Config{WorkerSlots: cfg.WorkerSlots, K8sMaxJobs: cfg.K8sMaxJobs,
			PollInterval: cfg.QueuePollInterval, HeartbeatTimeout: cfg.HeartbeatTimeout, APIURL: cfg.InternalURL,
			DockerAPIURL: cfg.DockerAPIURL, ClusterAPIURL: cfg.InternalURL,
			MaxArtifactBytes: int64(cfg.MaxArtifactBytes), MaxBundleBytes: int64(cfg.MaxBundleBytes)}}
	s.Engine.Executors = map[string]executor.Executor{}
	for _, t := range s.Instance.Executors {
		switch t {
		case executor.Inline:
			s.Engine.Executors[t] = &executor.InlineExecutor{Run: s.Engine.RunInline}
		case executor.Process:
			s.Engine.Executors[t] = &executor.ProcessExecutor{Log: log, Output: os.Stderr}
		}
	}
	created, err := s.Auth.Bootstrap(ctx, cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap admin: %w", err)
	}
	if created {
		log.Info("bootstrap admin created", "email", cfg.BootstrapAdminEmail)
	}
	return s, nil
}

func newAuthService(cfg *Config, pool *pgxpool.Pool, clk clock.Clock, aw *audit.Writer, log *slog.Logger) *auth.Service {
	origin := ""
	if u, err := url.Parse(cfg.PublicURL); err == nil && u.Host != "" {
		origin = u.Scheme + "://" + u.Host
	}
	return &auth.Service{Pool: pool, Clock: clk, Audit: aw, Log: log, SessionTTL: cfg.SessionTTL,
		SecureCookie: cfg.SecureCookies(), PublicOrigin: origin}
}

func enabledExecutors(cfg *Config) []string {
	out := []string{"inline"}
	for _, e := range cfg.Executors {
		switch e {
		case "auto":
			out = append(out, "process")
		case "inline":
		default:
			out = append(out, e)
		}
	}
	return out
}

// Handler builds the root HTTP handler.
func (s *Server) Handler() (http.Handler, error) {
	r := chi.NewMux()
	r.Use(httpx.SecurityHeaders, httpx.WithRequestID, s.withLogger, s.metrics, s.Auth.Middleware,
		runnerapi.ContentTypeMiddleware, runnerapi.TokenMiddleware(s.Engine))
	r.Get("/healthz", health.Healthz)
	r.Get("/readyz", s.Health.Readyz)
	r.Method(http.MethodGet, "/metrics", s.Metrics.Handler())
	api := httpx.NewAPI(r)
	registerRoutes(api, r, s.services())
	if err := httpx.CheckAccess(api); err != nil {
		return nil, err
	}
	legacy, err := s.legacyAPI()
	if err != nil {
		return nil, err
	}
	r.Handle("/api/*", legacy)
	r.Handle("/*", spaHandler())
	return r, nil
}

// legacyAPI serves the operations of the old generated server until each feature moves to huma.
// chi matches the huma routes first, because a static path segment wins over the /api/* wildcard.
func (s *Server) legacyAPI() (http.Handler, error) {
	mux := http.NewServeMux()
	apiServer := &api.Server{
		System:    api.System{Instances: s.Registry, Clock: s.Clock},
		Handlers:  auth.Handlers{Svc: s.Auth},
		API:       namespace.API{Svc: s.Namespaces},
		ExecAPI:   execution.ExecAPI{E: s.Engine},
		RunnerAPI: runnerapi.RunnerAPI{B: s.Engine, MaxArtifactBytes: int64(s.Cfg.MaxArtifactBytes)},
	}
	if err := api.Mount(mux, apiServer, nil); err != nil {
		return nil, err
	}
	authorize, err := api.AuthorizeMiddleware(auth.Authorize)
	if err != nil {
		return nil, err
	}
	return authorize(mux), nil
}

func (s *Server) withLogger(next http.Handler) http.Handler { return withLogger(s.Log, next) }

// metrics records the HTTP duration with the chi route pattern as the route label.
func (s *Server) metrics(next http.Handler) http.Handler {
	return s.Metrics.Middleware(func(r *http.Request) string {
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			return rc.RoutePattern()
		}
		return "unmatched"
	}, next)
}

func withLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(logging.With(r.Context(), log)))
	})
}

// Run serves until ctx ends, then shuts down within the grace period (REQ-CORE-008).
func (s *Server) Run(ctx context.Context) error {
	if err := s.Registry.Register(ctx, s.Instance); err != nil {
		return fmt.Errorf("register instance: %w", err)
	}
	h, err := s.Handler()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", s.Cfg.ListenAddr)
	if err != nil {
		return err
	}
	s.listener = ln
	s.httpServer = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}

	bg, stopBG := context.WithCancel(context.WithoutCancel(ctx))
	s.goBG(func() { s.Registry.Run(bg, s.Instance) })
	s.goBG(func() { s.Engine.Run(bg) })
	maintenance := &lease.Leader{Store: s.Leases, Name: lease.Maintenance, Log: s.Log, Work: s.leaderWork}
	s.goBG(func() { maintenance.Run(bg) })

	errc := make(chan error, 1)
	go func() { errc <- s.httpServer.Serve(ln) }()
	s.Log.Info("server started", "addr", ln.Addr().String(), "instance", s.Instance.ID, "version", Version)

	select {
	case err := <-errc:
		stopBG()
		s.wg.Wait()
		return err
	case <-ctx.Done():
	}
	s.Log.Info("shutdown started", "grace", s.Cfg.ShutdownGrace)
	deadline := time.Now().Add(s.Cfg.ShutdownGrace)
	sctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	s.Engine.Shutdown(sctx)
	stopBG()
	s.wg.Wait()
	if err := s.httpServer.Shutdown(sctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		s.Log.Warn("http shutdown", "err", err)
	}
	s.Pool.Close()
	s.Log.Info("shutdown complete")
	return nil
}

func (s *Server) goBG(fn func()) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn()
	}()
}

// Addr returns the listen address after Run has started.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// leaderWork runs while this instance holds the maintenance lease: the engine checks
// every 2 s and the hourly maintenance.
func (s *Server) leaderWork(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			s.Engine.LeaderTick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	s.maintenance(ctx)
	wg.Wait()
}

// maintenance runs cleanup while this instance holds the maintenance lease.
func (s *Server) maintenance(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		s.runMaintenance(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) runMaintenance(ctx context.Context) {
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"instances", func(ctx context.Context) error { _, err := s.Registry.DeleteStale(ctx); return err }},
		{"sessions", s.Auth.Cleanup},
		{"audit", func(ctx context.Context) error { _, err := s.Audit.DeleteExpired(ctx); return err }},
		{"retention", func(ctx context.Context) error {
			_, err := s.Engine.DeleteExpired(ctx, time.Duration(s.Cfg.RetentionDays)*24*time.Hour)
			return err
		}},
		{"storage_gc", func(ctx context.Context) error { _, err := s.GC.RunIfDue(ctx); return err }},
	}
	for _, st := range steps {
		if err := st.fn(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("maintenance step failed", "step", st.name, "err", err)
		}
	}
}
