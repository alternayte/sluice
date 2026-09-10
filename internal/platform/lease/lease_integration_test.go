//go:build integration

package lease_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/lease"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

// leaderWrite is a leader-only write: it updates a setting only while holder has the lease.
func leaderWrite(ctx context.Context, pool *pgxpool.Pool, holder string, now time.Time) (bool, error) {
	tag, err := pool.Exec(ctx, `INSERT INTO settings (key, value, updated_by)
		SELECT 'leader.test', '"x"', $2::text WHERE `+lease.Guard("$1", "$2", "$3")+`
		ON CONFLICT (key) DO UPDATE SET updated_by = EXCLUDED.updated_by`, lease.Scheduler, holder, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func TestSCN_CORE_005_LeaseHandover(t *testing.T) {
	pool, _ := pgtest.Shared(t).NewPool(t)
	ctx := context.Background()
	a := &lease.Store{Pool: pool, Clock: clock.Real{}, Holder: "A"}
	b := &lease.Store{Pool: pool, Clock: clock.Real{}, Holder: "B"}

	ok, err := a.TryAcquire(ctx, lease.Scheduler)
	if err != nil || !ok {
		t.Fatalf("A acquire: %v %v", ok, err)
	}
	if ok, _ := b.TryAcquire(ctx, lease.Scheduler); ok {
		t.Fatal("B acquired a held lease")
	}
	if w, err := leaderWrite(ctx, pool, "A", time.Now()); err != nil || !w {
		t.Fatalf("leader write from A while holder: %v %v", w, err)
	}

	// A stops renewing. B runs a real leader loop and must hold the lease within 20 s.
	start := time.Now()
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var gotAt time.Time
	leader := &lease.Leader{Store: b, Name: lease.Scheduler, Every: lease.Renew, Work: func(ctx context.Context) {
		mu.Lock()
		gotAt = time.Now()
		mu.Unlock()
		<-ctx.Done()
	}}
	go leader.Run(lctx)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !leader.IsLeader() {
		time.Sleep(100 * time.Millisecond)
	}
	if !leader.IsLeader() {
		t.Fatalf("B did not hold the lease within 20 s")
	}
	mu.Lock()
	elapsed := gotAt.Sub(start)
	mu.Unlock()
	if elapsed > 20*time.Second {
		t.Fatalf("handover took %s", elapsed)
	}
	if w, err := leaderWrite(ctx, pool, "A", time.Now()); err != nil || w {
		t.Fatalf("leader-only write from A was not rejected: wrote=%v err=%v", w, err)
	}
	if w, err := leaderWrite(ctx, pool, "B", time.Now()); err != nil || !w {
		t.Fatalf("leader write from B: %v %v", w, err)
	}
}

func TestLeaseFakeClockExpiry(t *testing.T) {
	pool, _ := pgtest.Shared(t).NewPool(t)
	ctx := context.Background()
	fc := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	a := &lease.Store{Pool: pool, Clock: fc, Holder: "A"}
	b := &lease.Store{Pool: pool, Clock: fc, Holder: "B"}
	if ok, _ := a.TryAcquire(ctx, "x"); !ok {
		t.Fatal("A")
	}
	fc.Advance(14 * time.Second)
	if ok, _ := b.TryAcquire(ctx, "x"); ok {
		t.Fatal("B before expiry")
	}
	fc.Advance(2 * time.Second)
	if ok, _ := b.TryAcquire(ctx, "x"); !ok {
		t.Fatal("B after expiry")
	}
	if err := b.Release(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.TryAcquire(ctx, "x"); !ok {
		t.Fatal("A after release")
	}
}
