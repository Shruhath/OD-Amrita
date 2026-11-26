-- user_roles.sql — taking a staff role away (admin "remove staff access").
--
-- Every write here runs on the transaction store.WithAudit opened for ONE
-- user.demoted audit row, so the role change, the access rows it removes and the
-- forced logout it records commit together or not at all.

-- name: LockUserForRoleChange :one
SELECT id, supabase_subject, email, role
FROM users
WHERE id = @id
FOR UPDATE;

-- name: LockAdminUsers :many
-- Every admin row, locked in id order. Two administrators demoting each other at
-- the same moment would otherwise each see the other still in place and both
-- succeed, leaving no admin at all. With the rows locked the second transaction
-- waits, re-reads, and sees that only one admin is left.
SELECT id
FROM users
WHERE role = 'admin'
ORDER BY id
FOR UPDATE;

-- name: LockActiveCoordinatorAssignmentsForUser :many
-- Same purpose as LockCoordinatorAssignment, for every active event at once.
-- Archived events are excluded on purpose: their rows are immutable (the
-- active-event trigger refuses the delete) and stay as the record of who
-- coordinated them.
SELECT eca.event_id
FROM event_coordinator_assignments eca
JOIN events e ON e.id = eca.event_id
WHERE eca.user_id = @user_id AND e.status = 'active'
ORDER BY eca.event_id
FOR UPDATE OF eca;

-- name: SumActiveCaptureCreditUsageForUser :one
-- For the audit snapshot only: these counters go with their assignments through
-- the ON DELETE CASCADE foreign key (0027), never by a direct delete.
SELECT count(*)::bigint AS rows, COALESCE(sum(u.consumed), 0)::bigint AS consumed
FROM event_capture_credit_usage u
JOIN events e ON e.id = u.event_id
WHERE e.status = 'active' AND u.user_id = @user_id;

-- name: DeleteActiveCoordinatorAssignmentsForUser :many
-- Cascades to each assignment's fallback-credit counters (0027).
DELETE FROM event_coordinator_assignments eca
USING events e
WHERE e.id = eca.event_id
  AND e.status = 'active'
  AND eca.user_id = @user_id
RETURNING eca.event_id;

-- name: DeleteAllUserCategoryAssignments :execrows
-- Category access is per user, not per event, so a student keeps none of it —
-- including categories of archived events, which no trigger guards and which
-- would otherwise resurface if the user were ever made staff again.
DELETE FROM user_category_assignments
WHERE user_id = @user_id;

-- name: DeleteUserBranches :execrows
DELETE FROM user_branches
WHERE user_id = @user_id;

-- name: DemoteUserToStudent :execrows
-- The AND role <> 'student' guard keeps a repeat a true no-op.
UPDATE users
SET role = 'student', updated_at = now()
WHERE id = @id AND role <> 'student';
