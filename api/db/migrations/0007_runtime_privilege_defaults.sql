-- 0007_runtime_privilege_defaults.sql — make the od_runtime default privilege
-- fail-safe instead of fail-open.
--
-- 0001 installed the standing rule
--
--     ALTER DEFAULT PRIVILEGES FOR ROLE od_migrator IN SCHEMA public
--         GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO od_runtime;
--
-- so EVERY table od_migrator creates hands od_runtime a DELETE automatically. A
-- table that must never be deletable is therefore safe only if whoever wrote the
-- migration remembered a REVOKE. Forgetting produces no error, no failing test,
-- and no visible symptom — just a runtime role that can quietly delete rows.
--
-- The tally at the time of writing runs the other way: four tables must NOT have
-- DELETE (audit_log, events, event_branches, categories) against three that want
-- it (users, auth_session_revocations, branches). The permissive default is both
-- the minority case and the unsafe direction, in a project whose whole posture is
-- no-hard-delete on anything OD records will reference.
--
-- After this migration the default is SELECT, INSERT, UPDATE. A new table that
-- needs DELETE must ask for it explicitly, and a new table whose author forgets
-- to think about deletion is protected rather than exposed.
--
-- Default privileges are NOT retroactive — they only affect tables created after
-- this runs. Existing tables keep the explicit grants their own migrations wrote,
-- which is why the three DELETE holders are restated below rather than re-granted
-- out of necessity: the restatement is documentation that survives a schema dump.

-- +goose Up

ALTER DEFAULT PRIVILEGES FOR ROLE od_migrator IN SCHEMA public
    REVOKE DELETE ON TABLES FROM od_runtime;

-- The three tables that genuinely want DELETE, restated so intent reads locally.
-- Each already holds this grant from its own migration; these are idempotent.
GRANT DELETE ON users                    TO od_runtime;  -- 0003
GRANT DELETE ON auth_session_revocations TO od_runtime;  -- 0004
GRANT DELETE ON branches                 TO od_runtime;  -- 0005

-- +goose Down

ALTER DEFAULT PRIVILEGES FOR ROLE od_migrator IN SCHEMA public
    GRANT DELETE ON TABLES TO od_runtime;
