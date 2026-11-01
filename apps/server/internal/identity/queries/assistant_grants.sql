-- name: InsertAssistantGrant :exec
-- InsertAssistantGrant records a grant for the session it is bound to
-- (Access.Check resolves it; the assistant design S3).
INSERT INTO identity.assistant_grants (
    id, user_id, session_id, conversation_id, run_id, created_at, expires_at
) VALUES (
    @id, @user_id, @session_id, @conversation_id, @run_id, @now::timestamptz, @expires_at
);

-- name: GetCurrentAssistantGrant :one
-- GetCurrentAssistantGrant reads a grant that is neither revoked nor expired
-- at now. Whether its session is still valid, and its user may act, is
-- GetSessionByID's and Access.Check's: this answers only the grant's own state.
SELECT id, user_id, session_id, conversation_id, run_id, created_at, expires_at, revoked_at
FROM identity.assistant_grants
WHERE id = @id
  AND revoked_at IS NULL
  AND expires_at > @now::timestamptz;

-- name: RevokeAssistantGrant :exec
-- RevokeAssistantGrant ends one grant; one already ended keeps its time.
UPDATE identity.assistant_grants
SET revoked_at = @now::timestamptz
WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeUserAssistantGrants :exec
-- RevokeUserAssistantGrants ends every grant of a user.
UPDATE identity.assistant_grants
SET revoked_at = @now::timestamptz
WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: PurgeDeadAssistantGrants :exec
-- PurgeDeadAssistantGrants deletes a user's grants that ended more than a day
-- before now, expired or revoked, so a person's rows stay bounded by the
-- messages of the last day: one grant is issued per message.
DELETE FROM identity.assistant_grants
WHERE user_id = @user_id
  AND LEAST(expires_at, coalesce(revoked_at, expires_at)) < sqlc.arg(now)::timestamptz - interval '1 day';
