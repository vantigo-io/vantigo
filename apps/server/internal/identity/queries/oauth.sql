-- name: GetOAuthAccessToken :one
-- GetOAuthAccessToken is the one read a request presenting an access token
-- costs (OAuth connections design C19, A10, A15, A17): the token by its hash
-- and kind, its connection, the integration and its user with its roles, and
-- whether the person who authorized the connection still may. The caller
-- judges each state and says which one refused the token.
SELECT t.connection_id, t.expires_at AS token_expires_at,
       c.integration_id, c.scopes, c.activated_at, c.expires_at AS connection_expires_at,
       c.revoked_at, c.last_used_at,
       i.archived_at, i.rate_limit_per_minute,
       u.kind, u.is_disabled, u.lockout_end, r.role_names,
       identity.oauth_authorizer_active(c.authorized_by, @scim_enabled::boolean)::boolean AS authorizer_active
FROM identity.oauth_tokens t
JOIN identity.oauth_connections c ON c.id = t.connection_id
JOIN identity.integrations i ON i.user_id = c.integration_id
JOIN identity.users u ON u.id = i.user_id
CROSS JOIN LATERAL (
    SELECT coalesce(array_agg(ro.name ORDER BY ro.name), '{}')::text[] AS role_names
    FROM identity.user_roles ur
    JOIN identity.roles ro ON ro.id = ur.role_id
    WHERE ur.user_id = u.id
) r
WHERE t.token_hash = @token_hash AND t.kind = 'access';

-- name: GetOAuthConnectionScope :one
-- GetOAuthConnectionScope is what a connection's scope needs of it: its
-- integration, its scopes and the integration's MCP setting (A15).
SELECT c.integration_id, c.scopes, i.mcp_access
FROM identity.oauth_connections c
JOIN identity.integrations i ON i.user_id = c.integration_id
WHERE c.id = @id;

-- name: TouchOAuthConnection :exec
-- TouchOAuthConnection records a connection's use at most once a minute (C20).
UPDATE identity.oauth_connections
SET last_used_at = sqlc.arg(now)::timestamptz, last_used_ip = sqlc.narg(ip)
WHERE id = sqlc.arg(id)
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(now)::timestamptz - interval '1 minute');

-- name: GetOAuthClientDocument :one
-- GetOAuthClientDocument is a cached client metadata document still fresh at
-- now (C7).
SELECT document
FROM identity.oauth_client_documents
WHERE client_id = @client_id AND expires_at > @now::timestamptz;

-- name: PutOAuthClientDocument :exec
INSERT INTO identity.oauth_client_documents (client_id, document, fetched_at, expires_at)
VALUES (@client_id, @document, @now::timestamptz, @expires_at::timestamptz)
ON CONFLICT (client_id) DO UPDATE
SET document = excluded.document, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: OAuthClientHostSeen :one
-- OAuthClientHostSeen is whether any client of the host has been connected
-- before, for the consent page's first-use notice (A7).
SELECT EXISTS (
    SELECT 1 FROM identity.oauth_connections WHERE client_host = @client_host AND activated_at IS NOT NULL
)::boolean;

-- name: InsertOAuthConnection :exec
INSERT INTO identity.oauth_connections (
    id, integration_id, client_id, client_kind, client_name, client_host, redirect_uri, scopes,
    authorized_by, created_at, expires_at
) VALUES (
    sqlc.arg(id), sqlc.arg(integration_id), sqlc.arg(client_id), sqlc.arg(client_kind), sqlc.arg(client_name),
    sqlc.arg(client_host), sqlc.arg(redirect_uri), sqlc.arg(scopes), sqlc.narg(authorized_by),
    sqlc.arg(now)::timestamptz, sqlc.arg(expires_at)::timestamptz
);

-- name: InsertOAuthCode :exec
INSERT INTO identity.oauth_codes (code_hash, connection_id, code_challenge, created_at, expires_at)
VALUES (@code_hash, @connection_id, @code_challenge, @now::timestamptz, @expires_at::timestamptz);

-- name: ConsumeOAuthCode :one
-- ConsumeOAuthCode spends a code once (C15): only an unconsumed, unexpired
-- code answers a row.
UPDATE identity.oauth_codes
SET consumed_at = @now::timestamptz
WHERE code_hash = @code_hash AND consumed_at IS NULL AND expires_at > @now::timestamptz
RETURNING connection_id, code_challenge;

-- name: GetOAuthCode :one
-- GetOAuthCode is a code that ConsumeOAuthCode did not spend: consumed
-- already (a replay) or expired.
SELECT connection_id, consumed_at
FROM identity.oauth_codes
WHERE code_hash = @code_hash;

-- name: LockOAuthConnection :one
SELECT *
FROM identity.oauth_connections
WHERE id = @id
FOR UPDATE;

-- name: ActivateOAuthConnection :exec
UPDATE identity.oauth_connections
SET activated_at = @now::timestamptz
WHERE id = @id AND activated_at IS NULL;

-- name: InsertOAuthToken :exec
INSERT INTO identity.oauth_tokens (token_hash, connection_id, kind, parent_hash, created_at, expires_at)
VALUES (@token_hash, @connection_id, @kind, sqlc.narg(parent_hash), @now::timestamptz, @expires_at::timestamptz);

