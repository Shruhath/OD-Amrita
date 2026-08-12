-- name: CreateCategory :one
INSERT INTO categories (event_id, name)
VALUES (@event_id, @name)
RETURNING id, event_id, name, visible_on_public_dashboard, created_at;

-- name: ListCategoriesByEvent :many
SELECT id, event_id, name, visible_on_public_dashboard, created_at
FROM categories
WHERE event_id = @event_id
ORDER BY name ASC, id ASC;

-- name: GetCategoryByID :one
SELECT id, event_id, name, visible_on_public_dashboard, created_at
FROM categories
WHERE id = @category_id;

-- name: SetCategoryPublicVisibility :one
UPDATE categories
SET visible_on_public_dashboard = @visible_on_public_dashboard,
    updated_at = now()
WHERE id = @category_id
RETURNING id, event_id, name, visible_on_public_dashboard, created_at;

-- name: ListAdminPortalMarkCategories :many
SELECT c.id, c.event_id, c.name, c.visible_on_public_dashboard, c.created_at
FROM categories c
JOIN events e ON e.id = c.event_id
WHERE c.event_id = @event_id
  AND e.status = 'active'
ORDER BY c.name ASC, c.id ASC;

-- name: ListTeacherPortalMarkCategories :many
SELECT c.id, c.event_id, c.name, c.visible_on_public_dashboard, c.created_at
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
  AND EXISTS (
      SELECT 1
      FROM user_category_assignments uca
      WHERE uca.user_id = @user_id
        AND uca.category_id = c.id
  )
ORDER BY c.name ASC, c.id ASC;

-- name: ListAssignedCategoriesForUser :many
SELECT c.id, c.event_id, c.name, c.visible_on_public_dashboard, c.created_at
FROM user_category_assignments uca
JOIN categories c ON c.id = uca.category_id
WHERE uca.user_id = @user_id
ORDER BY c.name ASC, c.id ASC;

-- name: UserCategoryAssignmentExists :one
SELECT EXISTS (
    SELECT 1
    FROM user_category_assignments
    WHERE user_id = @user_id
      AND category_id = @category_id
)::boolean;

-- name: AssignUserCategory :one
INSERT INTO user_category_assignments (user_id, category_id, created_by_user_id)
VALUES (@user_id, @category_id, @created_by_user_id)
ON CONFLICT (user_id, category_id) DO NOTHING
RETURNING user_id, category_id, created_by_user_id, created_at;

-- name: CountCategoriesByIDs :one
SELECT count(*)::bigint
FROM categories
WHERE id = ANY(@category_ids::uuid[]);
