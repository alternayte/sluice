-- name: InsertNamespace :exec
INSERT INTO namespaces (id, name, source_type, git_source_id, description, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetNamespace :one
SELECT n.*, s.version AS head_version, s.git_sha AS head_git_sha, s.created_at AS head_created_at
FROM namespaces n LEFT JOIN snapshots s ON s.id = n.head_snapshot_id
WHERE n.name = $1 AND n.deleted_at IS NULL;

-- name: LockNamespace :one
SELECT * FROM namespaces WHERE id = $1 FOR UPDATE;

-- name: ListNamespaces :many
SELECT n.*, s.version AS head_version, s.git_sha AS head_git_sha
FROM namespaces n LEFT JOIN snapshots s ON s.id = n.head_snapshot_id
WHERE n.deleted_at IS NULL ORDER BY n.name;

-- name: SetNamespaceHead :exec
UPDATE namespaces SET head_snapshot_id = $2 WHERE id = $1;

-- name: SoftDeleteNamespace :exec
UPDATE namespaces SET deleted_at = $2 WHERE id = $1;

-- name: CountActiveExecutions :one
SELECT count(*) FROM executions WHERE namespace_id = $1 AND state IN ('QUEUED', 'RUNNING', 'CANCELLING');

-- name: InsertFileObject :exec
INSERT INTO file_objects (hash, size, created_at) VALUES ($1, $2, $3) ON CONFLICT (hash) DO NOTHING;

-- name: ShareLockFileObjects :many
SELECT hash FROM file_objects WHERE hash = ANY(sqlc.arg('hashes')::text[]) FOR SHARE;

-- name: FileObjectExists :one
SELECT EXISTS (SELECT 1 FROM file_objects WHERE hash = $1);

-- name: NextSnapshotVersion :one
SELECT coalesce(max(version), 0)::int + 1 FROM snapshots WHERE namespace_id = $1;

-- name: InsertSnapshot :exec
INSERT INTO snapshots (id, namespace_id, version, git_sha, manifest_hash, message, created_by, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: InsertSnapshotFiles :exec
INSERT INTO snapshot_files (snapshot_id, path, hash, size, executable)
SELECT sqlc.arg('snapshot_id')::uuid, unnest(sqlc.arg('paths')::text[]), unnest(sqlc.arg('hashes')::text[]),
    unnest(sqlc.arg('sizes')::bigint[]), unnest(sqlc.arg('executables')::bool[]);

-- name: GetSnapshot :one
SELECT s.*, u.email AS author_email FROM snapshots s LEFT JOIN users u ON u.id = s.created_by WHERE s.id = $1;

-- name: GetSnapshotByVersion :one
SELECT s.*, u.email AS author_email FROM snapshots s LEFT JOIN users u ON u.id = s.created_by
WHERE s.namespace_id = $1 AND s.version = $2;

-- name: ListSnapshots :many
SELECT s.*, u.email AS author_email FROM snapshots s LEFT JOIN users u ON u.id = s.created_by
WHERE s.namespace_id = $1 ORDER BY s.created_at DESC, s.id DESC LIMIT $2;

-- name: ListSnapshotFiles :many
SELECT * FROM snapshot_files WHERE snapshot_id = $1 ORDER BY path;

-- name: TouchBundle :exec
UPDATE bundles SET last_used_at = $2 WHERE manifest_hash = $1;

-- name: GetBundle :one
SELECT * FROM bundles WHERE manifest_hash = $1;

-- name: InsertBundle :exec
INSERT INTO bundles (manifest_hash, storage_key, size, last_used_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (manifest_hash) DO UPDATE SET last_used_at = EXCLUDED.last_used_at, size = EXCLUDED.size;
-- name: ListNamespaceFlows :many
SELECT * FROM flows WHERE namespace_id = $1 AND deleted_at IS NULL;

-- name: InsertFlow :exec
INSERT INTO flows (id, namespace_id, flow_key, path, created_at) VALUES ($1, $2, $3, $4, $5);

-- name: UpdateFlowRevision :exec
UPDATE flows SET current_revision_id = $2, valid = $3, path = $4 WHERE id = $1;

-- name: UpdateFlowPathValid :exec
UPDATE flows SET valid = $2, path = $3 WHERE id = $1;

-- name: MarkFlowDeleted :exec
UPDATE flows SET deleted_at = $2 WHERE id = $1;

-- name: SetFlowDisabled :exec
UPDATE flows SET disabled = $2 WHERE id = $1;

-- name: GetFlow :one
SELECT f.*, n.name AS namespace_name FROM flows f JOIN namespaces n ON n.id = f.namespace_id
WHERE n.name = $1 AND f.flow_key = $2 AND f.deleted_at IS NULL AND n.deleted_at IS NULL;

-- name: InsertFlowRevision :exec
INSERT INTO flow_revisions (id, flow_id, snapshot_id, source_hash, source, path, definition, errors, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetFlowRevision :one
SELECT * FROM flow_revisions WHERE id = $1;

-- name: ListFlowRevisions :many
SELECT r.id, r.flow_id, r.snapshot_id, r.source_hash, r.path, r.errors, r.created_at, s.version, s.git_sha, s.message
FROM flow_revisions r JOIN snapshots s ON s.id = r.snapshot_id
WHERE r.flow_id = $1 ORDER BY r.created_at DESC, r.id DESC LIMIT $2;

-- name: ListFlowTriggers :many
SELECT * FROM triggers WHERE flow_id = $1 ORDER BY trigger_key;

-- name: UpsertTrigger :exec
INSERT INTO triggers (id, flow_id, revision_id, trigger_key, type, config, active)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (flow_id, trigger_key) DO UPDATE SET revision_id = EXCLUDED.revision_id, type = EXCLUDED.type,
    config = EXCLUDED.config, active = EXCLUDED.active,
    next_fire_at = CASE WHEN triggers.config IS DISTINCT FROM EXCLUDED.config OR NOT triggers.active THEN NULL ELSE triggers.next_fire_at END;

-- name: DeactivateFlowTriggers :exec
UPDATE triggers SET active = false, next_fire_at = NULL WHERE flow_id = $1;

-- name: DeactivateMissingTriggers :exec
UPDATE triggers SET active = false, next_fire_at = NULL WHERE flow_id = $1 AND NOT (trigger_key = ANY(sqlc.arg('keys')::text[]));
