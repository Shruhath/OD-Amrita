-- name: GetCardTokenStudentByID :one
SELECT
    s.id,
    s.roll_no,
    s.name,
    b.code AS branch,
    s.section,
    s.batch
FROM students s
JOIN branches b ON b.id = s.branch_id
WHERE s.id = @student_id;

-- name: UpsertStudentCardToken :one
WITH upsert AS (
    INSERT INTO student_card_tokens (student_id, version, token_sha256)
    VALUES (@student_id, @version, @token_sha256)
    ON CONFLICT (student_id, version) DO UPDATE
    SET token_sha256 = EXCLUDED.token_sha256,
        updated_at = now(),
        -- A CHANGED token means the card in the operator's hand now holds a
        -- stale one, so whatever was verified before is no longer true. Re-
        -- sealing the SAME student under the same key produces the SAME token
        -- (it is an HMAC of the roll number), so the guard below skips the row
        -- entirely and an ordinary re-write does not throw away a verification.
        verified_at = NULL,
        verified_by_user_id = NULL
    WHERE student_card_tokens.token_sha256 IS DISTINCT FROM EXCLUDED.token_sha256
    RETURNING id, (xmax = 0) AS inserted
)
SELECT id, inserted
FROM upsert;

-- name: ResolveStudentCardToken :one
SELECT
    s.id,
    s.roll_no,
    s.name,
    b.code AS branch,
    s.section,
    s.batch
FROM student_card_tokens t
JOIN students s ON s.id = t.student_id
JOIN branches b ON b.id = s.branch_id
WHERE t.version = @version
  AND t.token_sha256 = @token_sha256;

-- name: MarkStudentCardTokenVerified :execrows
-- Records that an ADMIN read this card back and the server recognised it.
--
-- Keyed on the fingerprint that was just resolved, so it can only ever mark the
-- row the read actually matched. Idempotent by nature: verifying twice simply
-- moves the timestamp.
UPDATE student_card_tokens
SET verified_at = now(),
    verified_by_user_id = @verified_by_user_id
WHERE version = @version
  AND token_sha256 = @token_sha256;

-- name: ListSectionCardStatus :many
-- The populator's durable progress for one branch/batch/section.
--
-- LEFT JOIN, not INNER: a student with no token row at all is the majority state
-- when a section is opened for the first time, and they must appear as "not
-- written" rather than vanish from the count.
--
-- Returns no token, no fingerprint and no key material — only whether a card
-- exists for this student and whether it has been read back.
SELECT
    s.id AS student_id,
    (t.id IS NOT NULL)::boolean AS issued,
    t.verified_at
FROM students s
LEFT JOIN student_card_tokens t
    ON t.student_id = s.id AND t.version = @version::text
WHERE s.branch_id = @branch_id
  AND s.batch = @batch::text
  AND s.section = @section::text
ORDER BY s.roll_no;
