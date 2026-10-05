-- name: InsertCreditDraft :one
-- InsertCreditDraft writes a credit-note draft for an issued invoice (D8),
-- copying what the correction must name: the customer, the currency, the rate
-- and the date it was taken on, the delivery and its place, the references,
-- the buyer snapshot, and the project the original's work belonged to with
-- its reference (invoices work design D9). It reads no directory: the original's snapshot is
-- what the correction names, an anonymised customer's included.
INSERT INTO invoices.invoices (
    kind, customer_id, credits_invoice_id, delivery_date, delivery_from, delivery_to,
    delivery_address_line1, delivery_address_line2, delivery_postal_code, delivery_city, delivery_country,
    currency, exchange_rate, exchange_rate_date, your_reference, our_reference, order_reference,
    buyer_customer_number, buyer_type, buyer_name, buyer_organisation_number, buyer_foreign_id,
    buyer_address_line1, buyer_address_line2, buyer_postal_code, buyer_city, buyer_region, buyer_country,
    buyer_peppol_id, buyer_gln, buyer_language, project_id, project_reference,
    net_total, vat_total, gross_total, vat_total_nok, created_by_user_id, created_at, updated_at
)
SELECT
    'credit_note', o.customer_id, o.id, o.delivery_date, o.delivery_from, o.delivery_to,
    o.delivery_address_line1, o.delivery_address_line2, o.delivery_postal_code, o.delivery_city, o.delivery_country,
    o.currency, o.exchange_rate, o.exchange_rate_date, o.your_reference, o.our_reference, o.order_reference,
    o.buyer_customer_number, o.buyer_type, o.buyer_name, o.buyer_organisation_number, o.buyer_foreign_id,
    o.buyer_address_line1, o.buyer_address_line2, o.buyer_postal_code, o.buyer_city, o.buyer_region, o.buyer_country,
    o.buyer_peppol_id, o.buyer_gln, o.buyer_language, o.project_id, o.project_reference,
    o.net_total, o.vat_total, o.gross_total, o.vat_total_nok, @created_by_user_id, @now::timestamptz, @now::timestamptz
FROM invoices.invoices o
WHERE o.id = @original_id AND o.kind = 'invoice' AND o.status = 'issued'
RETURNING *;

-- name: CopyLinesToCredit :exec
-- CopyLinesToCredit copies every line of the original onto its credit-note
-- draft, each pointing at the line it credits (D8). The VAT snapshot is the
-- credit note's issue's to write, from the original line's.
INSERT INTO invoices.lines (
    invoice_id, position, description, quantity, unit, unit_price, discount_percent, vat_code_id,
    credits_line_id, line_gross, line_allowance, line_net
)
SELECT sqlc.arg(credit_id)::bigint, src.position, src.description, src.quantity, src.unit, src.unit_price,
       src.discount_percent, src.vat_code_id, src.id, src.line_gross, src.line_allowance, src.line_net
FROM invoices.lines src
WHERE src.invoice_id = sqlc.arg(original_id)::bigint AND src.quantity > 0
ORDER BY src.position;

-- name: CreditNotesOf :many
-- CreditNotesOf is every credit note of an invoice, drafts included, the
-- oldest first.
SELECT id, number, issue_date, gross_total, status FROM invoices.invoices
WHERE credits_invoice_id = @original_id
ORDER BY id;

-- name: CreditedGross :one
-- CreditedGross is what an invoice's issued credit notes credit, gross.
SELECT coalesce(sum(gross_total), 0)::numeric(14,2) AS credited FROM invoices.invoices
WHERE credits_invoice_id = @original_id AND status = 'issued';

-- name: CreditedPerLine :many
-- CreditedPerLine is, per line of an invoice, what its issued credit notes
-- credit: the quantity and the net the per-line cap is judged against (D8),
-- the gross and the allowance a final credit note's line takes the rest of,
-- and whether every one of them was a return at the line's own price and
-- discount — only then are the rest's øre rounding, and the final note's.
SELECT l.credits_line_id::bigint AS line_id,
       sum(l.quantity)::numeric(14,3) AS quantity,
       sum(l.line_net)::numeric(14,2) AS net,
       sum(l.line_gross)::numeric(14,2) AS gross,
       sum(l.line_allowance)::numeric(14,2) AS allowance,
       bool_and(l.unit_price = o.unit_price AND l.discount_percent = o.discount_percent)::boolean AS returns
FROM invoices.lines l
JOIN invoices.invoices c ON c.id = l.invoice_id
JOIN invoices.lines o ON o.id = l.credits_line_id
WHERE c.credits_invoice_id = @original_id AND c.status = 'issued' AND l.credits_line_id IS NOT NULL
GROUP BY l.credits_line_id;

-- name: CreditedVatPerRate :many
-- CreditedVatPerRate is, per (category, rate) row of an invoice, the VAT its
-- issued credit notes reversed: what a final credit note takes from the
-- original's row, so the credits sum to what was charged, øre for øre (D8).
SELECT s.vat_category, s.rate_percent,
       sum(s.vat_amount)::numeric(14,2) AS vat,
       sum(s.vat_amount_nok)::numeric(14,2) AS vat_nok
FROM invoices.vat_summaries s
JOIN invoices.invoices c ON c.id = s.invoice_id
WHERE c.credits_invoice_id = @original_id AND c.status = 'issued'
GROUP BY s.vat_category, s.rate_percent;

-- name: SquareCreditLine :exec
-- SquareCreditLine gives a credit note's line that returns its original line's
-- last unit what that line has left — gross, allowance and net — in place of
-- its own rounding (D8). It runs while the credit note is still a draft; the
-- trigger refuses it afterwards.
UPDATE invoices.lines SET
    line_gross = @line_gross,
    line_allowance = @line_allowance,
    line_net = @line_net
WHERE id = @id;
