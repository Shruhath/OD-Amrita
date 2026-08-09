-- 0020_reconciliation.sql — durable end-of-day reconciliation runs/issues.

-- +goose Up

CREATE TABLE reconciliation_runs (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id           uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    revision           integer NOT NULL CHECK (revision > 0),
    status             text NOT NULL DEFAULT 'queued'
                       CHECK (status IN ('queued', 'running', 'completed', 'completed_with_issues', 'failed')),
    requested_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    started_at         timestamptz,
    completed_at       timestamptz,
    scanned_records    bigint NOT NULL DEFAULT 0 CHECK (scanned_records >= 0),
    issue_count        bigint NOT NULL DEFAULT 0 CHECK (issue_count >= 0),
    repair_count       bigint NOT NULL DEFAULT 0 CHECK (repair_count >= 0),
    error_summary      text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reconciliation_runs_event_revision_uq UNIQUE (event_id, revision),
    CONSTRAINT reconciliation_runs_completion_ck CHECK (
        (status IN ('queued', 'running') AND completed_at IS NULL)
        OR
        (status IN ('completed', 'completed_with_issues', 'failed') AND completed_at IS NOT NULL)
    )
);

CREATE INDEX reconciliation_runs_event_created_idx
    ON reconciliation_runs (event_id, created_at DESC, id DESC);

CREATE TABLE reconciliation_issues (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id             uuid NOT NULL REFERENCES reconciliation_runs(id) ON DELETE CASCADE,
    event_id           uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    issue_key          text NOT NULL CHECK (btrim(issue_key) <> ''),
    kind               text NOT NULL CHECK (kind IN ('missing_audit', 'post_processing_pending')),
    record_id          uuid REFERENCES od_records(id) ON DELETE RESTRICT,
    details            jsonb NOT NULL DEFAULT '{}'::jsonb,
    status             text NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open', 'resolved')),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    resolved_at        timestamptz,
    CONSTRAINT reconciliation_issues_run_key_uq UNIQUE (run_id, issue_key),
    CONSTRAINT reconciliation_issues_resolution_ck CHECK (
        (status = 'open' AND resolved_at IS NULL)
        OR
        (status = 'resolved' AND resolved_at IS NOT NULL)
    )
);

CREATE INDEX reconciliation_issues_event_status_idx
    ON reconciliation_issues (event_id, status, created_at DESC);

GRANT SELECT, INSERT, UPDATE ON reconciliation_runs, reconciliation_issues TO od_runtime;

-- +goose Down

DROP TABLE reconciliation_issues;
DROP TABLE reconciliation_runs;
