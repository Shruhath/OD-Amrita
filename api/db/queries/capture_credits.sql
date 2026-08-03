-- name: GetCaptureCreditConfig :one
SELECT
    e.id AS event_id,
    COALESCE(cfg.credit_limit, 5)::integer AS credit_limit,
    cfg.updated_by_user_id,
    COALESCE(cfg.updated_at, e.created_at) AS updated_at
FROM events e
LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id
WHERE e.id = @event_id;

-- name: UpsertCaptureCreditConfig :one
WITH locked_event AS (
    SELECT id
    FROM events
    WHERE id = @event_id
    FOR UPDATE
),
previous AS (
    SELECT COALESCE(cfg.credit_limit, 5)::integer AS previous_limit
    FROM locked_event
    LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = locked_event.id
),
updated AS (
    INSERT INTO event_capture_credit_config (event_id, credit_limit, updated_by_user_id, updated_at)
    SELECT locked_event.id, @credit_limit, @updated_by_user_id, now()
    FROM locked_event
    ON CONFLICT (event_id) DO UPDATE
    SET credit_limit = EXCLUDED.credit_limit,
        updated_by_user_id = EXCLUDED.updated_by_user_id,
        updated_at = now()
    RETURNING event_id, credit_limit, updated_by_user_id, updated_at
)
SELECT
    updated.event_id,
    updated.credit_limit,
    updated.updated_by_user_id,
    updated.updated_at,
    previous.previous_limit
FROM updated, previous;

-- name: GetCaptureCreditState :one
WITH effective AS (
    SELECT COALESCE(cfg.credit_limit, 5)::integer AS credit_limit
    FROM events e
    LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id
    WHERE e.id = @event_id
),
usage AS (
    SELECT COALESCE(u.consumed, 0)::integer AS consumed
    FROM effective
    LEFT JOIN event_capture_credit_usage u
        ON u.event_id = @event_id AND u.user_id = @user_id
)
SELECT
    effective.credit_limit,
    usage.consumed,
    GREATEST(effective.credit_limit - usage.consumed, 0)::integer AS remaining,
    (usage.consumed >= effective.credit_limit)::boolean AS exhausted
FROM effective, usage;

-- name: DebitCaptureCredit :one
WITH assignment AS (
    SELECT eca.event_id, eca.user_id
    FROM event_coordinator_assignments eca
    WHERE eca.event_id = @event_id AND eca.user_id = @user_id
    FOR UPDATE
),
effective AS (
    SELECT COALESCE(cfg.credit_limit, 5)::integer AS credit_limit
    FROM events e
    JOIN assignment ON assignment.event_id = e.id
    LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id
),
debited AS (
    INSERT INTO event_capture_credit_usage (event_id, user_id, consumed, updated_at)
    SELECT assignment.event_id, assignment.user_id, 1, now()
    FROM effective, assignment
    WHERE effective.credit_limit > 0
    ON CONFLICT (event_id, user_id) DO UPDATE
    SET consumed = event_capture_credit_usage.consumed + 1,
        updated_at = now()
    WHERE event_capture_credit_usage.consumed < (SELECT credit_limit FROM effective)
    RETURNING consumed
)
SELECT
    effective.credit_limit,
    debited.consumed,
    GREATEST(effective.credit_limit - debited.consumed, 0)::integer AS remaining,
    (debited.consumed >= effective.credit_limit)::boolean AS exhausted
FROM effective, debited;

-- name: ListCaptureCreditUsageByEvent :many
WITH effective AS (
    SELECT COALESCE(cfg.credit_limit, 5)::integer AS credit_limit
    FROM events e
    LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id
    WHERE e.id = @event_id
)
SELECT
    eca.event_id,
    eca.user_id,
    u.email,
    u.name,
    effective.credit_limit,
    COALESCE(usage.consumed, 0)::integer AS consumed,
    GREATEST(effective.credit_limit - COALESCE(usage.consumed, 0), 0)::integer AS remaining,
    (COALESCE(usage.consumed, 0) >= effective.credit_limit)::boolean AS exhausted
FROM event_coordinator_assignments eca
JOIN users u ON u.id = eca.user_id
CROSS JOIN effective
LEFT JOIN event_capture_credit_usage usage
    ON usage.event_id = eca.event_id AND usage.user_id = eca.user_id
WHERE eca.event_id = @event_id
ORDER BY eca.created_at ASC, eca.id ASC;

-- name: GetCaptureCreditUsageForCoordinator :one
WITH effective AS (
    SELECT COALESCE(cfg.credit_limit, 5)::integer AS credit_limit
    FROM events e
    LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id
    WHERE e.id = @event_id
)
SELECT
    eca.event_id,
    eca.user_id,
    u.email,
    u.name,
    effective.credit_limit,
    COALESCE(usage.consumed, 0)::integer AS consumed,
    GREATEST(effective.credit_limit - COALESCE(usage.consumed, 0), 0)::integer AS remaining,
    (COALESCE(usage.consumed, 0) >= effective.credit_limit)::boolean AS exhausted
FROM event_coordinator_assignments eca
JOIN users u ON u.id = eca.user_id
CROSS JOIN effective
LEFT JOIN event_capture_credit_usage usage
    ON usage.event_id = eca.event_id AND usage.user_id = eca.user_id
WHERE eca.event_id = @event_id AND eca.user_id = @user_id;

-- name: ResetCaptureCreditUsage :one
WITH assignment AS (
    SELECT eca.event_id, eca.user_id
    FROM event_coordinator_assignments eca
    WHERE eca.event_id = @event_id AND eca.user_id = @user_id
    FOR UPDATE
),
previous AS (
    SELECT COALESCE(usage.consumed, 0)::integer AS previous_consumed
    FROM assignment
    LEFT JOIN event_capture_credit_usage usage
        ON usage.event_id = assignment.event_id AND usage.user_id = assignment.user_id
),
reset AS (
    INSERT INTO event_capture_credit_usage (event_id, user_id, consumed, updated_at)
    SELECT assignment.event_id, assignment.user_id, 0, now()
    FROM assignment
    ON CONFLICT (event_id, user_id) DO UPDATE
    SET consumed = 0,
        updated_at = now()
    RETURNING event_id, user_id, consumed, updated_at
)
SELECT
    reset.event_id,
    reset.user_id,
    reset.consumed,
    reset.updated_at,
    previous.previous_consumed
FROM reset, previous;
