-- 0026_live_capture_needs_no_reason.sql — a live capture is not a manual entry.

-- +goose Up

-- WHY THIS EXISTS.
--
-- 0018 added od_records_manual_reason_note_ck as
--
--     CHECK (actor_role = 'coordinator' OR reason_note IS NOT NULL)
--
-- and at the time that was an exact statement of the rule: a coordinator marked
-- live and nobody else marked at all except through the manual search form,
-- which always carries a typed reason. "Not a coordinator" and "typed it by
-- hand" were the same set, so either could stand for the other.
--
-- They stopped being the same set when admins were given the live marking
-- screen. An admin tapping a card captures exactly what a coordinator tapping a
-- card captures — the card is the evidence, and there is nothing for a reason
-- note to say that the capture method does not already say. The service agrees:
-- reason_note is required only for a manual search mark, so an admin's nfc or
-- camera mark arrives here NULL by design.
--
-- The constraint then rejected it, and because a check violation on this name
-- was not mapped anywhere the caller saw a bare 500. Production had never
-- accepted a single admin nfc or camera record; every attempt since the admin
-- marking screen shipped failed this way.
--
-- So the predicate is corrected to say what was always meant: a reason is
-- required for a MANUAL entry, and a live capture is not one. The rule for
-- capture_method = 'search' and 'dispute' is unchanged — a teacher or admin
-- writing a record without the card in front of them still has to say why.
ALTER TABLE od_records
    DROP CONSTRAINT od_records_manual_reason_note_ck;

-- Still NOT VALID, for 0018's reason and no other: historic rows predate the
-- requirement and inventing a reason for already-recorded attendance would be a
-- lie. PostgreSQL enforces it on every insert and every update regardless, which
-- is the forward-only guarantee that was wanted then and is wanted now.
ALTER TABLE od_records
    ADD CONSTRAINT od_records_manual_reason_note_ck
        CHECK (
            capture_method IN ('nfc', 'camera')
            OR actor_role = 'coordinator'
            OR reason_note IS NOT NULL
        ) NOT VALID;

COMMENT ON CONSTRAINT od_records_manual_reason_note_ck ON od_records IS
    'A manually entered record must say why. A live capture (nfc/camera) need not: the capture method is the evidence.';

-- +goose Down

-- Narrowing back can only fail if a row exists that the old predicate rejects —
-- an admin nfc or camera record — and that is precisely the row this migration
-- exists to allow. PostgreSQL will not re-check it (NOT VALID), so the Down is
-- clean, but the rows stay and the next VALIDATE CONSTRAINT would refuse them.
ALTER TABLE od_records
    DROP CONSTRAINT od_records_manual_reason_note_ck;

ALTER TABLE od_records
    ADD CONSTRAINT od_records_manual_reason_note_ck
        CHECK (actor_role = 'coordinator' OR reason_note IS NOT NULL) NOT VALID;
