package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/api"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/db"
	"github.com/alternayte/sluice/internal/platform/health"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/instance"
	"github.com/alternayte/sluice/internal/platform/lease"
	"github.com/alternayte/sluice/internal/platform/logging"
	"github.com/alternayte/sluice/internal/platform/promx"
)

// Server is one running sluice server instance.
type Server struct {
	Cfg      *Config
	Log      *slog.Logger
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Instance instance.Info
	Leases   *lease.Store
	Health   *health.Checker
	Metrics  *promx.Metrics
	Registry *instance.Registry

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
	}
	s.Leases = &lease.Store{Pool: pool, Clock: clk, Holder: id.String()}
	s.Metrics = promx.New(pool, log)
	s.Health.Add("database", func(ctx context.Context) error {
		var one int
		return pool.QueryRow(ctx, "SELECT 1").Scan(&one)
	})
	s.Health.Add("migrations", func(ctx context.Context) error { return db.MigrationsCurrent(ctx, pool) })
	return s, nil
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Healthz)
	mux.HandleFunc("GET /readyz", s.Health.Readyz)
	mux.Handle("GET /metrics", s.Metrics.Handler())
	apiServer := &api.Server{System: api.System{Instances: s.Registry, Clock: s.Clock}}
	if err := api.Mount(mux, apiServer, nil); err != nil {
		return nil, err
	}
	mux.Handle("/", spaHandler())
	route := func(r *http.Request) string {
		if r.Pattern == "" {
			return "unmatched"
		}
		return r.Pattern
	}
	h := httpx.WithRequestID(s.Metrics.Middleware(route, mux))
	return withLogger(s.Log, h), nil
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
	maintenance := &lease.Leader{Store: s.Leases, Name: lease.Maintenance, Log: s.Log, Work: s.maintenance}
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

// maintenance runs daily cleanup while this instance holds the maintenance lease.
func (s *Server) maintenance(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := s.Registry.DeleteStale(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("delete stale instances", "err", err)
		} else if n > 0 {
			s.Log.Info("deleted stale instances", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
