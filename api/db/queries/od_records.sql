-- name: GetMarkEvent :one
-- event_date is the FIRST day of the range and end_date the last; the slot gate
-- is range containment, not a date equality.
SELECT id, event_date, end_date, status
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
-- od_date is the day the OD BELONGS to and is always derived server-side; it is
-- never the same fact as server_received_at, which is when the row arrived.
INSERT INTO od_records (
    student_id,
    event_id,
    category_id,
    slot,
    od_date,
    capture_method,
    actor_user_id,
    actor_role,
    server_received_at
) VALUES (
    @student_id,
    @event_id,
    @category_id,
    @slot,
    @od_date,
    @capture_method,
    @actor_user_id,
    @actor_role,
    @server_received_at
)
RETURNING id, student_id, event_id, category_id, slot, od_date, capture_method, server_received_at;

-- name: CountMarksForCategory :one
-- The two FR13 counters in one round trip: every coordinator's marks for this
-- event+category, and the calling coordinator's own.
--
-- Deliberately a live COUNT(*) rather than a tally table. AR10's tally is
-- deferred: one event at roughly 20 writes/second over a few hours does not need
-- a denormalised counter, and a tally row is a second source of truth that can
-- drift from od_records — the very thing the audit trail exists to prevent.
-- od_records_event_category_slot_idx covers the scope predicate.
--
-- SCOPED TO ONE DAY. Both figures are read by a coordinator as "today", so on a
-- thirty-day festival an unscoped count would show a running cumulative total
-- under a label that says otherwise.
SELECT
    count(*)::bigint AS category_total,
    (count(*) FILTER (WHERE actor_user_id = @actor_user_id))::bigint AS mine
FROM od_records
WHERE event_id = @event_id
  AND category_id = @category_id
  AND od_date = @od_date
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
    r.od_date,
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
  AND (sqlc.narg('od_date')::date IS NULL OR r.od_date = sqlc.narg('od_date'))
ORDER BY r.od_date DESC, r.slot ASC, r.server_received_at DESC, s.roll_no ASC, r.id ASC
LIMIT @page_size::bigint OFFSET @page_offset::bigint;

-- name: CountAttendanceRecords :one
SELECT count(*)::bigint
FROM od_records r
WHERE r.event_id = @event_id
  AND r.status = 'present'
  AND (sqlc.narg('category_id')::uuid IS NULL OR r.category_id = sqlc.narg('category_id'))
  AND (sqlc.narg('slot')::integer IS NULL OR r.slot = sqlc.narg('slot'))
  AND (sqlc.narg('od_date')::date IS NULL OR r.od_date = sqlc.narg('od_date'));

-- name: ListPublicStudentODRecords :many
-- One student's present OD records for the UNAUTHENTICATED public dashboard
-- (Story 0.10, FR39).
--
-- GROUPING KEY IS od_records.od_date, NOT server_received_at and no longer
-- events.event_date. An event spans a range of days, so the event's first day
-- would collapse a thirty-day festival onto one heading; and deriving the day
-- from the receipt time would file a teacher's next-morning correction under the
-- wrong day, which on this screen reads to the student as "I was not marked".
-- od_date is the day the OD belongs to, which is the only one of the three that
-- is the fact this screen is about.
--
-- e.event_date is STILL selected alongside it: the record's event_date is the
-- event's first day, and the dispute form's event options are built from it.
--
-- NO events.status FILTER, deliberately (T6). Archiving is an administrative act
-- on the event, not a retraction of the student's attendance; filtering active
-- events here would erase a student's whole record the moment a coordinator
-- tidied up after the event — exactly when they are most likely to check it.
--
-- LEFT JOIN users because actor_user_id is ON DELETE SET NULL. An inner join
-- would silently delete a student's attendance record as a side effect of
-- off-boarding a teacher. actor_role is read from the RECORD, not the user row,
-- so the role survives the actor.
--
-- Selects display columns only: no users.email, no actor id, and
-- student_card_tokens is never joined. This result is serialised to the open
-- internet.
--
-- od_records_student_event_slot_idx leads with student_id, which is this
-- query's only equality predicate — no new index is needed.
--
-- row_limit is a server-chosen ceiling rather than a page parameter: pagination
-- cannot be reconciled with day grouping without splitting a day across pages,
-- and 12 slots a day makes any realistic history far smaller than the cap.
SELECT
    r.id,
    r.slot,
    r.event_id,
    e.name       AS event_name,
    e.event_date AS event_date,
    r.od_date    AS od_date,
    c.id         AS category_id,
    c.name       AS category_name,
    u.name       AS marked_by_name,
    r.actor_role AS marked_by_role,
    r.server_received_at
FROM od_records r
JOIN events e ON e.id = r.event_id
JOIN categories c ON c.id = r.category_id
LEFT JOIN users u ON u.id = r.actor_user_id
WHERE r.student_id = @student_id
  AND r.status = 'present'
ORDER BY r.od_date DESC, r.slot ASC, r.server_received_at ASC, r.id ASC
LIMIT @row_limit::bigint;
