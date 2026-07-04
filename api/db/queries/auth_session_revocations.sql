-- auth_session_revocations.sql — named queries for the forced-logout deny-list
-- (Story 1.5c). sqlc reads this + db/migrations (the schema) and emits typed Go
-- into internal/store/sqlc (DO NOT EDIT that output). The insert goes through
-- store.WithAudit so the revocation row and its audit row co-commit in one tx;
-- the check runs read-only on the pool from the auth middleware.

-- name: InsertRevocation :one
-- Record a revocation, idempotently. session_id is NULL for scope='all' and a
-- uuid for scope='session'. On a duplicate (supabase_subject, session_id) the
-- ON CONFLICT DO UPDATE self-assigns session_id (a true no-op) so RETURNING still
-- yields the existing row: (xmax = 0) is TRUE only on a genuine insert, giving the
-- clean `inserted` signal WithAudit uses to decide whether an audit row is written
-- (a duplicate session revoke commits with none). scope='all' rows never conflict
-- (session_id NULL is DISTINCT under UNIQUE), so each admin force-logout records a
-- fresh row that can force-log-out a later re-login.
INSERT INTO auth_session_revocations (supabase_subject, session_id, scope, reason, revoked_by_user_id)
VALUES (@supabase_subject, sqlc.narg('session_id')::uuid, @scope, @reason, @revoked_by_user_id::uuid)
ON CONFLICT (supabase_subject, session_id) DO UPDATE
    SET session_id = excluded.session_id
RETURNING id::text, (xmax = 0) AS inserted;

-- name: IsTokenRevoked :one
-- The next-request enforcement predicate (DoD 6). A verified token (sub,
-- session_id, iat) is revoked when either its specific session was revoked
-- (scope='session') OR a scope='all' revocation for the subject was recorded at/
-- after the token was issued. revoked_after is DB-authoritative time, so a
-- legitimate re-login (iat AFTER revoked_after) is NOT revoked.
SELECT EXISTS (
    SELECT 1 FROM auth_session_revocations
    WHERE supabase_subject = @supabase_subject
      AND (
          (scope = 'session' AND session_id = sqlc.narg('session_id')::uuid)
          OR (scope = 'all' AND revoked_after >= @issued_at)
      )
) AS revoked;
