-- name: GetSettings :one
SELECT * FROM meta.settings WHERE id = 1;

-- name: UpdateSettings :one
-- A field left null keeps its value; the Human Agent switch changing clears a
-- Graph error that said the permission was missing.
UPDATE meta.settings SET
    app_id              = COALESCE(sqlc.narg('app_id'), app_id),
    app_secret_sealed   = COALESCE(sqlc.narg('app_secret_sealed'), app_secret_sealed),
    human_agent_granted = COALESCE(sqlc.narg('human_agent_granted'), human_agent_granted),
    last_graph_error_code = CASE
        WHEN sqlc.narg('human_agent_granted')::boolean IS NOT NULL
         AND sqlc.narg('human_agent_granted')::boolean IS DISTINCT FROM human_agent_granted
         AND last_graph_error_code = 'human_agent_not_permitted' THEN NULL
        ELSE last_graph_error_code END,
    last_graph_error = CASE
        WHEN sqlc.narg('human_agent_granted')::boolean IS NOT NULL
         AND sqlc.narg('human_agent_granted')::boolean IS DISTINCT FROM human_agent_granted
         AND last_graph_error_code = 'human_agent_not_permitted' THEN NULL
        ELSE last_graph_error END,
    last_graph_error_at = CASE
        WHEN sqlc.narg('human_agent_granted')::boolean IS NOT NULL
         AND sqlc.narg('human_agent_granted')::boolean IS DISTINCT FROM human_agent_granted
         AND last_graph_error_code = 'human_agent_not_permitted' THEN NULL
        ELSE last_graph_error_at END,
    updated_at          = @now,
    updated_by_user_id  = @updated_by
WHERE id = 1
RETURNING *;

-- name: SetVerifyToken :one
UPDATE meta.settings SET verify_token = @verify_token, updated_at = @now, updated_by_user_id = @updated_by
WHERE id = 1
RETURNING *;

-- name: TouchWebhook :exec
-- older_than is thirty seconds before now: at most one write in that time, so a
-- busy Page does not hammer one row.
UPDATE meta.settings SET last_webhook_at = @now
WHERE id = 1 AND (last_webhook_at IS NULL OR last_webhook_at < @older_than);

-- name: TouchRejected :exec
UPDATE meta.settings SET last_rejected_at = @now
WHERE id = 1 AND (last_rejected_at IS NULL OR last_rejected_at < @older_than);

-- name: RecordGraphError :exec
UPDATE meta.settings SET
    last_graph_error_code = @code,
    last_graph_error      = @message,
    last_graph_error_at   = @now
WHERE id = 1;
