-- name: ListAuditLogForRecord :many
SELECT action, actor_account, actor_role, created_at, payload
FROM audit_log
WHERE target_table = 'od_records' AND target_id = @record_id
ORDER BY created_at ASC, id ASC;

-- name: CountAuditLog :one
SELECT count(*)::bigint
FROM audit_log
WHERE (@action::text = '' OR action = @action::text)
  AND (@actor_account::text = '' OR actor_account = @actor_account::text)
  AND (@target_table::text = '' OR target_table = @target_table::text)
  AND (@target_id::text = '' OR target_id = @target_id::text);

-- name: ListAuditLog :many
SELECT id, action, actor_account, actor_role, target_table, target_id, created_at, payload
FROM audit_log
WHERE (@action::text = '' OR action = @action::text)
  AND (@actor_account::text = '' OR actor_account = @actor_account::text)
  AND (@target_table::text = '' OR target_table = @target_table::text)
  AND (@target_id::text = '' OR target_id = @target_id::text)
ORDER BY created_at DESC, id DESC
LIMIT @row_limit::bigint
OFFSET @row_offset::bigint;
