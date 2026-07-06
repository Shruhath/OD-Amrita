-- 0004_auth_session_revocations.sql — the local session-revocation deny-list
-- (forced logout, Story 1.5c) + runtime grant.
--
-- Runs as od_migrator (DATABASE_URL_MIGRATOR). Under the hybrid auth model
-- (Story 1.5a) Supabase owns sessions and refresh tokens; this table is NOT a
-- session store and does NOT hold any token. It is an auditable deny-list the
-- auth middleware consults so a still-cryptographically-valid access JWT is
-- rejected on its NEXT request after a forced logout — without waiting for the
-- token to expire. Supabase-side revocation is best-effort; THIS table is the
-- authoritative enforcement record for our API.
--
-- Keying: revocations are keyed by the Supabase subject (the JWT `sub`), never
-- by email (which is mutable). revoked_by_user_id references the acting admin's
-- provisioned users row.

-- +goose Up

CREATE TABLE auth_session_revocations (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The revoked identity's Supabase subject (JWT `sub`). Not an FK: the
    -- deny-list must be assertable even for a subject that has no users row.
    supabase_subject   text        NOT NULL,
    -- The specific Supabase session for scope='session'; NULL for scope='all'.
    session_id         uuid,
    scope              text        NOT NULL CHECK (scope IN ('all', 'session')),
    -- DB-authoritative revocation instant. A scope='all' check rejects tokens
    -- whose `iat` is at/before this; client time is never trusted for it.
    revoked_after      timestamptz NOT NULL DEFAULT now(),
    reason             text        NOT NULL,
    revoked_by_user_id uuid        NOT NULL REFERENCES users(id),
    created_at         timestamptz NOT NULL DEFAULT now(),

    -- Coherence: a session-scoped revoke MUST name a session; an all-scoped
    -- revoke MUST NOT (session_id is meaningless there).
    CONSTRAINT auth_session_revocations_scope_session_ck
        CHECK ((scope = 'all' AND session_id IS NULL)
            OR (scope = 'session' AND session_id IS NOT NULL)),

    -- Idempotency: re-revoking the same session is a no-op (the upsert's
    -- ON CONFLICT DO UPDATE returns the existing row with inserted=false, so no
    -- duplicate audit row is written). scope='all' rows carry session_id = NULL,
    -- which SQL treats as DISTINCT under UNIQUE, so a later explicit admin
    -- force-logout is deliberately recordable again (a re-login can be re-revoked).
    CONSTRAINT auth_session_revocations_subject_session_uq
        UNIQUE (supabase_subject, session_id)
);

-- The middleware revocation check filters by subject on every protected request;
-- index it so the per-request lookup stays cheap as the deny-list grows.
CREATE INDEX auth_session_revocations_subject_idx
    ON auth_session_revocations (supabase_subject);

-- od_runtime gets DML only (mirroring 0003's explicit grant). It owns nothing and
-- holds no CREATE, so it can never run DDL against this table — the
-- api/test/db role-denial gate is the backstop.
GRANT SELECT, INSERT, UPDATE, DELETE ON auth_session_revocations TO od_runtime;

-- +goose Down

DROP TABLE auth_session_revocations;
