-- name: GetSessionByTokenHash :one
-- GetSessionByTokenHash resolves a session cookie in one query that also
-- rejects every state that ends a session: a revoked row; an idle or
-- absolute bound that has elapsed (the privileged pair applies while the
-- user currently holds Owner or SystemAdmin, decided here per request); a
-- disabled user; and, while SCIM is configured, a user the directory
-- deactivated unless they hold Owner (SV/ScimLifecycleService.cs:21-44).
-- Lockout does not end a session. Every bound is measured against the now
-- parameter, the caller's clock, never the database's: a session is rejected once
-- now - last_seen_at >= idle or now - created_at >= absolute
-- (SV/SessionValidationService.cs:83-93).
SELECT s.id, s.user_id, s.persistent, s.mfa_verified_at, s.created_at, s.last_seen_at,
       u.is_disabled, u.lockout_end,
       r.role_names, r.privileged,
       m.upstream_active AS scim_upstream_active
FROM identity.sessions s
JOIN identity.users u ON u.id = s.user_id
CROSS JOIN LATERAL (
    SELECT coalesce(array_agg(ro.name ORDER BY ro.name), '{}')::text[] AS role_names,
           coalesce(bool_or(ro.name IN ('Owner', 'SystemAdmin')), false)::boolean AS privileged,
           coalesce(bool_or(ro.name = 'Owner'), false)::boolean AS is_owner
    FROM identity.user_roles ur
    JOIN identity.roles ro ON ro.id = ur.role_id
    WHERE ur.user_id = s.user_id
) r
LEFT JOIN identity.scim_user_mappings m ON m.user_id = s.user_id
WHERE s.token_hash = @token_hash
  AND s.revoked_at IS NULL
  AND NOT u.is_disabled
  AND (NOT @scim_enabled::boolean OR m.upstream_active IS NOT FALSE OR r.is_owner)
  -- sqlc.arg, not @: PostgreSQL parses @ as a prefix operator that binds
  -- looser than -, which would swallow the whole subtraction.
  AND s.last_seen_at > sqlc.arg(now)::timestamptz - CASE WHEN r.privileged THEN sqlc.arg(privileged_idle)::interval ELSE sqlc.arg(idle)::interval END
  AND s.created_at > sqlc.arg(now)::timestamptz - CASE WHEN r.privileged THEN sqlc.arg(privileged_absolute)::interval ELSE sqlc.arg(absolute)::interval END;

-- name: TouchSession :exec
-- TouchSession slides a session's idle window to now. It never moves
-- last_seen_at backwards, so concurrent requests cannot undo each other.
UPDATE identity.sessions
SET last_seen_at = @now::timestamptz
WHERE id = @id AND last_seen_at < @now::timestamptz;

-- name: InsertSession :exec
INSERT INTO identity.sessions (
    id, user_id, token_hash, persistent, mfa_verified_at, created_at, last_seen_at, ip, user_agent
) VALUES (
    @id, @user_id, @token_hash, @persistent, @mfa_verified_at, @now::timestamptz, @now::timestamptz, @ip, @user_agent
);

-- name: RevokeSession :exec
UPDATE identity.sessions
SET revoked_at = @now::timestamptz
WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeUserSessions :exec
UPDATE identity.sessions
SET revoked_at = @now::timestamptz
WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: RevokeUserSessionsExcept :exec
UPDATE identity.sessions
SET revoked_at = @now::timestamptz
WHERE user_id = @user_id AND id <> @keep AND revoked_at IS NULL;

-- name: InsertLoginTicket :exec
INSERT INTO identity.login_tickets (token_hash, user_id, expires_at)
VALUES (@token_hash, @user_id, @expires_at::timestamptz);

-- name: DeleteExpiredLoginTickets :exec
DELETE FROM identity.login_tickets
WHERE expires_at <= @now::timestamptz;
