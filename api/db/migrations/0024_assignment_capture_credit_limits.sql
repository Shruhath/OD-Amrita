-- 0018_assignment_capture_credit_limits.sql — fallback cap belongs to each
-- coordinator assignment.

-- +goose Up

ALTER TABLE event_coordinator_assignments
    ADD COLUMN fallback_credit_limit integer NOT NULL DEFAULT 5 CHECK (fallback_credit_limit >= 0);

UPDATE event_coordinator_assignments eca
SET fallback_credit_limit = COALESCE(cfg.credit_limit, 5)
FROM events e
LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id
WHERE e.id = eca.event_id;

COMMENT ON COLUMN event_coordinator_assignments.fallback_credit_limit IS 'Camera/search fallback credit limit for this coordinator assignment. NFC is unlimited.';

-- +goose Down

ALTER TABLE event_coordinator_assignments
    DROP COLUMN fallback_credit_limit;
