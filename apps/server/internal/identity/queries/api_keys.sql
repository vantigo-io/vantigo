-- name: GetAPIKeyByTokenHash :one
-- GetAPIKeyByTokenHash is the one read an API key request costs (API access
-- design K17): the key by its hash through the unique index, its integration
-- and the integration's user with its roles. Revocation, archiving,
-- disabling, the kind and the expiry are judged by the caller, which says
-- which one refused the key.
SELECT ak.id, ak.integration_id, ak.permissions, ak.read_only, ak.allowed_networks, ak.expires_at,
       ak.revoked_at, ak.last_used_at,
       i.archived_at, i.rate_limit_per_minute,
       u.kind, u.is_disabled, u.lockout_end, r.role_names
FROM identity.api_keys ak
JOIN identity.integrations i ON i.user_id = ak.integration_id
JOIN identity.users u ON u.id = i.user_id
CROSS JOIN LATERAL (
    SELECT coalesce(array_agg(ro.name ORDER BY ro.name), '{}')::text[] AS role_names
    FROM identity.user_roles ur
    JOIN identity.roles ro ON ro.id = ur.role_id
    WHERE ur.user_id = u.id
) r
WHERE ak.token_hash = @token_hash;

-- name: GetAPIKeyScope :one
-- GetAPIKeyScope is what a key's scope needs of it: its integration, its
-- narrowing and whether it only reads.
SELECT integration_id, permissions, read_only
FROM identity.api_keys
WHERE id = @id;

-- name: TouchAPIKey :exec
-- TouchAPIKey records a key's use at most once a minute (K17).
UPDATE identity.api_keys
SET last_used_at = sqlc.arg(now)::timestamptz, last_used_ip = sqlc.narg(ip)
WHERE id = sqlc.arg(id)
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(now)::timestamptz - interval '1 minute');

-- name: InsertAPIKey :one
INSERT INTO identity.api_keys (
    id, integration_id, name, token_hash, display, permissions, read_only, allowed_networks,
    expires_at, created_by, created_at, rolled_from
) VALUES (
    sqlc.arg(id), sqlc.arg(integration_id), sqlc.arg(name), sqlc.arg(token_hash), sqlc.arg(display), sqlc.narg(permissions), sqlc.arg(read_only), sqlc.arg(allowed_networks),
    sqlc.arg(expires_at), sqlc.narg(created_by), sqlc.arg(now)::timestamptz, sqlc.narg(rolled_from)
)
RETURNING *;

-- name: ListIntegrationAPIKeys :many
-- ListIntegrationAPIKeys is an integration's keys, newest first.
SELECT *
FROM identity.api_keys
WHERE integration_id = @integration_id
ORDER BY created_at DESC, id;

-- name: LockAPIKey :one
SELECT *
FROM identity.api_keys
WHERE id = @id AND integration_id = @integration_id
FOR UPDATE;

-- name: ShortenAPIKeyExpiry :one
-- ShortenAPIKeyExpiry ends a rolled key's life at until, or keeps its own
-- expiry when that is earlier (K13).
UPDATE identity.api_keys
SET expires_at = least(expires_at, @until::timestamptz)
WHERE id = @id
RETURNING *;

-- name: RevokeAPIKey :one
UPDATE identity.api_keys
SET revoked_at = sqlc.arg(now)::timestamptz, revoked_by = sqlc.narg(revoked_by)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: RevokeIntegrationAPIKeys :many
-- RevokeIntegrationAPIKeys revokes every key of an integration not already
-- revoked, as archiving it does, and answers their ids.
UPDATE identity.api_keys
SET revoked_at = sqlc.arg(now)::timestamptz, revoked_by = sqlc.narg(revoked_by)
WHERE integration_id = sqlc.arg(integration_id) AND revoked_at IS NULL
RETURNING id;

-- name: ListUnrecordedExpiredAPIKeys :many
-- ListUnrecordedExpiredAPIKeys is the keys that expired by now, unrevoked,
-- whose expiry the worker has not audited yet, oldest first, a batch at a
-- time, each held so another cycle skips it.
SELECT ak.id, ak.integration_id, ak.name, ak.expires_at
FROM identity.api_keys ak
WHERE ak.revoked_at IS NULL AND ak.expires_at <= @now::timestamptz AND ak.expired_recorded_at IS NULL
ORDER BY ak.expires_at, ak.id
LIMIT @batch_size
FOR UPDATE SKIP LOCKED;

-- name: MarkAPIKeyExpiryRecorded :exec
UPDATE identity.api_keys SET expired_recorded_at = @now::timestamptz WHERE id = @id;

-- name: DeleteExpiredAPIKeys :execrows
-- DeleteExpiredAPIKeys deletes the keys whose expiry is older than cutoff
-- (90 days before now, K12), a batch at a time. Their audit rows stay.
DELETE FROM identity.api_keys
WHERE id IN (
    SELECT id FROM identity.api_keys
    WHERE expires_at < @cutoff::timestamptz
    ORDER BY expires_at
    LIMIT @batch_size
);
