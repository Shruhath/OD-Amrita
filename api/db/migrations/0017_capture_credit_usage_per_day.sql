-- 0017_capture_credit_usage_per_day.sql — the fallback-credit pool refills each
-- day; the cap does not move.

-- +goose Up

ALTER TABLE event_capture_credit_usage
    ADD COLUMN usage_date date;

-- No rows exist today, but the migration has to be correct against any database:
-- updated_at is the instant the pool was last debited, and its campus-local date
-- is the day that debit belonged to.
UPDATE event_capture_credit_usage
SET usage_date = (updated_at AT TIME ZONE 'Asia/Kolkata')::date
WHERE usage_date IS NULL;

ALTER TABLE event_capture_credit_usage
    ALTER COLUMN usage_date SET NOT NULL;

-- The asymmetry with event_capture_credit_config is the whole point. Usage is per
-- day so a coordinator who burns the pool on day one starts day two with a fresh
-- one. The CAP stays per event, which is what keeps the documented NFC-failure
-- fallback — an admin raising the cap mid-festival — a single PUT covering every
-- remaining day rather than one per day.
ALTER TABLE event_capture_credit_usage
    DROP CONSTRAINT event_capture_credit_usage_pkey;

ALTER TABLE event_capture_credit_usage
    ADD PRIMARY KEY (event_id, user_id, usage_date);

COMMENT ON COLUMN event_capture_credit_usage.usage_date IS 'Campus-local day this pool belongs to. The cap on event_capture_credit_config stays per event.';

-- event_capture_credit_usage_assignment_fk is OUTBOUND, to
-- event_coordinator_assignments (user_id, event_id), so it is unaffected by this
-- table's own primary key changing. Nothing references this table's PK.

-- +goose Down

ALTER TABLE event_capture_credit_usage
    DROP CONSTRAINT event_capture_credit_usage_pkey;

ALTER TABLE event_capture_credit_usage
    ADD PRIMARY KEY (event_id, user_id);

ALTER TABLE event_capture_credit_usage
    DROP COLUMN usage_date;
