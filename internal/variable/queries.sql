-- name: NamespaceID :one
SELECT id FROM namespaces WHERE name = sqlc.arg(name) AND deleted_at IS NULL;

-- name: ListVariables :many
-- Variables of the global scope and of the namespaces in chain. An empty chain lists only global variables.
SELECT v.key, v.value, coalesce(n.name, '')::text AS scope, v.updated_by, v.updated_at
FROM variables v
LEFT JOIN namespaces n ON n.id = v.namespace_id
WHERE v.namespace_id IS NULL OR (n.name = ANY(sqlc.arg(chain)::text[]) AND n.deleted_at IS NULL)
ORDER BY v.key;

-- name: UpsertVariable :one
INSERT INTO variables (id, namespace_id, key, value, updated_by, updated_at)
VALUES (sqlc.arg(id), sqlc.narg(namespace_id), sqlc.arg(key), sqlc.arg(value), sqlc.arg(actor), sqlc.arg(now))
ON CONFLICT (namespace_id, key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at
RETURNING (xmax = 0)::boolean AS created;

-- name: DeleteVariable :execrows
DELETE FROM variables WHERE namespace_id IS NOT DISTINCT FROM sqlc.narg(namespace_id) AND key = sqlc.arg(key);
