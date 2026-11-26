-- +goose Up
-- +goose StatementBegin

-- Make the roster search indexable.
--
-- WHY
--   SearchStudents matches with `lower(roll_no || ' ' || name) ~~ ALL (patterns)`,
--   and every pattern is a CONTAINMENT pattern — `%term%`. A btree cannot serve
--   a leading wildcard, so every keystroke of the debounced search planned as a
--   sequential scan of students joined to branches. The query note in
--   db/queries/students.sql already said so, and named this index as the fix if
--   the roster ever grew.
--
--   It did not need to grow. Production measured SearchStudents at 2,332 calls
--   averaging 1.5ms for 3 seconds of database time — the third most expensive
--   query in the system, and roughly fifteen times the median. A scan is cheap
--   per row and this one runs on every character typed by every coordinator
--   searching for a student, which is the fallback path the whole camera flow
--   depends on.
--
-- pg_trgm is the right index for containment. A GIN index over trigrams answers
-- `LIKE '%term%'` directly, which is exactly the shape `~~ ALL (array)` expands
-- to — the planner applies the index per pattern and intersects.
--
-- INSTALL IT IF WE CAN, FAIL READABLY IF WE CANNOT.
--
-- CREATE EXTENSION needs CREATE on the DATABASE, which is a different privilege
-- from owning the schema — and whether od_migrator has it depends on how the
-- database was made:
--
--   * A scratch database in the test harness is created `OWNER od_migrator`, so
--     od_migrator owns it and CAN install a trusted extension. pg_trgm has been
--     trusted since PG13.
--   * The shared local database is owned by od_amrita, and the Neon database by
--     neondb_owner, so od_migrator cannot.
--
-- Attempting it covers the first case, which is what keeps every isolated-database
-- test able to build a complete schema from migrations alone. Catching the
-- privilege error and naming the fix covers the second — because the alternative
-- failure modes are both bad: skipping silently leaves the index absent and the
-- query planning a sequential scan with nobody the wiser, and failing with a raw
-- 42501 tells an operator nothing about what to run.
DO $ensure$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        BEGIN
            CREATE EXTENSION pg_trgm;
        EXCEPTION WHEN insufficient_privilege THEN
            RAISE EXCEPTION
                'pg_trgm is not installed and this role may not install it, so the '
                'roster search index cannot be created. Run db/init/00_roles.sql '
                'against this database as the superuser (local) or the owner role '
                'neondb_owner (Neon), then re-run this migration. '
                'See docs/deployment-runbook.md section 3.';
        END;
    END IF;
END
$ensure$;

-- THE EXPRESSION MUST MATCH THE QUERY'S CHARACTER FOR CHARACTER.
--
-- An expression index is only usable when the planner can prove the indexed
-- expression is the one in the predicate, and that proof is syntactic. A
-- different spelling of the same idea — concat(), a different separator, a cast
-- placed elsewhere — produces an index that is built, maintained, and never
-- used, which is strictly worse than not having one. The line below is copied
-- from the WHERE clause in db/queries/students.sql; keep them in step.
CREATE INDEX IF NOT EXISTS students_search_trgm_idx
    ON students USING gin ((lower(roll_no || ' ' || name)) gin_trgm_ops);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- The index is droppable without consequence: the query it accelerates is
-- correct either way, it simply plans as a sequential scan again.
DROP INDEX IF EXISTS students_search_trgm_idx;

-- The EXTENSION is deliberately NOT dropped. Extensions are database-wide, and
-- a Down that removed pg_trgm would break any other index or query that had
-- come to rely on it since — a far worse failure than leaving an unused
-- extension installed, which costs nothing.

-- +goose StatementEnd
