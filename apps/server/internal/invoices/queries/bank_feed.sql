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
-- 403; F7), keeping the first moment — only while the row is still the
-- version (@version, its updated_at) the caller opened the key from: a
-- refusal of an old key never flags a newer one. Every account the feed is
-- switched on for stops with it: unauthorized, no next sync and no lease, so
-- a cycle in flight on another account finds its claim gone and stores
-- nothing, until a PUT stores a key again (ReviveUnauthorizedBankFeedAccounts).
WITH conn AS (
    UPDATE invoices.bank_feed_connections
    SET status = 'unauthorized', rejected_at = coalesce(rejected_at, @now::timestamptz)
    WHERE id = 1 AND updated_at = @version::timestamptz
    RETURNING id
)
UPDATE invoices.bank_feed_accounts SET
    status = 'unauthorized', reason = NULL, detail = '', next_sync_at = NULL, lease_id = NULL, lease_until = NULL,
    updated_at = CASE WHEN status <> 'unauthorized' THEN @now::timestamptz ELSE updated_at END
WHERE enabled AND EXISTS (SELECT 1 FROM conn);

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

-- name: MakeBankFeedAccountsDue :exec
-- MakeBankFeedAccountsDue is Fetch now (POST /invoices/bank-feed/sync, F5):
-- the account named, or every one when @account is NULL, that the feed is
-- switched on for and the bank did not refuse the key for, due now where it
-- was due later. The lease is left: a cycle in flight finishes and the next
-- claim takes the account. A refused account is made due too — its next read
-- is its own — and an unauthorized one waits for a new key.
UPDATE invoices.bank_feed_accounts SET next_sync_at = @now::timestamptz
WHERE enabled AND status <> 'unauthorized'
  AND (sqlc.narg(account)::text IS NULL OR account = sqlc.narg(account)::text)
  AND (next_sync_at IS NULL OR next_sync_at > @now::timestamptz);

-- name: StoredFeedLines :many
-- StoredFeedLines is what a sync already stored of the transactions it read
-- (F5 step 4): every live line of @account with one of @fingerprints, and
-- every feed line of it with one of @line_refs — the bank's own id, which
-- stays the same when the bank books the transaction on another day than it
-- first did. A transaction either names is the same record read again.
SELECT fingerprint, line_ref FROM invoices.bank_transactions
WHERE account = @account
  AND ((duplicate_of_id IS NULL AND fingerprint = ANY (sqlc.arg(fingerprints)::text[]))
       OR (format = 'feed' AND line_ref = ANY (sqlc.arg(line_refs)::text[])));

-- name: PendingFeedFilesOfAccount :many
-- PendingFeedFilesOfAccount is every feed batch with a line of @account
-- still pending, oldest first: what an earlier cycle stored and did not get
-- to match — it stopped, or the process ended between the commit and the
-- match — which every cycle matches again.
SELECT DISTINCT bank_file_id FROM invoices.bank_transactions
WHERE account = @account AND format = 'feed' AND status = 'pending'
ORDER BY bank_file_id;

-- name: ClaimBankFeedAccount :one
-- ClaimBankFeedAccount leases the next account due by @now whose lease, if
-- any, has run out (F5, F7): switched on, due, and only while the stored key
-- is ok — the payment worker's claim (ClaimPaymentAttempt). One statement:
-- the row lock FOR NO KEY UPDATE SKIP LOCKED is held for it alone, so two
-- workers never take one account, and the claim holds no connection while it
-- works. No row is pgx.ErrNoRows.
UPDATE invoices.bank_feed_accounts
SET lease_id = @lease_id::text, lease_until = @lease_until::timestamptz
WHERE account = (
    SELECT a.account FROM invoices.bank_feed_accounts a
    WHERE a.enabled AND a.next_sync_at <= @now::timestamptz
      AND (a.lease_until IS NULL OR a.lease_until <= @now::timestamptz)
      AND EXISTS (SELECT 1 FROM invoices.bank_feed_connections c WHERE c.id = 1 AND c.status = 'ok')
    ORDER BY a.next_sync_at, a.account
    LIMIT 1
    FOR NO KEY UPDATE SKIP LOCKED
)
RETURNING *;

-- name: BankFeedSynced :execrows
-- BankFeedSynced ends a claim whose read succeeded (F7): ok, no failures,
-- the reason and detail cleared, last_synced_at the claim's time, the batch
-- it stored when it stored one, read again at @next_sync_at. Only under the
-- claim's lease, which it clears; no row is a claim lost. updated_at moves
-- only with the status, so attention dates an item by its change.
UPDATE invoices.bank_feed_accounts SET
    status = 'ok', reason = NULL, detail = '', failures = 0, last_synced_at = @now::timestamptz,
    last_bank_file_id = coalesce(sqlc.narg(last_bank_file_id)::bigint, last_bank_file_id),
    next_sync_at = @next_sync_at::timestamptz, lease_id = NULL, lease_until = NULL,
    updated_at = CASE WHEN status <> 'ok' THEN @now::timestamptz ELSE updated_at END
WHERE account = @account AND lease_id = @lease_id::text;

-- name: BankFeedRateLimited :execrows
-- BankFeedRateLimited ends a claim the bank asked to wait (a 429; F7): the
-- status and the failures as they were, the wait in detail, read again at
-- @next_sync_at.
UPDATE invoices.bank_feed_accounts SET
    detail = @detail, next_sync_at = @next_sync_at::timestamptz, lease_id = NULL, lease_until = NULL
WHERE account = @account AND lease_id = @lease_id::text;

-- name: BankFeedFailed :execrows
-- BankFeedFailed ends a claim whose read failed (F7): one more failure in a
-- row, failing from the third, what failed in detail, read again at
-- @next_sync_at — the backoff the caller worked out from the count.
UPDATE invoices.bank_feed_accounts SET
    failures = failures + 1,
    status = CASE WHEN failures + 1 >= @failing_after::integer THEN 'failing' ELSE status END,
    detail = @detail, next_sync_at = @next_sync_at::timestamptz, lease_id = NULL, lease_until = NULL,
    updated_at = CASE WHEN failures + 1 >= @failing_after::integer AND status <> 'failing'
        THEN @now::timestamptz ELSE updated_at END
WHERE account = @account AND lease_id = @lease_id::text;

-- name: BankFeedRefused :execrows
-- BankFeedRefused ends a claim whose batch broke a rule (F5 step 2, F7):
-- refused, the rule in reason and the transaction's id in detail, no
-- failures, read again at @next_sync_at. Nothing of the batch was stored.
UPDATE invoices.bank_feed_accounts SET
    status = 'refused', reason = @reason::text, detail = @detail, failures = 0,
    next_sync_at = @next_sync_at::timestamptz, lease_id = NULL, lease_until = NULL,
    updated_at = CASE WHEN status <> 'refused' OR reason IS DISTINCT FROM @reason::text THEN @now::timestamptz ELSE updated_at END
WHERE account = @account AND lease_id = @lease_id::text;

-- name: ReleaseBankFeedClaim :execrows
-- ReleaseBankFeedClaim ends a claim whose key the bank refused but whose
-- refusal recorded nothing — a newer key was stored while the call was out —
-- with nothing written but the lease cleared and the account due now, for
-- the newer key. After MarkBankFeedUnauthorized the lease is gone and it
-- writes nothing.
UPDATE invoices.bank_feed_accounts SET next_sync_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
WHERE account = @account AND lease_id = @lease_id::text;
