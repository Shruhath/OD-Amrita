-- 0001_audit_log.sql — the audit_log table structure + runtime grants.
--
-- Runs as od_migrator (DATABASE_URL_MIGRATOR). The login roles themselves are
-- cluster infrastructure created by db/init/00_roles.sql, not by goose.
--
-- Base extensions: none. Postgres 17 provides gen_random_uuid() in core, and
-- this schema references nothing else — per AC 1 the extension set that Up
-- creates and Down reverts is exactly the set the schema uses (empty).
--
-- Scope note: the immutability trigger, the same-transaction write helper and
-- the error/event registries are deliberately NOT here — they land in the next
-- migration. This one lands only the table structure and the grant model.

-- +goose Up

-- audit_log rows are server-written; id is server-generated (v4 is fine — audit
-- ordering uses created_at, never a UUID-embedded timestamp). Every column that
-- a legitimate audit write supplies is nullable so the insert is infallible.
CREATE TABLE audit_log (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    action        text        NOT NULL,
    actor_account text,
    actor_role    text,
    target_table  text,
    target_id     text,
    payload       jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- od_runtime gets DML only. It does not own the table and holds no CREATE on
-- the schema (see db/init/00_roles.sql), so it can never run DDL against it.
GRANT SELECT, INSERT, UPDATE, DELETE ON audit_log TO od_runtime;

-- Future migrator-created tables are usable by od_runtime without re-granting.
ALTER DEFAULT PRIVILEGES FOR ROLE od_migrator IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO od_runtime;

-- +goose Down

ALTER DEFAULT PRIVILEGES FOR ROLE od_migrator IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM od_runtime;

DROP TABLE audit_log;
