// Package lease implements leader election with the leases table (D-02, REQ-CORE-006).
package lease

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/platform/clock"
)

// Lease timings from REQ-CORE-006.
const (
	TTL   = 15 * time.Second
	Renew = 5 * time.Second
)

// Well-known lease names.
const (
	Scheduler   = "scheduler"
	Maintenance = "maintenance"
	GitSync     = "git-sync"
)

// K8sReconcile returns the lease name of the Kubernetes reconciler of a pool.
func K8sReconcile(pool string) string { return "k8s-reconcile:" + pool }

// ErrNotHolder is returned when a leader-only write finds that this instance does not hold the lease.
var ErrNotHolder = errors.New("lease not held")

// Store acquires, renews and releases leases.
type Store struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock
	Holder string
	TTL    time.Duration
}

func (s *Store) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return TTL
}

// TryAcquire takes or renews the lease. It returns true when this holder has it.
func (s *Store) TryAcquire(ctx context.Context, name string) (bool, error) {
	now := s.Clock.Now()
	var holder string
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO leases (name, holder, expires_at) VALUES ($1, $2, $3)
		ON CONFLICT (name) DO UPDATE SET holder = EXCLUDED.holder, expires_at = EXCLUDED.expires_at
		WHERE leases.holder = EXCLUDED.holder OR leases.expires_at <= $4
		RETURNING holder`, name, s.Holder, now.Add(s.ttl()), now).Scan(&holder)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return holder == s.Holder, nil
}

// Release gives up the lease when this holder has it.
func (s *Store) Release(ctx context.Context, name string) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM leases WHERE name = $1 AND holder = $2", name, s.Holder)
	return err
}

// Leader runs work while this instance holds a lease. The work context is
// cancelled when the lease is lost.
type Leader struct {
	Store *Store
	Name  string
	Log   *slog.Logger
	// Every is the renew interval. Default Renew.
	Every time.Duration
	// Work runs while the lease is held. It must return when ctx ends.
	Work func(ctx context.Context)

	mu     sync.Mutex
	leader bool
}

// IsLeader reports whether this instance currently holds the lease.
func (l *Leader) IsLeader() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.leader
}

// Run renews the lease until ctx ends, then releases it.
func (l *Leader) Run(ctx context.Context) {
	every := l.Every
	if every <= 0 {
		every = Renew
	}
	var w *worker
	stopWork := func() {
		if w != nil {
			w.stop()
			w = nil
		}
		l.mu.Lock()
		l.leader = false
		l.mu.Unlock()
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		ok, err := l.Store.TryAcquire(ctx, l.Name)
		if err != nil && ctx.Err() == nil && l.Log != nil {
			l.Log.Warn("lease renew failed", "lease", l.Name, "err", err)
		}
		if ok && w == nil {
			l.mu.Lock()
			l.leader = true
			l.mu.Unlock()
			if l.Log != nil {
				l.Log.Info("lease acquired", "lease", l.Name)
			}
			w = startWorker(ctx, l.Work)
		} else if !ok && w != nil {
			if l.Log != nil {
				l.Log.Info("lease lost", "lease", l.Name)
			}
			stopWork()
		}
		select {
		case <-ctx.Done():
			stopWork()
			rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_ = l.Store.Release(rctx, l.Name)
			rcancel()
			return
		case <-tick.C:
		}
	}
}

type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startWorker(ctx context.Context, work func(context.Context)) *worker {
	wctx, cancel := context.WithCancel(ctx)
	w := &worker{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		if work != nil {
			work(wctx)
		}
	}()
	return w
}

func (w *worker) stop() {
	w.cancel()
	<-w.done
}

// Guard returns a SQL condition that is true only while holder has the lease.
// The arguments are SQL placeholders such as "$1". Leader-only writes add it to
// their WHERE clause so that the check and the write are one statement (REQ-CORE-006).
func Guard(name, holder, now string) string {
	return "EXISTS (SELECT 1 FROM leases WHERE name = " + name + " AND holder = " + holder + " AND expires_at > " + now + ")"
}
