-- name: InsertAgentGrant :exec
-- InsertAgentGrant records a grant for one run of one agent (Access.Check
-- resolves it; virtual employees design V6).
INSERT INTO identity.agent_grants (
    id, user_id, agent_id, run_id, customer_id, approved_by_user_id, created_at, expires_at
) VALUES (
    @id, @user_id, @agent_id, @run_id, @customer_id, @approved_by_user_id, @now::timestamptz, @expires_at
);

-- name: GetCurrentAgentGrant :one
-- GetCurrentAgentGrant reads a grant that is neither revoked nor expired at
-- now. Whether its user is still an active agent is Access.Check's: this
-- answers only the grant's own state.
SELECT id, user_id, agent_id, run_id, customer_id, approved_by_user_id, created_at, expires_at, revoked_at
FROM identity.agent_grants
WHERE id = @id
  AND revoked_at IS NULL
  AND expires_at > @now::timestamptz;

-- name: RevokeAgentGrant :exec
-- RevokeAgentGrant ends one grant; one already ended keeps its time.
UPDATE identity.agent_grants
SET revoked_at = @now::timestamptz
WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeUserAgentGrants :exec
-- RevokeUserAgentGrants ends every grant of a user.
UPDATE identity.agent_grants
SET revoked_at = @now::timestamptz
WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: PurgeDeadAgentGrants :exec
-- PurgeDeadAgentGrants deletes a user's grants that ended more than a day
-- before now, expired or revoked: one grant is issued per run.
DELETE FROM identity.agent_grants
WHERE user_id = @user_id
  AND LEAST(expires_at, coalesce(revoked_at, expires_at)) < sqlc.arg(now)::timestamptz - interval '1 day';
