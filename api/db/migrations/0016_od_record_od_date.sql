-- 0016_od_record_od_date.sql — an OD record belongs to a day, and the duplicate
-- guard is keyed on it.

-- +goose Up

-- Same three-step shape as 0015, and for the same reason. NOT NULL here is not
-- stylistic: Postgres treats NULLs as DISTINCT in a unique index, so a single
-- NULL-dated row would never collide with anything and the FR10 duplicate guard
-- would stop guarding with no error anywhere.
ALTER TABLE od_records
    ADD COLUMN od_date date;

-- (timestamptz AT TIME ZONE 'Asia/Kolkata')::date converts an absolute instant to
-- the campus wall-clock date. Never use this at READ time: the two facts diverge
-- the moment a teacher files a correction the next morning.
--
-- CLAMPED into the owning event's range, for the same reason and by the same rule
-- the Go clamp uses (ClampToEventRange in service/od_record/schedule.go). The
-- receipt day equals the belonged-to day only for live coordinator marks. The
-- teacher/admin search-mark is exempt from slot windows and had NO date
-- check at all before this migration, so a correction typed the morning after a
-- one-day event is a real, already-writable row whose receipt date falls outside
-- the event entirely.
--
-- Unclamped, such a row would be permanently illegal data: the trigger created
-- below validates new writes only and never revalidates existing rows, so it
-- would sit there unreachable from the admin day filter (which enumerates only
-- event_date..end_date), rendered on the public dashboard under a day the event
-- never had, and would raise 23514 on any later UPDATE of event_id or od_date.
UPDATE od_records r
SET od_date = GREATEST(
        e.event_date,
        LEAST(e.end_date, (r.server_received_at AT TIME ZONE 'Asia/Kolkata')::date)
    )
FROM events e
WHERE e.id = r.event_id
  AND r.od_date IS NULL;

ALTER TABLE od_records
    ALTER COLUMN od_date SET NOT NULL;

COMMENT ON COLUMN od_records.od_date IS 'Campus-local day the OD belongs to, never the day the row was written. Derived server-side; never accepted from a client.';

-- FR10 is re-keyed: one OD per student per slot PER DAY. The rename is load-
-- bearing — mapPgError matches the constraint name as a string literal, so the
-- old name left in place would keep a duplicate mark returning an unmapped 500
-- instead of SCAN_DUPLICATE while the insert still fails.
DROP INDEX uq_od_records_present_student_event_slot;

CREATE UNIQUE INDEX uq_od_records_present_student_event_date_slot
    ON od_records (student_id, event_id, od_date, slot)
    WHERE status = 'present';

-- The supporting indexes cover the per-day counters and the public read. Without
-- od_date those queries scan the busiest table in the schema.
DROP INDEX od_records_event_category_slot_idx;
DROP INDEX od_records_student_event_slot_idx;
DROP INDEX od_records_actor_event_slot_idx;

CREATE INDEX od_records_event_category_slot_idx
    ON od_records (event_id, category_id, od_date, slot);

CREATE INDEX od_records_student_event_slot_idx
    ON od_records (student_id, event_id, od_date, slot);

CREATE INDEX od_records_actor_event_slot_idx
    ON od_records (actor_user_id, event_id, od_date, slot);

-- Modelled on require_dispute_od_date_in_event_range in 0014: same SQLSTATE, same
-- constraint-name convention, same not-found branch. Service-level validation is
-- not enough — once a human picks the day, a mark filed outside the festival
-- entirely would otherwise be accepted with nothing downstream noticing.
-- +goose StatementBegin
CREATE FUNCTION require_od_record_od_date_in_event_range()
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
        RAISE EXCEPTION 'event % not found for od record', NEW.event_id
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    IF NEW.od_date < event_start OR NEW.od_date > event_end THEN
        RAISE EXCEPTION 'od record od_date % outside event range % .. %', NEW.od_date, event_start, event_end
            USING ERRCODE = '23514',
                  CONSTRAINT = 'od_records_od_date_event_range_ck';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Created AFTER the backfill above on purpose: a BEFORE UPDATE OF od_date trigger
-- already in place would fire on the backfill itself.
--
-- This is a SECOND guard alongside od_records_active_event_guard, not a
-- replacement — that one is about the event's archive state, this one is about
-- the day, and they are different facts.
CREATE TRIGGER od_records_od_date_event_range_guard
BEFORE INSERT OR UPDATE OF event_id, od_date ON od_records
FOR EACH ROW EXECUTE FUNCTION require_od_record_od_date_in_event_range();

-- +goose Down

DROP TRIGGER od_records_od_date_event_range_guard ON od_records;
DROP FUNCTION require_od_record_od_date_in_event_range();

DROP INDEX od_records_event_category_slot_idx;
DROP INDEX od_records_student_event_slot_idx;
DROP INDEX od_records_actor_event_slot_idx;

CREATE INDEX od_records_event_category_slot_idx
    ON od_records (event_id, category_id, slot);

CREATE INDEX od_records_student_event_slot_idx
    ON od_records (student_id, event_id, slot);

CREATE INDEX od_records_actor_event_slot_idx
    ON od_records (actor_user_id, event_id, slot);

DROP INDEX uq_od_records_present_student_event_date_slot;

-- ⚠️ ONE-WAY DOOR ONCE THE FEATURE HAS BEEN USED.
--
-- This restores the narrower single-day key. It succeeds only while no student
-- has been marked in the same slot on two different days — which is precisely
-- what the Up half exists to permit. After the first multi-day festival this
-- CREATE raises 23505 and goose rolls the whole step back.
--
-- That failure is SAFE (the transaction aborts cleanly, the database keeps the
-- multi-day key) but it means rollback past 0016 stops being available the
-- moment real multi-day data exists. Deliberately not "fixed" by deleting or
-- collapsing the offending rows: a Down migration that destroys attendance
-- records to make itself succeed is far worse than one that refuses.
--
-- To roll back after that point, reconcile the duplicates by hand first.
CREATE UNIQUE INDEX uq_od_records_present_student_event_slot
    ON od_records (student_id, event_id, slot)
    WHERE status = 'present';

ALTER TABLE od_records
    DROP COLUMN od_date;
