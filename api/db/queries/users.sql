-- users.sql — named queries for the application users table (Story 1.5b). sqlc
-- reads this + db/migrations (the schema) and emits typed Go into
-- internal/store/sqlc (DO NOT EDIT that output). Provisioning writes go through
-- store.WithAudit, so the user row and its audit row co-commit in one tx.

-- name: ProvisionUser :one
-- First-login upsert keyed on the stable Supabase subject. A brand-new identity
-- is inserted (inserted=true) and earns a user.provisioned audit row; a returning
-- identity refreshes its mutable identity fields (email/name track Supabase) plus
-- last_login_at (inserted=false) — a no-op for audit that WithAudit commits with
-- NO audit row. role is set ONLY on insert: a returning admin/coordinator is never
-- reset to their first-login role. (xmax = 0) is the Postgres insert-vs-update
-- discriminator: true on a fresh insert, false on the conflict-update path — the
-- clean `changed` signal for WithAudit, independent of which columns the update
-- touches. Subject is the only conflict target; email is NOT UNIQUE, so a new
-- subject reusing an existing email inserts cleanly instead of erroring.
INSERT INTO users (supabase_subject, email, name, role, last_login_at)
VALUES (@supabase_subject, lower(@email), @name, @role, now())
ON CONFLICT (supabase_subject) DO UPDATE
    SET email         = lower(excluded.email),
        name          = excluded.name,
        last_login_at = CASE
                            WHEN users.last_login_at IS NULL
                              OR users.last_login_at < now() - INTERVAL '15 minutes'
                            THEN now()
                            ELSE users.last_login_at
                        END,
        updated_at    = CASE
                            WHEN users.email IS DISTINCT FROM lower(excluded.email)
                              OR users.name IS DISTINCT FROM excluded.name
                            THEN now()
                            ELSE users.updated_at
                        END
RETURNING id::text, supabase_subject, email, name, role, (xmax = 0) AS inserted;

-- name: GetUserBySubject :one
-- Look up a provisioned identity by its stable Supabase subject (never by email,
-- which can change). Epic 2's per-request role loading will build on this read.
SELECT id, supabase_subject, email, name, role, created_at, updated_at, last_login_at
FROM users
WHERE supabase_subject = $1;

-- name: GetActorBySubject :one
-- Resolve the acting identity's provisioned user id + base role by Supabase
-- subject (Story 1.5c admin gate). id feeds the revocation's revoked_by_user_id
-- FK; role feeds the narrow admin-only check. No row = unprovisioned actor.
SELECT id::text, role
FROM users
WHERE supabase_subject = $1;

-- name: GetUserSubjectByID :one
-- Resolve a target user's Supabase subject from their application user id (the
-- id carried in the admin revocation URL). Revocations are keyed by subject, so
-- the handler resolves id -> subject here before writing the deny-list row.
SELECT supabase_subject
FROM users
WHERE id = @id::uuid;
