//go:build integration

package execution_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

// TestSCN_EXE_002_StateTransitions runs every state pair through the transition
// function and checks that the database rejects unknown states (REQ-EXE-002).
func TestSCN_EXE_002_StateTransitions(t *testing.T) {
	allowedExec := map[[2]string]bool{
		{"QUEUED", "RUNNING"}: true, {"QUEUED", "SKIPPED"}: true, {"QUEUED", "CANCELLED"}: true,
		{"RUNNING", "SUCCESS"}: true, {"RUNNING", "FAILED"}: true, {"RUNNING", "TIMED_OUT"}: true, {"RUNNING", "CANCELLING"}: true,
		{"CANCELLING", "CANCELLED"}: true,
	}
	for _, from := range execution.ExecutionStates {
		for _, to := range execution.ExecutionStates {
			err := execution.CheckExecution(from, to)
			if allowedExec[[2]string{from, to}] != (err == nil) {
				t.Errorf("execution %s -> %s: %v", from, to, err)
			}
		}
	}
	allowedTask := map[[2]string]bool{
		{"PENDING", "QUEUED"}: true, {"PENDING", "SKIPPED"}: true, {"PENDING", "CANCELLED"}: true,
		{"QUEUED", "RUNNING"}: true, {"QUEUED", "CANCELLED"}: true,
		{"RUNNING", "SUCCESS"}: true, {"RUNNING", "FAILED"}: true, {"RUNNING", "TIMED_OUT"}: true, {"RUNNING", "CANCELLED"}: true,
	}
	for _, from := range execution.TaskStates {
		for _, to := range execution.TaskStates {
			err := execution.CheckTask(from, to)
			if allowedTask[[2]string{from, to}] != (err == nil) {
				t.Errorf("task %s -> %s: %v", from, to, err)
			}
		}
	}

	pool, _ := pgtest.Shared(t).NewPool(t)
	ctx := context.Background()
	ns, snap := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO namespaces (id, name, source_type) VALUES ($1, 'st', 'managed')`, ns); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO snapshots (id, namespace_id, version, manifest_hash) VALUES ($1, $2, 1, 'm')`, snap, ns); err != nil {
		t.Fatal(err)
	}
	for _, s := range execution.ExecutionStates {
		if _, err := pool.Exec(ctx, `INSERT INTO executions (id, namespace_id, snapshot_id, state, trigger_type, created_at) VALUES ($1, $2, $3, $4, 'manual', now())`,
			uuid.New(), ns, snap, s); err != nil {
			t.Fatalf("known state %s rejected: %v", s, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO executions (id, namespace_id, snapshot_id, state, trigger_type, created_at) VALUES ($1, $2, $3, 'DONE', 'manual', now())`,
		uuid.New(), ns, snap); err == nil {
		t.Fatal("unknown execution state accepted")
	}
	exec := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO executions (id, namespace_id, snapshot_id, state, trigger_type, created_at) VALUES ($1, $2, $3, 'RUNNING', 'manual', now())`,
		exec, ns, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO task_runs (id, execution_id, task_key, state) VALUES ($1, $2, 't', 'WAITING')`, uuid.New(), exec); err == nil {
		t.Fatal("unknown task state accepted")
	}
}
