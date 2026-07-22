-- name: CreateCategory :one
INSERT INTO categories (event_id, name)
VALUES (@event_id, @name)
RETURNING id, event_id, name, created_at;

-- name: ListCategoriesByEvent :many
SELECT id, event_id, name, created_at
FROM categories
WHERE event_id = @event_id
ORDER BY name ASC, id ASC;
