-- name: LivePaymentsSum :one
-- LivePaymentsSum is what an invoice's live registrations add up to — the
-- paid of the open amount (D2, D3). A removed registration counts for nothing.
SELECT coalesce(sum(amount), 0)::numeric(14,2) AS paid FROM invoices.payments
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
INSERT INTO invoices.payments (
    invoice_id, paid_on, amount, currency, reference, note, registered_by_user_id, registered_at
) VALUES (
    @invoice_id, @paid_on, @amount, @currency, @reference, @note, @registered_by_user_id, @registered_at::timestamptz
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
