-- 0023_scan_post_processing.sql — durable follow-up state for accepted scans.

-- +goose Up

CREATE TABLE scan_post_processings (
    scan_id        uuid PRIMARY KEY REFERENCES scan_idempotency_keys(scan_id) ON DELETE CASCADE,
    event_id       uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    record_id      uuid REFERENCES od_records(id) ON DELETE RESTRICT,
    status         text NOT NULL DEFAULT 'queued'
                   CHECK (status IN ('queued', 'processing', 'completed', 'failed')),
    audit_verified boolean NOT NULL DEFAULT false,
    attempts       integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    error_summary  text,
    started_at     timestamptz,
    completed_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT scan_post_processings_completion_ck CHECK (
        (status IN ('queued', 'processing') AND completed_at IS NULL)
        OR
        (status IN ('completed', 'failed') AND completed_at IS NOT NULL)
    )
);

CREATE INDEX scan_post_processings_event_status_idx
    ON scan_post_processings (event_id, status, created_at DESC);

GRANT SELECT, INSERT, UPDATE ON scan_post_processings TO od_runtime;

-- +goose Down

DROP TABLE scan_post_processings;
