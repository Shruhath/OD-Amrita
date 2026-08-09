-- name: ListAuditLogForRecord :many
SELECT action, actor_account, actor_role, created_at, payload
FROM audit_log
WHERE target_table = 'od_records' AND target_id = @record_id
ORDER BY created_at ASC, id ASC;
