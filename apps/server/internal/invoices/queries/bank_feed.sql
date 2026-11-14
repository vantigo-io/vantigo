-- The bank feed's connection and accounts (invoices bank feed design F3, F4):
-- the one connection row, sealed, and the accounts a person switched on. The
-- lock statements are queries/locks.sql's (LockBankFeedConnection,
-- ShareBankFeedConnection), taken through locks.go. Every time is a
-- parameter, never now().

-- name: GetBankFeedConnection :one
-- GetBankFeedConnection reads the one connection row, unlocked. No row means
-- no key is stored, which the caller reads from pgx.ErrNoRows.
SELECT * FROM invoices.bank_feed_connections WHERE id = 1;

-- name: UpsertBankFeedConnection :one
-- UpsertBankFeedConnection stores a key the bank accepted (F3): the first, or
-- one over the stored row, which the statement locks — two PUTs at once store
-- one row, the later one's. A new key is ok until the bank refuses it, and
-- connected_by and connected_at are its own. updated_at is the key's version
-- (MarkBankFeedUnauthorized).
INSERT INTO invoices.bank_feed_connections (
    id, provider, secret_ciphertext, key_last4, status, rejected_at, connected_by_user_id, connected_at, updated_at
) VALUES (
    1, @provider, @secret_ciphertext, @key_last4, 'ok', NULL, @connected_by_user_id, @now::timestamptz, @now::timestamptz
)
ON CONFLICT (id) DO UPDATE SET
    provider = EXCLUDED.provider,
    secret_ciphertext = EXCLUDED.secret_ciphertext,
    key_last4 = EXCLUDED.key_last4,
    status = 'ok',
    rejected_at = NULL,
    connected_by_user_id = EXCLUDED.connected_by_user_id,
    connected_at = EXCLUDED.connected_at,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteBankFeedConnection :execrows
-- DeleteBankFeedConnection removes the key. The caller holds the row FOR
-- UPDATE and switches every account off in the same transaction.
DELETE FROM invoices.bank_feed_connections WHERE id = 1;

-- name: MarkBankFeedUnauthorized :exec
-- MarkBankFeedUnauthorized records that the bank refused the key (a 401 or
-- 403), keeping the first moment — only while the row is still the version
-- (@version, its updated_at) the caller opened the key from: a refusal of an
-- old key never flags a newer one.
UPDATE invoices.bank_feed_connections SET status = 'unauthorized', rejected_at = @now::timestamptz
WHERE id = 1 AND status = 'ok' AND updated_at = @version::timestamptz;

-- name: ReviveUnauthorizedBankFeedAccounts :exec
-- ReviveUnauthorizedBankFeedAccounts is a new key's: every account the bank
-- refused the old one for is ok again, and due now when it is switched on.
-- The caller holds the connection row.
UPDATE invoices.bank_feed_accounts SET
    status = 'ok', reason = NULL, detail = '', failures = 0,
    next_sync_at = CASE WHEN enabled THEN @now::timestamptz END,
    updated_at = @now::timestamptz
WHERE status = 'unauthorized';

-- name: DisableBankFeedAccounts :exec
-- DisableBankFeedAccounts switches every account off (the DELETE, F8): no
-- next sync, the lease gone, so a cycle in flight cannot write its account
-- back on. The caller holds the connection row FOR UPDATE.
UPDATE invoices.bank_feed_accounts SET
    enabled = false, next_sync_at = NULL, lease_id = NULL, lease_until = NULL, updated_at = @now::timestamptz
WHERE enabled OR lease_id IS NOT NULL OR next_sync_at IS NOT NULL;

-- name: ListBankFeedAccounts :many
-- ListBankFeedAccounts is every account the feed was ever switched on for, in
-- account order.
SELECT * FROM invoices.bank_feed_accounts ORDER BY account;

-- name: GetBankFeedAccount :one
-- GetBankFeedAccount is one account's feed row, unlocked: the format PUT
-- reads it under the account's bank import row FOR UPDATE, which every switch
-- of the feed takes first.
SELECT * FROM invoices.bank_feed_accounts WHERE account = @account;

-- name: EnableBankFeedAccount :one
-- EnableBankFeedAccount switches the feed on for one account from start_day
-- (F4): ok, no failures, due now. The caller holds the account's bank import
-- row FOR UPDATE and has set its format to feed. A lease held by a cycle in
-- flight is left to it.
INSERT INTO invoices.bank_feed_accounts (account, enabled, start_day, status, enabled_by_user_id, next_sync_at, updated_at)
VALUES (@account, true, @start_day, 'ok', @enabled_by_user_id, @now::timestamptz, @now::timestamptz)
ON CONFLICT (account) DO UPDATE SET
    enabled = true,
    start_day = EXCLUDED.start_day,
    status = 'ok',
    reason = NULL,
    detail = '',
    failures = 0,
    enabled_by_user_id = EXCLUDED.enabled_by_user_id,
    next_sync_at = EXCLUDED.next_sync_at,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DisableBankFeedAccount :one
-- DisableBankFeedAccount switches the feed off for one account: no next
-- sync, the lease gone. Its format is left as it is (F4). No row is an
-- account the feed was never switched on for.
UPDATE invoices.bank_feed_accounts SET
    enabled = false, next_sync_at = NULL, lease_id = NULL, lease_until = NULL, updated_at = @now::timestamptz
WHERE account = @account
RETURNING *;
