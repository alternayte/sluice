-- name: InsertExecution :exec
INSERT INTO executions (id, namespace_id, flow_id, flow_revision_id, snapshot_id, state, trigger_type, trigger_id,
    scheduled_for, trigger_payload, definition, inputs, labels, parent_execution_id, parent_task_run_id, restart_of_id,
    chain_depth, created_by, created_at, ended_at, reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21);

-- name: GetExecution :one
SELECT e.*, n.name AS namespace_name, f.flow_key, s.version AS snapshot_version, s.git_sha AS snapshot_git_sha
FROM executions e
JOIN namespaces n ON n.id = e.namespace_id
LEFT JOIN flows f ON f.id = e.flow_id
JOIN snapshots s ON s.id = e.snapshot_id
WHERE e.id = $1;

-- name: LockExecution :one
SELECT * FROM executions WHERE id = $1 FOR UPDATE;

-- name: ClaimQueuedExecutions :many
SELECT id FROM executions WHERE state = 'QUEUED' ORDER BY created_at, id LIMIT $1 FOR UPDATE SKIP LOCKED;

-- name: CountActiveFlowExecutions :one
SELECT count(*) FROM executions WHERE flow_id = $1 AND state IN ('RUNNING', 'CANCELLING');

-- name: LockFlowRow :exec
SELECT id FROM flows WHERE id = $1 FOR UPDATE;

-- name: StartExecution :exec
UPDATE executions SET state = 'RUNNING', started_at = $2, deadline_at = $3 WHERE id = $1 AND state = 'QUEUED';

-- name: SetExecutionState :execrows
UPDATE executions SET state = sqlc.arg('to_state'), reason = sqlc.arg('reason'), error = sqlc.arg('error'),
    ended_at = sqlc.narg('ended_at'), duration_ms = sqlc.narg('duration_ms'), outputs = coalesce(sqlc.narg('outputs'), outputs)
WHERE id = sqlc.arg('id') AND state = sqlc.arg('from_state');

-- name: ListExecutionTaskRuns :many
SELECT * FROM task_runs WHERE execution_id = $1 ORDER BY task_key, attempt;

-- name: InsertTaskRun :exec
INSERT INTO task_runs (id, execution_id, task_key, task_type, attempt, state, reason, executor_type, pool, not_before,
    queued_at, started_at, ended_at, outputs, reused_from_id, error, exit_code)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: QueueTaskRun :execrows
UPDATE task_runs SET state = 'QUEUED', queued_at = $2, reason = '' WHERE id = $1 AND state = 'PENDING';

-- name: SkipTaskRun :execrows
UPDATE task_runs SET state = 'SKIPPED', reason = $2, ended_at = $3 WHERE id = $1 AND state = 'PENDING';

-- name: CancelPendingTaskRuns :execrows
UPDATE task_runs SET state = 'CANCELLED', reason = 'cancelled', ended_at = $2
WHERE execution_id = $1 AND state IN ('PENDING', 'QUEUED');

-- name: RequestCancelRunning :execrows
UPDATE task_runs SET cancel_requested = true WHERE execution_id = $1 AND state = 'RUNNING';

-- name: LockTaskRun :one
SELECT * FROM task_runs WHERE id = $1 FOR UPDATE;

-- name: GetTaskRun :one
SELECT * FROM task_runs WHERE id = $1;

-- name: GetTaskRunByTokenHash :one
SELECT * FROM task_runs WHERE run_token_hash = $1;

-- name: FinishTaskRun :execrows
UPDATE task_runs SET state = sqlc.arg('to_state'), reason = sqlc.arg('reason'), error = sqlc.arg('error'),
    exit_code = sqlc.narg('exit_code'), ended_at = sqlc.arg('ended_at'), run_token_hash = NULL
WHERE id = sqlc.arg('id') AND state = sqlc.arg('from_state');

-- name: TouchHeartbeat :one
UPDATE task_runs SET heartbeat_at = $2 WHERE id = $1 AND state = 'RUNNING' RETURNING cancel_requested;

-- name: MergeTaskOutputs :exec
UPDATE task_runs SET outputs = coalesce(outputs, '{}'::jsonb) || sqlc.arg('outputs')::jsonb WHERE id = sqlc.arg('id');

-- name: SetTaskRunExternal :exec
UPDATE task_runs SET external_ref = $2 WHERE id = $1;

-- name: SetTaskRunChild :exec
UPDATE task_runs SET child_execution_id = $2 WHERE id = $1;

-- name: InsertLogChunk :execrows
INSERT INTO log_chunks (task_run_id, execution_id, seq, first_line, line_count, data, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (task_run_id, seq) DO NOTHING;

-- name: NextLogLine :one
SELECT coalesce(max(first_line + line_count), 1)::bigint FROM log_chunks WHERE task_run_id = $1;

-- name: ListLogChunks :many
SELECT * FROM log_chunks WHERE task_run_id = $1 AND first_line + line_count > $2 ORDER BY seq;

-- name: DeleteLogChunks :exec
DELETE FROM log_chunks WHERE task_run_id = $1;

-- name: InsertMetric :exec
INSERT INTO metrics (execution_id, task_run_id, flow_id, name, value, unit, tags, ts, seq, idx)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (task_run_id, seq, idx) DO NOTHING;

-- name: ListExecutionMetrics :many
SELECT m.*, t.task_key FROM metrics m JOIN task_runs t ON t.id = m.task_run_id WHERE m.execution_id = $1 ORDER BY m.ts, m.id;

-- name: UpsertArtifact :exec
INSERT INTO artifacts (id, execution_id, task_run_id, name, storage_key, size, content_type, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (task_run_id, name) DO UPDATE SET size = EXCLUDED.size, content_type = EXCLUDED.content_type, storage_key = EXCLUDED.storage_key;

-- name: ListExecutionArtifacts :many
SELECT a.*, t.task_key FROM artifacts a JOIN task_runs t ON t.id = a.task_run_id WHERE a.execution_id = $1 ORDER BY a.created_at, a.name;

-- name: GetArtifact :one
SELECT * FROM artifacts WHERE id = $1 AND execution_id = $2;

-- name: ListChildExecutions :many
SELECT id, state, created_at FROM executions WHERE parent_execution_id = $1 ORDER BY created_at;

-- name: SetLogArchived :exec
UPDATE executions SET log_archived = true WHERE id = $1;

-- Copy of namespace query GetFlow: execution owns its reads (SDD S4.4).
-- name: GetFlow :one
SELECT f.*, n.name AS namespace_name FROM flows f JOIN namespaces n ON n.id = f.namespace_id
WHERE n.name = $1 AND f.flow_key = $2 AND f.deleted_at IS NULL AND n.deleted_at IS NULL;

-- Copy of namespace query GetFlowRevision: execution owns its reads (SDD S4.4).
-- name: GetFlowRevision :one
SELECT * FROM flow_revisions WHERE id = $1;

-- Copy of namespace query GetSnapshot: execution owns its reads (SDD S4.4).
-- name: GetSnapshot :one
SELECT s.*, u.email AS author_email FROM snapshots s LEFT JOIN users u ON u.id = s.created_by WHERE s.id = $1;

-- Copy of namespace query GetSnapshotFile: execution owns its reads (SDD S4.4).
-- name: GetSnapshotFile :one
SELECT * FROM snapshot_files WHERE snapshot_id = $1 AND path = $2;

