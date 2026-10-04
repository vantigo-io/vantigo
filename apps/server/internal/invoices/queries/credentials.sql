-- The access point's credentials (EHF and KID design D7): one row, id 1, its
-- API key sealed by the secrets box. Nothing here ever reads the key in
-- clear; the caller opens secret_ciphertext.

-- name: GetAccessPointCredentials :one
-- GetAccessPointCredentials reads the one credentials row. No row means none
-- is configured, which the caller reads from pgx.ErrNoRows.
SELECT * FROM invoices.access_point_credentials WHERE id = 1;

-- name: UpsertAccessPointCredentials :one
-- UpsertAccessPointCredentials writes the provider, its non-secret settings
-- and the sealed key, and clears rejected_at: a new PUT is a new key, or the
-- same key the operator says is good again.
INSERT INTO invoices.access_point_credentials (id, provider, settings_json, secret_ciphertext, rejected_at, updated_at)
VALUES (1, @provider, @settings_json, @secret_ciphertext, NULL, @now::timestamptz)
ON CONFLICT (id) DO UPDATE SET
    provider = EXCLUDED.provider,
    settings_json = EXCLUDED.settings_json,
    secret_ciphertext = EXCLUDED.secret_ciphertext,
    rejected_at = NULL,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteAccessPointCredentials :execrows
-- DeleteAccessPointCredentials removes the credentials. The caller has
-- checked that no transmission is in flight (409 transmissions_active).
DELETE FROM invoices.access_point_credentials WHERE id = 1;

-- name: MarkAccessPointRejected :exec
-- MarkAccessPointRejected records that the provider refused the key (a 401 or
-- 403, or a key the box could not open), keeping the first moment it did.
UPDATE invoices.access_point_credentials SET rejected_at = @now::timestamptz
WHERE id = 1 AND rejected_at IS NULL;

-- name: ClearAccessPointRejected :exec
-- ClearAccessPointRejected forgets a refusal once the provider accepted a
-- call again.
UPDATE invoices.access_point_credentials SET rejected_at = NULL
WHERE id = 1 AND rejected_at IS NOT NULL;
