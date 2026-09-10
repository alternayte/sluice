-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: InsertUser :exec
INSERT INTO users (id, email, name, password_hash, role, must_change_password, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users
WHERE (sqlc.narg('after_created')::timestamptz IS NULL
    OR (created_at, id) > (sqlc.narg('after_created')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at, id
LIMIT sqlc.arg('lim');

-- name: LockEnabledAdmins :many
SELECT id FROM users WHERE role = 'admin' AND disabled_at IS NULL ORDER BY id FOR UPDATE;

-- name: GetUserForUpdate :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- name: UpdateUser :one
UPDATE users SET name = $2, role = $3, disabled_at = $4 WHERE id = $1 RETURNING *;

-- name: SetUserPassword :exec
UPDATE users SET password_hash = $2, must_change_password = $3 WHERE id = $1;

-- name: TouchLogin :exec
UPDATE users SET last_login_at = $2 WHERE id = $1;

-- name: InsertSession :exec
INSERT INTO sessions (id_hash, user_id, created_at, last_seen_at, expires_at, ip, user_agent)
VALUES ($1, $2, $3, $3, $4, $5, $6);

-- name: GetSession :one
SELECT s.user_id, s.expires_at, s.last_seen_at, u.email, u.name, u.role, u.must_change_password, u.disabled_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.id_hash = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = $2, expires_at = $3 WHERE id_hash = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = $1;

-- name: DeleteUserSessions :execrows
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteOtherSessions :execrows
DELETE FROM sessions WHERE user_id = $1 AND id_hash <> $2;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < $1;

-- name: InsertLoginAttempt :exec
INSERT INTO login_attempts (email, ip, attempted_at, success) VALUES ($1, $2, $3, $4);

-- name: LoginFailures :one
SELECT
    count(*) FILTER (WHERE email = sqlc.arg('email'))::int AS email_failures,
    count(*) FILTER (WHERE ip = sqlc.arg('ip'))::int AS ip_failures,
    coalesce(min(attempted_at) FILTER (WHERE email = sqlc.arg('email')), sqlc.arg('since'))::timestamptz AS email_oldest,
    coalesce(min(attempted_at) FILTER (WHERE ip = sqlc.arg('ip')), sqlc.arg('since'))::timestamptz AS ip_oldest
FROM login_attempts
WHERE NOT success AND attempted_at > sqlc.arg('since') AND (email = sqlc.arg('email') OR ip = sqlc.arg('ip'));

-- name: DeleteOldLoginAttempts :execrows
DELETE FROM login_attempts WHERE attempted_at < $1;

-- name: InsertToken :exec
INSERT INTO api_tokens (id, user_id, name, token_hash, prefix, role, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetTokenByHash :one
SELECT t.id, t.user_id, t.role AS token_role, t.expires_at, t.revoked_at, t.last_used_at,
    u.email, u.name, u.role AS user_role, u.must_change_password, u.disabled_at
FROM api_tokens t JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1;

-- name: TouchToken :exec
UPDATE api_tokens SET last_used_at = $2 WHERE id = $1;

-- name: GetToken :one
SELECT t.*, u.email AS user_email FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE t.id = $1;

-- name: RevokeToken :exec
UPDATE api_tokens SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL;

-- name: ListTokens :many
SELECT t.*, u.email AS user_email FROM api_tokens t JOIN users u ON u.id = t.user_id
WHERE (sqlc.narg('user_id')::uuid IS NULL OR t.user_id = sqlc.narg('user_id')::uuid)
    AND (sqlc.narg('after_created')::timestamptz IS NULL
        OR (t.created_at, t.id) < (sqlc.narg('after_created')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg('lim');

