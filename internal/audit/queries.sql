-- name: InsertAuditEvent :exec
INSERT INTO audit_events (id, ts, actor_type, actor_id, action, target_type, target_id, details, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: DeleteOldAuditEvents :execrows
DELETE FROM audit_events WHERE ts < $1;
