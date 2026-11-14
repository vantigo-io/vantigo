-- The reads of contracts.SalesJournal (accounting design A12, R1): issued
-- documents by number, the fact log by seq, and the invoices open at a day.

-- name: JournalDocuments :many
-- JournalDocuments is up to row_limit issued documents numbered after
-- after_number, in number order, each with the number of the invoice it
-- credits. A number becomes visible in order (the documents counter is held
-- to the issue's commit), so a reader resuming after the last it read never
-- skips one.
SELECT i.id, i.number::bigint AS number, i.kind, i.issue_date, i.due_date, i.customer_id, i.currency, i.exchange_rate,
    i.credits_invoice_id, o.number AS credits_number, i.net_total, i.vat_total, i.gross_total, i.vat_total_nok
FROM invoices.invoices i
LEFT JOIN invoices.invoices o ON o.id = i.credits_invoice_id
WHERE i.status = 'issued' AND i.number > sqlc.arg(after_number)::bigint
ORDER BY i.number
LIMIT sqlc.arg(row_limit)::int;

-- name: JournalLines :many
-- JournalLines is the lines of the documents named, by document and position,
-- with what they were issued with and made from.
SELECT id, invoice_id, position, line_net, vat_rate_percent, vat_category, saf_t_code,
    line_kind, tax_category_id, project_id
FROM invoices.lines
WHERE invoice_id = ANY(sqlc.arg(invoice_ids)::bigint[])
ORDER BY invoice_id, position;

-- name: JournalVatSummaries :many
-- JournalVatSummaries is the VAT summaries of the documents named, the
-- highest rate first, as a document prints them.
SELECT invoice_id, vat_category, rate_percent, saf_t_code, taxable_amount, vat_amount, vat_amount_nok
FROM invoices.vat_summaries
WHERE invoice_id = ANY(sqlc.arg(invoice_ids)::bigint[])
ORDER BY invoice_id, rate_percent DESC, vat_category;

-- name: JournalFacts :many
-- JournalFacts is up to row_limit facts after after_seq, in seq order, each
-- joined to the payment, charge payment or write-off it is about, its
-- invoice, and the bank line it came from: that line's account is the
-- receiving account. A recovered payment's fact carries the write-off it
-- recovers.
SELECT f.seq, f.kind, f.subject_id, f.invoice_id, f.business_date, f.removed_at,
    i.number::bigint AS invoice_number, i.customer_id, i.exchange_rate,
    COALESCE(p.amount, c.amount, w.amount)::numeric AS amount,
    COALESCE(p.source, c.source, 'manual')::text AS source,
    COALESCE(t.account, '')::text AS bank_account,
    w.id AS write_off_id, w.reason AS write_off_reason, w.vat_correction AS write_off_vat_correction,
    w.amount AS write_off_amount,
    COALESCE(CASE WHEN f.kind = 'recovered_payment' THEN (
        SELECT sum(r.amount) FROM invoices.payments r
        JOIN invoices.journal_facts rf ON rf.kind = 'recovered_payment' AND rf.subject_id = r.id
        WHERE r.write_off_id = p.write_off_id AND rf.seq < f.seq
          AND NOT EXISTS (SELECT 1 FROM invoices.journal_facts xf
                          WHERE xf.kind = 'recovered_payment_removed' AND xf.subject_id = r.id AND xf.seq < f.seq))
    END, 0)::numeric AS recovered_before
FROM invoices.journal_facts f
JOIN invoices.invoices i ON i.id = f.invoice_id
LEFT JOIN invoices.payments p
    ON f.kind IN ('payment', 'payment_removed', 'recovered_payment', 'recovered_payment_removed') AND p.id = f.subject_id
LEFT JOIN invoices.charge_payments c ON f.kind IN ('charge_payment', 'charge_payment_removed') AND c.id = f.subject_id
LEFT JOIN invoices.write_offs w
    ON w.id = CASE WHEN f.kind IN ('write_off', 'write_off_undone') THEN f.subject_id ELSE p.write_off_id END
LEFT JOIN invoices.bank_transactions t ON t.id = COALESCE(p.bank_transaction_id, c.bank_transaction_id)
WHERE f.seq > sqlc.arg(after_seq)::bigint
ORDER BY f.seq
LIMIT sqlc.arg(row_limit)::int;

-- name: JournalLastSeq :one
-- JournalLastSeq is the highest seq of a fact this snapshot sees, 0 when it
-- sees none: OpenAt's AsOfSeq, read in its page's snapshot.
SELECT COALESCE(max(seq), 0)::bigint AS last_seq FROM invoices.journal_facts;

-- name: JournalOpenAt :many
-- JournalOpenAt is up to row_limit invoices numbered after after_number, in
-- number order, issued on or before day and whose open amount at the end of
-- that day is not zero: its gross, less the credit notes issued on or before
-- day, less the payments paid on or before day that were not removed before
-- live_until — the first moment of the next business day — and less the
-- write-offs made on or before day that were not undone before it. A
-- recovered payment is not part of the open amount.
SELECT i.id, i.number::bigint AS number, i.customer_id, i.issue_date, i.due_date, i.currency, i.exchange_rate,
    (i.gross_total - COALESCE(cr.credited, 0) - COALESCE(pd.paid, 0))::numeric AS open_amount
FROM invoices.invoices i
CROSS JOIN LATERAL (
    SELECT sum(c.gross_total) AS credited FROM invoices.invoices c
    WHERE c.credits_invoice_id = i.id AND c.status = 'issued' AND c.issue_date <= sqlc.arg(day)::date
) AS cr
CROSS JOIN LATERAL (
    SELECT coalesce((SELECT sum(p.amount) FROM invoices.payments p
        WHERE p.invoice_id = i.id AND p.kind = 'payment' AND p.paid_on <= sqlc.arg(day)::date
          AND (p.removed_at IS NULL OR p.removed_at >= sqlc.arg(live_until)::timestamptz)), 0)
      + coalesce((SELECT sum(w.amount) FROM invoices.write_offs w
        WHERE w.invoice_id = i.id AND w.written_off_on <= sqlc.arg(day)::date
          AND (w.undone_at IS NULL OR w.undone_at >= sqlc.arg(live_until)::timestamptz)), 0) AS paid
) AS pd
WHERE i.kind = 'invoice' AND i.status = 'issued' AND i.issue_date <= sqlc.arg(day)::date
  AND i.number > sqlc.arg(after_number)::bigint
  AND i.gross_total - COALESCE(cr.credited, 0) - COALESCE(pd.paid, 0) <> 0
ORDER BY i.number
LIMIT sqlc.arg(row_limit)::int;

-- name: JournalCreditNotesOf :many
-- JournalCreditNotesOf is the issued credit notes of an invoice, as
-- JournalDocuments answers them, in number order.
SELECT i.id, i.number::bigint AS number, i.kind, i.issue_date, i.due_date, i.customer_id, i.currency, i.exchange_rate,
    i.credits_invoice_id, o.number AS credits_number, i.net_total, i.vat_total, i.gross_total, i.vat_total_nok
FROM invoices.invoices i
LEFT JOIN invoices.invoices o ON o.id = i.credits_invoice_id
WHERE i.status = 'issued' AND i.kind = 'credit_note' AND i.credits_invoice_id = sqlc.arg(invoice_id)::bigint
ORDER BY i.number;

-- name: JournalDocumentByID :many
-- JournalDocumentByID is the issued document with that id, as
-- JournalDocuments answers one: no row when there is none or it is a draft.
SELECT i.id, i.number::bigint AS number, i.kind, i.issue_date, i.due_date, i.customer_id, i.currency, i.exchange_rate,
    i.credits_invoice_id, o.number AS credits_number, i.net_total, i.vat_total, i.gross_total, i.vat_total_nok
FROM invoices.invoices i
LEFT JOIN invoices.invoices o ON o.id = i.credits_invoice_id
WHERE i.status = 'issued' AND i.id = sqlc.arg(id)::bigint;
