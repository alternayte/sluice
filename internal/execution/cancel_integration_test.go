//go:build integration

package execution_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

// TestCancelEndedExecution checks that a cancel of an ended execution returns 409
// execution_ended with the state, and that a cancel of a CANCELLING execution is accepted.
func TestCancelEndedExecution(t *testing.T) {
	pool, _ := pgtest.Shared(t).NewPool(t)
	ctx := context.Background()
	e := &execution.Engine{Pool: pool, Clock: clock.NewFake(time.Now()), Log: slog.New(slog.DiscardHandler)}
	ns, snap := uuid.New(), uuid.New()
	mustExec(t, pool, `INSERT INTO namespaces (id, name, source_type) VALUES ($1, 'cancel', 'managed')`, ns)
	mustExec(t, pool, `INSERT INTO snapshots (id, namespace_id, version, manifest_hash) VALUES ($1, $2, 1, 'm')`, snap, ns)
	seed := func(state string) uuid.UUID {
		id := uuid.New()
		mustExec(t, pool, `INSERT INTO executions (id, namespace_id, snapshot_id, state, trigger_type, created_at) VALUES ($1, $2, $3, $4, 'manual', now())`,
			id, ns, snap, state)
		return id
	}

	err := e.Cancel(ctx, seed("SUCCESS"))
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 409 || he.Code != "execution_ended" || he.Message != "the execution has ended" {
		t.Fatalf("cancel of ended execution: %v, want 409 execution_ended", err)
	}
	if d, ok := he.Details.(map[string]string); !ok || d["state"] != "SUCCESS" {
		t.Errorf("details %v, want state SUCCESS", he.Details)
	}
	if err := e.Cancel(ctx, seed("CANCELLING")); err != nil {
		t.Errorf("cancel of cancelling execution: %v, want nil", err)
	}
}
