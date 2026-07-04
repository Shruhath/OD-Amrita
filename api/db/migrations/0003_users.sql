-- 0003_users.sql — the application users table (first-login provisioning target)
-- + runtime grant.
--
-- Runs as od_migrator (DATABASE_URL_MIGRATOR). This is the first real BUSINESS
-- table (audit_log in 0001/0002 is audit infrastructure). A verified Supabase
-- identity is provisioned into exactly one row here on first login (Story 1.5b);
-- the role column is the GLOBAL base role only — per-event coordinator
-- assignment and RBAC enforcement are later epics.
--
-- Scope: exactly ONE table (users). There is deliberately NO refresh_tokens
-- table — under the hybrid auth model (Story 1.5a) Supabase owns access and
-- refresh tokens and their TTLs; the backend never mints or persists them.

-- +goose Up

-- One row per Supabase identity. supabase_subject (the JWT `sub`) is the stable,
-- immutable join key; email can change, so it is kept UNIQUE for defence in
-- depth but is not the identity key. role defaults to the LEAST-privileged
-- 'student'; a CHECK (not a Postgres enum) keeps the closed set while staying
-- trivial to extend and mapping to a plain Go string via sqlc.
CREATE TABLE users (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    supabase_subject text        NOT NULL UNIQUE,
    email            text        NOT NULL UNIQUE,
    name             text,
    role             text        NOT NULL DEFAULT 'student'
                                 CHECK (role IN ('admin', 'teacher', 'coordinator', 'student')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    last_login_at    timestamptz
);

-- Belt-and-suspenders with 0001's ALTER DEFAULT PRIVILEGES: grant od_runtime the
-- DML it needs on users explicitly, so the grant reads clearly at the table and
-- does not depend solely on the default-privilege machinery. od_runtime owns
-- nothing and holds no CREATE, so it can never run DDL against this table (the
-- api/test/db role-denial gate is the backstop).
GRANT SELECT, INSERT, UPDATE, DELETE ON users TO od_runtime;

-- +goose Down

DROP TABLE users;
