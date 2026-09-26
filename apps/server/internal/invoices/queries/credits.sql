-- name: InsertCreditDraft :one
-- InsertCreditDraft writes a credit-note draft for an issued invoice (D8),
-- copying what the correction must name: the customer, the currency and rate,
-- the delivery and its place, the references, and the buyer snapshot. It reads
-- no directory: the original's snapshot is what the correction names, an
-- anonymised customer's included.
INSERT INTO invoices.invoices (
    kind, customer_id, credits_invoice_id, delivery_date, delivery_from, delivery_to,
    delivery_address_line1, delivery_address_line2, delivery_postal_code, delivery_city, delivery_country,
    currency, exchange_rate, your_reference, our_reference, order_reference,
    buyer_customer_number, buyer_type, buyer_name, buyer_organisation_number, buyer_foreign_id,
    buyer_address_line1, buyer_address_line2, buyer_postal_code, buyer_city, buyer_region, buyer_country,
    buyer_peppol_id, buyer_gln, buyer_language,
    net_total, vat_total, gross_total, vat_total_nok, created_by_user_id, created_at, updated_at
)
SELECT
    'credit_note', o.customer_id, o.id, o.delivery_date, o.delivery_from, o.delivery_to,
    o.delivery_address_line1, o.delivery_address_line2, o.delivery_postal_code, o.delivery_city, o.delivery_country,
    o.currency, o.exchange_rate, o.your_reference, o.our_reference, o.order_reference,
    o.buyer_customer_number, o.buyer_type, o.buyer_name, o.buyer_organisation_number, o.buyer_foreign_id,
    o.buyer_address_line1, o.buyer_address_line2, o.buyer_postal_code, o.buyer_city, o.buyer_region, o.buyer_country,
    o.buyer_peppol_id, o.buyer_gln, o.buyer_language,
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
-- CreditedPerLine is, per line of an invoice, the quantity and the net its
-- issued credit notes credit: what the per-line cap is judged against (D8).
SELECT l.credits_line_id::bigint AS line_id,
       sum(l.quantity)::numeric(14,3) AS quantity,
       sum(l.line_net)::numeric(14,2) AS net
FROM invoices.lines l
JOIN invoices.invoices c ON c.id = l.invoice_id
WHERE c.credits_invoice_id = @original_id AND c.status = 'issued' AND l.credits_line_id IS NOT NULL
GROUP BY l.credits_line_id;
