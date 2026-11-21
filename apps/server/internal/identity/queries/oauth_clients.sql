-- name: InsertOAuthClient :exec
-- InsertOAuthClient registers a client by hand (OAuth connections design C8).
INSERT INTO identity.oauth_clients (id, client_id, name, redirect_uris, created_by, created_at, updated_at, version)
VALUES (@id, @client_id, @name, @redirect_uris, sqlc.narg(created_by), @now::timestamptz, @now::timestamptz, @version);

-- name: ListOAuthClients :many
-- ListOAuthClients is the registered clients not archived, oldest first, or
-- the one id names, each with who registered it and how many of its
-- connections are live at now: activated, not revoked and not expired.
SELECT c.id, c.client_id, c.name, c.redirect_uris, c.created_by, u.display_name AS created_by_name,
       c.created_at, c.updated_at, c.version,
       (SELECT count(*) FROM identity.oauth_connections k
        WHERE k.client_id = c.client_id AND k.activated_at IS NOT NULL AND k.revoked_at IS NULL
          AND k.expires_at > @now::timestamptz)::int AS live_connections
FROM identity.oauth_clients c
LEFT JOIN identity.users u ON u.id = c.created_by
WHERE c.archived_at IS NULL AND (sqlc.narg(id)::uuid IS NULL OR c.id = sqlc.narg(id)::uuid)
ORDER BY c.created_at, c.id;

-- name: CountLiveOAuthClients :one
SELECT count(*)::int FROM identity.oauth_clients WHERE archived_at IS NULL;

-- name: LockOAuthClient :one
SELECT * FROM identity.oauth_clients WHERE id = @id FOR UPDATE;

-- name: UpdateOAuthClient :exec
UPDATE identity.oauth_clients
SET name = @name, redirect_uris = @redirect_uris, updated_at = @now::timestamptz, version = @version
WHERE id = @id;

-- name: ArchiveOAuthClient :exec
UPDATE identity.oauth_clients
SET archived_at = @now::timestamptz, archived_by = sqlc.narg(archived_by), updated_at = @now::timestamptz, version = @version
WHERE id = @id;

-- name: GetRegisteredOAuthClient :one
-- GetRegisteredOAuthClient is a registered client by its client_id, not
-- archived, with the name of the person who registered it for the consent
-- page (C13).
SELECT c.client_id, c.name, c.redirect_uris, u.display_name AS created_by_name
FROM identity.oauth_clients c
LEFT JOIN identity.users u ON u.id = c.created_by
WHERE c.client_id = @client_id AND c.archived_at IS NULL;

-- name: RevokeClientOAuthConnections :many
-- RevokeClientOAuthConnections ends every live connection of a registered
-- client being archived (C8), activated or not.
UPDATE identity.oauth_connections
SET revoked_at = sqlc.arg(now)::timestamptz, revoked_by = sqlc.narg(revoked_by), revoke_reason = 'client_archived'
WHERE client_id = sqlc.arg(client_id) AND client_kind = 'registered' AND revoked_at IS NULL
RETURNING *;

-- name: GetOAuthClientHosts :one
-- GetOAuthClientHosts is the client hosts that may connect by their metadata
-- document (C11); the singleton row may not exist yet, and then the list is
-- empty: any host.
SELECT coalesce(s.oauth_client_hosts, '{}')::text[] AS hosts
FROM (VALUES (1)) AS one (x)
LEFT JOIN identity.system_settings s ON s.id = 1;

-- name: LockOAuthClientHosts :one
-- LockOAuthClientHosts reads the list holding the singleton row, which the
-- caller has just ensured exists (EnsureSystemSettings), so two saves at once
-- queue and each audits the change it made.
SELECT oauth_client_hosts FROM identity.system_settings WHERE id = 1 FOR UPDATE;

-- name: SetOAuthClientHosts :exec
UPDATE identity.system_settings
SET oauth_client_hosts = @hosts::text[], updated_at = @now::timestamptz, updated_by_user_id = @updated_by_user_id
WHERE id = 1;

-- name: GetOAuthTokenConnection :one
-- GetOAuthTokenConnection is the connection a token of kind belongs to, for
-- the revocation endpoint (A10, A23): read without a lock, so the revocation
-- locks the connection first, as every path does.
SELECT connection_id
FROM identity.oauth_tokens
WHERE token_hash = @token_hash AND kind = @kind;
