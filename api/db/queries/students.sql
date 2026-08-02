-- name: GetBranchByCode :one
SELECT id, code
FROM branches
WHERE lower(code) = lower(@code::text)
ORDER BY id ASC
LIMIT 1;

-- name: UpsertStudent :one
WITH upsert AS (
    INSERT INTO students (roll_no, name, branch_id, section, batch)
    VALUES (@roll_no, @name, @branch_id, @section, @batch)
    ON CONFLICT (roll_no) DO UPDATE
    SET name = EXCLUDED.name,
        branch_id = EXCLUDED.branch_id,
        section = EXCLUDED.section,
        batch = EXCLUDED.batch,
        updated_at = now()
    WHERE students.name IS DISTINCT FROM EXCLUDED.name
       OR students.branch_id IS DISTINCT FROM EXCLUDED.branch_id
       OR students.section IS DISTINCT FROM EXCLUDED.section
       OR students.batch IS DISTINCT FROM EXCLUDED.batch
    RETURNING id, (xmax = 0) AS inserted
)
SELECT id, inserted
FROM upsert;

-- name: InsertStudentRosterImport :one
INSERT INTO student_roster_imports (
    source_filename,
    total_rows,
    inserted_rows,
    updated_rows,
    unchanged_rows,
    invalid_rows,
    database_count,
    actor_user_id,
    actor_email
) VALUES (
    @source_filename,
    @total_rows,
    @inserted_rows,
    @updated_rows,
    @unchanged_rows,
    @invalid_rows,
    @database_count,
    @actor_user_id,
    @actor_email
)
RETURNING id;

-- name: CountStudents :one
SELECT count(*)::integer
FROM students;

-- name: ListStudentsForCardPopulation :many
-- Read-only roster page behind the admin card populator. Deliberately selects
-- only display columns: no email lives on students, and card token material is
-- confined to student_card_tokens, which this query never joins.
-- Empty-string filters mean "unfiltered", matching the ListEvents sentinel
-- style; section/batch can never be blank thanks to the roster CHECK
-- constraints, so a blank value is unambiguous.
SELECT
    s.id,
    s.roll_no,
    s.name,
    b.code AS branch,
    s.section,
    s.batch
FROM students s
JOIN branches b ON b.id = s.branch_id
WHERE (sqlc.narg('branch_id')::uuid IS NULL OR s.branch_id = sqlc.narg('branch_id')::uuid)
  AND (@batch::text = '' OR s.batch = @batch::text)
  AND (@section::text = '' OR s.section = @section::text)
ORDER BY b.code ASC, s.batch ASC, s.section ASC, s.roll_no ASC, s.id ASC
LIMIT @page_size::bigint OFFSET @page_offset::bigint;

-- name: CountStudentsForCardPopulation :one
SELECT count(*)
FROM students s
WHERE (sqlc.narg('branch_id')::uuid IS NULL OR s.branch_id = sqlc.narg('branch_id')::uuid)
  AND (@batch::text = '' OR s.batch = @batch::text)
  AND (@section::text = '' OR s.section = @section::text);
