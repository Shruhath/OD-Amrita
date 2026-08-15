-- 0023_edit_category_assignments.sql — admin category-access edits.

-- +goose Up

GRANT DELETE ON user_category_assignments TO od_runtime;

-- +goose Down

REVOKE DELETE ON user_category_assignments FROM od_runtime;
