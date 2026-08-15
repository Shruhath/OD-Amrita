-- name: UpdateCoordinatorCaptureCreditLimit :one
WITH assignment AS (
    SELECT eca.event_id, eca.user_id, eca.fallback_credit_limit AS previous_limit
    FROM event_coordinator_assignments eca
    WHERE eca.event_id = @event_id AND eca.user_id = @user_id
    FOR UPDATE
),
updated AS (
    UPDATE event_coordinator_assignments eca
    SET fallback_credit_limit = @fallback_credit_limit
    FROM assignment
    WHERE eca.event_id = assignment.event_id AND eca.user_id = assignment.user_id
    RETURNING eca.event_id, eca.user_id, eca.fallback_credit_limit, eca.created_at
)
SELECT
    updated.event_id,
    updated.user_id,
    updated.fallback_credit_limit,
    updated.created_at AS updated_at,
    assignment.previous_limit
FROM updated, assignment;

-- name: GetCaptureCreditState :one
WITH effective AS (
    SELECT eca.fallback_credit_limit::integer AS credit_limit
    FROM event_coordinator_assignments eca
    WHERE eca.event_id = @event_id AND eca.user_id = @user_id
),
usage AS (
    SELECT COALESCE(u.consumed, 0)::integer AS consumed
    FROM effective
    LEFT JOIN event_capture_credit_usage u
        ON u.event_id = @event_id AND u.user_id = @user_id AND u.usage_date = @usage_date
)
SELECT
    effective.credit_limit,
    usage.consumed,
    GREATEST(effective.credit_limit - usage.consumed, 0)::integer AS remaining,
    (usage.consumed >= effective.credit_limit)::boolean AS exhausted
FROM effective, usage;

-- name: DebitCaptureCredit :one
WITH assignment AS (
    SELECT eca.event_id, eca.user_id, eca.fallback_credit_limit
    FROM event_coordinator_assignments eca
    WHERE eca.event_id = @event_id AND eca.user_id = @user_id
    FOR UPDATE
),
effective AS (
    SELECT assignment.fallback_credit_limit::integer AS credit_limit
    FROM assignment
),
debited AS (
    INSERT INTO event_capture_credit_usage (event_id, user_id, usage_date, consumed, updated_at)
    SELECT assignment.event_id, assignment.user_id, @usage_date, 1, now()
    FROM effective, assignment
    WHERE effective.credit_limit > 0
    ON CONFLICT (event_id, user_id, usage_date) DO UPDATE
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
SELECT
    eca.event_id,
    eca.user_id,
    u.email,
    u.name,
    eca.fallback_credit_limit::integer AS credit_limit,
    COALESCE(usage.consumed, 0)::integer AS consumed,
    GREATEST(eca.fallback_credit_limit - COALESCE(usage.consumed, 0), 0)::integer AS remaining,
    (COALESCE(usage.consumed, 0) >= eca.fallback_credit_limit)::boolean AS exhausted
FROM event_coordinator_assignments eca
JOIN users u ON u.id = eca.user_id
LEFT JOIN event_capture_credit_usage usage
    ON usage.event_id = eca.event_id AND usage.user_id = eca.user_id
   AND usage.usage_date = @usage_date
WHERE eca.event_id = @event_id
ORDER BY eca.created_at ASC, eca.id ASC;

-- name: GetCaptureCreditUsageForCoordinator :one
SELECT
    eca.event_id,
    eca.user_id,
    u.email,
    u.name,
    eca.fallback_credit_limit::integer AS credit_limit,
    COALESCE(usage.consumed, 0)::integer AS consumed,
    GREATEST(eca.fallback_credit_limit - COALESCE(usage.consumed, 0), 0)::integer AS remaining,
    (COALESCE(usage.consumed, 0) >= eca.fallback_credit_limit)::boolean AS exhausted
FROM event_coordinator_assignments eca
JOIN users u ON u.id = eca.user_id
LEFT JOIN event_capture_credit_usage usage
    ON usage.event_id = eca.event_id AND usage.user_id = eca.user_id
   AND usage.usage_date = @usage_date
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
       AND usage.usage_date = @usage_date
),
reset AS (
    INSERT INTO event_capture_credit_usage (event_id, user_id, usage_date, consumed, updated_at)
    SELECT assignment.event_id, assignment.user_id, @usage_date, 0, now()
    FROM assignment
    ON CONFLICT (event_id, user_id, usage_date) DO UPDATE
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
