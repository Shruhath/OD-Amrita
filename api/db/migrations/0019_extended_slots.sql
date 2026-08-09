-- 0019_extended_slots.sql — room for slots 13..20, and the switch that admits them.
--
-- TWO SEPARATE THINGS, and the split is the whole design.
--
-- The CHECK constraints are WIDENED PERMANENTLY to 20. A CHECK cannot be
-- toggled at runtime — changing one is DDL, needs the migrator role, and takes
-- a lock — so it cannot be what an administrator flips from a settings screen.
-- Widening it is also the safe direction: every row that was legal before is
-- still legal, and code that only ever writes 1..12 keeps working unchanged.
-- That is what lets this migration be applied BEFORE the code that uses it,
-- which is the ordering the deployment runbook requires.
--
-- What the toggle actually controls is the APPLICATION's ceiling, held in
-- app_settings and read on the marking path. The database bound is the outer
-- wall; the setting is the door in it.
--
-- WHY THIS EXISTS AT ALL: slot 12 ends at 18:15, so after quarter past six no
-- slot is open and a coordinator cannot mark anybody. That makes an evening
-- rehearsal of the real marking flow impossible. Slots 13..20 are evening slots
-- for exactly that, defaulted OFF so production behaves as it always has.

-- +goose Up

ALTER TABLE od_records
    DROP CONSTRAINT od_records_slot_check,
    ADD CONSTRAINT od_records_slot_check CHECK (slot BETWEEN 1 AND 20);

ALTER TABLE disputes
    DROP CONSTRAINT disputes_slot_check,
    ADD CONSTRAINT disputes_slot_check CHECK (slot BETWEEN 1 AND 20);

-- One row per setting, not one column per setting: a boolean column added for
-- every future switch is a migration each time, and this table is read by one
-- service that already treats the value as opaque.
--
-- `updated_by` is nullable and ON DELETE SET NULL for the same reason
-- od_records.actor_user_id is: off-boarding a staff member must not delete the
-- record of what they changed.
CREATE TABLE app_settings (
    key         text        PRIMARY KEY,
    value       text        NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    updated_by  uuid        NULL REFERENCES users(id) ON DELETE SET NULL
);

COMMENT ON TABLE app_settings IS
    'Runtime switches an administrator may flip without a deploy. Read by the application; never by a trigger.';

-- Seeded OFF. The default has to be the behaviour production already has:
-- a migration that silently opened eight new slots on the eve of an event would
-- be a change nobody asked for arriving without a deploy.
INSERT INTO app_settings (key, value) VALUES ('extended_slots_enabled', 'false')
ON CONFLICT (key) DO NOTHING;

-- The runtime role reads and writes settings; it may not change their shape.
GRANT SELECT, INSERT, UPDATE ON app_settings TO od_runtime;

-- +goose Down

DROP TABLE app_settings;

-- Narrowing the CHECK back to 12 will FAIL if any row above 12 exists, and that
-- is correct: those are real attendance records, and silently deleting or
-- rewriting them to satisfy a constraint would destroy history. PostgreSQL
-- aborts the migration atomically, so the database stays at the widened state
-- until somebody decides what should happen to those rows.
ALTER TABLE disputes
    DROP CONSTRAINT disputes_slot_check,
    ADD CONSTRAINT disputes_slot_check CHECK (slot BETWEEN 1 AND 12);

ALTER TABLE od_records
    DROP CONSTRAINT od_records_slot_check,
    ADD CONSTRAINT od_records_slot_check CHECK (slot BETWEEN 1 AND 12);
