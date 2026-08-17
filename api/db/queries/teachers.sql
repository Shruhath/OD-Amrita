-- name: GetUserByEmail :one
-- The ORDER BY ... LIMIT 1 is load-bearing, not decoration: users.email is
-- deliberately NOT UNIQUE (0003's own comment), so a plain WHERE email = $1
-- can match more than one row. Ordering by most-recently-active makes the
-- match deterministic and picks the identity that is actually in current use.
-- The trailing `id DESC` is a pure tiebreaker for the (very unlikely) case of
-- two colliding-email rows sharing an identical last_login_at and created_at —
-- without it, a tie resolves however Postgres happens to return matching rows.
SELECT id, supabase_subject, email, name, role, created_at, updated_at, last_login_at
FROM users
WHERE lower(email) = lower(@email)
ORDER BY last_login_at DESC NULLS LAST, created_at DESC, id DESC
LIMIT 1;

-- name: AssignTeacherRole :one
-- The AND role = 'student' guard is the race backstop (mirrors ArchiveEvent's
-- AND status = 'active'): a concurrent second elevation of the same user
-- returns zero rows, which the caller must turn into pgx.ErrNoRows, not a
-- silent success.
UPDATE users
SET role = 'teacher', updated_at = now()
WHERE id = @id AND role = 'student'
RETURNING id, email, name, role;

-- name: AddUserBranch :execrows
INSERT INTO user_branches (user_id, branch_id)
VALUES (@user_id, @branch_id);

-- name: AddUserBranchesBulk :execrows
-- Every branch for one teacher in ONE statement.
--
-- The caller used to loop AddUserBranch, which is a round trip per branch. The
-- counts are small — a teacher covers a handful of branches — but this runs
-- inside the transaction that creates the teacher, on a database roughly half a
-- second away, so each one is felt directly by an administrator waiting on a
-- form.
--
-- :execrows so the caller can still assert every branch landed, exactly as the
-- loop did per row: the expected count is len(branch_ids).
INSERT INTO user_branches (user_id, branch_id)
SELECT @user_id, branch_id
FROM unnest(@branch_ids::uuid[]) AS branch_id;

-- name: GetTeacherProfile :one
SELECT
    u.id,
    u.email,
    u.name,
    u.created_at,
    COALESCE(
        jsonb_agg(
            jsonb_build_object('id', b.id, 'code', b.code, 'name', b.name)
            ORDER BY b.code ASC, b.id ASC
        ) FILTER (WHERE b.id IS NOT NULL),
        '[]'::jsonb
    )::text AS branches_json,
    COALESCE(
        (
            SELECT jsonb_agg(
                jsonb_build_object(
                    'id', c.id,
                    'event_id', c.event_id,
                    'name', c.name,
                    'visible_on_public_dashboard', c.visible_on_public_dashboard,
                    'created_at', c.created_at
                )
                ORDER BY c.name ASC, c.id ASC
            )
            FROM user_category_assignments uca
            JOIN categories c ON c.id = uca.category_id
            WHERE uca.user_id = u.id
        ),
        '[]'::jsonb
    )::text AS categories_json
FROM users u
LEFT JOIN user_branches ub ON ub.user_id = u.id
LEFT JOIN branches b ON b.id = ub.branch_id
WHERE u.id = @user_id AND u.role = 'teacher'
GROUP BY u.id;

-- name: ListTeachers :many
SELECT
    u.id,
    u.email,
    u.name,
    u.created_at,
    COALESCE(
        jsonb_agg(
            jsonb_build_object('id', b.id, 'code', b.code, 'name', b.name)
            ORDER BY b.code ASC, b.id ASC
        ) FILTER (WHERE b.id IS NOT NULL),
        '[]'::jsonb
    )::text AS branches_json,
    COALESCE(
        (
            SELECT jsonb_agg(
                jsonb_build_object(
                    'id', c.id,
                    'event_id', c.event_id,
                    'name', c.name,
                    'visible_on_public_dashboard', c.visible_on_public_dashboard,
                    'created_at', c.created_at
                )
                ORDER BY c.name ASC, c.id ASC
            )
            FROM user_category_assignments uca
            JOIN categories c ON c.id = uca.category_id
            WHERE uca.user_id = u.id
        ),
        '[]'::jsonb
    )::text AS categories_json
FROM users u
LEFT JOIN user_branches ub ON ub.user_id = u.id
LEFT JOIN branches b ON b.id = ub.branch_id
WHERE u.role = 'teacher'
GROUP BY u.id
ORDER BY u.email ASC, u.id ASC
LIMIT @page_size::bigint OFFSET @page_offset::bigint;

-- name: CountTeachers :one
SELECT count(*)
FROM users
WHERE role = 'teacher';

-- name: TeacherExists :one
SELECT EXISTS(
    SELECT 1
    FROM users
    WHERE id = @user_id AND role = 'teacher'
)::boolean;
