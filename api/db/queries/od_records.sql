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

-- name: AdminCanSearchMark :one
SELECT EXISTS (
    SELECT 1
    FROM events e
    JOIN categories c ON c.event_id = e.id AND c.id = @category_id
    WHERE e.id = @event_id
      AND e.status = 'active'
)::boolean;

-- name: TeacherCanSearchMark :one
SELECT EXISTS (
    SELECT 1
    FROM students s
    JOIN user_branches ub ON ub.branch_id = s.branch_id AND ub.user_id = @user_id
    JOIN event_branches eb ON eb.branch_id = s.branch_id AND eb.event_id = @event_id
    JOIN events e ON e.id = eb.event_id AND e.status = 'active'
    JOIN categories c ON c.id = @category_id AND c.event_id = e.id
    WHERE s.id = @student_id
)::boolean;

-- name: ListAttendanceRecords :many
SELECT
    r.id,
    r.student_id,
    s.roll_no,
    s.name AS student_name,
    b.code AS branch,
    s.section,
    s.batch,
    r.event_id,
    c.id AS category_id,
    c.name AS category_name,
    c.created_at AS category_created_at,
    r.slot,
    r.capture_method,
    r.actor_user_id,
    u.email AS actor_email,
    u.name AS actor_name,
    r.actor_role,
    r.server_received_at
FROM od_records r
JOIN students s ON s.id = r.student_id
JOIN branches b ON b.id = s.branch_id
JOIN categories c ON c.id = r.category_id
LEFT JOIN users u ON u.id = r.actor_user_id
WHERE r.event_id = @event_id
  AND r.status = 'present'
  AND (sqlc.narg('category_id')::uuid IS NULL OR r.category_id = sqlc.narg('category_id'))
  AND (sqlc.narg('slot')::integer IS NULL OR r.slot = sqlc.narg('slot'))
ORDER BY r.slot ASC, r.server_received_at DESC, s.roll_no ASC, r.id ASC
LIMIT @page_size::bigint OFFSET @page_offset::bigint;

-- name: CountAttendanceRecords :one
SELECT count(*)::bigint
FROM od_records r
WHERE r.event_id = @event_id
  AND r.status = 'present'
  AND (sqlc.narg('category_id')::uuid IS NULL OR r.category_id = sqlc.narg('category_id'))
  AND (sqlc.narg('slot')::integer IS NULL OR r.slot = sqlc.narg('slot'));
