-- The lock statements of the receivables (invoices payments and reminders
-- design D18): each new path takes its row locks through these, by way of
-- locks.go's helpers, which report every lock to the lock-order seam. Each
-- statement that locks several rows orders them itself, so the order is the
-- statement's, never the planner's: own row first, then invoices in
-- descending id, never the reverse.

-- name: LockBankTransaction :one
-- LockBankTransaction takes a bank line FOR NO KEY UPDATE: its key never
-- changes, and FOR UPDATE would needlessly conflict with the KEY SHARE the
-- foreign keys onto it take (a payment or a charge payment naming it). Always
-- the first lock of a match and of a queue action (D18).
SELECT * FROM invoices.bank_transactions WHERE id = @id FOR NO KEY UPDATE;

-- name: UpsertImportAccounts :exec
-- UpsertImportAccounts inserts a bank file's accounts not seen before, with
-- the file's format — the account's first import sets it (D3 step 7.1) — in
-- account order, so two first imports of overlapping files queue on the same
-- rows in the same order (I18). An account already there, or named twice,
-- is left as it is. No DISTINCT: the ORDER BY alone sets the order, so a
-- plan's hashing never reorders it.
INSERT INTO invoices.bank_import_accounts (account, format, set_by_user_id, set_at)
SELECT a, sqlc.arg(format)::text, sqlc.arg(set_by_user_id)::uuid, sqlc.arg(set_at)::timestamptz
FROM unnest(sqlc.arg(accounts)::text[]) AS a
ORDER BY 1
ON CONFLICT (account) DO NOTHING;

-- name: ShareImportAccounts :many
-- ShareImportAccounts reads a bank file's accounts FOR SHARE in account
-- order, right after UpsertImportAccounts: the import's format check holds
-- against a format PUT until it commits, and two imports share them.
SELECT * FROM invoices.bank_import_accounts
WHERE account = ANY(sqlc.arg(accounts)::text[])
ORDER BY account
FOR SHARE;

-- name: LockImportAccount :one
-- LockImportAccount takes one account FOR UPDATE: the format PUT's only lock
-- (D3, D18).
SELECT * FROM invoices.bank_import_accounts WHERE account = @account FOR UPDATE;

-- name: LockReminder :one
-- LockReminder takes a letter FOR NO KEY UPDATE, for the same reason as a
-- bank line: a waiver's foreign key takes KEY SHARE on it.
SELECT * FROM invoices.reminders WHERE id = @id FOR NO KEY UPDATE;

-- name: LockPrintBatch :one
-- LockPrintBatch takes a print batch FOR NO KEY UPDATE, so the KEY SHARE its
-- letters' foreign keys take on it never conflicts (m11): a batch posted or
-- reprinted locks it first, then its letters' invoices in descending id.
SELECT * FROM invoices.reminder_print_batches WHERE id = @id FOR NO KEY UPDATE;

-- name: ShareCustomerDocuments :many
-- ShareCustomerDocuments reads one customer's documents FOR SHARE, newest
-- first — LockCustomerDocuments' order, so it cycles with no invoice-only
-- path — before a policy PUT judges the customer and locks the policy row
-- (D7, D18, plan reading 11).
SELECT id FROM invoices.invoices
WHERE customer_id = @customer_id
ORDER BY id DESC
FOR SHARE;

-- name: LockPolicies :many
-- LockPolicies takes the policy rows of customers FOR UPDATE in customer id
-- order: the merge's, after the documents (D7, D18). Customers without a row
-- lock nothing.
SELECT customer_id FROM invoices.customer_reminder_policies
WHERE customer_id = ANY(sqlc.arg(customer_ids)::integer[])
ORDER BY customer_id
FOR UPDATE;

-- name: LockInvoicesDescending :many
-- LockInvoicesDescending takes several invoices FOR UPDATE in descending id,
-- the module's invariant (R/invoices.md's lock order): the queue's apply and
-- handle-reversal, after their bank line, and a batch posted or reprinted,
-- after the batch.
SELECT * FROM invoices.invoices
WHERE id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY id DESC
FOR UPDATE;
