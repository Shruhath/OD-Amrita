-- 0024_event_closeouts.sql — durable archived-event closeout/report jobs.

-- +goose Up

CREATE TABLE event_closeouts (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id             uuid NOT NULL UNIQUE REFERENCES events(id) ON DELETE RESTRICT,
    requested_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    status               text NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued', 'processing', 'completed', 'failed')),
    total_records        bigint NOT NULL DEFAULT 0 CHECK (total_records >= 0),
    report_csv           text NOT NULL DEFAULT '',
    error_summary        text,
    started_at           timestamptz,
    completed_at         timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT event_closeouts_completion_ck CHECK (
        (status IN ('queued', 'processing') AND completed_at IS NULL)
        OR
        (status IN ('completed', 'failed') AND completed_at IS NOT NULL)
    )
);

GRANT SELECT, INSERT, UPDATE ON event_closeouts TO od_runtime;

-- +goose Down

DROP TABLE event_closeouts;
