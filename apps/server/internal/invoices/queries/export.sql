-- name: ExportRows :many
-- ExportRows is the accountant's CSV (payments and delivery design D5): the
-- journal's selection — the issued documents with an issue date in the range —
-- one row per document and VAT row, in number order and in each document by
-- category then rate, each credit note with the number of the invoice it
-- credits, an invoice's KID (EHF and KID design D3) and the document's project
-- reference (invoices work design D9). The amounts are as stored, positive; the file signs a credit note's.
-- The caller asks for one row more than a file may hold, so "the whole period"
-- and "more than a file may hold" are told apart without a count of their own.
SELECT i.number, i.kind, i.issue_date, i.delivery_date, i.delivery_from, i.delivery_to, i.due_date,
       i.buyer_customer_number, i.buyer_name, i.buyer_organisation_number, i.currency, i.exchange_rate,
       s.saf_t_code, s.rate_percent, s.taxable_amount, s.vat_amount, s.vat_amount_nok,
       o.number AS credits_number, i.kid, i.project_reference
FROM invoices.invoices i
JOIN invoices.vat_summaries s ON s.invoice_id = i.id
LEFT JOIN invoices.invoices o ON o.id = i.credits_invoice_id
WHERE i.status = 'issued' AND i.issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date
ORDER BY i.number, s.vat_category, s.rate_percent
LIMIT sqlc.arg('limit');
