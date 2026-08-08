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

-- COLLAPSE the per-day rows before restoring the two-column key.
--
-- Without this the ADD PRIMARY KEY below raises 23505 on the first coordinator
-- who spent credits on two days of one event — which is precisely what the Up
-- half exists to permit, so the Down would be unusable exactly when it was
-- needed. Worse, `goose reset` walks every Down, so an un-collapsible 0017 also
-- breaks TestMigrationsRoundTrip_Idempotent against any database where the
-- feature has been exercised.
--
-- This destroys no information the migration was not already destroying:
-- `usage_date` is dropped three statements later, so the per-day split cannot
-- survive in any case. Summing `consumed` and keeping the latest `updated_at`
-- reconstructs exactly what the pre-0017 row meant — one coordinator's total
-- usage for one event — so Down(Up(x)) really does return x.
--
-- Contrast 0016, whose Down deliberately does NOT collapse: there the colliding
-- rows are attendance records, and deleting a student's OD to make a rollback
-- succeed would be far worse than refusing.
WITH collapsed AS (
    SELECT event_id, user_id, sum(consumed) AS consumed, max(updated_at) AS updated_at
    FROM event_capture_credit_usage
    GROUP BY event_id, user_id
    HAVING count(*) > 1
), removed AS (
    DELETE FROM event_capture_credit_usage u
    USING collapsed c
    WHERE u.event_id = c.event_id AND u.user_id = c.user_id
    RETURNING u.event_id
)
INSERT INTO event_capture_credit_usage (event_id, user_id, consumed, updated_at, usage_date)
SELECT c.event_id, c.user_id, c.consumed, c.updated_at, c.updated_at::date
FROM collapsed c
WHERE EXISTS (SELECT 1 FROM removed);

ALTER TABLE event_capture_credit_usage
    ADD PRIMARY KEY (event_id, user_id);

ALTER TABLE event_capture_credit_usage
    DROP COLUMN usage_date;
