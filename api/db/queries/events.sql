-- name: CreateEvent :one
INSERT INTO events (name, event_date)
VALUES (@name, @event_date)
RETURNING id, name, event_date, status, archived_at, created_at;

-- name: AddEventBranch :execrows
INSERT INTO event_branches (event_id, branch_id)
VALUES (@event_id, @branch_id);

-- name: GetEvent :one
SELECT
    e.id,
    e.name,
    e.event_date,
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
FROM events e
LEFT JOIN event_branches eb ON eb.event_id = e.id
LEFT JOIN branches b ON b.id = eb.branch_id
WHERE e.id = @event_id
GROUP BY e.id;

-- name: ListEvents :many
SELECT
    e.id,
    e.name,
    e.event_date,
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
FROM events e
LEFT JOIN event_branches eb ON eb.event_id = e.id
LEFT JOIN branches b ON b.id = eb.branch_id
WHERE (@status::text = 'all' OR e.status = @status::text)
GROUP BY e.id
ORDER BY e.event_date DESC, e.created_at DESC, e.id DESC
LIMIT @page_size::bigint OFFSET @page_offset::bigint;

-- name: CountEvents :one
SELECT count(*)
FROM events
WHERE (@status::text = 'all' OR status = @status::text);

-- name: ArchiveEvent :one
UPDATE events
SET status = 'archived',
    archived_at = now(),
    archived_by_user_id = @archived_by_user_id,
    updated_at = now()
WHERE id = @event_id AND status = 'active'
RETURNING id;

-- name: GetEventMutationStatus :one
SELECT status
FROM events
WHERE id = @event_id;
