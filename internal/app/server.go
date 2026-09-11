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
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/executor"
	"github.com/alternayte/sluice/internal/gitsync"
	"github.com/alternayte/sluice/internal/instance"
	"github.com/alternayte/sluice/internal/metrics"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/db"
	"github.com/alternayte/sluice/internal/platform/health"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/lease"
	"github.com/alternayte/sluice/internal/platform/logging"
	"github.com/alternayte/sluice/internal/platform/promx"
	"github.com/alternayte/sluice/internal/secret"
	"github.com/alternayte/sluice/internal/storage"
	"github.com/alternayte/sluice/internal/trigger"
	"github.com/alternayte/sluice/internal/variable"
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
	Triggers   *trigger.Service
	Secrets    *secret.Service
	Variables  *variable.Service
	Git        *gitsync.Service
	Stats      *metrics.Service

	httpServer *http.Server
	listener   net.Listener
	wg         sync.WaitGroup
}

// NewServer opens the database, applies migrations and builds all components.
func NewServer(ctx context.Context, cfg *Config, log *slog.Logger) (*Server, error) {
	return newServer(ctx, cfg, log, clock.Real{})
}

// newServer is NewServer with a clock. Fake-clock tests use it.
func newServer(ctx context.Context, cfg *Config, log *slog.Logger, clk clock.Clock) (*Server, error) {
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
			Executors: enabledExecutors(ctx, cfg),
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
	s.Engine = &execution.Engine{Pool: pool, OfflineAfter: instance.OfflineAfter, Clock: clk, Log: log, Audit: s.Audit, Namespaces: namespacesAdapter{s.Namespaces}, Store: s.Store,
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
		case executor.Docker:
			dc, err := executor.NewDockerClient()
			if err != nil {
				pool.Close()
				return nil, fmt.Errorf("docker executor: %w", err)
			}
			de := &executor.DockerExecutor{Client: dc, RunnerImage: cfg.RunnerImage, Keep: cfg.DockerKeepContainers, Log: log}
			de.Sweep(ctx)
			s.Engine.Executors[t] = de
		case executor.Kubernetes:
			kc, err := executor.NewKubeClient(cfg.K8sKubeconfig)
			if err != nil {
				pool.Close()
				return nil, fmt.Errorf("kubernetes executor: %w", err)
			}
			s.Engine.Executors[t] = &executor.KubernetesExecutor{Client: kc, Namespace: k8sNamespace(cfg), RunnerImage: cfg.RunnerImage,
				JobTTL: cfg.K8sJobTTL, Log: log}
		}
	}
	s.Triggers = &trigger.Service{Pool: pool, Clock: clk, Audit: s.Audit, Log: log, Starter: triggerStarter{s.Engine},
		Holder: id.String(), PublicURL: cfg.PublicURL}
	s.Engine.EndHooks = append(s.Engine.EndHooks, flowTriggerHook(s.Triggers))
	keys, err := masterKeyring(cfg)
	if err != nil {
		pool.Close()
		return nil, err
	}
	s.Secrets = &secret.Service{Pool: pool, Clock: clk, Audit: s.Audit, Log: log, Keys: keys, CacheTTL: cfg.SecretCacheTTL,
		Vault:         secret.VaultAuth{Addr: cfg.VaultAddr, Token: cfg.VaultToken, K8sRole: cfg.VaultK8sRole},
		K8sKubeconfig: cfg.K8sKubeconfig, K8sNamespace: cfg.K8sNamespace}
	if err := s.Secrets.EnsureDefaults(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("secret providers: %w", err)
	}
	s.Health.Add("master_keys", s.Secrets.CheckKeys)
	s.Engine.Secrets = secretResolver{s.Secrets}
	s.Variables = &variable.Service{Pool: pool, Clock: clk, Audit: s.Audit}
	s.Git = &gitsync.Service{Pool: pool, Clock: clk, Audit: s.Audit, Log: log, Namespaces: gitNamespaces{s.Namespaces},
		Secrets: globalSecrets{s.Secrets}, Holder: id.String(), PublicURL: cfg.PublicURL, MaxFileBytes: int64(cfg.MaxFileBytes)}
	s.Stats = &metrics.Service{Pool: pool, Clock: clk}
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

