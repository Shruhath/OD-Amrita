-- 0008_teacher_branches.sql — teacher-to-branch associations (Story 2.3).
--
-- Same shape as event_branches (0005): a plain association table with a
-- composite primary key and no updated_at (the pair is either present or
-- not; there is nothing to update). Unlike event_branches/categories, this
-- table is created AFTER 0007_runtime_privilege_defaults.sql flipped the
-- standing ALTER DEFAULT PRIVILEGES rule to SELECT/INSERT/UPDATE (no DELETE)
-- for od_migrator-created tables — exactly the DML posture a no-hard-delete
-- association needs, so this migration writes zero GRANT/REVOKE statements.
-- Branches and users have no active/archived lifecycle, so no
-- require_active_event_for_child_mutation()-style guard applies here either.

-- +goose Up

CREATE TABLE user_branches (
    user_id    uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    branch_id  uuid        NOT NULL REFERENCES branches(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, branch_id)
);

-- The composite PK already serves user_id-first lookups; this serves the
-- branch_id-first direction, mirroring event_branches_branch_id_idx.
CREATE INDEX user_branches_branch_id_idx
    ON user_branches (branch_id, user_id);

-- +goose Down

DROP TABLE user_branches;
