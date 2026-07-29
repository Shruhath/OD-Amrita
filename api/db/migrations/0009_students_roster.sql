-- 0009_students_roster.sql — one-event roster import tables.
--
-- These tables are created after 0007 changed default table privileges, so
-- od_runtime inherits SELECT/INSERT/UPDATE and no DELETE. That is the intended
-- no-hard-delete posture: later attendance, dashboard, and correction flows
-- need stable student/import references.

-- +goose Up

CREATE TABLE students (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    roll_no    text        NOT NULL UNIQUE,
    name       text        NOT NULL,
    branch_id  uuid        NOT NULL REFERENCES branches(id) ON DELETE RESTRICT,
    section    text        NOT NULL,
    batch      text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT students_roll_no_trimmed_nonblank_chk CHECK (roll_no = btrim(roll_no) AND roll_no <> '' AND length(roll_no) <= 64),
    CONSTRAINT students_name_trimmed_nonblank_chk CHECK (name = btrim(name) AND name <> '' AND length(name) <= 200),
    CONSTRAINT students_section_trimmed_nonblank_chk CHECK (section = btrim(section) AND section <> '' AND length(section) <= 32),
    CONSTRAINT students_batch_trimmed_nonblank_chk CHECK (batch = btrim(batch) AND batch <> '' AND length(batch) <= 32)
);

CREATE INDEX students_roll_no_idx
    ON students (roll_no);

CREATE INDEX students_name_lower_idx
    ON students (lower(name), id);

CREATE INDEX students_branch_id_idx
    ON students (branch_id, roll_no);

CREATE TABLE student_roster_imports (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    source_filename text        NOT NULL,
    total_rows      integer     NOT NULL,
    inserted_rows   integer     NOT NULL,
    updated_rows    integer     NOT NULL,
    unchanged_rows  integer     NOT NULL,
    invalid_rows    integer     NOT NULL,
    database_count  integer     NOT NULL,
    actor_user_id   uuid        NULL REFERENCES users(id) ON DELETE SET NULL,
    actor_email     text        NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT student_roster_imports_source_filename_chk CHECK (source_filename = btrim(source_filename) AND source_filename <> '' AND length(source_filename) <= 255),
    CONSTRAINT student_roster_imports_counts_nonnegative_chk CHECK (
        total_rows >= 0
        AND inserted_rows >= 0
        AND updated_rows >= 0
        AND unchanged_rows >= 0
        AND invalid_rows >= 0
        AND database_count >= 0
    )
);

CREATE INDEX student_roster_imports_created_at_idx
    ON student_roster_imports (created_at DESC, id DESC);

-- +goose Down

DROP TABLE student_roster_imports;
DROP TABLE students;
