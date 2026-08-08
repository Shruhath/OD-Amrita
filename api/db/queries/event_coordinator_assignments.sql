-- name: ListCoordinatorAssignmentsByEvent :many
SELECT
    eca.id,
    eca.user_id,
    u.email,
    u.name,
    eca.event_id,
    eca.created_at
FROM event_coordinator_assignments eca
JOIN users u ON u.id = eca.user_id
WHERE eca.event_id = @event_id
ORDER BY eca.created_at ASC, eca.id ASC;

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
