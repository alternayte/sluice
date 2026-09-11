-- name: GetSetting :one
SELECT value FROM settings WHERE key = $1;

-- name: PutSetting :exec
INSERT INTO settings (key, value, updated_by, updated_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at;

-- name: DeleteSetting :execrows
DELETE FROM settings WHERE key = $1;

-- name: CreateConversation :one
INSERT INTO ai_conversations (id, user_id, title, created_at) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListConversations :many
SELECT * FROM ai_conversations WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2;

-- name: GetConversation :one
SELECT * FROM ai_conversations WHERE id = $1 AND user_id = $2;

-- name: SetConversationTitle :exec
UPDATE ai_conversations SET title = $2 WHERE id = $1 AND title = '';

-- name: DeleteConversation :execrows
DELETE FROM ai_conversations WHERE id = $1 AND user_id = $2;

-- name: InsertMessage :exec
INSERT INTO ai_messages (id, conversation_id, role, content, created_at) VALUES ($1, $2, $3, $4, $5);

-- name: ListMessages :many
SELECT * FROM ai_messages WHERE conversation_id = $1 ORDER BY created_at, id;

-- name: InsertPendingAction :exec
INSERT INTO ai_pending_actions (id, conversation_id, tool_call_id, tool, arguments, status, created_at)
VALUES ($1, $2, $3, $4, $5, 'pending', $6);

-- name: ListPendingActions :many
SELECT * FROM ai_pending_actions WHERE conversation_id = $1 ORDER BY created_at, id;

-- name: DecidePendingAction :one
UPDATE ai_pending_actions SET status = $3, decided_by = $4, decided_at = $5
WHERE id = $1 AND conversation_id = $2 AND status = 'pending'
RETURNING *;

-- name: InsertInsight :exec
INSERT INTO ai_insights (id, execution_id, kind, status, created_at) VALUES ($1, $2, 'triage', 'pending', $3);

-- name: ActiveInsightExists :one
SELECT EXISTS (SELECT 1 FROM ai_insights WHERE execution_id = $1 AND status IN ('pending', 'running'));

-- name: ClaimInsight :one
UPDATE ai_insights SET status = 'running'
WHERE id = (SELECT i.id FROM ai_insights i WHERE i.status = 'pending' ORDER BY i.created_at, i.id FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING *;

-- name: FailStaleInsights :execrows
UPDATE ai_insights SET status = 'failed', error = 'the triage stopped before it ended'
WHERE status = 'running' AND created_at < $1;

-- name: FinishInsight :exec
UPDATE ai_insights SET status = $2, summary = $3, probable_cause = $4, evidence = $5, suggested_fix = $6,
    confidence = $7, model = $8, error = $9
WHERE id = $1;

-- name: ListInsights :many
SELECT * FROM ai_insights WHERE execution_id = $1 ORDER BY created_at DESC, id DESC;
