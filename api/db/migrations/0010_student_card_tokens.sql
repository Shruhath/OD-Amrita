-- 0010_student_card_tokens.sql — server-side NFC card token ledger.
--
-- Stores only a one-way fingerprint of the sealed card token. The raw HMAC
-- token is returned once to the admin populator and must never be persisted.

-- +goose Up

CREATE TABLE student_card_tokens (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id   uuid        NOT NULL REFERENCES students(id) ON DELETE RESTRICT,
    version      text        NOT NULL,
    token_sha256 bytea       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT student_card_tokens_version_chk CHECK (version = btrim(version) AND version <> '' AND length(version) <= 16),
    CONSTRAINT student_card_tokens_token_sha256_chk CHECK (length(token_sha256) = 32)
);

CREATE UNIQUE INDEX student_card_tokens_student_version_unique_idx
    ON student_card_tokens (student_id, version);

CREATE UNIQUE INDEX student_card_tokens_version_fingerprint_unique_idx
    ON student_card_tokens (version, token_sha256);

CREATE INDEX student_card_tokens_student_id_idx
    ON student_card_tokens (student_id);

-- +goose Down

DROP TABLE student_card_tokens;
