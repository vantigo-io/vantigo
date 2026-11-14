-- name: LivePaymentsSum :one
-- LivePaymentsSum is what an invoice's live registrations add up to — the
-- paid of the open amount (D2, D3) — beside its standing write-off and its
-- live recoveries (A16, R4). A removed registration counts for nothing; a
-- recovery is not part of the open amount, which the write-off closed.
SELECT coalesce(sum(amount) FILTER (WHERE kind = 'payment'), 0)::numeric(14,2) AS paid,
    invoices.written_off_amount(@invoice_id)::numeric(14,2) AS written_off,
    coalesce(sum(amount) FILTER (WHERE kind = 'recovered'), 0)::numeric(14,2) AS recovered
FROM invoices.payments
WHERE invoice_id = @invoice_id AND removed_at IS NULL;

-- name: PaymentsOf :many
-- PaymentsOf is every registration of an invoice, removed ones included —
-- the record is the point (D2) — in the order the money arrived.
SELECT * FROM invoices.payments
WHERE invoice_id = @invoice_id
ORDER BY paid_on, id;

-- name: InsertPayment :one
-- InsertPayment registers money received against an issued invoice (D2). The
-- caller holds the invoice FOR UPDATE and has judged the open amount under
-- that lock; tr_payments_parent refuses a draft's or a credit note's anyway.
-- A recovery (kind recovered, A16) names the standing write-off it recovers.
INSERT INTO invoices.payments (
    invoice_id, paid_on, amount, currency, reference, note, registered_by_user_id, registered_at, kind, write_off_id
) VALUES (
    @invoice_id, @paid_on, @amount, @currency, @reference, @note, @registered_by_user_id, @registered_at::timestamptz,
    @kind::text, sqlc.narg(write_off_id)::bigint
)
RETURNING *;

-- name: InsertVippsPayment :one
-- InsertVippsPayment registers a Vipps capture (D2's third branch, D16):
-- source vipps, its attempt — payment_attempt_id's uniqueness is the
-- exactly-once guard — no bank line and no user. The caller holds the
-- attempt and then the invoice, and moves the attempt out of its reserving
-- state in the same transaction, so the payment and the reservation never
-- both count against the open amount.
INSERT INTO invoices.payments (
    invoice_id, paid_on, amount, currency, reference, source, payment_attempt_id, registered_at
) VALUES (
    @invoice_id, @paid_on, @amount, @currency, @reference, 'vipps', @payment_attempt_id, @registered_at::timestamptz
)
RETURNING *;

-- name: GetPayment :one
-- GetPayment is one registration of one invoice, read after LockInvoice so
-- two racing removals see each other (D2).
SELECT * FROM invoices.payments WHERE id = @id AND invoice_id = @invoice_id;

-- name: RemovePayment :execrows
-- RemovePayment is the soft removal, once: the three columns set together
-- (D2), of that invoice's registration only. A registration already removed
-- matches no row; tr_payments_immutable would refuse it anyway.
UPDATE invoices.payments
SET removed_at = @removed_at::timestamptz, removed_by_user_id = @removed_by_user_id::uuid, removal_reason = @removal_reason::text
WHERE id = @id AND invoice_id = @invoice_id AND removed_at IS NULL;
