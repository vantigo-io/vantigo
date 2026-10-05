-- name: DeductibleNet :many
-- DeductibleNet is, per (issued invoice of a customer, VAT code), the
-- invoice's lines' net at the code less what its issued credit notes credited
-- on those lines (invoices work design D7): what an a-konto has at the code
-- before any settlement deducts it. excludeID — the settlement being saved or
-- issued — is never one. It locks nothing: the issue reads it after the
-- counter, which serialises every write that could change it (reading 4).
WITH own AS (
    SELECT l.invoice_id, l.vat_code_id, sum(l.line_net) AS net
    FROM invoices.lines l
    JOIN invoices.invoices i ON i.id = l.invoice_id
    WHERE i.customer_id = @customer_id AND i.kind = 'invoice' AND i.status = 'issued' AND i.id <> @exclude_id::bigint
    GROUP BY l.invoice_id, l.vat_code_id
), credited AS (
    SELECT o.invoice_id, o.vat_code_id, sum(cl.line_net) AS net
    FROM invoices.lines cl
    JOIN invoices.invoices c ON c.id = cl.invoice_id
    JOIN invoices.lines o ON o.id = cl.credits_line_id
    JOIN invoices.invoices i ON i.id = o.invoice_id
    WHERE c.kind = 'credit_note' AND c.status = 'issued'
      AND i.customer_id = @customer_id AND i.kind = 'invoice' AND i.status = 'issued'
    GROUP BY o.invoice_id, o.vat_code_id
)
SELECT i.id AS invoice_id, i.number::bigint AS number, i.issue_date, own.vat_code_id,
       (own.net - coalesce(credited.net, 0))::numeric(14,2) AS net
FROM own
JOIN invoices.invoices i ON i.id = own.invoice_id
LEFT JOIN credited ON credited.invoice_id = own.invoice_id AND credited.vat_code_id = own.vat_code_id
ORDER BY i.number, own.vat_code_id;

-- name: DeductedNet :many
-- DeductedNet is, per (deducted invoice of a customer, VAT code), what issued
-- settlements' deduction lines took, net of what their issued credit notes
-- gave back (D7) — a positive amount, 0 once every deduction is credited —
-- and the numbers of the settlements that deducted it.
WITH taken AS (
    SELECT l.deducts_invoice_id AS invoice_id, l.vat_code_id, sum(l.line_net) AS net,
           array_agg(DISTINCT s.number)::bigint[] AS settlements
    FROM invoices.lines l
    JOIN invoices.invoices s ON s.id = l.invoice_id
    JOIN invoices.invoices d ON d.id = l.deducts_invoice_id
    WHERE s.kind = 'invoice' AND s.status = 'issued' AND d.customer_id = @customer_id
    GROUP BY l.deducts_invoice_id, l.vat_code_id
), given AS (
    SELECT o.deducts_invoice_id AS invoice_id, o.vat_code_id, sum(cl.line_net) AS net
    FROM invoices.lines cl
    JOIN invoices.invoices c ON c.id = cl.invoice_id
    JOIN invoices.lines o ON o.id = cl.credits_line_id
    JOIN invoices.invoices d ON d.id = o.deducts_invoice_id
    WHERE c.kind = 'credit_note' AND c.status = 'issued' AND d.customer_id = @customer_id
    GROUP BY o.deducts_invoice_id, o.vat_code_id
)
SELECT taken.invoice_id::bigint AS invoice_id, taken.vat_code_id,
       (coalesce(given.net, 0) - taken.net)::numeric(14,2) AS taken, taken.settlements
FROM taken
LEFT JOIN given ON given.invoice_id = taken.invoice_id AND given.vat_code_id = taken.vat_code_id
ORDER BY taken.invoice_id, taken.vat_code_id;

-- name: DeductionSnapshot :many
-- DeductionSnapshot is, per (issued invoice, VAT code) of the invoices given,
-- the VAT snapshot its lines at the code were issued with (D7): what a
-- deduction of it at the code is taxed at, never today's rate. An ordinary
-- line's snapshot comes before one of the invoice's own deduction lines.
SELECT DISTINCT ON (l.invoice_id, l.vat_code_id)
       l.invoice_id, l.vat_code_id, l.vat_rate_percent, l.vat_category, l.saf_t_code, l.exemption_reason
FROM invoices.lines l
JOIN invoices.invoices i ON i.id = l.invoice_id
WHERE l.invoice_id = ANY(@invoice_ids::bigint[]) AND i.status = 'issued' AND l.vat_category IS NOT NULL
ORDER BY l.invoice_id, l.vat_code_id, (l.deducts_invoice_id IS NOT NULL), l.position;

-- name: DeductedDocuments :many
-- DeductedDocuments is the documents a settlement's deduction lines name, by
-- id (D7): what the save and the issue judge them by — an issued invoice of
-- the same customer — and what the PDF and the EHF reference them by.
SELECT id, kind, status, customer_id, number, issue_date
FROM invoices.invoices
WHERE id = ANY(@ids::bigint[])
ORDER BY number NULLS LAST, id;
