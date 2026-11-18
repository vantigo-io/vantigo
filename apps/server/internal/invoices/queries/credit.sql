-- The customer's credit (invoices phase 6 design I8-I13, plan delivery B-1):
-- the ledger's movements and balances, the refunds, a credit note's surplus
-- and what a merge and an erase do to them. Every write is made under the
-- customer's credit lock (LockCustomerCredit), taken before any invoice
-- (I17a as amended by the plan's reading 1). Every time is a parameter.

-- name: LockCustomerCredit :exec
-- LockCustomerCredit takes the customer's credit lock, a transaction-scoped
-- advisory lock on the customer's id: the second lock of I17a's order, after
-- a bank line and before every invoice. guard_customer_credit_insert takes
-- the same lock, on the same key, as the floor.
SELECT pg_advisory_xact_lock(hashtextextended('invoices.customer_credit:' || sqlc.arg(customer_id)::int::text, 0));

-- name: CreditBalance :one
-- CreditBalance is the customer's credit in the currency: the sum of its
-- movements, 0 with none. Read after the customer's lock, it is the balance
-- a write judges by.
SELECT coalesce(sum(amount), 0)::numeric(14,2) AS balance
FROM invoices.customer_credits
WHERE customer_id = @customer_id AND currency = @currency;

-- name: CreditBalances :many
-- CreditBalances is the customer's credit per currency in which it has any
-- movement, by currency: the balance may be 0.
SELECT currency::text AS currency, sum(amount)::numeric(14,2) AS balance, sum(amount_nok)::numeric(14,2) AS balance_nok
FROM invoices.customer_credits
WHERE customer_id = @customer_id
GROUP BY currency
ORDER BY currency;

-- name: InsertCustomerCredit :one
-- InsertCustomerCredit writes one movement (I8). The caller holds the
-- customer's credit lock and has judged the balance under it;
-- tr_customer_credits_guard judges it again, fills amount_nok for NOK and
-- blanks a marked customer's note.
INSERT INTO invoices.customer_credits (
    customer_id, currency, amount, amount_nok, kind, credit_day, note, invoice_id, payment_id, bank_transaction_id,
    document_id, refund_id, reverses_id, registered_by_user_id, registered_at, idempotency_key
) VALUES (
    @customer_id, @currency, @amount, sqlc.narg(amount_nok)::numeric, @kind, @credit_day, @note,
    sqlc.narg(invoice_id)::bigint, sqlc.narg(payment_id)::bigint, sqlc.narg(bank_transaction_id)::bigint,
    sqlc.narg(document_id)::bigint, sqlc.narg(refund_id)::bigint, sqlc.narg(reverses_id)::bigint,
    sqlc.narg(registered_by_user_id)::uuid, @registered_at::timestamptz, sqlc.narg(idempotency_key)::text
)
RETURNING *;

-- name: InsertCreditNoteMove :one
-- InsertCreditNoteMove writes a credit note's surplus as the customer's
-- credit (I10), unattributed: once per credit note, so a second run of the
-- follow-up writes nothing and answers no row.
INSERT INTO invoices.customer_credits (
    customer_id, currency, amount, amount_nok, kind, credit_day, invoice_id, document_id, registered_at
) VALUES (
    @customer_id, @currency, @amount, sqlc.narg(amount_nok)::numeric, 'credit_note', @credit_day, @invoice_id, @document_id,
    @registered_at::timestamptz
)
ON CONFLICT (document_id) WHERE kind = 'credit_note' DO NOTHING
RETURNING id;

-- name: GetCustomerCredit :one
-- GetCustomerCredit is one movement, with whether a reversal took it back
-- (all of it, for every kind but credit_note) and what of it is left.
SELECT sqlc.embed(c),
    coalesce((SELECT sum(r.amount) FROM invoices.customer_credits r WHERE r.reverses_id = c.id), 0)::numeric(14,2) AS reversed
FROM invoices.customer_credits c
WHERE c.id = @id;

-- name: CreditsOfPayment :many
-- CreditsOfPayment is the movements that name a payment — the overpayment
-- registered beside it by hand, or the applied row of a payment of source
-- credit — each with what reversals took of it.
SELECT sqlc.embed(c),
    coalesce((SELECT sum(r.amount) FROM invoices.customer_credits r WHERE r.reverses_id = c.id), 0)::numeric(14,2) AS reversed
FROM invoices.customer_credits c
WHERE c.payment_id = @payment_id
ORDER BY c.id;

-- name: CreditsOfLine :many
-- CreditsOfLine is the overpayments kept as credit from a bank line (I9),
-- each with what a reversal took of it.
SELECT sqlc.embed(c),
    coalesce((SELECT sum(r.amount) FROM invoices.customer_credits r WHERE r.reverses_id = c.id), 0)::numeric(14,2) AS reversed
FROM invoices.customer_credits c
WHERE c.bank_transaction_id = @bank_transaction_id
ORDER BY c.id;

-- name: CreditNoteMovesOf :many
-- CreditNoteMovesOf is the credit_note movements of an invoice (I10), the
-- newest first, each with what reversals already returned of it: what a
-- payment's removal or a write-off's undoing returns from.
SELECT sqlc.embed(c),
    coalesce((SELECT sum(r.amount) FROM invoices.customer_credits r WHERE r.reverses_id = c.id), 0)::numeric(14,2) AS reversed
FROM invoices.customer_credits c
WHERE c.invoice_id = @invoice_id AND c.kind = 'credit_note'
ORDER BY c.id DESC;

-- name: MovedOf :one
-- MovedOf is what the invoice's credit notes' surplus moved to the customer,
-- less what was returned (I10).
SELECT invoices.moved_amount(@invoice_id)::numeric(14,2) AS moved;

-- name: PendingCreditNoteMoves :many
-- PendingCreditNoteMoves is the issued credit notes whose original reads
-- below zero open and that have moved nothing — the follow-up the issue
-- made and that did not run, or failed (I10, plan reading 8). The newest
-- first, at most row_limit.
SELECT c.id
FROM invoices.invoices c
JOIN invoices.invoices o ON o.id = c.credits_invoice_id
WHERE c.kind = 'credit_note' AND c.status = 'issued' AND o.kind = 'invoice' AND o.status = 'issued'
  AND NOT EXISTS (SELECT 1 FROM invoices.customer_credits m WHERE m.document_id = c.id AND m.kind = 'credit_note')
  AND o.gross_total
      - coalesce((SELECT sum(x.gross_total) FROM invoices.invoices x
                  WHERE x.credits_invoice_id = o.id AND x.kind = 'credit_note' AND x.status = 'issued'), 0)
      - invoices.settled_amount(o.id) < 0
ORDER BY c.id DESC
LIMIT sqlc.arg(row_limit)::int;

-- name: CustomerCredits :many
-- CustomerCredits is a page of the customer's movements, the newest first,
-- each with the numbers of what it came from: the invoice, the credit note,
-- the bank line, the refund — and whether it was reversed.
SELECT sqlc.embed(c),
    i.number AS invoice_number, d.number AS document_number, t.line_ref AS line_ref,
    coalesce((SELECT sum(r.amount) FROM invoices.customer_credits r WHERE r.reverses_id = c.id), 0)::numeric(14,2) AS reversed
FROM invoices.customer_credits c
LEFT JOIN invoices.invoices i ON i.id = c.invoice_id
LEFT JOIN invoices.invoices d ON d.id = c.document_id
LEFT JOIN invoices.bank_transactions t ON t.id = c.bank_transaction_id
WHERE c.customer_id = @customer_id
ORDER BY c.credit_day DESC, c.id DESC
LIMIT sqlc.arg(page_size)::int OFFSET sqlc.arg(page_offset)::int;

-- name: CountCustomerCredits :one
SELECT count(*)::int FROM invoices.customer_credits WHERE customer_id = @customer_id;

-- name: InsertCreditRefund :one
-- InsertCreditRefund records money paid back outside Vantigo (I12), under
-- the customer's credit lock; its refunded movement follows in the same
-- transaction.
INSERT INTO invoices.credit_refunds (
    customer_id, refunded_on, amount, currency, amount_nok, bank_account, reference, note, registered_by_user_id, registered_at
) VALUES (
    @customer_id, @refunded_on, @amount, @currency, sqlc.narg(amount_nok)::numeric, @bank_account, @reference, @note,
    @registered_by_user_id, @registered_at::timestamptz
)
RETURNING *;

-- name: GetCreditRefund :one
-- GetCreditRefund is one refund of a customer, read after the customer's lock.
SELECT * FROM invoices.credit_refunds WHERE id = @id AND customer_id = @customer_id;

-- name: RemoveCreditRefund :execrows
-- RemoveCreditRefund is the refund's removal, once: the three columns set
-- together. Its reversed movement follows in the same transaction.
UPDATE invoices.credit_refunds
SET removed_at = @removed_at::timestamptz, removed_by_user_id = @removed_by_user_id::uuid, removal_reason = @removal_reason::text
WHERE id = @id AND customer_id = @customer_id AND removed_at IS NULL;

-- name: CreditRefundsOf :many
-- CreditRefundsOf is every refund of the customer, removed ones included,
-- the newest first.
SELECT * FROM invoices.credit_refunds WHERE customer_id = @customer_id ORDER BY refunded_on DESC, id DESC;

-- name: RefundedMoveOf :one
-- RefundedMoveOf is the refunded movement a refund wrote.
SELECT * FROM invoices.customer_credits WHERE refund_id = @refund_id AND kind = 'refunded';

-- name: RepointCustomerCredits :execrows
-- RepointCustomerCredits moves every movement of from to into (the merge):
-- the balances add.
UPDATE invoices.customer_credits SET customer_id = @into_customer_id WHERE customer_id = @from_customer_id;

-- name: RepointCreditRefunds :execrows
UPDATE invoices.credit_refunds SET customer_id = @into_customer_id WHERE customer_id = @from_customer_id;

-- name: InsertCreditMerge :exec
-- InsertCreditMerge records a balance a merge moved (plan reading 4), after
-- the movements are re-pointed: its event reads the merged balances.
INSERT INTO invoices.credit_merges (from_customer_id, into_customer_id, currency, amount, amount_nok, merged_at)
VALUES (@from_customer_id, @into_customer_id, @currency, @amount, @amount_nok, @merged_at::timestamptz);

-- name: BlankCustomerCreditNotes :execrows
-- BlankCustomerCreditNotes is the erase's (D6): the customer's movements
-- keep their amounts and lose their notes.
UPDATE invoices.customer_credits SET note = '' WHERE customer_id = @customer_id AND note <> '';

-- name: BlankCreditRefundNotes :execrows
UPDATE invoices.credit_refunds SET note = '' WHERE customer_id = @customer_id AND note <> '';

-- name: CustomersWithCredit :many
-- CustomersWithCredit is, of the customers named, those whose credit in a
-- currency is above zero, with it.
SELECT customer_id, currency::text AS currency, sum(amount)::numeric(14,2) AS balance
FROM invoices.customer_credits
WHERE customer_id = ANY(sqlc.arg(customer_ids)::int[])
GROUP BY customer_id, currency
HAVING sum(amount) > 0
ORDER BY customer_id, currency;

-- name: AppliedByIdempotencyKey :many
-- AppliedByIdempotencyKey is the applied movement a person sent with an
-- Idempotency-Key (B-1 review m4): none, or one, by ux_customer_credits_idempotency.
SELECT c.invoice_id, c.amount FROM invoices.customer_credits c
WHERE c.registered_by_user_id = @registered_by_user_id::uuid AND c.idempotency_key = @idempotency_key::text;
