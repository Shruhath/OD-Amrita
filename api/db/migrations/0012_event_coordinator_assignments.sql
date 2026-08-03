-- 0012_event_coordinator_assignments.sql — event-level coordinator assignment.

-- +goose Up

CREATE TABLE event_coordinator_assignments (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    event_id           uuid        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    created_by_user_id uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT event_coordinator_assignments_user_event_unique UNIQUE (user_id, event_id)
);

CREATE INDEX event_coordinator_assignments_event_created_idx
    ON event_coordinator_assignments (event_id, created_at ASC, id ASC);

CREATE TRIGGER event_coordinator_assignments_active_event_guard
BEFORE INSERT OR UPDATE OR DELETE ON event_coordinator_assignments
FOR EACH ROW EXECUTE FUNCTION require_active_event_for_child_mutation('event_id');

GRANT SELECT, INSERT, UPDATE ON event_coordinator_assignments TO od_runtime;
REVOKE DELETE ON event_coordinator_assignments FROM od_runtime;

-- +goose Down

DROP TABLE event_coordinator_assignments;
