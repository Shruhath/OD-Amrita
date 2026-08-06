-- name: InsertDispute :one
INSERT INTO disputes (
    student_id,
    event_id,
    category_id,
    od_date,
    slot,
    submitted_email,
    claimed_coordinator_name,
    student_note
) VALUES (
    @student_id,
    @event_id,
    @category_id,
    @od_date,
    @slot,
    @submitted_email,
    sqlc.narg('claimed_coordinator_name'),
    sqlc.narg('student_note')
)
RETURNING id, created_at;

-- name: GetOpenDisputeForSlot :one
SELECT id, created_at
FROM disputes
WHERE student_id = @student_id
  AND event_id = @event_id
  AND od_date = @od_date
  AND slot = @slot
  AND status = 'open'
ORDER BY created_at ASC, id ASC
LIMIT 1;

-- name: ListDisputesForAdmin :many
SELECT
    d.id,
    d.submitted_email,
    d.event_id,
    e.name AS event_name,
    e.event_date,
    d.category_id,
    c.name AS category_name,
    d.od_date,
    d.slot,
    d.claimed_coordinator_name,
    d.student_note,
    d.status,
    d.created_at,
    d.resolution_note,
    d.resolved_by_user_id,
    d.resolved_at,
    s.id AS student_id,
    s.roll_no,
    s.name AS student_name,
    b.code AS branch,
    s.section,
    s.batch
FROM disputes d
JOIN students s ON s.id = d.student_id
JOIN branches b ON b.id = s.branch_id
JOIN events e ON e.id = d.event_id
JOIN categories c ON c.id = d.category_id
WHERE (@status::text = 'all' OR d.status = @status::text)
ORDER BY d.created_at DESC, d.id DESC
LIMIT @page_size::bigint OFFSET @page_offset::bigint;

-- name: CountDisputesForAdmin :one
SELECT count(*)::bigint
FROM disputes d
WHERE (@status::text = 'all' OR d.status = @status::text);

-- name: ListDisputesForTeacher :many
SELECT
    d.id,
    d.submitted_email,
    d.event_id,
    e.name AS event_name,
    e.event_date,
    d.category_id,
    c.name AS category_name,
    d.od_date,
    d.slot,
    d.claimed_coordinator_name,
    d.student_note,
    d.status,
    d.created_at,
    d.resolution_note,
    d.resolved_by_user_id,
    d.resolved_at,
    s.id AS student_id,
    s.roll_no,
    s.name AS student_name,
    b.code AS branch,
    s.section,
    s.batch
FROM disputes d
JOIN students s ON s.id = d.student_id
JOIN branches b ON b.id = s.branch_id
JOIN user_branches ub ON ub.branch_id = s.branch_id AND ub.user_id = @teacher_user_id
JOIN events e ON e.id = d.event_id
JOIN categories c ON c.id = d.category_id
WHERE (@status::text = 'all' OR d.status = @status::text)
ORDER BY d.created_at DESC, d.id DESC
LIMIT @page_size::bigint OFFSET @page_offset::bigint;

-- name: CountDisputesForTeacher :one
SELECT count(*)::bigint
FROM disputes d
JOIN students s ON s.id = d.student_id
JOIN user_branches ub ON ub.branch_id = s.branch_id AND ub.user_id = @teacher_user_id
WHERE (@status::text = 'all' OR d.status = @status::text);

-- name: GetDisputeForResolution :one
SELECT
    d.id,
    d.status,
    d.student_id,
    s.branch_id,
    d.resolution_note,
    d.resolved_by_user_id,
    d.resolved_at
FROM disputes d
JOIN students s ON s.id = d.student_id
WHERE d.id = @dispute_id
FOR UPDATE OF d;

-- name: TeacherCanResolveDispute :one
SELECT EXISTS (
    SELECT 1
    FROM disputes d
    JOIN students s ON s.id = d.student_id
    JOIN user_branches ub ON ub.branch_id = s.branch_id AND ub.user_id = @teacher_user_id
    WHERE d.id = @dispute_id
)::boolean;

-- name: ResolveDispute :execrows
UPDATE disputes
SET status = 'resolved',
    resolution_note = @resolution_note,
    resolved_by_user_id = @resolved_by_user_id,
    resolved_at = @resolved_at
WHERE id = @dispute_id
  AND status = 'open';

-- name: GetDisputeByID :one
SELECT
    d.id,
    d.submitted_email,
    d.event_id,
    e.name AS event_name,
    e.event_date,
    d.category_id,
    c.name AS category_name,
    d.od_date,
    d.slot,
    d.claimed_coordinator_name,
    d.student_note,
    d.status,
    d.created_at,
    d.resolution_note,
    d.resolved_by_user_id,
    d.resolved_at,
    s.id AS student_id,
    s.roll_no,
    s.name AS student_name,
    b.code AS branch,
    s.section,
    s.batch
FROM disputes d
JOIN students s ON s.id = d.student_id
JOIN branches b ON b.id = s.branch_id
JOIN events e ON e.id = d.event_id
JOIN categories c ON c.id = d.category_id
WHERE d.id = @dispute_id;
