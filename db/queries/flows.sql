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

-- name: GetFlowByID :one
SELECT f.*, n.name AS namespace_name FROM flows f JOIN namespaces n ON n.id = f.namespace_id WHERE f.id = $1;

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
