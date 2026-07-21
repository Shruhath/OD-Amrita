-- name: ListBranches :many
SELECT id, code, name
FROM branches
ORDER BY code ASC, id ASC;

-- name: CountBranchesByIDs :one
SELECT count(*)::integer
FROM branches
WHERE id = ANY(@branch_ids::uuid[]);
