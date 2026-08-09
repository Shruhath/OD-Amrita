-- +goose Up
-- +goose StatementBegin
CREATE INDEX disputes_status_created_at_idx ON disputes (status, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS disputes_status_created_at_idx;
-- +goose StatementEnd
