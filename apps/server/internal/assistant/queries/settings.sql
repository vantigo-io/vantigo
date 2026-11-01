-- The workspace's settings: one row, id 1 (the migration writes it).

-- name: GetSettings :one
SELECT * FROM assistant.settings WHERE id = 1;

-- name: LockSettings :one
-- LockSettings reads the row and holds it for the transaction, so a change
-- judges its If-Match version against the row it then writes.
SELECT * FROM assistant.settings WHERE id = 1 FOR UPDATE;

-- name: UpdateSettings :one
UPDATE assistant.settings SET
    mode = @mode,
    retention_months = @retention_months,
    sensitive_default = @sensitive_default,
    version = version + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = @updated_by::uuid
WHERE id = 1
RETURNING *;

-- name: BumpSettingsVersion :one
-- BumpSettingsVersion moves the version on after a permission level changed.
UPDATE assistant.settings SET
    version = version + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = @updated_by::uuid
WHERE id = 1
RETURNING *;

-- name: ListPermissionLevels :many
SELECT * FROM assistant.permission_levels ORDER BY permission_key;

-- name: GetPermissionLevel :one
SELECT * FROM assistant.permission_levels WHERE permission_key = @permission_key;

-- name: UpsertPermissionLevel :exec
INSERT INTO assistant.permission_levels (permission_key, level, updated_at, updated_by)
VALUES (@permission_key, @level, @now::timestamptz, @updated_by::uuid)
ON CONFLICT (permission_key) DO UPDATE SET
    level = EXCLUDED.level, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by;

-- name: DeletePermissionLevel :exec
DELETE FROM assistant.permission_levels WHERE permission_key = @permission_key;

-- name: InsertEvent :exec
INSERT INTO assistant.events (at, actor_user_id, action, target, before, after)
VALUES (@at::timestamptz, @actor_user_id::uuid, @action, sqlc.narg(target), @before::jsonb, @after::jsonb);
