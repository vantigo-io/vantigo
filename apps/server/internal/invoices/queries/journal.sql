-- name: JournalPage :many
-- JournalPage is one page of the journal (D11): the issued documents with an
-- issue date in the range, in number order, each credit note with the number
-- of the invoice it credits.
SELECT i.id, i.number, i.kind, i.issue_date, i.delivery_date, i.delivery_from, i.delivery_to, i.due_date,
       i.buyer_customer_number, i.buyer_name, i.buyer_organisation_number, i.currency,
       i.net_total, i.vat_total, i.gross_total, o.number AS credits_number
FROM invoices.invoices i
LEFT JOIN invoices.invoices o ON o.id = i.credits_invoice_id
WHERE i.status = 'issued' AND i.issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date
ORDER BY i.number
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: JournalCount :one
SELECT count(*)::int FROM invoices.invoices
WHERE status = 'issued' AND issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date;

-- name: JournalSummaries :many
-- JournalSummaries is the VAT of a page's documents in one round trip.
SELECT * FROM invoices.vat_summaries WHERE invoice_id = ANY(sqlc.arg(invoice_ids)::bigint[])
ORDER BY invoice_id, rate_percent DESC, vat_category;

-- name: JournalTotalsByCode :many
-- JournalTotalsByCode is the range's VAT per (SAF-T code, category, rate)
-- over every document in it, not the page — credit notes subtracted: they are
-- stored positive, and a journal that summed them would overstate revenue.
SELECT s.saf_t_code, s.vat_category, s.rate_percent,
       sum(CASE WHEN i.kind = 'credit_note' THEN -s.taxable_amount ELSE s.taxable_amount END)::numeric AS taxable_amount,
       sum(CASE WHEN i.kind = 'credit_note' THEN -s.vat_amount ELSE s.vat_amount END)::numeric AS vat_amount
FROM invoices.vat_summaries s
JOIN invoices.invoices i ON i.id = s.invoice_id
WHERE i.status = 'issued' AND i.issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date
GROUP BY s.saf_t_code, s.vat_category, s.rate_percent
ORDER BY s.rate_percent DESC, s.vat_category, s.saf_t_code;

-- name: JournalTotals :one
-- JournalTotals is the range's net, VAT and gross, credit notes subtracted.
SELECT coalesce(sum(CASE WHEN kind = 'credit_note' THEN -net_total ELSE net_total END), 0)::numeric AS net_total,
       coalesce(sum(CASE WHEN kind = 'credit_note' THEN -vat_total ELSE vat_total END), 0)::numeric AS vat_total,
       coalesce(sum(CASE WHEN kind = 'credit_note' THEN -gross_total ELSE gross_total END), 0)::numeric AS gross_total
FROM invoices.invoices
WHERE status = 'issued' AND issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date;

-- name: JournalCheckedRange :one
-- JournalCheckedRange is the numbers the gap check covers (D11): from one past
-- the issued document before the range's first — the series start when there
-- is none — to the range's last. Every number missing between the previous
-- issued document and the range's first is thereby listed, not only the one
-- just before it. No row when the range holds no document.
SELECT greatest(
           coalesce((SELECT max(p.number) FROM invoices.invoices p WHERE p.status = 'issued' AND p.number < b.first_number),
                    sqlc.arg(series_start)::bigint - 1) + 1,
           sqlc.arg(series_start)::bigint)::bigint AS checked_from,
       b.last_number::bigint AS checked_to
FROM (
    SELECT min(number) AS first_number, max(number) AS last_number FROM invoices.invoices
    WHERE status = 'issued' AND issue_date BETWEEN sqlc.arg(issued_from)::date AND sqlc.arg(issued_to)::date
) b
WHERE b.first_number IS NOT NULL;

-- name: JournalGaps :many
-- JournalGaps is the gap check (D11): every number of the checked range that
-- no issued document holds. At most max_gaps are listed; a caller asking for
-- one more than it shows learns there are more.
SELECT g::bigint AS missing
FROM generate_series(sqlc.arg(checked_from)::bigint, sqlc.arg(checked_to)::bigint) AS g
WHERE NOT EXISTS (SELECT 1 FROM invoices.invoices d WHERE d.number = g AND d.status = 'issued')
ORDER BY g
LIMIT sqlc.arg(max_gaps);

-- name: HighestIssuedNumber :one
-- HighestIssuedNumber is the highest number an issued document holds; no row
-- when nothing is issued. The journal compares the counter with it (D11).
SELECT number FROM invoices.invoices WHERE status = 'issued' ORDER BY number DESC LIMIT 1;
