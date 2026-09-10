// Package instance registers this server instance and its heartbeat (REQ-CORE-007).
package instance

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/dbq"
)

// Timings from REQ-CORE-007.
const (
	HeartbeatEvery = 10 * time.Second
	OfflineAfter   = 60 * time.Second
	DeleteAfter    = 24 * time.Hour
)

// Info describes one instance.
type Info struct {
	ID          uuid.UUID
	Hostname    string
	Version     string
	Pools       []string
	Executors   []string
	StartedAt   time.Time
	HeartbeatAt time.Time
}

// Online reports whether the instance heartbeat is recent at now.
func (i Info) Online(now time.Time) bool { return now.Sub(i.HeartbeatAt) <= OfflineAfter }

// Registry writes and reads the instances table.
type Registry struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
	// Every is the heartbeat interval. Default HeartbeatEvery.
	Every time.Duration
}

// Register inserts or updates the row of this instance.
func (r *Registry) Register(ctx context.Context, info Info) error {
	return dbq.New(r.Pool).UpsertInstance(ctx, dbq.UpsertInstanceParams{
		ID: info.ID, Hostname: info.Hostname, Version: info.Version,
		Pools: info.Pools, Executors: info.Executors, StartedAt: r.Clock.Now(),
	})
}

// Heartbeat updates heartbeat_at. It re-registers when the row was deleted.
func (r *Registry) Heartbeat(ctx context.Context, info Info) error {
	n, err := dbq.New(r.Pool).HeartbeatInstance(ctx, dbq.HeartbeatInstanceParams{ID: info.ID, HeartbeatAt: r.Clock.Now()})
	if err != nil {
		return err
	}
	if n == 0 {
		return r.Register(ctx, info)
	}
	return nil
}

// Run heartbeats until ctx ends.
func (r *Registry) Run(ctx context.Context, info Info) {
	every := r.Every
	if every <= 0 {
		every = HeartbeatEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Heartbeat(ctx, info); err != nil && ctx.Err() == nil {
				r.Log.Warn("instance heartbeat failed", "err", err)
			}
		}
	}
}

// List returns all instance rows, newest start first.
func (r *Registry) List(ctx context.Context) ([]Info, error) {
	rows, err := dbq.New(r.Pool).ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(rows))
	for _, i := range rows {
		out = append(out, Info{ID: i.ID, Hostname: i.Hostname, Version: i.Version, Pools: i.Pools,
			Executors: i.Executors, StartedAt: i.StartedAt, HeartbeatAt: i.HeartbeatAt})
	}
	return out, nil
}

// DeleteStale deletes rows without heartbeat for 24 h. The maintenance leader calls it.
func (r *Registry) DeleteStale(ctx context.Context) (int64, error) {
	return dbq.New(r.Pool).DeleteStaleInstances(ctx, r.Clock.Now().Add(-DeleteAfter))
}
