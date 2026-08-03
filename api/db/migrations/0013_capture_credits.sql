-- 0013_capture_credits.sql — event-wide fallback credit cap and usage.

-- +goose Up

CREATE TABLE event_capture_credit_config (
    event_id           uuid        PRIMARY KEY REFERENCES events(id) ON DELETE RESTRICT,
    credit_limit       integer     NOT NULL DEFAULT 5 CHECK (credit_limit >= 0),
    updated_by_user_id uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE event_capture_credit_usage (
    event_id   uuid        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    user_id    uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    consumed   integer     NOT NULL DEFAULT 0 CHECK (consumed >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, user_id),
    CONSTRAINT event_capture_credit_usage_assignment_fk
        FOREIGN KEY (user_id, event_id)
        REFERENCES event_coordinator_assignments(user_id, event_id)
        ON DELETE RESTRICT
);

CREATE TRIGGER event_capture_credit_config_active_event_guard
BEFORE INSERT OR UPDATE OR DELETE ON event_capture_credit_config
FOR EACH ROW EXECUTE FUNCTION require_active_event_for_child_mutation('event_id');

CREATE TRIGGER event_capture_credit_usage_active_event_guard
BEFORE INSERT OR UPDATE OR DELETE ON event_capture_credit_usage
FOR EACH ROW EXECUTE FUNCTION require_active_event_for_child_mutation('event_id');

-- +goose Down

DROP TABLE event_capture_credit_usage;
DROP TABLE event_capture_credit_config;
