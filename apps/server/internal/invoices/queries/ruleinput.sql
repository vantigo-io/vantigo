-- The rule-input loader (invoices payments and reminders design D8): every
-- fact the reminder engine judges an invoice on, read for many invoices at
-- once — each statement over id = ANY(@ids), so a list of any length reads
-- the same handful of statements. ruleinput.go maps the rows onto
-- reminderrules.Input; no other code builds one (plan reading 2).

-- name: RuleInvoices :many
-- RuleInvoices is the issued invoices among ids with the snapshot the engine
-- reads: the buyer, the issue and due dates, the gross, and the customer
-- whose policy applies. A draft or a credit note is not one. The buyer's
-- language is not the engine's: the letter's dispatch reads it from the
-- invoice row it holds locked.
SELECT id, customer_id, buyer_type, buyer_organisation_number, buyer_foreign_id,
       issue_date, due_date, gross_total
FROM invoices.invoices
WHERE id = ANY(@ids::bigint[]) AND kind = 'invoice' AND status = 'issued'
ORDER BY id;

-- name: RuleCredits :many
-- RuleCredits is the issued credit notes of the invoices: the live ones —
-- a draft credits nothing.
SELECT credits_invoice_id::bigint AS invoice_id, issue_date, gross_total
FROM invoices.invoices
WHERE credits_invoice_id = ANY(@ids::bigint[]) AND kind = 'credit_note' AND status = 'issued'
ORDER BY credits_invoice_id, id;

-- name: RulePayments :many
-- RulePayments is the live payments of the invoices, each with its bank
-- line's ordered_on (OCR's oppdragsdato; NULL for a manual payment and for
-- camt, reading 32), the deadline-met rule's day.
SELECT p.invoice_id, p.paid_on, p.amount, t.ordered_on
FROM invoices.payments p
LEFT JOIN invoices.bank_transactions t ON t.id = p.bank_transaction_id
WHERE p.invoice_id = ANY(@ids::bigint[]) AND p.removed_at IS NULL
ORDER BY p.invoice_id, p.paid_on, p.id;

-- name: RuleDeliveries :many
-- RuleDeliveries is every live delivery of the invoices the engine's delivery
-- fact reads (D8; plan reading 34) — the one definition of a qualifying
-- delivery, which deliveriesOf reads too, for one invoice: each e-mail by
-- when it was sent, each delivered EHF transmission by when it was delivered
-- — at, whose Oslo day the caller derives, never this statement — and each
-- manual delivery not removed by its delivered_on. A failed, queued or
-- cancelled transmission and a removed manual record are not deliveries.
-- manual_kind is a manual record's own kind (handed_over or posted), which
-- the collection file names; '' for the others.
SELECT m.invoice_id, 'manual'::text AS kind, NULL::timestamptz AS at, m.delivered_on AS delivered_on,
       m.kind::text AS manual_kind
FROM invoices.manual_deliveries m
WHERE m.invoice_id = ANY(@ids::bigint[]) AND m.removed_at IS NULL
UNION ALL
SELECT d.invoice_id, 'email'::text, d.sent_at, NULL::date, ''::text
FROM invoices.deliveries d
WHERE d.invoice_id = ANY(@ids::bigint[])
UNION ALL
SELECT t.invoice_id, 'ehf'::text, t.delivered_at, NULL::date, ''::text
FROM invoices.transmissions t
WHERE t.invoice_id = ANY(@ids::bigint[]) AND t.status = 'delivered';

-- name: RuleLetters :many
-- RuleLetters is every letter of the invoices, in any status, by sequence:
-- the engine tells the sent from the ones in flight itself.
SELECT * FROM invoices.reminders
WHERE invoice_id = ANY(@ids::bigint[])
ORDER BY invoice_id, sequence, id;

-- name: RuleWaivers :many
-- RuleWaivers is every charge waiver of the invoices; a waiver is never
-- removed, so every row counts.
SELECT * FROM invoices.charge_waivers
WHERE invoice_id = ANY(@ids::bigint[])
ORDER BY invoice_id, id;

-- name: RuleChargePayments :many
-- RuleChargePayments is the live charge payments of the invoices, in the
-- order the money arrived.
SELECT * FROM invoices.charge_payments
WHERE invoice_id = ANY(@ids::bigint[]) AND removed_at IS NULL
ORDER BY invoice_id, paid_on, id;

-- name: RuleHolds :many
-- RuleHolds is, per invoice with a hold, whether one is live and whether a
-- lift barred charges (charges_allowed false — for good, D11).
SELECT invoice_id,
       bool_or(lifted_at IS NULL)::boolean AS on_hold,
       bool_or(lifted_at IS NOT NULL AND charges_allowed = false)::boolean AS charges_barred
FROM invoices.invoice_holds
WHERE invoice_id = ANY(@ids::bigint[])
GROUP BY invoice_id
ORDER BY invoice_id;

-- name: RuleHandoffs :many
-- RuleHandoffs is the invoices among ids with a live hand-off.
SELECT invoice_id FROM invoices.collection_handoffs
WHERE invoice_id = ANY(@ids::bigint[]) AND withdrawn_on IS NULL
ORDER BY invoice_id;

-- name: RulePolicies :many
-- RulePolicies is the reminder policy of each invoice's customer, for the
-- invoices whose customer has one; no row is normal (D7).
SELECT i.id AS invoice_id, p.mode
FROM invoices.invoices i
JOIN invoices.customer_reminder_policies p ON p.customer_id = i.customer_id
WHERE i.id = ANY(@ids::bigint[])
ORDER BY i.id;
