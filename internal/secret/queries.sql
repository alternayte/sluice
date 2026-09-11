-- name: EnsureProvider :exec
INSERT INTO secret_providers (id, name, type) VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(type)) ON CONFLICT (name) DO NOTHING;

-- name: ListProviders :many
SELECT * FROM secret_providers ORDER BY name;

-- name: GetProvider :one
SELECT * FROM secret_providers WHERE name = sqlc.arg(name);

-- name: InsertProvider :one
INSERT INTO secret_providers (id, name, type, config, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(type), sqlc.arg(config), sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: UpdateProviderConfig :one
UPDATE secret_providers SET config = sqlc.arg(config), updated_at = sqlc.arg(now) WHERE name = sqlc.arg(name) RETURNING *;

-- name: ProviderInUse :one
SELECT EXISTS (SELECT 1 FROM secrets WHERE provider_id = sqlc.arg(provider_id));

-- name: DeleteProvider :exec
DELETE FROM secret_providers WHERE id = sqlc.arg(id);

-- name: NamespaceID :one
SELECT id FROM namespaces WHERE name = sqlc.arg(name) AND deleted_at IS NULL;

-- name: ListSecrets :many
-- Secrets of the global scope and of the namespaces in chain. An empty chain lists only global secrets.
SELECT s.id, s.key, coalesce(n.name, '')::text AS scope, s.provider_ref, s.description, s.created_by, s.updated_by, s.updated_at,
    s.last_resolved_at, p.name AS provider, p.type AS provider_type
FROM secrets s
JOIN secret_providers p ON p.id = s.provider_id
LEFT JOIN namespaces n ON n.id = s.namespace_id
WHERE s.namespace_id IS NULL OR (n.name = ANY(sqlc.arg(chain)::text[]) AND n.deleted_at IS NULL)
ORDER BY s.key;

-- name: GetSecret :one
SELECT s.id, s.key, s.provider_ref, s.ciphertext, s.key_id, s.description, s.created_by, p.id AS provider_id, p.name AS provider,
    p.type AS provider_type, p.config AS provider_config, p.updated_at AS provider_updated_at
FROM secrets s
JOIN secret_providers p ON p.id = s.provider_id
WHERE s.namespace_id IS NOT DISTINCT FROM sqlc.narg(namespace_id) AND s.key = sqlc.arg(key);

-- name: UpsertSecret :one
INSERT INTO secrets (id, namespace_id, key, provider_id, provider_ref, ciphertext, key_id, description, created_by, updated_by, updated_at)
VALUES (sqlc.arg(id), sqlc.narg(namespace_id), sqlc.arg(key), sqlc.arg(provider_id), sqlc.arg(provider_ref), sqlc.narg(ciphertext),
    sqlc.arg(key_id), sqlc.arg(description), sqlc.arg(actor), sqlc.arg(actor), sqlc.arg(now))
ON CONFLICT (namespace_id, key) DO UPDATE SET provider_id = EXCLUDED.provider_id, provider_ref = EXCLUDED.provider_ref,
    ciphertext = EXCLUDED.ciphertext, key_id = EXCLUDED.key_id, description = EXCLUDED.description,
    updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at
RETURNING (xmax = 0)::boolean AS created;

-- name: DeleteSecret :execrows
DELETE FROM secrets WHERE namespace_id IS NOT DISTINCT FROM sqlc.narg(namespace_id) AND key = sqlc.arg(key);

-- name: ResolveCandidates :many
-- The definitions of one key in the global scope and in the namespaces of chain.
SELECT s.id, coalesce(n.name, '')::text AS scope, s.provider_ref, s.ciphertext, s.key_id, p.id AS provider_id, p.name AS provider,
    p.type AS provider_type, p.config AS provider_config, p.updated_at AS provider_updated_at
FROM secrets s
JOIN secret_providers p ON p.id = s.provider_id
LEFT JOIN namespaces n ON n.id = s.namespace_id
WHERE s.key = sqlc.arg(key) AND (s.namespace_id IS NULL OR (n.name = ANY(sqlc.arg(chain)::text[]) AND n.deleted_at IS NULL));

-- name: TouchSecret :exec
UPDATE secrets SET last_resolved_at = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- name: ListBuiltinSecrets :many
SELECT s.id, s.key, coalesce(n.name, '')::text AS scope, s.ciphertext, s.key_id
FROM secrets s
JOIN secret_providers p ON p.id = s.provider_id
LEFT JOIN namespaces n ON n.id = s.namespace_id
WHERE p.type = 'builtin'
ORDER BY s.id
FOR UPDATE OF s;

-- name: StoredKeyIDs :many
SELECT DISTINCT s.key_id FROM secrets s JOIN secret_providers p ON p.id = s.provider_id WHERE p.type = 'builtin' ORDER BY s.key_id;

-- name: SetCiphertext :exec
UPDATE secrets SET ciphertext = sqlc.arg(ciphertext), key_id = sqlc.arg(key_id) WHERE id = sqlc.arg(id);
