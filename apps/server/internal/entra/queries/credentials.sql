-- The client secrets (Entra design D11, credentials in the plural): each
-- sealed under entra/client-secret, never answered, revoked and never
-- deleted. The newest unrevoked one whose expiry is ahead is the one used.

-- name: ListCredentials :many
-- ListCredentials is every secret, revoked ones included, newest first — the
-- order the client tries them in, after the revoked and the expired are
-- skipped.
SELECT * FROM entra.credentials ORDER BY created_at DESC, id DESC;

-- name: InsertCredential :one
INSERT INTO entra.credentials (kind, label, sealed, expires_at, created_at, created_by_user_id)
VALUES (@kind, @label, @sealed, @expires_at, @now::timestamptz, @created_by_user_id)
RETURNING *;

-- name: RevokeCredential :one
-- RevokeCredential marks the secret revoked; one revoked already keeps the
-- instant it was. No row is returned for an unknown id.
UPDATE entra.credentials SET revoked_at = COALESCE(revoked_at, @now::timestamptz)
WHERE id = @id
RETURNING *;

-- name: MarkCredentialUsed :exec
-- MarkCredentialUsed records a token fetched with the secret, at most once a
-- minute (used_before is now less a minute), so a busy cycle writes the row
-- once rather than per call.
UPDATE entra.credentials SET last_used_at = @now::timestamptz
WHERE id = @id AND (last_used_at IS NULL OR last_used_at < @used_before::timestamptz);

-- name: MarkCredentialFailed :exec
-- MarkCredentialFailed records a token the tenant refused for the secret
-- (invalid_client, unauthorized_client).
UPDATE entra.credentials SET last_failed_at = @now::timestamptz WHERE id = @id;
