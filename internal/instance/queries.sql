-- name: UpsertInstance :exec
INSERT INTO instances (id, hostname, version, pools, executors, started_at, heartbeat_at)
VALUES ($1, $2, $3, $4, $5, $6, $6)
ON CONFLICT (id) DO UPDATE SET hostname = EXCLUDED.hostname, version = EXCLUDED.version,
    pools = EXCLUDED.pools, executors = EXCLUDED.executors, heartbeat_at = EXCLUDED.heartbeat_at;

-- name: HeartbeatInstance :execrows
UPDATE instances SET heartbeat_at = $2 WHERE id = $1;

-- name: ListInstances :many
SELECT * FROM instances ORDER BY started_at DESC, id;

-- name: DeleteStaleInstances :execrows
DELETE FROM instances WHERE heartbeat_at < $1;
