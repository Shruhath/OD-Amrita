-- name: GetMarkEvent :one
SELECT id, event_date, status
FROM events
WHERE id = @event_id;

-- name: GetMarkCategoryForEvent :one
SELECT id, event_id
FROM categories
WHERE id = @category_id AND event_id = @event_id;

-- name: GetMarkStudentByID :one
SELECT
    s.id,
    s.roll_no,
    s.name,
    b.code AS branch,
    s.section,
    s.batch
FROM students s
JOIN branches b ON b.id = s.branch_id
WHERE s.id = @student_id;

-- name: InsertODPresentRecord :one
INSERT INTO od_records (
    student_id,
    event_id,
    category_id,
    slot,
    capture_method,
    actor_user_id,
    actor_role,
    server_received_at
) VALUES (
    @student_id,
    @event_id,
    @category_id,
    @slot,
    @capture_method,
    @actor_user_id,
    @actor_role,
    @server_received_at
)
RETURNING id, student_id, event_id, category_id, slot, capture_method, server_received_at;

-- name: CountMarksForCategory :one
-- The two FR13 counters in one round trip: every coordinator's marks for this
-- event+category, and the calling coordinator's own.
--
-- Deliberately a live COUNT(*) rather than a tally table. AR10's tally is
-- deferred: one event at roughly 20 writes/second over a few hours does not need
-- a denormalised counter, and a tally row is a second source of truth that can
-- drift from od_records — the very thing the audit trail exists to prevent.
-- od_records_event_category_slot_idx covers the scope predicate.
SELECT
    count(*)::bigint AS category_total,
    (count(*) FILTER (WHERE actor_user_id = @actor_user_id))::bigint AS mine
FROM od_records
WHERE event_id = @event_id
  AND category_id = @category_id
  AND status = 'present';
