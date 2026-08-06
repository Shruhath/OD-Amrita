-- 0014_disputes.sql — public missed-slot disputes and teacher/admin resolution.

-- +goose Up

CREATE TABLE disputes (
    id                       uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id               uuid        NOT NULL REFERENCES students(id) ON DELETE RESTRICT,
    event_id                 uuid        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    category_id              uuid        NOT NULL,
    od_date                  date        NOT NULL,
    slot                     integer     NOT NULL CHECK (slot BETWEEN 1 AND 12),
    submitted_email          text        NOT NULL CHECK (
        submitted_email = lower(btrim(submitted_email))
        AND length(submitted_email) BETWEEN 3 AND 320
        AND position('@' in submitted_email) > 1
    ),
    claimed_coordinator_name text        NULL CHECK (
        claimed_coordinator_name IS NULL OR length(claimed_coordinator_name) <= 200
    ),
    student_note             text        NULL CHECK (
        student_note IS NULL OR length(student_note) <= 1000
    ),
    status                   text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    resolution_note          text        NULL CHECK (
        resolution_note IS NULL OR length(resolution_note) BETWEEN 1 AND 2000
    ),
    resolved_by_user_id      uuid        NULL REFERENCES users(id) ON DELETE SET NULL,
    resolved_at              timestamptz NULL,
    created_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT disputes_category_event_fk
        FOREIGN KEY (event_id, category_id)
        REFERENCES categories(event_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT disputes_resolved_state_ck CHECK (
        (
            status = 'open'
            AND resolution_note IS NULL
            AND resolved_by_user_id IS NULL
            AND resolved_at IS NULL
        )
        OR
        (
            status = 'resolved'
            AND resolution_note IS NOT NULL
            AND resolved_at IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX uq_disputes_open_student_event_date_slot
    ON disputes (student_id, event_id, od_date, slot)
    WHERE status = 'open';

CREATE INDEX disputes_created_at_idx
    ON disputes (created_at DESC, id DESC);

CREATE INDEX disputes_student_event_date_slot_idx
    ON disputes (student_id, event_id, od_date, slot);

-- Deliberately no require_active_event_for_child_mutation trigger: archived
-- events remain disputable. The separate day guard below is still required; it
-- bounds the disputed day and says nothing about archive state.
-- +goose StatementBegin
CREATE FUNCTION require_dispute_od_date_in_event_range()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    event_start date;
BEGIN
    SELECT e.event_date INTO event_start
    FROM events e
    WHERE e.id = NEW.event_id;

    IF event_start IS NULL THEN
        RAISE EXCEPTION 'event % not found for dispute', NEW.event_id
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    -- Story 0.12a widens this to BETWEEN event_date AND end_date. Today there
    -- is no end_date column, so the valid range is the single event_date.
    IF NEW.od_date <> event_start THEN
        RAISE EXCEPTION 'dispute od_date % outside event date %', NEW.od_date, event_start
            USING ERRCODE = '23514',
                  CONSTRAINT = 'disputes_od_date_event_range_ck';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER disputes_od_date_event_range_guard
BEFORE INSERT OR UPDATE OF event_id, od_date ON disputes
FOR EACH ROW EXECUTE FUNCTION require_dispute_od_date_in_event_range();

-- No GRANT/REVOKE here: 0007 sets od_runtime's default table privileges to
-- SELECT, INSERT, UPDATE and no DELETE. The runtime privilege registry asserts
-- that posture for this table.

-- +goose Down

DROP TABLE disputes;
DROP FUNCTION require_dispute_od_date_in_event_range();
