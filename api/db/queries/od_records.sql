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
