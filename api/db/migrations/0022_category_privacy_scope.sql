-- 0022_category_privacy_scope.sql — per-category public visibility and
-- user/category assignments.
--
-- Existing users and categories are preserved. Categories default to visible on
-- the public dashboard; teachers/coordinators start with no category assignment
-- until an admin assigns them. That fail-closed default is intentional: an
-- unassigned staff member must not keep seeing every category.

-- +goose Up

ALTER TABLE categories
    ADD COLUMN visible_on_public_dashboard boolean NOT NULL DEFAULT true;

CREATE TABLE user_category_assignments (
    user_id             uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    category_id         uuid        NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
    created_by_user_id  uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, category_id)
);

CREATE INDEX user_category_assignments_category_id_idx
    ON user_category_assignments (category_id, user_id);

-- +goose Down

DROP TABLE user_category_assignments;

ALTER TABLE categories
    DROP COLUMN visible_on_public_dashboard;
