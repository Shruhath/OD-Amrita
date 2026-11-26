-- name: ListCoordinatorAssignmentsByEvent :many
SELECT
    eca.id,
    eca.user_id,
    u.email,
    u.name,
    eca.event_id,
    COALESCE(
        jsonb_agg(
            jsonb_build_object(
                'id', c.id,
                'event_id', c.event_id,
                'name', c.name,
                'visible_on_public_dashboard', c.visible_on_public_dashboard,
                'created_at', c.created_at
            )
            ORDER BY c.name ASC, c.id ASC
        ) FILTER (WHERE c.id IS NOT NULL),
        '[]'::jsonb
    )::text AS categories_json,
    eca.created_at
FROM event_coordinator_assignments eca
JOIN users u ON u.id = eca.user_id
LEFT JOIN user_category_assignments uca ON uca.user_id = eca.user_id
LEFT JOIN categories c ON c.id = uca.category_id AND c.event_id = eca.event_id
WHERE eca.event_id = @event_id
GROUP BY eca.id, u.id
ORDER BY eca.created_at ASC, eca.id ASC;

-- name: GetCoordinatorAssignmentByEventAndUser :one
SELECT
    eca.id,
    eca.user_id,
    u.email,
    u.name,
    eca.event_id,
    COALESCE(
        jsonb_agg(
            jsonb_build_object(
                'id', c.id,
                'event_id', c.event_id,
                'name', c.name,
                'visible_on_public_dashboard', c.visible_on_public_dashboard,
                'created_at', c.created_at
            )
            ORDER BY c.name ASC, c.id ASC
        ) FILTER (WHERE c.id IS NOT NULL),
        '[]'::jsonb
    )::text AS categories_json,
    eca.created_at
FROM event_coordinator_assignments eca
JOIN users u ON u.id = eca.user_id
LEFT JOIN user_category_assignments uca ON uca.user_id = eca.user_id
LEFT JOIN categories c ON c.id = uca.category_id AND c.event_id = eca.event_id
WHERE eca.event_id = @event_id AND eca.user_id = @user_id
GROUP BY eca.id, u.id;

-- name: GetCoordinatorCandidateByEmail :one
SELECT id, email, name, role
FROM users
WHERE lower(email) = lower(@email)
ORDER BY last_login_at DESC NULLS LAST, created_at DESC, id DESC
LIMIT 1;

-- name: CoordinatorAssignmentExists :one
SELECT EXISTS(
    SELECT 1
    FROM event_coordinator_assignments
    WHERE user_id = @user_id AND event_id = @event_id
)::boolean;

-- name: LockCoordinatorAssignmentUser :one
SELECT id, email, name, role
FROM users
WHERE id = @user_id
FOR UPDATE;

-- name: PromoteUserToCoordinator :execrows
UPDATE users
SET role = 'coordinator', updated_at = now()
WHERE id = @user_id AND role = 'student';

-- name: InsertEventCoordinatorAssignment :one
INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id)
VALUES (@user_id, @event_id, @created_by_user_id)
RETURNING id, user_id, event_id, created_at;

-- name: ListAssignedActiveEvents :many
SELECT
    e.id,
    e.name,
    e.event_date,
    e.end_date,
    e.status,
    e.archived_at,
    e.created_at,
    COALESCE(
        jsonb_agg(
            jsonb_build_object('id', b.id, 'code', b.code, 'name', b.name)
            ORDER BY b.code ASC, b.id ASC
        ) FILTER (WHERE b.id IS NOT NULL),
        '[]'::jsonb
    )::text AS branches_json
FROM event_coordinator_assignments eca
JOIN events e ON e.id = eca.event_id
JOIN categories c ON c.event_id = e.id
JOIN user_category_assignments uca ON uca.category_id = c.id AND uca.user_id = eca.user_id
LEFT JOIN event_branches eb ON eb.event_id = e.id
LEFT JOIN branches b ON b.id = eb.branch_id
WHERE eca.user_id = @user_id AND e.status = 'active'
GROUP BY e.id
ORDER BY e.event_date DESC, e.created_at DESC, e.id DESC;

-- name: CoordinatorAssignedToEvent :one
SELECT EXISTS(
    SELECT 1
    FROM event_coordinator_assignments
    WHERE user_id = @user_id AND event_id = @event_id
)::boolean;

-- name: CoordinatorAssignedToEventCategory :one
SELECT EXISTS(
    SELECT 1
    FROM event_coordinator_assignments eca
    JOIN user_category_assignments uca ON uca.user_id = eca.user_id
    JOIN categories c ON c.id = uca.category_id AND c.event_id = eca.event_id
    WHERE eca.user_id = @user_id
      AND eca.event_id = @event_id
      AND c.id = @category_id
)::boolean;

-- name: LockCoordinatorAssignment :one
-- Removal's first step: take the assignment row FOR UPDATE so a concurrent
-- fallback-credit debit (whose FK check needs a KEY SHARE lock on this row)
-- waits for the removal and then fails its FK check, instead of slipping a new
-- usage row in between the credit snapshot and the delete.
SELECT eca.id, eca.user_id, eca.event_id, eca.fallback_credit_limit, u.email
FROM event_coordinator_assignments eca
JOIN users u ON u.id = eca.user_id
WHERE eca.event_id = @event_id AND eca.user_id = @user_id
FOR UPDATE OF eca;

-- name: SumCoordinatorCaptureCreditUsage :one
-- The assignment's fallback-credit counters, for the audit snapshot. Read under
-- the assignment lock, just before the assignment is deleted; the counters then
-- go with it through event_capture_credit_usage_assignment_fk ON DELETE CASCADE
-- (0027) — od_runtime holds no DELETE on them.
SELECT count(*)::bigint AS rows, COALESCE(sum(consumed), 0)::bigint AS consumed
FROM event_capture_credit_usage
WHERE event_id = @event_id AND user_id = @user_id;

-- name: DeleteUserCategoriesForEvent :many
-- Every category access the user holds on ONE event. A coordinator's category
-- rows mean nothing once they no longer coordinate the event, and leaving them
-- would hand the access straight back on a later re-assignment.
DELETE FROM user_category_assignments uca
USING categories c
WHERE c.id = uca.category_id
  AND uca.user_id = @user_id
  AND c.event_id = @event_id
RETURNING uca.category_id;

-- name: DeleteEventCoordinatorAssignment :execrows
DELETE FROM event_coordinator_assignments
WHERE event_id = @event_id AND user_id = @user_id;
