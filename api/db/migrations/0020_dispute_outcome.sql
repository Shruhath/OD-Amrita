-- 0020_dispute_outcome.sql — an APPROVED dispute credits the slot.
--
-- Until now resolving a dispute was an annotation and nothing else: the teacher
-- agreed the student had been missed, wrote a note, and the student still did
-- not appear in od_records or on the public dashboard. Repairing the record was
-- a separate trip to the record-management surface that nothing prompted and
-- nothing tracked, so in practice the student's slot was simply never credited.
--
-- A resolution now carries an OUTCOME, and an approved one writes the record in
-- the same transaction. That makes "rejected" a first-class answer too, which
-- matters more than it looks: without it, wiring resolution to the record would
-- credit every dispute including the ones a teacher resolves BECAUSE the student
-- was not there.

-- +goose Up

ALTER TABLE disputes
    ADD COLUMN outcome text NULL
        CONSTRAINT disputes_outcome_value_ck
            CHECK (outcome IS NULL OR outcome IN ('approved', 'rejected'));

-- Ties the outcome to the status the way disputes_resolved_state_ck ties the
-- note and the resolver: an open dispute has no outcome, a resolved one always
-- does.
--
-- NOT VALID, following 0018's precedent. Any dispute resolved BEFORE this
-- migration was resolved under the annotate-only rule, and there is no honest
-- way to backfill whether the teacher meant to credit the slot — guessing
-- "approved" would fabricate attendance, guessing "rejected" would fabricate a
-- refusal. PostgreSQL still enforces this for every new and every updated row,
-- which is the forward-only guarantee that matters. Resolution is guarded on
-- status = 'open', so a legacy resolved row is never updated and never trips it.
ALTER TABLE disputes
    ADD CONSTRAINT disputes_outcome_state_ck
        CHECK (
            (status = 'open' AND outcome IS NULL)
            OR (status = 'resolved' AND outcome IS NOT NULL)
        ) NOT VALID;

COMMENT ON COLUMN disputes.outcome IS
    'approved credits the disputed slot in od_records; rejected records the refusal. NULL only while open.';

-- A record created by an approved dispute is NOT a search mark, and recording it
-- as one would put a lie in the audit trail of the one table whose whole purpose
-- is to say who marked a student and how. Widening a CHECK cannot invalidate an
-- existing row, so this is additive to every marking path already in use.
ALTER TABLE od_records
    DROP CONSTRAINT od_records_capture_method_check,
    ADD CONSTRAINT od_records_capture_method_check
        CHECK (capture_method IN ('nfc', 'camera', 'search', 'dispute'));

-- +goose Down

-- ⚠️ ONE-WAY DOOR ONCE A DISPUTE HAS BEEN APPROVED.
--
-- Narrowing the CHECK back succeeds only while no od_records row was created by
-- an approved dispute. After the first one, this raises 23514 and goose rolls
-- the whole step back — the transaction aborts cleanly and the database keeps
-- the widened constraint.
--
-- Deliberately not "fixed" by rewriting those rows to 'search' or deleting them.
-- A Down migration that falsifies or destroys attendance records to make itself
-- succeed is far worse than one that refuses. To roll back past this point,
-- reconcile those rows by hand first.
ALTER TABLE od_records
    DROP CONSTRAINT od_records_capture_method_check,
    ADD CONSTRAINT od_records_capture_method_check
        CHECK (capture_method IN ('nfc', 'camera', 'search'));

ALTER TABLE disputes
    DROP CONSTRAINT disputes_outcome_state_ck;

ALTER TABLE disputes
    DROP COLUMN outcome;
