-- The payment provider's credentials and the payment settings (invoices
-- payments and reminders design D14): two one-row tables, id 1. The
-- credentials' keys are sealed by the secrets box; nothing here ever reads
-- them in clear — the caller opens secret_ciphertext. Their locks are
-- queries/locks.sql's (LockPaymentCredentials, SharePaymentCredentials,
-- LockPaymentSettings), taken through locks.go.

-- name: GetPaymentCredentials :one
-- GetPaymentCredentials reads the one credentials row, unlocked. No row means
-- none is stored, which the caller reads from pgx.ErrNoRows.
SELECT * FROM invoices.payment_provider_credentials WHERE id = 1;

-- name: InsertPaymentCredentials :one
-- InsertPaymentCredentials writes the first credentials row. A concurrent
-- first PUT that inserted it already makes this answer no row — the insert
-- waits for that transaction and does nothing once it committed — and the
-- caller runs again on the locked path, where the sales unit's change is
-- judged against the live attempts (never written over unjudged).
INSERT INTO invoices.payment_provider_credentials (id, provider, settings_json, secret_ciphertext, rejected_at, updated_at)
VALUES (1, @provider, @settings_json, @secret_ciphertext, NULL, @now::timestamptz)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: UpdatePaymentCredentials :one
-- UpdatePaymentCredentials writes the sales unit and the sealed keys over the
-- row the caller holds FOR UPDATE, and clears rejected_at: a new PUT is new
-- keys, or the same keys the operator says are good again. updated_at is the
-- credentials' version (providerCreds.version). A webhook registration is
-- left as it is.
UPDATE invoices.payment_provider_credentials SET
    provider = @provider,
    settings_json = @settings_json,
    secret_ciphertext = @secret_ciphertext,
    rejected_at = NULL,
    updated_at = @now::timestamptz
WHERE id = 1
RETURNING *;

-- name: DeletePaymentCredentials :execrows
-- DeletePaymentCredentials removes the credentials. The caller has counted
-- the live attempts under the row's lock (409 payment_attempts_active).
DELETE FROM invoices.payment_provider_credentials WHERE id = 1;

-- name: MarkPaymentCredentialsRejected :exec
-- MarkPaymentCredentialsRejected records that the provider refused the keys
-- (a 401 or 403) or that they could not be opened, keeping the first moment
-- — only while the row is still the version (@version, its updated_at) the
-- caller read the keys from: a refusal of old keys never flags newer ones.
UPDATE invoices.payment_provider_credentials SET rejected_at = @now::timestamptz
WHERE id = 1 AND rejected_at IS NULL AND updated_at = @version::timestamptz;

-- name: ClearPaymentCredentialsRejected :exec
-- ClearPaymentCredentialsRejected forgets a refusal once a verify succeeded —
-- only while the row is still the version (@version, its updated_at) the
-- verify read the keys from: old keys accepted never clear a refusal of newer
-- ones.
UPDATE invoices.payment_provider_credentials SET rejected_at = NULL
WHERE id = 1 AND rejected_at IS NOT NULL AND updated_at = @version::timestamptz;

-- name: LivePaymentAttemptsCount :one
-- LivePaymentAttemptsCount counts the attempts the credentials still serve
-- (D14, I20): creating, created, authorized, capturing, capture_unknown or
-- cancelling, or with a release pending. A credentials PUT that changes the
-- sales unit or its keys, and a DELETE, count them under the credentials
-- row's FOR UPDATE.
SELECT count(*) FROM invoices.payment_attempts
WHERE state IN ('creating', 'created', 'authorized', 'capturing', 'capture_unknown', 'cancelling')
   OR release_pending;

-- name: GetPaymentSettings :one
-- GetPaymentSettings reads the one payment settings row, which the migration
-- inserted and a trigger keeps.
SELECT * FROM invoices.payment_settings WHERE id = 1;

-- name: UpdatePaymentSettings :one
-- UpdatePaymentSettings replaces the settings when @revision is the row's —
-- the caller holds the row FOR UPDATE and has compared it — and writes the
-- kontantsalg acknowledgement the caller decided: kept, recorded or cleared.
UPDATE invoices.payment_settings SET
    pay_links_enabled = @pay_links_enabled,
    terms_url = sqlc.narg(terms_url),
    privacy_url = sqlc.narg(privacy_url),
    on_site_payments_enabled = @on_site_payments_enabled,
    kontantsalg_acknowledged_by_user_id = sqlc.narg(acknowledged_by),
    kontantsalg_acknowledged_at = sqlc.narg(acknowledged_at),
    revision = revision + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = @by::uuid
WHERE id = 1 AND revision = @revision
RETURNING *;
