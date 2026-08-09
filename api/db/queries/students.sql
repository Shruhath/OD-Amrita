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

-- name: SearchStudents :many
-- Authenticated roster search behind the coordinator marking surface (FR55) and
-- the resolution step for the camera path. Also the public dashboard's lookup,
-- through the same service.
--
-- EVERY TERM, ANYWHERE, IN ANY ORDER. This was prefix-only, and prefix-only is
-- wrong for the thing people actually type: a roster holds full names, and
-- somebody looking for "Shruhat Reddy" is as likely to type the surname, or the
-- two words the other way round, or the tail of a roll number. A prefix pattern
-- answers all three with "no such student", which reads as the roster being
-- incomplete rather than as the search being narrow.
--
-- The haystack is `roll_no || ' ' || name` so one query covers both columns, and
-- EVERY term must appear in it — terms narrow, they never widen, so a longer
-- query can only ever return fewer rows. The service lowercases, escapes `%` and
-- `_`, and splits on whitespace before building the array, so a coordinator
-- typing `%` matches a literal `%` rather than dumping the roster.
--
-- `~~` is LIKE's operator spelling, needed for the `ALL (array)` form; its
-- default escape character is the backslash the service already escapes with.
-- The array is NEVER empty — `x ~~ ALL (ARRAY[]::text[])` is vacuously true and
-- would return the whole roster, so buildSearchQuery refuses to produce one.
--
-- Index note: containment matching cannot use a btree, so this plans as a
-- sequential scan — as the prefix form already did under a non-C collation. At
-- the roster's real size (~1.5k rows) that is sub-millisecond. If the roster
-- ever grows past tens of thousands, the fix is a pg_trgm GIN index on the same
-- expression, not a narrower query.
--
-- Selects display columns only: no email lives on students, and card token
-- material is confined to student_card_tokens, which this query never joins.
SELECT
    s.id,
    s.roll_no,
    s.name,
    b.code AS branch,
    s.section,
    s.batch
FROM students s
JOIN branches b ON b.id = s.branch_id
WHERE lower(s.roll_no || ' ' || s.name) ~~ ALL (@patterns::text[])
ORDER BY
    (s.roll_no = @exact_roll::text) DESC,
    -- Then whoever the query STARTS a word of, so typing a first name still
    -- puts that person above someone who merely contains the letters mid-word.
    -- The leading space is what makes a match at position one count as a word
    -- start too.
    ((' ' || lower(s.roll_no || ' ' || s.name)) LIKE @head_pattern::text ESCAPE '\') DESC,
    s.roll_no ASC,
    lower(s.name) ASC,
    s.id ASC
LIMIT @result_limit::bigint;

-- name: GetStudentByRollNo :one
SELECT
    s.id,
    s.roll_no,
    s.name,
    b.code AS branch,
    s.section,
    s.batch
FROM students s
JOIN branches b ON b.id = s.branch_id
WHERE s.roll_no = @roll_no
LIMIT 1;
