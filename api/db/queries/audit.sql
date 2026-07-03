-- audit.sql — named queries for the immutable audit ledger. sqlc reads this +
-- db/migrations (the schema) and emits typed Go into internal/store/sqlc
-- (DO NOT EDIT that output). This is the ONLY write path into audit_log; the
-- store.WithAudit helper co-commits it in the same tx as the business write.
--
-- Only `action` is supplied NOT-NULL; every other column is nullable so a
-- legitimate audit insert can never trip a constraint (the infallible-insert
-- contract from 0001). id and created_at are server-defaulted and intentionally
-- not parameters.

-- name: InsertAuditLog :exec
INSERT INTO audit_log (action, actor_account, actor_role, target_table, target_id, payload)
VALUES ($1, $2, $3, $4, $5, $6);
