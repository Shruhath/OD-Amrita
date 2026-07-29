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
