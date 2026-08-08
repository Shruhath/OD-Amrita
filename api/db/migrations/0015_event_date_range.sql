-- 0015_event_date_range.sql — an event spans a range of days, not a single day.

-- +goose Up

-- Three statements, in this order, deliberately: ADD COLUMN ... NOT NULL without
-- a default fails outright on a populated table, so the column arrives nullable,
-- every existing row is backfilled to a one-day range, and only then does the
-- NOT NULL land.
ALTER TABLE events
    ADD COLUMN end_date date;

UPDATE events
SET end_date = event_date
WHERE end_date IS NULL;

ALTER TABLE events
    ALTER COLUMN end_date SET NOT NULL;

-- The ORDERING is the part that must never be tunable: an event ending before it
-- starts makes range containment vacuous and every per-day counter meaningless.
-- The range LENGTH bound is policy and lives in the service, where tuning it does
-- not cost a migration.
ALTER TABLE events
    ADD CONSTRAINT events_date_range_ck CHECK (end_date >= event_date);

-- event_date is deliberately NOT renamed to start_date: the rename would churn
-- six queries, the generated sqlc/Go/TS types, the contract and five web surfaces
-- for a cosmetic gain. The comment is what records the meaning instead.
COMMENT ON COLUMN events.event_date IS 'First day of the event range; end_date is the last.';
COMMENT ON COLUMN events.end_date IS 'Last day of the event range; event_date is the first. Backfilled to event_date for pre-range events.';

-- 0014_disputes.sql created this guard against a single event_date and left the
-- instruction to widen it here. It is a live constraint on a PUBLIC write path:
-- left single-day, every dispute filed for day 2+ of a festival is refused by the
-- database, for a student who was already missed once.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION require_dispute_od_date_in_event_range()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    event_start date;
    event_end   date;
BEGIN
    SELECT e.event_date, e.end_date INTO event_start, event_end
    FROM events e
    WHERE e.id = NEW.event_id;

    IF event_start IS NULL THEN
        RAISE EXCEPTION 'event % not found for dispute', NEW.event_id
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    IF NEW.od_date < event_start OR NEW.od_date > event_end THEN
        RAISE EXCEPTION 'dispute od_date % outside event range % .. %', NEW.od_date, event_start, event_end
            USING ERRCODE = '23514',
                  CONSTRAINT = 'disputes_od_date_event_range_ck';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- No GRANT: this is a column on an existing table and 0005's grants already cover
-- it. The runtime privilege registry asserts that posture unchanged.

-- +goose Down

-- Restore the single-day form first: it only reads event_date, so it must be back
-- in place before end_date disappears.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION require_dispute_od_date_in_event_range()
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

    IF NEW.od_date <> event_start THEN
        RAISE EXCEPTION 'dispute od_date % outside event date %', NEW.od_date, event_start
            USING ERRCODE = '23514',
                  CONSTRAINT = 'disputes_od_date_event_range_ck';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

COMMENT ON COLUMN events.event_date IS NULL;

ALTER TABLE events
    DROP CONSTRAINT events_date_range_ck;

ALTER TABLE events
    DROP COLUMN end_date;
