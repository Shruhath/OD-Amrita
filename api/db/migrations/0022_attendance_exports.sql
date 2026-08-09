-- 0022_attendance_exports.sql — durable asynchronous attendance exports.

-- +goose Up

CREATE TABLE attendance_exports (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id             uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    requested_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    format               text NOT NULL DEFAULT 'csv' CHECK (format = 'csv'),
    status               text NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued', 'processing', 'completed', 'failed')),
    output_csv           text NOT NULL DEFAULT '',
    row_count            integer NOT NULL DEFAULT 0 CHECK (row_count >= 0),
    error_summary        text,
    created_at           timestamptz NOT NULL DEFAULT now(),
    started_at           timestamptz,
    completed_at         timestamptz,
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX attendance_exports_event_created_idx
    ON attendance_exports (event_id, created_at DESC, id DESC);
CREATE INDEX attendance_exports_status_idx
    ON attendance_exports (status, created_at ASC, id ASC);

GRANT SELECT, INSERT, UPDATE ON attendance_exports TO od_runtime;

-- +goose Down

DROP TABLE attendance_exports;
