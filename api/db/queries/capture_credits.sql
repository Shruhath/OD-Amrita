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
INSERT INTO event_capture_credit_config (event_id, credit_limit, updated_by_user_id, updated_at)
VALUES (@event_id, @credit_limit, @updated_by_user_id, now())
ON CONFLICT (event_id) DO UPDATE
SET credit_limit = EXCLUDED.credit_limit,
    updated_by_user_id = EXCLUDED.updated_by_user_id,
    updated_at = now()
RETURNING event_id, credit_limit, updated_by_user_id, updated_at;

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
WITH effective AS (
    SELECT COALESCE(cfg.credit_limit, 5)::integer AS credit_limit
    FROM events e
    LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id
    WHERE e.id = @event_id
),
debited AS (
    INSERT INTO event_capture_credit_usage (event_id, user_id, consumed, updated_at)
    SELECT @event_id, @user_id, 1, now()
    FROM effective
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
INSERT INTO event_capture_credit_usage (event_id, user_id, consumed, updated_at)
SELECT @event_id, @user_id, 0, now()
WHERE EXISTS (
    SELECT 1
    FROM event_coordinator_assignments
    WHERE event_id = @event_id AND user_id = @user_id
)
ON CONFLICT (event_id, user_id) DO UPDATE
SET consumed = 0,
    updated_at = now()
RETURNING event_id, user_id, consumed, updated_at;
