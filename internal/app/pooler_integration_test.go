//go:build integration

package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/lease"
	"github.com/alternayte/sluice/internal/platform/logging"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

// TestSCN_CORE_004_ThroughPgBouncer runs the server through PgBouncer in transaction
// mode: it creates a user, a managed flow and an execution that reaches SUCCESS, and
// hands over the scheduler lease between two holders (REQ-CORE-005, REQ-CORE-006).
func TestSCN_CORE_004_ThroughPgBouncer(t *testing.T) {
	pg := pgtest.Shared(t)
	pooled := pg.PgBouncer(t, pg.NewDatabase(t))
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer hook.Close()

	ctx := context.Background()
	cfg, err := LoadConfig(LoadOptions{Server: true, Env: map[string]string{
		"SLUICE_DATABASE_URL": pooled,
		"SLUICE_PUBLIC_URL":   "http://127.0.0.1:8080",
		"SLUICE_LISTEN_ADDR":  "127.0.0.1:0",
		"SLUICE_EXECUTORS":    "process",
	}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(ctx, cfg, logging.New(io.Discard, "error", "text"))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.Run(runCtx) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("server run: %v", err)
		}
	}()

	if _, err := s.Auth.CreateUser(ctx, "pooled@example.com", "Pooled", kernel.Editor, "correct-horse-battery", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := s.Namespaces.Create(ctx, "pooled", ""); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	flowYAML := "id: ping\ntasks:\n  - {id: call, type: http, method: GET, url: \"" + hook.URL + "\"}\n"
	if _, err := s.Namespaces.Save(ctx, "pooled", []namespace.Change{{Op: "put", Path: "ping.flow.yaml", Content: []byte(flowYAML)}}, "add flow", nil); err != nil {
		t.Fatalf("save flow: %v", err)
	}
	id, err := s.Engine.Trigger(ctx, execution.TriggerParams{Namespace: "pooled", FlowKey: "ping", TriggerType: "manual"})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	state := ""
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if err := s.Pool.QueryRow(ctx, "SELECT state FROM executions WHERE id = $1", id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == execution.ExecSuccess {
			break
		}
	}
	if state != execution.ExecSuccess {
		t.Fatalf("execution state %s, want SUCCESS", state)
	}

	// The fake clock is far ahead of the wall clock, so the lease of the running server
	// does not interfere.
	fake := clock.NewFake(time.Now().Add(100 * 365 * 24 * time.Hour))
	a := &lease.Store{Pool: s.Pool, Clock: fake, Holder: "instance-a"}
	b := &lease.Store{Pool: s.Pool, Clock: fake, Holder: "instance-b"}
	if ok, err := a.TryAcquire(ctx, lease.Scheduler); err != nil || !ok {
		t.Fatalf("A acquire: %v %v", ok, err)
	}
	if ok, err := b.TryAcquire(ctx, lease.Scheduler); err != nil || ok {
		t.Fatalf("B acquire while A holds: %v %v", ok, err)
	}
	fake.Advance(lease.TTL + time.Second)
	if ok, err := b.TryAcquire(ctx, lease.Scheduler); err != nil || !ok {
		t.Fatalf("B acquire after A expired: %v %v", ok, err)
	}
	guarded := "UPDATE leases SET expires_at = expires_at WHERE name = $1 AND " + lease.Guard("$1", "$2", "$3")
	for holder, want := range map[string]int64{"instance-a": 0, "instance-b": 1} {
		tag, err := s.Pool.Exec(ctx, guarded, lease.Scheduler, holder, fake.Now())
		if err != nil {
			t.Fatal(err)
		}
		if tag.RowsAffected() != want {
			t.Fatalf("leader-only write of %s changed %d rows, want %d", holder, tag.RowsAffected(), want)
		}
	}
}