// enabledExecutors returns the executors of this instance (REQ-EXR-002). With auto, inline
// and process are always on and docker is on when the Docker API answers.
func enabledExecutors(ctx context.Context, cfg *Config) []string {
	out := []string{"inline"}
	for _, e := range cfg.Executors {
		switch e {
		case "auto":
			out = append(out, "process")
			if executor.DockerAvailable(ctx) {
				out = append(out, "docker")
			}
			if executor.KubernetesAvailable(ctx, cfg.K8sKubeconfig, k8sNamespace(cfg)) {
				out = append(out, "kubernetes")
			}
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
	r.Use(httpx.SecurityHeaders, httpx.WithRequestID, s.withLogger, s.metrics, getHead, s.Auth.Middleware,
		execution.RunnerContentType, execution.RunTokenMiddleware(s.Engine))
	r.Get("/healthz", health.Healthz)
	r.Get("/readyz", s.Health.Readyz)
	r.Method(http.MethodGet, "/metrics", s.Metrics.Handler())
	api := httpx.NewAPI(r)
	registerRoutes(api, r, s.services())
	if err := httpx.CheckAccess(api); err != nil {
		return nil, err
	}
	r.Handle("/api", httpx.NotFoundJSON())
	r.Handle("/api/*", httpx.NotFoundJSON())
	r.Handle("/*", spaHandler())
	return r, nil
}

// getHead routes a HEAD request to the GET handler of its path, as the net/http ServeMux of
// the old server did. chi middleware.GetHead is not sufficient: the /api/* catch-all takes
// every method, so a HEAD match always exists. Here a HEAD match on a catch-all pattern
// does not count when a more specific GET route matches. The net/http server sends no body
// for HEAD.
func getHead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			rctx := chi.RouteContext(r.Context())
			path := r.URL.RawPath
			if path == "" {
				path = r.URL.Path
			}
			head := rctx.Routes.Find(chi.NewRouteContext(), http.MethodHead, path)
			if head == "" || strings.HasSuffix(head, "*") {
				if get := rctx.Routes.Find(chi.NewRouteContext(), http.MethodGet, path); get != "" && get != head {
					rctx.RouteMethod = http.MethodGet
					rctx.RoutePath = path
				}
			}
		}
		next.ServeHTTP(w, r)
	})
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
	scheduler := &lease.Leader{Store: s.Leases, Name: lease.Scheduler, Log: s.Log, Work: s.schedulerWork}
	s.goBG(func() { scheduler.Run(bg) })
	gitLeader := &lease.Leader{Store: s.Leases, Name: lease.GitSync, Log: s.Log, Work: s.gitSyncWork}
	s.goBG(func() { gitLeader.Run(bg) })
	if _, ok := s.Engine.Executors[executor.Kubernetes]; ok {
		for _, p := range s.Cfg.Pools {
			reconciler := &lease.Leader{Store: s.Leases, Name: lease.K8sReconcile(p), Log: s.Log, Work: s.k8sReconcileWork(p)}
			s.goBG(func() { reconciler.Run(bg) })
		}
	}

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

// schedulerWork fires due schedules each second while this instance holds the scheduler lease.
func (s *Server) schedulerWork(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if err := s.Triggers.Tick(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("scheduler tick", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// k8sReconcileInterval is the pass interval of the k8s-reconcile:<pool> leader (REQ-EXR-006).
const k8sReconcileInterval = 60 * time.Second

// k8sReconcileWork returns the work of the k8s-reconcile:<pool> leader: one pass every 60 s.
func (s *Server) k8sReconcileWork(pool string) func(ctx context.Context) {
	return func(ctx context.Context) {
		t := time.NewTicker(k8sReconcileInterval)
		defer t.Stop()
		for {
			if err := s.Engine.ReconcileKubernetes(ctx, pool, s.Cfg.K8sPendingTimeout); err != nil && ctx.Err() == nil {
				s.Log.Warn("kubernetes reconcile", "pool", pool, "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}
}

// k8sNamespace returns the namespace of task Jobs: SLUICE_K8S_NAMESPACE, or default.
func k8sNamespace(cfg *Config) string {
	if cfg.K8sNamespace != "" {
		return cfg.K8sNamespace
	}
	return "default"
}

// gitSyncWork syncs due git sources each second while this instance holds the git-sync lease.
func (s *Server) gitSyncWork(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if err := s.Git.Tick(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("git sync tick", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
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
