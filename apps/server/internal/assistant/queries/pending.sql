-- The pending actions (the Assistant design S12): every query filters by
-- user_id, so another person's action is not found. A decided action keeps no
-- request values (assistant.redact_request, redact_preview).

-- name: InsertPendingAction :one
INSERT INTO assistant.pending_actions (id, run_id, conversation_id, user_id, operation_id, call_id, tool_message_id, request, preview, created_at, expires_at, kind)
VALUES (@id, @run_id, @conversation_id, @user_id, @operation_id, @call_id, sqlc.narg(tool_message_id)::uuid, @request::jsonb, @preview::jsonb, @now::timestamptz, @expires_at::timestamptz, @kind::text)
RETURNING *;

-- name: GetPendingAction :one
SELECT * FROM assistant.pending_actions WHERE id = @id AND user_id = @user_id;

-- name: ListPendingActions :many
-- ListPendingActions is a conversation's actions in order, for the caller's
-- own conversation only.
SELECT p.* FROM assistant.pending_actions p
JOIN assistant.conversations c ON c.id = p.conversation_id
WHERE p.conversation_id = @conversation_id AND c.user_id = @user_id
ORDER BY p.created_at, p.id;

-- name: DecidePendingAction :one
-- DecidePendingAction claims an undecided, unexpired action for the person's
-- decision, once: a second decision finds it taken. The request stays until the
-- decision's outcome is recorded.
UPDATE assistant.pending_actions SET status = @status, decided_at = @now::timestamptz
WHERE id = @id AND user_id = @user_id AND status = 'pending' AND expires_at > @now::timestamptz
RETURNING *;

-- name: FinishPendingAction :one
-- FinishPendingAction records how a decided action ended and drops the values
-- the person's request held.
UPDATE assistant.pending_actions SET
    status = @status,
    outcome = @outcome::jsonb,
    request = assistant.redact_request(request),
    preview = CASE WHEN sqlc.narg(target)::jsonb IS NULL THEN assistant.redact_preview(preview)
                   ELSE jsonb_set(assistant.redact_preview(preview), '{target}', sqlc.narg(target)::jsonb) END,
    decided_at = COALESCE(decided_at, @now::timestamptz)
WHERE id = @id AND outcome IS NULL
RETURNING *;

-- name: CancelRunPendingActions :exec
-- CancelRunPendingActions is a stopped run's undecided action.
UPDATE assistant.pending_actions SET
    status = 'cancelled',
    outcome = '{"reason":"stopped"}'::jsonb,
    request = assistant.redact_request(request),
    preview = assistant.redact_preview(preview),
    decided_at = @now::timestamptz
WHERE run_id = @run_id AND status = 'pending';

-- name: ExpirePendingActions :many
-- ExpirePendingActions expires the actions nobody decided in time and ends the
-- runs that waited for them. It is one statement and takes no second
-- connection.
WITH expired AS (
    UPDATE assistant.pending_actions SET
        status = 'expired',
        outcome = '{"reason":"expired"}'::jsonb,
        request = assistant.redact_request(request),
        preview = assistant.redact_preview(preview),
        decided_at = @now::timestamptz
    WHERE status = 'pending' AND expires_at <= @now::timestamptz
    RETURNING id, run_id
)
UPDATE assistant.runs r SET state = 'expired', ended_at = @now::timestamptz
FROM expired
WHERE r.id = expired.run_id AND r.state = 'awaiting_confirmation'
RETURNING r.id;

-- name: InterruptClaimedActions :execrows
-- InterruptClaimedActions closes the actions whose confirmation was claimed and
-- never recorded, because the server went away between: whether the change was
-- made is not known, so the action says it was interrupted. A bulk plan is not
-- one of them: its job holds its own claim and is resumed.
UPDATE assistant.pending_actions SET
    status = 'failed',
    outcome = '{"reason":"interrupted"}'::jsonb,
    request = assistant.redact_request(request),
    preview = assistant.redact_preview(preview)
WHERE status = 'confirmed' AND outcome IS NULL AND kind = 'change' AND decided_at < @cutoff::timestamptz;
