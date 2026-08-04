-- name: CreateCategory :one
INSERT INTO categories (event_id, name)
VALUES (@event_id, @name)
RETURNING id, event_id, name, created_at;

-- name: ListCategoriesByEvent :many
SELECT id, event_id, name, created_at
FROM categories
WHERE event_id = @event_id
ORDER BY name ASC, id ASC;

-- name: ListAdminPortalMarkCategories :many
SELECT c.id, c.event_id, c.name, c.created_at
FROM categories c
JOIN events e ON e.id = c.event_id
WHERE c.event_id = @event_id
  AND e.status = 'active'
ORDER BY c.name ASC, c.id ASC;

-- name: ListTeacherPortalMarkCategories :many
SELECT c.id, c.event_id, c.name, c.created_at
FROM categories c
JOIN events e ON e.id = c.event_id
WHERE c.event_id = @event_id
  AND e.status = 'active'
  AND EXISTS (
      SELECT 1
      FROM event_branches eb
      JOIN user_branches ub ON ub.branch_id = eb.branch_id
      WHERE eb.event_id = c.event_id
        AND ub.user_id = @user_id
  )
ORDER BY c.name ASC, c.id ASC;
