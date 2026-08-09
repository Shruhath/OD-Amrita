-- name: GetAppSetting :one
-- One runtime switch. Missing is not an error at this layer: the service decides
-- what a missing key means, and for every switch so far that is "off" — the
-- behaviour production already had.
SELECT key, value, updated_at
FROM app_settings
WHERE key = @key;

-- name: UpsertAppSetting :one
-- Upsert rather than update, so a setting introduced by a later release can be
-- written before anything has seeded a row for it.
INSERT INTO app_settings (key, value, updated_at, updated_by)
VALUES (@key, @value, now(), @updated_by)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = EXCLUDED.updated_at,
    updated_by = EXCLUDED.updated_by
RETURNING key, value, updated_at;