-- name: LockOAuthRefreshToken :one
-- LockOAuthRefreshToken is a refresh token by its hash, held for the
-- refresh (A10, A11): never an access token, whose bytes may be the same.
SELECT *
FROM identity.oauth_tokens
WHERE token_hash = @token_hash AND kind = 'refresh'
FOR UPDATE;

-- name: CountOAuthTokenChildren :one
-- CountOAuthTokenChildren is how many refresh tokens were issued from the
-- refresh token parent: none for one superseded unspent (A11).
SELECT count(*)::int
FROM identity.oauth_tokens
WHERE parent_hash = @parent_hash AND kind = 'refresh';

-- name: SpendOAuthRefreshTokens :exec
-- SpendOAuthRefreshTokens marks every unspent refresh token of the
-- connection used, so the connection never has two live ones (A11).
UPDATE identity.oauth_tokens
SET used_at = @now::timestamptz
WHERE connection_id = @connection_id AND kind = 'refresh' AND used_at IS NULL;

-- name: RevokeOAuthConnection :one
-- RevokeOAuthConnection ends a live connection with its reason (C18); no row
-- when it has ended already.
UPDATE identity.oauth_connections
SET revoked_at = sqlc.arg(now)::timestamptz, revoked_by = sqlc.narg(revoked_by), revoke_reason = sqlc.arg(reason)::text
WHERE id = sqlc.arg(id) AND revoked_at IS NULL
RETURNING *;

-- name: RevokeIntegrationOAuthConnections :many
-- RevokeIntegrationOAuthConnections ends every live connection of an
-- integration, as archiving it does (A20).
UPDATE identity.oauth_connections
SET revoked_at = sqlc.arg(now)::timestamptz, revoked_by = sqlc.narg(revoked_by), revoke_reason = sqlc.arg(reason)::text
WHERE integration_id = sqlc.arg(integration_id) AND revoked_at IS NULL
RETURNING *;

-- name: RevokeAuthorizerOAuthConnections :many
-- RevokeAuthorizerOAuthConnections ends the live connections a person
-- authorized (A17): all of them when unconditional, as the person's delete
-- or disable does, else those whose authorizer no longer qualifies, as the
-- Owner role's removal does.
UPDATE identity.oauth_connections
SET revoked_at = sqlc.arg(now)::timestamptz, revoked_by = sqlc.narg(revoked_by), revoke_reason = 'authorizer_removed'
WHERE authorized_by = sqlc.arg(authorized_by) AND revoked_at IS NULL
  AND (sqlc.arg(unconditional)::boolean OR NOT identity.oauth_authorizer_active(authorized_by, sqlc.arg(scim_enabled)::boolean))
RETURNING *;

-- name: ListIntegrationOAuthConnections :many
-- ListIntegrationOAuthConnections is an integration's activated connections
-- that are neither revoked nor expired, newest first (C25), each with who
-- authorized it and whether that person still qualifies (A17).
SELECT c.id, c.client_id, c.client_kind, c.client_name, c.client_host, c.scopes, c.authorized_by,
       a.display_name AS authorized_by_name, c.activated_at, c.expires_at, c.last_used_at, c.last_used_ip,
       identity.oauth_authorizer_active(c.authorized_by, @scim_enabled::boolean)::boolean AS authorizer_active
FROM identity.oauth_connections c
LEFT JOIN identity.users a ON a.id = c.authorized_by
WHERE c.integration_id = @integration_id
  AND c.activated_at IS NOT NULL AND c.revoked_at IS NULL AND c.expires_at > @now::timestamptz
ORDER BY c.activated_at DESC, c.id;

-- name: LockIntegrationOAuthConnection :one
SELECT *
FROM identity.oauth_connections
WHERE id = @id AND integration_id = @integration_id
FOR UPDATE;

-- name: DeleteSpentOAuthCodes :execrows
-- DeleteSpentOAuthCodes deletes the codes consumed or expired more than an
-- hour ago (A21): a consumed one is kept an hour so a replay finds its
-- connection.
DELETE FROM identity.oauth_codes
WHERE expires_at < @cutoff::timestamptz OR consumed_at < @cutoff::timestamptz;

-- name: DeleteExpiredOAuthTokens :execrows
-- DeleteExpiredOAuthTokens deletes the tokens a day past their expiry (C26).
DELETE FROM identity.oauth_tokens WHERE expires_at < @cutoff::timestamptz;

-- name: DeleteUnactivatedOAuthConnections :execrows
-- DeleteUnactivatedOAuthConnections deletes the connections whose code was
-- never exchanged, created before cutoff (A21).
DELETE FROM identity.oauth_connections WHERE activated_at IS NULL AND created_at < @cutoff::timestamptz;

-- name: DeleteEndedOAuthConnections :execrows
-- DeleteEndedOAuthConnections deletes the connections revoked or expired
-- before cutoff, 90 days ago (C26). Their audit rows stay.
DELETE FROM identity.oauth_connections
WHERE revoked_at < @cutoff::timestamptz OR expires_at < @cutoff::timestamptz;

-- name: DeleteStaleOAuthClientDocuments :execrows
-- DeleteStaleOAuthClientDocuments deletes the documents a week past their
-- expiry (C26).
DELETE FROM identity.oauth_client_documents WHERE expires_at < @cutoff::timestamptz;
