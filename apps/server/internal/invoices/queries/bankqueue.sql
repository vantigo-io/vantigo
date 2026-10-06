-- The exception queue (invoices payments and reminders design D5): the bank
-- lines listed with what is applied from them, their events, the line a
-- possible duplicate may repeat, the suggestions for a queued line, and the
-- queue's writes to a line — each under the line's lock, FOR NO KEY UPDATE
-- (LockBankTransaction), taken first. Every time is a parameter, never now().

-- name: CountBankTransactions :one
-- CountBankTransactions is ListBankTransactions' total, over the same
-- filters and the same lateral sum, word for word.
SELECT count(*)::int
FROM invoices.bank_transactions t
CROSS JOIN LATERAL (
    SELECT (coalesce((SELECT sum(p.amount) FROM invoices.payments p WHERE p.bank_transaction_id = t.id AND p.removed_at IS NULL), 0)
          + coalesce((SELECT sum(c.amount) FROM invoices.charge_payments c WHERE c.bank_transaction_id = t.id AND c.removed_at IS NULL), 0)
           )::numeric(14,2) AS applied
) a
WHERE (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
  AND (sqlc.narg(reason)::text IS NULL OR t.reason = sqlc.narg(reason)::text)
  AND (sqlc.narg(bank_file_id)::bigint IS NULL OR t.bank_file_id = sqlc.narg(bank_file_id)::bigint)
  AND (sqlc.narg(from_on)::date IS NULL OR t.booked_on >= sqlc.narg(from_on)::date)
  AND (sqlc.narg(to_on)::date IS NULL OR t.booked_on <= sqlc.narg(to_on)::date)
  AND (NOT @unapplied::boolean
       OR (t.status IN ('matched', 'resolved') AND t.direction = 'credit' AND NOT t.negative
           AND (coalesce(t.resolution, 'applied') = 'applied'
                OR (t.resolution = 'not_customer_payment' AND t.reason IN ('invoice_credited', 'invoice_settled', 'exceeds_open')))
           AND NOT EXISTS (SELECT 1 FROM invoices.bank_transaction_events e WHERE e.bank_transaction_id = t.id AND e.event = 'reversed')
           AND t.amount > a.applied));

-- name: ListBankTransactions :many
-- ListBankTransactions is a page of the bank lines (D5), each with what its
-- live payments and charge payments apply: the open lines — pending,
-- exception, duplicate — first, each group oldest booking day first, then by
-- id. unapplied keeps the matched and resolved credit lines with a rest —
-- a line resolved otherwise than applied, a reversal and a negative line are
-- not money waiting to be applied, but a line dismissed after it was queued
-- invoice_credited, invoice_settled or exceeds_open is money owed back; a
-- line whose payment a reversal took back is never money to apply again.
SELECT sqlc.embed(t)
FROM invoices.bank_transactions t
CROSS JOIN LATERAL (
    SELECT (coalesce((SELECT sum(p.amount) FROM invoices.payments p WHERE p.bank_transaction_id = t.id AND p.removed_at IS NULL), 0)
          + coalesce((SELECT sum(c.amount) FROM invoices.charge_payments c WHERE c.bank_transaction_id = t.id AND c.removed_at IS NULL), 0)
           )::numeric(14,2) AS applied
) a
WHERE (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
  AND (sqlc.narg(reason)::text IS NULL OR t.reason = sqlc.narg(reason)::text)
  AND (sqlc.narg(bank_file_id)::bigint IS NULL OR t.bank_file_id = sqlc.narg(bank_file_id)::bigint)
  AND (sqlc.narg(from_on)::date IS NULL OR t.booked_on >= sqlc.narg(from_on)::date)
  AND (sqlc.narg(to_on)::date IS NULL OR t.booked_on <= sqlc.narg(to_on)::date)
  AND (NOT @unapplied::boolean
       OR (t.status IN ('matched', 'resolved') AND t.direction = 'credit' AND NOT t.negative
           AND (coalesce(t.resolution, 'applied') = 'applied'
                OR (t.resolution = 'not_customer_payment' AND t.reason IN ('invoice_credited', 'invoice_settled', 'exceeds_open')))
           AND NOT EXISTS (SELECT 1 FROM invoices.bank_transaction_events e WHERE e.bank_transaction_id = t.id AND e.event = 'reversed')
           AND t.amount > a.applied))
ORDER BY (t.status IN ('pending', 'exception', 'duplicate')) DESC, t.booked_on, t.id
LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: GetBankTransaction :one
-- GetBankTransaction is one line, read on the pool before its action's
-- transaction (the 404 and the first judgement) and for its answer.
SELECT * FROM invoices.bank_transactions WHERE id = @id;

-- name: BankFileRefs :many
-- BankFileRefs is the files the lines came in: id, format, upload time.
SELECT id, format, uploaded_at FROM invoices.bank_files WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: AppliedToTransactions :many
-- AppliedToTransactions is every payment and charge payment that refers to
-- one of the lines, removed ones included, with its invoice's number — per
-- line, the payments first, each kind by id (D5).
SELECT 'payment'::text AS kind, p.id, p.bank_transaction_id::bigint AS bank_transaction_id, p.invoice_id, i.number,
       p.amount, (p.removed_at IS NOT NULL)::boolean AS removed
FROM invoices.payments p
JOIN invoices.invoices i ON i.id = p.invoice_id
WHERE p.bank_transaction_id = ANY(sqlc.arg(ids)::bigint[])
UNION ALL
SELECT 'charge_payment'::text AS kind, c.id, c.bank_transaction_id::bigint AS bank_transaction_id, c.invoice_id, i.number,
       c.amount, (c.removed_at IS NOT NULL)::boolean AS removed
FROM invoices.charge_payments c
JOIN invoices.invoices i ON i.id = c.invoice_id
WHERE c.bank_transaction_id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY bank_transaction_id, kind DESC, id;

-- name: LiveAppliedTo :one
-- LiveAppliedTo is what a line's live payments and charge payments apply,
-- and how many there are: read under the line's lock by the apply (what is
-- left of it) and the reopen (bank_transaction_applied). A payment or charge
-- payment naming the line takes FOR KEY SHARE on it, which its FOR NO KEY
-- UPDATE lets through — only the queue's apply and a match write one, and
-- both hold the line first.
SELECT count(*)::int AS live,
       coalesce(sum(amount), 0)::numeric(14,2) AS applied
FROM (
    SELECT p.amount FROM invoices.payments p WHERE p.bank_transaction_id = sqlc.arg(line_id)::bigint AND p.removed_at IS NULL
    UNION ALL
    SELECT c.amount FROM invoices.charge_payments c WHERE c.bank_transaction_id = sqlc.arg(line_id)::bigint AND c.removed_at IS NULL
) live;

-- name: LineReversed :one
-- LineReversed reports whether a reversal took back a payment of the line
-- (its reversed event, D5): its money is never applied again, so it is not
-- reopened.
SELECT EXISTS (
    SELECT 1 FROM invoices.bank_transaction_events
    WHERE bank_transaction_id = @line_id AND event = 'reversed'
) AS reversed;

-- name: EventsOf :many
-- EventsOf is what happened to the lines, per line the first first.
SELECT * FROM invoices.bank_transaction_events
WHERE bank_transaction_id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY bank_transaction_id, id;

-- name: TwinsOf :many
-- TwinsOf is, per possible duplicate or duplicate line, the line it may
-- repeat (D5): the one it was kept as a duplicate of, or else the earliest
-- matched or resolved line of another file with the same account, booking
-- day, amount and KID — one with a live payment or charge payment first —
-- and whether a reversal took back a payment of it (its reversed event).
SELECT DISTINCT ON (l.id) l.id AS line_id, sqlc.embed(t),
       EXISTS (SELECT 1 FROM invoices.bank_transaction_events e
               WHERE e.bank_transaction_id = t.id AND e.event = 'reversed') AS twin_reversed
FROM invoices.bank_transactions l
JOIN invoices.bank_transactions t
  ON t.id <> l.id
 AND (t.id = l.duplicate_of_id
      OR (l.duplicate_of_id IS NULL AND t.account = l.account AND t.booked_on = l.booked_on AND t.amount = l.amount
          AND t.kid IS NOT DISTINCT FROM l.kid AND t.bank_file_id <> l.bank_file_id AND t.status IN ('matched', 'resolved')))
WHERE l.id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY l.id,
         coalesce(t.id = l.duplicate_of_id, false) DESC,
         (EXISTS (SELECT 1 FROM invoices.payments p WHERE p.bank_transaction_id = t.id AND p.removed_at IS NULL)
          OR EXISTS (SELECT 1 FROM invoices.charge_payments c WHERE c.bank_transaction_id = t.id AND c.removed_at IS NULL)) DESC,
         t.id;

-- name: InvoicesByNumbers :many
-- InvoicesByNumbers is the issued invoices whose number is one of numbers —
-- the whole-word digit runs of a line's text — with their open amount
-- (gross less issued credit notes less live payments), a suggestion's
-- number_in_text (D5). Credit notes are not paid.
SELECT i.id, i.number::bigint AS number, i.customer_id, i.buyer_name,
       (i.gross_total - coalesce(cr.credited, 0) - coalesce(pd.paid, 0))::numeric(14,2) AS open_amount
FROM invoices.invoices i
LEFT JOIN LATERAL (
    SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited
    FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
) cr ON true
LEFT JOIN LATERAL (
    SELECT coalesce(sum(p.amount), 0)::numeric(14,2) AS paid
    FROM invoices.payments p WHERE p.invoice_id = i.id AND p.removed_at IS NULL
) pd ON true
WHERE i.kind = 'invoice' AND i.status = 'issued' AND i.number = ANY(sqlc.arg(numbers)::bigint[])
ORDER BY i.id
LIMIT 10;

-- name: InvoicesOpenEqual :many
-- InvoicesOpenEqual is the issued invoices whose open amount equals amount,
-- compared by value — a suggestion's amount_equals_open (D5) — the first ten
-- by id.
SELECT i.id, i.number::bigint AS number, i.customer_id, i.buyer_name, o.open_amount
FROM invoices.invoices i
LEFT JOIN LATERAL (
    SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited
    FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
) cr ON true
LEFT JOIN LATERAL (
    SELECT coalesce(sum(p.amount), 0)::numeric(14,2) AS paid
    FROM invoices.payments p WHERE p.invoice_id = i.id AND p.removed_at IS NULL
) pd ON true
CROSS JOIN LATERAL (
    SELECT (i.gross_total - coalesce(cr.credited, 0) - coalesce(pd.paid, 0))::numeric(14,2) AS open_amount
) o
WHERE i.kind = 'invoice' AND i.status = 'issued' AND o.open_amount = @amount::numeric
ORDER BY i.id
LIMIT 10;

-- name: InvoicesOfDebtorAccount :many
-- InvoicesOfDebtorAccount is the open issued invoices of the customers whose
-- earlier payments from a bank line came from debtor_account — a line
-- booked on or before booked_on, never the line itself — a suggestion's
-- debtor_account (D5), the first ten by id.
SELECT i.id, i.number::bigint AS number, i.customer_id, i.buyer_name, o.open_amount
FROM invoices.invoices i
LEFT JOIN LATERAL (
    SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited
    FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
) cr ON true
LEFT JOIN LATERAL (
    SELECT coalesce(sum(p.amount), 0)::numeric(14,2) AS paid
    FROM invoices.payments p WHERE p.invoice_id = i.id AND p.removed_at IS NULL
) pd ON true
CROSS JOIN LATERAL (
    SELECT (i.gross_total - coalesce(cr.credited, 0) - coalesce(pd.paid, 0))::numeric(14,2) AS open_amount
) o
WHERE i.kind = 'invoice' AND i.status = 'issued' AND o.open_amount > 0
  AND i.customer_id IN (
      SELECT paid.customer_id FROM invoices.payments p
      JOIN invoices.bank_transactions t ON t.id = p.bank_transaction_id
      JOIN invoices.invoices paid ON paid.id = p.invoice_id
      WHERE t.debtor_account = @debtor_account::text AND t.id <> @line_id AND t.booked_on <= @booked_on::date
        AND p.removed_at IS NULL)
ORDER BY i.id
LIMIT 10;

-- name: ResolveTransaction :execrows
-- ResolveTransaction resolves a line the caller holds (D5): applied,
-- not_customer_payment, reversal_handled or duplicate_confirmed, by and at,
-- the note kept; reason is the line's — confirm-duplicate's
-- possible_duplicate where it had none (a reason is never cleared). Only
-- from the statuses the action takes.
UPDATE invoices.bank_transactions
SET status = 'resolved', resolution = @resolution::text, reason = @reason::text,
    resolved_by_user_id = @resolved_by_user_id::uuid, resolved_at = @resolved_at::timestamptz,
    resolution_note = @resolution_note::text
WHERE id = @id AND status = ANY(sqlc.arg(from_statuses)::text[]);

-- name: ReopenTransaction :execrows
-- ReopenTransaction returns a resolved or matched line the caller holds to
-- the queue (D5): an exception with reason — its own, or payment_removed
-- for a matched one — its resolution cleared (the events keep it) and its
-- suggestion as the caller decided.
UPDATE invoices.bank_transactions
SET status = 'exception', reason = @reason::text, suggested_invoice_id = sqlc.narg(suggested_invoice_id)::bigint,
    resolution = NULL, resolved_by_user_id = NULL, resolved_at = NULL, resolution_note = ''
WHERE id = @id AND status IN ('resolved', 'matched');

-- name: TreatAsDistinct :execrows
-- TreatAsDistinct makes a duplicate row the caller holds an exception
-- queued possible_duplicate (D5), so it can be applied; duplicate_of_id is
-- frozen, so it stays out of ux_bank_transactions_fingerprint.
UPDATE invoices.bank_transactions
SET status = 'exception', reason = 'possible_duplicate', suggested_invoice_id = sqlc.narg(suggested_invoice_id)::bigint
WHERE id = @id AND status = 'duplicate';
