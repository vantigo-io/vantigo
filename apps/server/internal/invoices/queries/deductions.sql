-- name: DeductibleNet :many
-- DeductibleNet is, per (issued invoice of a customer, VAT code), the
-- invoice's lines' net at the code less what its issued credit notes credited
-- on those lines (invoices work design D7): what an a-konto has at the code
-- before any settlement deducts it, and its currency. only_ids narrows it to
-- those invoices; empty is every one. A code where the invoice carries
-- deduction lines of its own is never deductible: its lines there would mix
-- today's rate with an older snapshot. exclude_id — the settlement itself —
-- is belt and braces: a draft is never issued. It locks nothing: the issue
-- reads it after the counter, which serialises every write that could change
-- it (reading 4).
WITH own AS (
    SELECT l.invoice_id, l.vat_code_id, sum(l.line_net) AS net
    FROM invoices.lines l
    JOIN invoices.invoices i ON i.id = l.invoice_id
    WHERE i.customer_id = @customer_id AND i.kind = 'invoice' AND i.status = 'issued' AND i.id <> @exclude_id::bigint
      AND (coalesce(cardinality(@only_ids::bigint[]), 0) = 0 OR i.id = ANY(@only_ids::bigint[]))
    GROUP BY l.invoice_id, l.vat_code_id
    HAVING bool_and(l.deducts_invoice_id IS NULL)
), credited AS (
    SELECT o.invoice_id, o.vat_code_id, sum(cl.line_net) AS net
    FROM invoices.lines cl
    JOIN invoices.invoices c ON c.id = cl.invoice_id
    JOIN invoices.lines o ON o.id = cl.credits_line_id
    JOIN invoices.invoices i ON i.id = o.invoice_id
    WHERE c.kind = 'credit_note' AND c.status = 'issued'
      AND i.customer_id = @customer_id AND i.kind = 'invoice' AND i.status = 'issued'
      AND (coalesce(cardinality(@only_ids::bigint[]), 0) = 0 OR i.id = ANY(@only_ids::bigint[]))
    GROUP BY o.invoice_id, o.vat_code_id
)
SELECT i.id AS invoice_id, i.number::bigint AS number, i.issue_date, i.currency::text AS currency, own.vat_code_id,
       (own.net - coalesce(credited.net, 0))::numeric(14,2) AS net
FROM own
JOIN invoices.invoices i ON i.id = own.invoice_id
LEFT JOIN credited ON credited.invoice_id = own.invoice_id AND credited.vat_code_id = own.vat_code_id
ORDER BY i.number, own.vat_code_id;

-- name: DeductedNet :many
-- DeductedNet is, per (deducted invoice of a customer, VAT code), what issued
-- settlements' deduction lines took, net of what their issued credit notes
-- gave back (D7) — a positive amount, 0 once every deduction is credited —
-- and the numbers of the settlements whose own deductions there are not all
-- given back. only_ids narrows it to those deducted invoices; empty is every
-- one.
WITH taken AS (
    SELECT l.deducts_invoice_id AS invoice_id, l.vat_code_id, s.number, sum(l.line_net) AS net
    FROM invoices.lines l
    JOIN invoices.invoices s ON s.id = l.invoice_id
    JOIN invoices.invoices d ON d.id = l.deducts_invoice_id
    WHERE s.kind = 'invoice' AND s.status = 'issued' AND d.customer_id = @customer_id
      AND (coalesce(cardinality(@only_ids::bigint[]), 0) = 0 OR l.deducts_invoice_id = ANY(@only_ids::bigint[]))
    GROUP BY l.deducts_invoice_id, l.vat_code_id, s.number
), given AS (
    SELECT o.deducts_invoice_id AS invoice_id, o.vat_code_id, s.number, sum(cl.line_net) AS net
    FROM invoices.lines cl
    JOIN invoices.invoices c ON c.id = cl.invoice_id
    JOIN invoices.lines o ON o.id = cl.credits_line_id
    JOIN invoices.invoices s ON s.id = o.invoice_id
    JOIN invoices.invoices d ON d.id = o.deducts_invoice_id
    WHERE c.kind = 'credit_note' AND c.status = 'issued' AND d.customer_id = @customer_id
      AND (coalesce(cardinality(@only_ids::bigint[]), 0) = 0 OR o.deducts_invoice_id = ANY(@only_ids::bigint[]))
    GROUP BY o.deducts_invoice_id, o.vat_code_id, s.number
), per AS (
    SELECT taken.invoice_id, taken.vat_code_id, taken.number, coalesce(given.net, 0) - taken.net AS net
    FROM taken
    LEFT JOIN given ON given.invoice_id = taken.invoice_id AND given.vat_code_id = taken.vat_code_id AND given.number = taken.number
)
SELECT per.invoice_id::bigint AS invoice_id, per.vat_code_id, sum(per.net)::numeric(14,2) AS taken,
       coalesce(array_agg(per.number ORDER BY per.number) FILTER (WHERE per.net > 0), '{}')::bigint[] AS settlements
FROM per
GROUP BY per.invoice_id, per.vat_code_id
ORDER BY per.invoice_id, per.vat_code_id;

-- name: DeductionSnapshot :many
-- DeductionSnapshot is, per (issued invoice, VAT code) of the invoices given,
-- the VAT snapshot its lines at the code were issued with (D7): what a
-- deduction of it at the code is taxed at, never today's rate — and whether
-- the invoice carries deduction lines of its own at the code, which makes it
-- not deductible there. An ordinary line's snapshot comes before one of the
-- invoice's own deduction lines.
SELECT DISTINCT ON (l.invoice_id, l.vat_code_id)
       l.invoice_id, l.vat_code_id, l.vat_rate_percent, l.vat_category, l.saf_t_code, l.exemption_reason,
       EXISTS (
           SELECT 1 FROM invoices.lines x
           WHERE x.invoice_id = l.invoice_id AND x.vat_code_id = l.vat_code_id AND x.deducts_invoice_id IS NOT NULL
       ) AS deducts_here
FROM invoices.lines l
JOIN invoices.invoices i ON i.id = l.invoice_id
WHERE l.invoice_id = ANY(@invoice_ids::bigint[]) AND i.status = 'issued' AND l.vat_category IS NOT NULL
ORDER BY l.invoice_id, l.vat_code_id, (l.deducts_invoice_id IS NOT NULL), l.position;

-- name: DeductedDocuments :many
-- DeductedDocuments is the documents a settlement's deduction lines name, by
-- id (D7): what the save and the issue judge them by — an issued invoice of
-- the same customer, in the settlement's currency — and what the PDF and the
-- EHF reference them by.
SELECT id, kind, status, customer_id, currency::text AS currency, number, issue_date
FROM invoices.invoices
WHERE id = ANY(@ids::bigint[])
ORDER BY number NULLS LAST, id;
