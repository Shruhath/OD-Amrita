-- 0025_od_records_manual_reason_note_ck.sql — admit live admin marks without a reason note.

-- +goose Up

-- Widen the manual reason note check:
-- A reason note is required only for manual search entries, not for live capture
-- methods (NFC, camera) or coordinator marks.
ALTER TABLE od_records
    DROP CONSTRAINT od_records_manual_reason_note_ck;

ALTER TABLE od_records
    ADD CONSTRAINT od_records_manual_reason_note_ck
        CHECK (capture_method IN ('nfc', 'camera') OR actor_role = 'coordinator' OR reason_note IS NOT NULL)
        NOT VALID;

-- +goose Down

ALTER TABLE od_records
    DROP CONSTRAINT od_records_manual_reason_note_ck;

ALTER TABLE od_records
    ADD CONSTRAINT od_records_manual_reason_note_ck
        CHECK (actor_role = 'coordinator' OR reason_note IS NOT NULL)
        NOT VALID;
