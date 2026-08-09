-- 0021_attendance_imports.sql — durable, retryable attendance CSV imports.

-- +goose Up

CREATE TABLE attendance_imports (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id             uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    requested_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    source_filename      text NOT NULL CHECK (btrim(source_filename) <> '' AND length(source_filename) <= 255),
    source_csv           text NOT NULL CHECK (length(source_csv) > 0),
    status               text NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued', 'processing', 'completed', 'completed_with_errors', 'failed')),
    total_rows           integer NOT NULL DEFAULT 0 CHECK (total_rows >= 0),
    processed_rows       integer NOT NULL DEFAULT 0 CHECK (processed_rows >= 0),
    accepted_rows        integer NOT NULL DEFAULT 0 CHECK (accepted_rows >= 0),
    duplicate_rows       integer NOT NULL DEFAULT 0 CHECK (duplicate_rows >= 0),
    invalid_rows         integer NOT NULL DEFAULT 0 CHECK (invalid_rows >= 0),
    error_summary        text,
    created_at           timestamptz NOT NULL DEFAULT now(),
    started_at           timestamptz,
    completed_at         timestamptz,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT attendance_imports_counts_ck CHECK (
        processed_rows >= accepted_rows + duplicate_rows + invalid_rows
    )
);

CREATE INDEX attendance_imports_event_created_idx
    ON attendance_imports (event_id, created_at DESC, id DESC);
CREATE INDEX attendance_imports_status_idx
    ON attendance_imports (status, created_at ASC, id ASC);

CREATE TABLE attendance_import_rows (
    import_id       uuid NOT NULL REFERENCES attendance_imports(id) ON DELETE CASCADE,
    row_number      integer NOT NULL CHECK (row_number >= 2),
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'accepted', 'duplicate', 'invalid')),
    record_id       uuid REFERENCES od_records(id) ON DELETE RESTRICT,
    field           text,
    error_code      text,
    error_message   text,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (import_id, row_number)
);

CREATE INDEX attendance_import_rows_status_idx
    ON attendance_import_rows (import_id, status, row_number);

GRANT SELECT, INSERT, UPDATE ON attendance_imports, attendance_import_rows TO od_runtime;

-- +goose Down

DROP TABLE attendance_import_rows;
DROP TABLE attendance_imports;
