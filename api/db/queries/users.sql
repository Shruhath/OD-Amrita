-- users.sql — named queries for the application users table (Story 1.5b). sqlc
-- reads this + db/migrations (the schema) and emits typed Go into
-- internal/store/sqlc (DO NOT EDIT that output). Provisioning writes go through
-- store.WithAudit, so the user row and its audit row co-commit in one tx.

-- name: ProvisionUser :one
-- First-login upsert keyed on the stable Supabase subject. A brand-new identity
-- is inserted (inserted=true) and earns a user.provisioned audit row; a
-- returning identity only bumps last_login_at (inserted=false) — a no-op that
-- WithAudit commits with NO audit row. role is set ONLY on insert: a returning
-- admin/coordinator is never reset to their first-login role. (xmax = 0) is the
-- Postgres insert-vs-update discriminator: true on a fresh insert, false on the
-- conflict-update path — the clean `changed` signal for WithAudit.
INSERT INTO users (supabase_subject, email, name, role, last_login_at)
VALUES (@supabase_subject, lower(@email), @name, @role, now())
ON CONFLICT (supabase_subject) DO UPDATE
    SET last_login_at = now()
RETURNING id::text, supabase_subject, email, name, role, (xmax = 0) AS inserted;

-- name: GetUserBySubject :one
-- Look up a provisioned identity by its stable Supabase subject (never by email,
-- which can change). Epic 2's per-request role loading will build on this read.
SELECT id, supabase_subject, email, name, role, created_at, updated_at, last_login_at
FROM users
WHERE supabase_subject = $1;
