-- name: ListDueSchedules :many
-- Active schedule triggers of valid, enabled flows whose next fire time is unset or passed.
SELECT t.id, t.config, t.next_fire_at, n.name AS namespace, f.flow_key
FROM triggers t
JOIN flows f ON f.id = t.flow_id
JOIN namespaces n ON n.id = f.namespace_id
WHERE t.type = 'schedule' AND t.active AND f.valid AND NOT f.disabled AND f.deleted_at IS NULL AND n.deleted_at IS NULL
    AND (t.next_fire_at IS NULL OR t.next_fire_at <= sqlc.arg(now))
ORDER BY t.next_fire_at NULLS FIRST, t.id
LIMIT 500;

-- name: AdvanceSchedule :execrows
-- A leader-only write: the lease check and the update are one statement (REQ-CORE-006).
UPDATE triggers SET next_fire_at = sqlc.arg(next_fire_at), last_fired_at = COALESCE(sqlc.narg(fired_at), last_fired_at)
WHERE id = sqlc.arg(id) AND next_fire_at IS NOT DISTINCT FROM sqlc.narg(expected)
    AND EXISTS (SELECT 1 FROM leases WHERE name = 'scheduler' AND holder = sqlc.arg(holder) AND expires_at > sqlc.arg(now));

-- name: ScheduledExecutionExists :one
SELECT EXISTS (SELECT 1 FROM executions WHERE trigger_id = sqlc.arg(trigger_id) AND scheduled_for = sqlc.arg(scheduled_for));

-- name: GetWebhookTrigger :one
SELECT t.id, t.config, t.webhook_key_hash, t.active, f.valid, f.disabled, n.name AS namespace, f.flow_key
FROM triggers t
JOIN flows f ON f.id = t.flow_id
JOIN namespaces n ON n.id = f.namespace_id
WHERE t.webhook_key_hash = sqlc.arg(hash) AND t.type = 'webhook' AND f.deleted_at IS NULL AND n.deleted_at IS NULL;

-- name: GetFlowTrigger :one
SELECT t.id, t.type
FROM triggers t
JOIN flows f ON f.id = t.flow_id
JOIN namespaces n ON n.id = f.namespace_id
WHERE n.name = sqlc.arg(namespace) AND f.flow_key = sqlc.arg(flow_key) AND t.trigger_key = sqlc.arg(trigger_key)
    AND f.deleted_at IS NULL AND n.deleted_at IS NULL;

-- name: SetWebhookKey :exec
UPDATE triggers SET webhook_key_hash = sqlc.arg(hash) WHERE id = sqlc.arg(id);

-- name: ListUpcomingSchedules :many
SELECT t.id, t.trigger_key, t.config, t.next_fire_at, n.name AS namespace, f.flow_key
FROM triggers t
JOIN flows f ON f.id = t.flow_id
JOIN namespaces n ON n.id = f.namespace_id
WHERE t.type = 'schedule' AND t.active AND f.valid AND NOT f.disabled AND f.deleted_at IS NULL AND n.deleted_at IS NULL
    AND (sqlc.arg(namespace)::text = '' OR n.name = sqlc.arg(namespace)::text OR n.name LIKE sqlc.arg(namespace)::text || '.%');

-- name: ListDownstreamTriggers :many
-- Active flow triggers of valid, enabled flows that name the upstream flow.
SELECT t.id, t.config, n.name AS namespace, f.flow_key
FROM triggers t
JOIN flows f ON f.id = t.flow_id
JOIN namespaces n ON n.id = f.namespace_id
WHERE t.type = 'flow' AND t.active AND f.valid AND NOT f.disabled AND f.deleted_at IS NULL AND n.deleted_at IS NULL
    AND t.config ->> 'flow' = sqlc.arg(upstream)::text
ORDER BY t.id;

-- name: GetFlowName :one
SELECT n.name AS namespace, f.flow_key FROM flows f JOIN namespaces n ON n.id = f.namespace_id WHERE f.id = sqlc.arg(id);
