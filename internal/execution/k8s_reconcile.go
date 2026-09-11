package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/executor"
)

// jobLister is the part of the kubernetes executor that the reconciler uses.
type jobLister interface {
	Jobs(ctx context.Context, pool string) ([]executor.JobInfo, error)
	Cancel(ctx context.Context, ref string) error
}

// ReconcileKubernetes runs one pass of the k8s-reconcile:<pool> leader (REQ-EXR-006): it
// deletes Jobs without a running task run, fails running task runs whose Job is gone
// (lost), whose pod stays pending longer than pendingTimeout (pod_pending_timeout) or
// whose image cannot be pulled (image_pull_failed). The retry policy applies.
func (e *Engine) ReconcileKubernetes(ctx context.Context, pool string, pendingTimeout time.Duration) error {
	ex, ok := e.Executors[executor.Kubernetes].(jobLister)
	if !ok {
		return nil
	}
	jobs, err := ex.Jobs(ctx, pool)
	if err != nil {
		return err
	}
	rows, err := e.Pool.Query(ctx, `SELECT id, external_ref FROM task_runs
		WHERE state = 'RUNNING' AND executor_type = 'kubernetes' AND pool = $1`, pool)
	if err != nil {
		return err
	}
	running := map[string]uuid.UUID{} // job name -> task run
	var noRef []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var ref string
		if err := rows.Scan(&id, &ref); err != nil {
			rows.Close()
			return err
		}
		if ref == "" {
			noRef = append(noRef, id)
			continue
		}
		running[ref] = id
	}
	rows.Close()

	now := e.Clock.Now()
	seen := map[string]bool{}
	for _, j := range jobs {
		seen[j.Ref] = true
		id, active := running[j.Ref]
		switch {
		case !active:
			// An orphan: no running task run owns this Job (a finished attempt, a cancel, a crash).
			if !j.Finished || j.TaskRunID == "" {
				if err := ex.Cancel(ctx, j.Ref); err != nil && ctx.Err() == nil {
					e.Log.Warn("delete orphan job", "job", j.Ref, "err", err)
				}
			}
		case j.PullFailure != "":
			e.failJob(ctx, ex, id, j.Ref, ReasonImagePullFailed, j.PullFailure)
		case !j.PendingSince.IsZero() && now.Sub(j.PendingSince) > pendingTimeout:
			e.failJob(ctx, ex, id, j.Ref, ReasonPodPendingTimeout,
				fmt.Sprintf("the pod stayed pending longer than %s", pendingTimeout))
		}
	}
	for ref, id := range running {
		if !seen[ref] {
			e.Log.Warn("kubernetes job gone", "job", ref, "task_run", id)
			e.finish(ctx, id, TaskFailed, ReasonLost, "the Job of the task run is gone", nil)
		}
	}
	_ = noRef // a claim sets the reference right after the Job create; a missing reference is not yet final
	return nil
}

func (e *Engine) failJob(ctx context.Context, ex jobLister, id uuid.UUID, ref, reason, msg string) {
	if err := ex.Cancel(ctx, ref); err != nil && ctx.Err() == nil {
		e.Log.Warn("delete job", "job", ref, "err", err)
	}
	e.finish(ctx, id, TaskFailed, reason, msg, nil)
}
