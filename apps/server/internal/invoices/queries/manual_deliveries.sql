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

-- name: QualifyingDeliveries :many
-- QualifyingDeliveries is every live delivery of an invoice the engine's
-- delivery fact reads (D8; plan reading 34): each e-mail by when it was sent,
-- each delivered EHF transmission by when it was delivered — at, whose Oslo
-- day the caller derives, never this statement — and each manual delivery
-- not removed by its day, delivered_on. A failed, queued or cancelled
-- transmission and a removed manual record are not deliveries.
SELECT 'manual'::text AS kind, NULL::timestamptz AS at, m.delivered_on AS delivered_on
FROM invoices.manual_deliveries m
WHERE m.invoice_id = @invoice_id AND m.removed_at IS NULL
UNION ALL
SELECT 'email'::text, d.sent_at, NULL::date
FROM invoices.deliveries d
WHERE d.invoice_id = @invoice_id
UNION ALL
SELECT 'ehf'::text, t.delivered_at, NULL::date
FROM invoices.transmissions t
WHERE t.invoice_id = @invoice_id AND t.status = 'delivered';
