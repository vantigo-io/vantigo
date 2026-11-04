-- The credentials (Google Workspace design G13): the service account's keys
-- and the Internal web client's secret, each sealed and never answered,
-- revoked and never deleted. Of a key only the four clear fields of its file
-- are kept.

-- name: ListCredentials :many
-- ListCredentials is every credential, revoked ones included, newest first.
SELECT * FROM google.credentials ORDER BY created_at DESC, id DESC;

-- name: InsertKey :one
INSERT INTO google.credentials (kind, client_email, client_id, private_key_id, project_id, sealed, created_at, created_by_user_id)
VALUES ('service_account_key', @client_email, @client_id, @private_key_id, @project_id, @sealed, @now::timestamptz, @created_by_user_id)
RETURNING *;

-- name: InsertClientSecret :one
INSERT INTO google.credentials (kind, sealed, created_at, created_by_user_id)
VALUES ('oauth_client_secret', @sealed, @now::timestamptz, @created_by_user_id)
RETURNING *;

-- name: RevokeCredential :one
-- RevokeCredential marks the credential revoked; one revoked already keeps
-- the instant it was. No row is returned for an unknown id.
UPDATE google.credentials SET revoked_at = COALESCE(revoked_at, @now::timestamptz)
WHERE id = @id
RETURNING *;

-- name: RevokeClientSecrets :many
-- RevokeClientSecrets revokes every live client secret but keep_id: a new
-- secret replaces the old.
UPDATE google.credentials SET revoked_at = @now::timestamptz
WHERE kind = 'oauth_client_secret' AND revoked_at IS NULL AND id <> @keep_id
RETURNING *;

-- name: MarkCredentialUsed :exec
-- MarkCredentialUsed records a token minted with the key, at most once a
-- minute (used_before is now less a minute).
UPDATE google.credentials SET last_used_at = @now::timestamptz
WHERE id = @id AND (last_used_at IS NULL OR last_used_at < @used_before::timestamptz);

-- name: MarkCredentialFailed :exec
-- MarkCredentialFailed records Google refusing the key (invalid_grant).
UPDATE google.credentials SET last_failed_at = @now::timestamptz WHERE id = @id;
