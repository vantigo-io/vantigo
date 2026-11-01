-- name: InsertRun :one
INSERT INTO assistant.runs (id, conversation_id, user_id, state, started_at)
VALUES (@id, @conversation_id, @user_id, 'running', @now::timestamptz)
RETURNING *;

-- name: GetRun :one
SELECT * FROM assistant.runs WHERE id = @id AND user_id = @user_id;

-- name: GetRunByID :one
-- GetRunByID is for the run's own goroutine, which owns its id.
SELECT * FROM assistant.runs WHERE id = @id;

-- name: GetActiveRun :one
SELECT r.* FROM assistant.runs r
WHERE r.conversation_id = @conversation_id AND r.user_id = @user_id AND r.state = 'running';

-- name: RequestStop :one
UPDATE assistant.runs SET stop_requested = true WHERE id = @id AND user_id = @user_id RETURNING *;

-- name: CountRunProgress :exec
UPDATE assistant.runs SET tool_calls = @tool_calls, model_turns = @model_turns WHERE id = @id;

-- name: FinishRun :one
UPDATE assistant.runs SET state = @state, error_code = sqlc.narg(error_code), ended_at = @now::timestamptz
WHERE id = @id AND state = 'running'
RETURNING *;

-- name: FailStaleRuns :many
-- FailStaleRuns marks the runs still running after the cutoff failed, so an
-- API restart never leaves one running forever.
UPDATE assistant.runs SET state = 'failed', error_code = 'server_restarted', ended_at = @now::timestamptz
WHERE state = 'running' AND started_at < @cutoff::timestamptz
RETURNING id;

-- name: InsertAction :exec
INSERT INTO assistant.actions (id, run_id, operation_id, mode, http_status, duration_ms, created_at)
VALUES (@id, @run_id, @operation_id, @mode, sqlc.narg(http_status)::integer, @duration_ms, @now::timestamptz);

-- name: ListActions :many
-- ListActions is the action log of a conversation, joined through the run to
-- the caller.
SELECT a.* FROM assistant.actions a
JOIN assistant.runs r ON r.id = a.run_id
WHERE r.conversation_id = @conversation_id AND r.user_id = @user_id
ORDER BY a.created_at, a.id;

-- name: GetStopRequested :one
SELECT stop_requested FROM assistant.runs WHERE id = @id;
