-- Manual deliveries and the delivery fact (invoices payments and reminders
-- design D8): an invoice not validly delivered does not fall due, so every
-- charge needs a recorded delivery on or before the due date — an e-mail, a
-- delivered EHF transmission, or a delivery recorded by hand.

-- name: ManualDeliveriesOf :many
-- ManualDeliveriesOf is every manual delivery of an invoice, removed ones
-- included with their removal, the earliest first.
SELECT * FROM invoices.manual_deliveries
WHERE invoice_id = @invoice_id
ORDER BY delivered_on, id;

-- name: InsertManualDelivery :one
-- InsertManualDelivery records the invoice handed over or posted on a day.
-- The caller holds the invoice FOR UPDATE; tr_manual_deliveries_parent
-- refuses a draft's or a credit note's and blanks the note of an anonymised
-- customer's.
INSERT INTO invoices.manual_deliveries (
    invoice_id, kind, delivered_on, note, recorded_by_user_id, recorded_at
) VALUES (
    @invoice_id, @kind, @delivered_on, @note, @recorded_by_user_id, @recorded_at::timestamptz
)
RETURNING *;

-- name: GetManualDelivery :one
-- GetManualDelivery is one manual delivery of one invoice, read after the
-- invoice's lock so two racing removals see each other.
SELECT * FROM invoices.manual_deliveries WHERE id = @id AND invoice_id = @invoice_id;

-- name: RemoveManualDelivery :execrows
-- RemoveManualDelivery is the soft removal, once: the three columns set
-- together, of that invoice's record only.
UPDATE invoices.manual_deliveries
SET removed_at = @removed_at::timestamptz, removed_by_user_id = @removed_by_user_id::uuid, removal_reason = @removal_reason::text
WHERE id = @id AND invoice_id = @invoice_id AND removed_at IS NULL;

-- name: ClaimingLettersOf :many
-- ClaimingLettersOf is every letter of an invoice that carries its facts —
-- sent, printed, or being sent (queued with its facts written) — by
-- sequence: the letters whose charges a manual delivery's removal must not
-- leave without a delivery (plan reading 37, as the Task 7 review decided).
-- A withdrawn letter claims nothing, and a failed one has its facts cleared.
SELECT * FROM invoices.reminders
WHERE invoice_id = @invoice_id AND status IN ('sent', 'printed', 'queued') AND sent_on IS NOT NULL
ORDER BY sequence, id;
