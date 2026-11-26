-- Role bootstrap for the OD-Amrita database — runs as the Postgres SUPERUSER,
-- outside goose. Locally this is mounted into /docker-entrypoint-initdb.d/ and
-- runs once on a fresh volume; in production/managed Postgres the operator runs
-- this same file once, out of band, before the first migration:
--
--     psql "$SUPERUSER_DSN" -f db/init/00_roles.sql
--
-- Login roles are cluster-scoped infrastructure, not schema — they are created
-- here (idempotently) and are NOT reverted by `goose down`. Schema objects and
-- grants live in db/migrations/ and run as od_migrator.
--
-- Passwords below are DEV-ONLY defaults for the local docker-compose stack.
-- In any real environment, change them here before running (or ALTER ROLE
-- afterwards) and put the real values only in the environment (.env), never in
-- a committed file.

DO $$
BEGIN
    -- od_migrator: DDL owner. Runs goose, owns the app schema and every table.
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'od_migrator') THEN
        CREATE ROLE od_migrator LOGIN PASSWORD 'migrator_dev_only_change_me';
    END IF;

    -- od_runtime: the app connection. DML only — owns nothing, holds no CREATE,
    -- so a compromised runtime credential can never alter audit structures.
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'od_runtime') THEN
        CREATE ROLE od_runtime LOGIN PASSWORD 'runtime_dev_only_change_me';
    END IF;
END
$$;

-- PG16+ reassigning ownership to a role requires the bootstrap role to be able to
-- SET ROLE to it. A true SUPERUSER (local docker / self-hosted) always can, so this
-- is a harmless no-op there. On MANAGED Postgres (e.g. Neon) the bootstrap runs as a
-- non-superuser database owner (neondb_owner), which is NOT implicitly a member of
-- od_migrator — without this grant the next ALTER SCHEMA fails with SQLSTATE 42501
-- "must be able to SET ROLE od_migrator". current_user keeps it portable across both.
GRANT od_migrator TO current_user WITH SET TRUE;

-- od_migrator owns the app schema so its migrations can create tables and issue
-- grants without superuser help.
ALTER SCHEMA public OWNER TO od_migrator;

-- Nobody gets implicit DDL: only the schema owner (od_migrator) may CREATE.
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- od_runtime may reference objects in the schema (table grants are issued per
-- table by the goose migrations), but may never create anything in it.
GRANT USAGE ON SCHEMA public TO od_runtime;

-- ── Extensions ──────────────────────────────────────────────────────────────
--
-- pg_trgm backs the roster search's index (migration 0025). It is created HERE,
-- with the roles, rather than in that migration, for the same reason the roles
-- are: CREATE EXTENSION needs CREATE on the DATABASE, which is a different
-- privilege from owning the schema. od_migrator owns every table and runs every
-- migration, but deliberately has no rights above the schema — and widening it
-- to install extensions would be a real expansion of what a leaked migrator
-- credential could do, to save one line in a file the operator already runs.
--
-- So this is bootstrap, run once by the superuser (local) or the database owner
-- (Neon's neondb_owner), exactly like the role creation above. pg_trgm is a
-- TRUSTED extension, so the managed-Postgres owner role can install it without
-- true superuser.
--
-- Migration 0025 checks for it and fails with an actionable message naming this
-- file if it is missing, so the ordering cannot go wrong silently.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
