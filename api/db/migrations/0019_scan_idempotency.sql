-- 0019_scan_idempotency.sql — durable client identities for web offline scans.

-- +goose Up

CREATE TABLE scan_idempotency_keys (
    scan_id       uuid        PRIMARY KEY,
    actor_user_id uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX scan_idempotency_keys_created_at_idx
    ON scan_idempotency_keys (created_at);

GRANT SELECT, INSERT ON scan_idempotency_keys TO od_runtime;

-- +goose Down

DROP TABLE scan_idempotency_keys;
