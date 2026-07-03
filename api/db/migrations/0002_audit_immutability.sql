-- 0002_audit_immutability.sql — make audit_log append-only + least-privilege the
-- runtime role on it.
--
-- Runs as od_migrator (DATABASE_URL_MIGRATOR), the owner of audit_log (0001).
--
-- Two guarantees land here, in layers:
--   1. Immutability lives in a TRIGGER, not in grants (NFR19). RLS/grants are
--      insufficient because the Supabase service role / a superuser bypasses
--      them; a BEFORE UPDATE OR DELETE trigger that RAISEs fires for EVERY role,
--      including the owner. The AC-1 test proves this by connecting as the owner
--      (the most-privileged non-superuser) and still expecting the raise.
--   2. Least-privilege first line: the app only ever appends + reads audit rows,
--      so od_runtime loses UPDATE/DELETE here (0001 over-granted all four DML).
--      The trigger is the below-role backstop; the revoke is defence-in-depth.
--
-- ENABLE ALWAYS is load-bearing: a plain (origin-mode) trigger is skipped under
-- `session_replication_role = replica`; ALWAYS makes it fire regardless, so a
-- replication-mode session cannot silently bypass immutability. Genuine
-- break-glass (a superuser DISABLEing the trigger) is explicitly out-of-band —
-- this migration makes tampering impossible for the application/runtime role.
--
-- Row-level UPDATE/DELETE triggers do NOT fire on TRUNCATE, so a separate
-- statement-level BEFORE TRUNCATE trigger closes that hole below role level too.
--
-- The raise uses the default P0001 SQLSTATE (not a custom code, and deliberately
-- NOT 42501): the AC-1 test distinguishes the trigger's raise (immutability) from
-- 42501 permission-denied (the runtime DDL-denial tests), so the two must not
-- collide.
--
-- Scope: this migration adds NO columns and changes NO column types — the 0001
-- audit_log signature (and its infallible-insert contract) is preserved so the
-- round-trip signature test still passes.

-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION audit_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only: % is forbidden', TG_OP;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- Row-level guard: blocks UPDATE and DELETE for every role, incl. the owner.
CREATE TRIGGER audit_log_no_update_delete
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
ALTER TABLE audit_log ENABLE ALWAYS TRIGGER audit_log_no_update_delete;

-- Defence-in-depth: TRUNCATE does not fire row UPDATE/DELETE triggers, so guard
-- it with its own statement-level BEFORE TRUNCATE trigger (also ENABLE ALWAYS).
CREATE TRIGGER audit_log_no_truncate
    BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_immutable();
ALTER TABLE audit_log ENABLE ALWAYS TRIGGER audit_log_no_truncate;

-- Least-privilege: the app appends + reads audit rows only. 0001 granted all
-- four DML privileges; revoke the two an append-only ledger never needs, leaving
-- SELECT, INSERT. od_runtime owns nothing and has no schema CREATE (0001 / init),
-- so it still cannot ALTER the table or DISABLE the trigger (those raise 42501).
REVOKE UPDATE, DELETE ON audit_log FROM od_runtime;

-- +goose Down

-- Restore the 0001 grant state exactly so up -> down -> up round-trips cleanly.
GRANT UPDATE, DELETE ON audit_log TO od_runtime;

DROP TRIGGER IF EXISTS audit_log_no_truncate ON audit_log;
DROP TRIGGER IF EXISTS audit_log_no_update_delete ON audit_log;
DROP FUNCTION IF EXISTS audit_log_immutable();
