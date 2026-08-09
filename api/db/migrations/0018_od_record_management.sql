-- 0018_od_record_management.sql — teacher/admin record corrections and removals.

-- +goose Up

-- Existing coordinator marks intentionally remain NULL here. A later CHECK is
-- NOT VALID so it governs every new manual write without inventing a reason for
-- already-recorded attendance.
ALTER TABLE od_records
    ADD COLUMN reason_note text NULL
        CONSTRAINT od_records_reason_note_length_ck
            CHECK (reason_note IS NULL OR length(reason_note) BETWEEN 1 AND 2000);

ALTER TABLE od_records
    DROP CONSTRAINT od_records_status_check,
    ADD CONSTRAINT od_records_status_check CHECK (status IN ('present', 'removed'));

-- This is deliberately NOT VALID: historic teacher/admin records were created
-- before a reason was required. PostgreSQL still enforces it for every new row
-- and every updated row, which is the required forward-only guarantee.
ALTER TABLE od_records
    ADD CONSTRAINT od_records_manual_reason_note_ck
        CHECK (actor_role = 'coordinator' OR reason_note IS NOT NULL) NOT VALID;

-- +goose Down

ALTER TABLE od_records
    DROP CONSTRAINT od_records_manual_reason_note_ck;

-- One-way once a record has been removed. Restoring the old CHECK must reject
-- removed rows; silently converting them to present or deleting them would
-- destroy attendance history. PostgreSQL aborts this migration atomically, so
-- the database remains at the widened state until the rows are reconciled.
ALTER TABLE od_records
    DROP CONSTRAINT od_records_status_check,
    ADD CONSTRAINT od_records_status_check CHECK (status = 'present');

ALTER TABLE od_records
    DROP COLUMN reason_note;
