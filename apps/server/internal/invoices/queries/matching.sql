-- Matching on KID (invoices payments and reminders design D4): a file's
-- pending lines, what a line's KID names, the soft key, the rows a match
-- registers, a line matched or queued — each only from pending — and its
-- events. Every time is a parameter, never now().

-- name: PendingTransactionsOfFile :many
-- PendingTransactionsOfFile is a file's lines still to be matched — pending
-- and live (a duplicate row is never pending) — in the order they were
-- stored. Read on the pool; each line is locked again before it is matched.
SELECT * FROM invoices.bank_transactions
WHERE bank_file_id = @bank_file_id AND status = 'pending' AND duplicate_of_id IS NULL
ORDER BY id;

-- name: ImportAccount :one
-- ImportAccount is one receiving account's format and cutover, read on the
-- pool for the classification's possible-duplicate step (D4 step 2).
SELECT * FROM invoices.bank_import_accounts WHERE account = @account;

-- name: InvoiceForKid :one
-- InvoiceForKid is the issued document numbered number (ux_invoices_number,
-- so at most one), with its stored kid and kid_algorithm, for D4 step 6:
-- the caller compares the kid exactly and verifies it. No index on kid: the
-- number is the KID's body (R4 §5.2). A credit note's kid is NULL.
SELECT * FROM invoices.invoices WHERE number = @number AND status = 'issued';

-- name: SoftKeyRegistered :one
-- SoftKeyRegistered reports whether a line of another file on the same
-- account, booking day, amount and KID has a live payment or charge payment
-- (D4 step 2, R4's soft key; ix_bank_transactions_soft), or had one a
-- reversal took back (its reversed event, D5): the same payment read again
-- from another notification, never registered again by itself. Read on the pool to classify, and
-- again under the invoice's lock before anything is registered.
SELECT EXISTS (
    SELECT 1 FROM invoices.bank_transactions t
    WHERE t.account = @account AND t.booked_on = @booked_on AND t.amount = @amount AND t.kid = @kid::text
      AND t.kid IS NOT NULL AND t.bank_file_id <> @bank_file_id
      AND (EXISTS (SELECT 1 FROM invoices.payments p WHERE p.bank_transaction_id = t.id AND p.removed_at IS NULL)
        OR EXISTS (SELECT 1 FROM invoices.charge_payments c WHERE c.bank_transaction_id = t.id AND c.removed_at IS NULL)
        OR EXISTS (SELECT 1 FROM invoices.bank_transaction_events e WHERE e.bank_transaction_id = t.id AND e.event = 'reversed'))
) AS registered;

-- name: InsertImportedPayment :one
-- InsertImportedPayment registers a payment a bank line was matched or
-- applied to (D2, D4): its source the file's format, the line, paid on the
-- line's booking day, its reference the KID, registered by the uploader or
-- the caller. The caller holds the line and the invoice and has judged the
-- open amount under the invoice's lock; ck_payments_origin holds the origin.
INSERT INTO invoices.payments (
    invoice_id, paid_on, amount, currency, source, bank_transaction_id, reference, registered_by_user_id, registered_at
) VALUES (
    @invoice_id, @paid_on, @amount, @currency, @source, @bank_transaction_id::bigint, @reference,
    @registered_by_user_id::uuid, @registered_at::timestamptz
)
RETURNING *;

-- name: MarkMatched :execrows
-- MarkMatched makes a pending line matched (D4); a line no longer pending
-- matches no row.
UPDATE invoices.bank_transactions SET status = 'matched'
WHERE id = @id AND status = 'pending';

-- name: QueueTransaction :execrows
-- QueueTransaction makes a pending line an exception with its reason (D4,
-- D5) and the invoice its KID named, when it named one, as the suggestion.
-- Only from pending: classification runs on the pool, and a concurrent match
-- of the same file may have matched or queued the line meanwhile — a zero
-- count is that, already done, not an error.
UPDATE invoices.bank_transactions
SET status = 'exception', reason = @reason::text, suggested_invoice_id = sqlc.narg(suggested_invoice_id)::bigint
WHERE id = @id AND status = 'pending';

-- name: InsertBankTransactionEvent :exec
-- InsertBankTransactionEvent records what happened to a line, by whom and
-- when (D5): matched, queued with its reason, and the queue's actions.
INSERT INTO invoices.bank_transaction_events (bank_transaction_id, event, reason, note, by_user_id, at)
VALUES (@bank_transaction_id, @event, sqlc.narg(reason)::text, @note, @by_user_id, @at::timestamptz);
