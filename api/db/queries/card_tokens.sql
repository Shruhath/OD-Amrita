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
        updated_at = now()
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
