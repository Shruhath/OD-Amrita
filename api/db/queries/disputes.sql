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

-- name: PublicCategoryAvailableForDispute :one
SELECT EXISTS (
    SELECT 1
    FROM categories c
    JOIN events e ON e.id = c.event_id
    WHERE e.id = @event_id
      AND c.id = @category_id
      AND c.visible_on_public_dashboard = true
      AND @od_date::date BETWEEN e.event_date AND e.end_date
)::boolean;

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
    d.outcome,
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
    d.outcome,
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
JOIN user_category_assignments uca ON uca.category_id = d.category_id AND uca.user_id = @teacher_user_id
WHERE (@status::text = 'all' OR d.status = @status::text)
ORDER BY d.created_at DESC, d.id DESC
LIMIT @page_size::bigint OFFSET @page_offset::bigint;

-- name: CountDisputesForTeacher :one
SELECT count(*)::bigint
FROM disputes d
JOIN students s ON s.id = d.student_id
JOIN user_branches ub ON ub.branch_id = s.branch_id AND ub.user_id = @teacher_user_id
JOIN user_category_assignments uca ON uca.category_id = d.category_id AND uca.user_id = @teacher_user_id
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
    JOIN user_category_assignments uca ON uca.category_id = d.category_id AND uca.user_id = @teacher_user_id
    WHERE d.id = @dispute_id
)::boolean;

-- name: ResolveDispute :execrows
UPDATE disputes
SET status = 'resolved',
    outcome = @outcome::text,
    resolution_note = @resolution_note,
    resolved_by_user_id = @resolved_by_user_id,
    resolved_at = @resolved_at
WHERE id = @dispute_id
  AND status = 'open';

-- name: CreateODRecordFromApprovedDispute :one
-- Credits the disputed slot, in the transaction that resolved the dispute.
--
-- The record's own facts are SELECTED FROM THE DISPUTE ROW rather than passed
-- in. That is the point: the student, event, category, day and slot are the ones
-- the student actually disputed and a teacher actually looked at, so no caller
-- can approve dispute A and credit slot B, and no drift is possible between what
-- the inbox showed and what got written.
--
-- capture_method is 'dispute', not 'search'. Nobody searched for this student —
-- recording it as a search would put a lie in the one table whose job is to say
-- who marked someone and how. reason_note carries the teacher's resolution note,
-- which od_records_manual_reason_note_ck requires for any non-coordinator actor.
--
-- ON CONFLICT DO NOTHING against the FR10 per-day duplicate key: if the student
-- was already marked for that slot — a coordinator caught up, or a teacher
-- corrected it by hand while the dispute sat open — the dispute is moot, not
-- failed. It returns no row, and the caller resolves the dispute anyway rather
-- than refusing an approval whose desired end state already holds.
INSERT INTO od_records (
    student_id, event_id, category_id, slot, od_date,
    status, capture_method, actor_user_id, actor_role, reason_note
)
SELECT
    d.student_id, d.event_id, d.category_id, d.slot, d.od_date,
    'present', 'dispute', @actor_user_id, @actor_role::text, @reason_note::text
FROM disputes d
WHERE d.id = @dispute_id
ON CONFLICT (student_id, event_id, od_date, slot) WHERE status = 'present' DO NOTHING
RETURNING id;

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
    d.outcome,
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
