-- The workspace's settings: one row, id 1 (the migration writes it).

-- name: GetSettings :one
SELECT * FROM agents.settings WHERE id = 1;

-- name: LockSettings :one
-- LockSettings reads the row and holds it for the transaction, so a change
-- judges its If-Match version against the row it then writes.
SELECT * FROM agents.settings WHERE id = 1 FOR UPDATE;

-- name: UpdateSettings :one
UPDATE agents.settings SET
    company_name = @company_name,
    retention_months = @retention_months,
    version = version + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = @updated_by::uuid
WHERE id = 1
RETURNING *;

-- name: InsertEvent :exec
INSERT INTO agents.events (at, actor_user_id, action, target, before, after)
VALUES (@at::timestamptz, @actor_user_id::uuid, @action, sqlc.narg(target), @before::jsonb, @after::jsonb);
