-- 0027_staff_access_removal.sql — let an administrator take staff access away:
-- remove one coordinator from one event, or demote a staff user to student.
--
-- HARD DELETE, NOT A SOFT-REVOKE COLUMN. These rows are access-control switches,
-- the same kind of row 0023 already made deletable on user_category_assignments:
--
--   event_coordinator_assignments  "this user coordinates this event"
--   user_branches                  "this teacher sees this branch"
--
-- Nothing an OD record points at lives here: od_records reference users (with
-- ON DELETE SET NULL), never an assignment, so removing an assignment erases no
-- attendance. A soft-revoke column would instead have to be honoured by every
-- one of the dozen scope queries that read these tables today — and the one that
-- forgot it would keep granting access, silently. Deleting the row makes "no
-- access" the default every existing query already computes.
--
-- THE FALLBACK-CREDIT COUNTERS FOLLOW THEIR ASSIGNMENT BY CASCADE, NOT BY GRANT.
-- event_capture_credit_usage rows belong to one assignment (the composite FK) and
-- were RESTRICT, which would make removing any coordinator who ever used a
-- fallback credit impossible. Rather than give od_runtime DELETE on the counters
-- — which would let any bug wipe them and hand a coordinator a fresh pool — the
-- FK becomes ON DELETE CASCADE: the counters can now disappear ONLY as part of
-- removing the assignment they count for. od_runtime still holds no DELETE on
-- event_capture_credit_usage.
--
-- The history is not lost: each removal co-commits a coordinator.removed or
-- user.demoted audit row (store.WithAudit) carrying a snapshot of what went,
-- credits included.
--
-- Archived events stay immutable. The require_active_event_for_child_mutation()
-- trigger on event_coordinator_assignments still fires on DELETE, so an archived
-- event's coordinator rows cannot be removed by the application — the same rule
-- assignment itself follows — and so their counters cannot cascade away either.

-- +goose Up

GRANT DELETE ON event_coordinator_assignments TO od_runtime;
GRANT DELETE ON user_branches TO od_runtime;

ALTER TABLE event_capture_credit_usage
    DROP CONSTRAINT event_capture_credit_usage_assignment_fk;
ALTER TABLE event_capture_credit_usage
    ADD CONSTRAINT event_capture_credit_usage_assignment_fk
        FOREIGN KEY (user_id, event_id)
        REFERENCES event_coordinator_assignments(user_id, event_id)
        ON DELETE CASCADE;

-- +goose Down

ALTER TABLE event_capture_credit_usage
    DROP CONSTRAINT event_capture_credit_usage_assignment_fk;
ALTER TABLE event_capture_credit_usage
    ADD CONSTRAINT event_capture_credit_usage_assignment_fk
        FOREIGN KEY (user_id, event_id)
        REFERENCES event_coordinator_assignments(user_id, event_id)
        ON DELETE RESTRICT;

REVOKE DELETE ON user_branches FROM od_runtime;
REVOKE DELETE ON event_coordinator_assignments FROM od_runtime;
