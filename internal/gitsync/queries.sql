-- name: InsertSource :one
INSERT INTO git_sources (id, name, repo_url, branch, auth_type, credential_secret_key, known_hosts, poll_interval, webhook_secret_key, created_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(repo_url), sqlc.arg(branch), sqlc.arg(auth_type), sqlc.arg(credential_secret_key),
    sqlc.arg(known_hosts), sqlc.arg(poll_interval), sqlc.arg(webhook_secret_key), sqlc.arg(now))
RETURNING *;

-- name: UpdateSource :one
UPDATE git_sources SET repo_url = sqlc.arg(repo_url), branch = sqlc.arg(branch), auth_type = sqlc.arg(auth_type),
    credential_secret_key = sqlc.arg(credential_secret_key), known_hosts = sqlc.arg(known_hosts),
    poll_interval = sqlc.arg(poll_interval), webhook_secret_key = sqlc.arg(webhook_secret_key)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetSource :one
SELECT * FROM git_sources WHERE id = sqlc.arg(id);

-- name: GetSourceByName :one
SELECT * FROM git_sources WHERE name = sqlc.arg(name);

-- name: ListSources :many
SELECT * FROM git_sources ORDER BY name;

-- name: DeleteSource :exec
DELETE FROM git_sources WHERE id = sqlc.arg(id);

-- name: ListMappings :many
SELECT m.git_source_id, m.repo_path, m.namespace_id, n.name AS namespace
FROM git_mappings m
JOIN namespaces n ON n.id = m.namespace_id
WHERE m.git_source_id = sqlc.arg(git_source_id)
ORDER BY m.repo_path;

-- name: DeleteMappings :exec
DELETE FROM git_mappings WHERE git_source_id = sqlc.arg(git_source_id);

-- name: InsertMapping :exec
INSERT INTO git_mappings (git_source_id, repo_path, namespace_id) VALUES (sqlc.arg(git_source_id), sqlc.arg(repo_path), sqlc.arg(namespace_id));

-- name: NamespaceByName :one
SELECT n.id, n.source_type, n.git_source_id, m.git_source_id AS mapped_by
FROM namespaces n
LEFT JOIN git_mappings m ON m.namespace_id = n.id
WHERE n.name = sqlc.arg(name) AND n.deleted_at IS NULL;

-- name: SetNamespaceSource :exec
UPDATE namespaces SET git_source_id = sqlc.narg(git_source_id) WHERE id = sqlc.arg(id);

-- name: DueSources :many
-- Sources with a requested sync, or whose poll interval passed since the last sync.
SELECT id FROM git_sources
WHERE sync_requested_at IS NOT NULL OR last_sync_at IS NULL OR last_sync_at + poll_interval * interval '1 second' <= sqlc.arg(now)
ORDER BY sync_requested_at NULLS LAST, last_sync_at NULLS FIRST
LIMIT 20;

-- name: ClaimSync :execrows
-- A leader-only write: the lease check and the claim are one statement (REQ-CORE-006).
UPDATE git_sources SET last_sync_status = 'running', sync_requested_at = NULL, last_sync_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND last_sync_status <> 'running'
    AND EXISTS (SELECT 1 FROM leases WHERE name = 'git-sync' AND holder = sqlc.arg(holder) AND expires_at > sqlc.arg(now));

-- name: FinishSync :exec
UPDATE git_sources SET last_sync_status = sqlc.arg(status), last_error = sqlc.arg(error), last_sync_at = sqlc.arg(now),
    last_synced_sha = CASE WHEN sqlc.arg(status)::text = 'success' THEN sqlc.arg(sha)::text ELSE last_synced_sha END
WHERE id = sqlc.arg(id);

-- name: ResetStaleRunning :exec
-- A sync that an instance left in running (for example after a crash) can run again.
UPDATE git_sources SET last_sync_status = 'failed', last_error = 'the sync stopped before it ended'
WHERE last_sync_status = 'running' AND last_sync_at < sqlc.arg(before);

-- name: InsertRun :exec
INSERT INTO git_sync_runs (id, git_source_id, started_at, status) VALUES (sqlc.arg(id), sqlc.arg(git_source_id), sqlc.arg(started_at), 'running');

-- name: FinishRun :exec
UPDATE git_sync_runs SET ended_at = sqlc.arg(ended_at), sha = sqlc.arg(sha), status = sqlc.arg(status), error = sqlc.arg(error),
    warnings = sqlc.arg(warnings), snapshots_created = sqlc.arg(snapshots_created)
WHERE id = sqlc.arg(id);

-- name: ListRuns :many
SELECT * FROM git_sync_runs WHERE git_source_id = sqlc.arg(git_source_id) ORDER BY started_at DESC LIMIT sqlc.arg(max_rows);

-- name: RequestSync :execrows
UPDATE git_sources SET sync_requested_at = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- name: SourceOfNamespace :one
SELECT s.id, s.name, s.repo_url, s.branch, s.auth_type, s.credential_secret_key, s.known_hosts, s.last_synced_sha,
    s.last_sync_at, s.last_sync_status, s.last_error, m.repo_path, n.id AS namespace_id
FROM git_sources s
JOIN git_mappings m ON m.git_source_id = s.id
JOIN namespaces n ON n.id = m.namespace_id
WHERE n.name = sqlc.arg(namespace) AND n.deleted_at IS NULL;
