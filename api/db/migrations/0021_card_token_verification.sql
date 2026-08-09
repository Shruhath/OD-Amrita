-- 0021_card_token_verification.sql — remember that a card was read back.
--
-- The populator's per-student progress lived ONLY in the browser's
-- sessionStorage. It did not survive a sign-out (which clears persisted state so
-- a shared phone does not hand the next operator the previous one's screen), and
-- it never survived a different device. An admin who wrote and verified a
-- section, signed out, and came back was shown 0 of 0 verified with every row
-- reading "Not written" — with no way to tell which cards were done except by
-- tapping all of them again.
--
-- A token row already proved a card had been ISSUED. It could not prove the card
-- was WRITTEN, and those are deliberately different states everywhere else in
-- this feature: a write promise only says the phone believed it wrote, while
-- verification is a fresh read the server recognised. Counting issued rows as
-- verified would report a section as done while a student walks to the event
-- with a blank card — the exact failure the two-state design exists to catch.
--
-- So verification becomes a stored fact, set when an ADMIN reads a card back
-- through the populator's resolve endpoint. Marking taps resolve through a
-- different path and deliberately do not write here: "a coordinator's phone read
-- this card during an event" is a different claim from "an admin checked this
-- card before the event", and the populator's counts must mean the second one.

-- +goose Up

ALTER TABLE student_card_tokens
    ADD COLUMN verified_at         timestamptz NULL,
    ADD COLUMN verified_by_user_id uuid        NULL REFERENCES users(id) ON DELETE SET NULL;

-- Both move together or neither does, so a row can never claim it was verified
-- by nobody, or name a verifier without a time.
ALTER TABLE student_card_tokens
    ADD CONSTRAINT student_card_tokens_verified_state_ck
        CHECK (
            (verified_at IS NULL AND verified_by_user_id IS NULL)
            OR (verified_at IS NOT NULL AND verified_by_user_id IS NOT NULL)
        );

-- The populator's counts read verified rows for one section at a time; the join
-- to students is on student_id, which is already indexed.
CREATE INDEX student_card_tokens_verified_at_idx
    ON student_card_tokens (verified_at)
    WHERE verified_at IS NOT NULL;

COMMENT ON COLUMN student_card_tokens.verified_at IS
    'When an admin last read this card back through the populator and the server recognised it. NULL means issued but never verified — which is NOT the same as written.';

-- +goose Down

DROP INDEX student_card_tokens_verified_at_idx;

ALTER TABLE student_card_tokens
    DROP CONSTRAINT student_card_tokens_verified_state_ck;

-- Reversible without loss of anything the pre-0021 schema could hold: these two
-- columns are the only place verification was ever recorded, so dropping them
-- returns the table to exactly its previous meaning. It DOES discard the
-- verification history, which is why the note above says the populator falls
-- back to per-device progress after a rollback.
ALTER TABLE student_card_tokens
    DROP COLUMN verified_by_user_id,
    DROP COLUMN verified_at;
