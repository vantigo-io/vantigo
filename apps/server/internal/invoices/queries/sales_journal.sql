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
-- joined to the payment, charge payment, write-off, credit movement or
-- credit merge it is about, its invoice when it has one (a refund's movement
-- and a merge have none), and the bank line it came from: that line's
-- account is the receiving account. A recovered payment's fact carries the
-- write-off it recovers. amount_nok is the NOK a payment, a write-off, a
-- movement or a merge stored (I13); a charge payment has none and is read at
-- its invoice's rate. A credit fact's customer is the one its movement
-- belonged to when it was written; a merge's the one it went into.
SELECT f.seq, f.kind, f.subject_id, f.invoice_id, f.business_date, f.removed_at,
    COALESCE(i.number, 0)::bigint AS invoice_number, COALESCE(f.customer_id, i.customer_id)::int AS customer_id, i.exchange_rate,
    COALESCE(p.amount, c.amount, w.amount, k.amount, m.amount)::numeric AS amount,
    COALESCE(p.amount_nok, w.amount_nok, k.amount_nok, m.amount_nok)::numeric AS amount_nok,
    COALESCE(p.source, c.source,
        CASE WHEN t.format = 'feed' THEN 'bank_feed' ELSE t.format END, 'manual')::text AS source,
    COALESCE(t.account, '')::text AS bank_account,
    (CASE WHEN t.format = 'feed' THEN t.line_ref ELSE COALESCE(t.archive_ref, '') END)::text AS bank_reference,
    w.id AS write_off_id, w.reason AS write_off_reason, w.vat_correction AS write_off_vat_correction,
    w.amount AS write_off_amount,
    COALESCE(CASE WHEN f.kind = 'recovered_payment' THEN (
        SELECT sum(r.amount) FROM invoices.payments r
        JOIN invoices.journal_facts rf ON rf.kind = 'recovered_payment' AND rf.subject_id = r.id
        WHERE r.write_off_id = p.write_off_id AND rf.seq < f.seq
          AND NOT EXISTS (SELECT 1 FROM invoices.journal_facts xf
                          WHERE xf.kind = 'recovered_payment_removed' AND xf.subject_id = r.id AND xf.seq < f.seq))
    END, 0)::numeric AS recovered_before,
    k.kind AS credit_kind, COALESCE(k.currency, m.currency, '')::text AS credit_currency,
    k.payment_id AS credit_payment_id, k.bank_transaction_id AS credit_bank_transaction_id,
    k.document_id AS credit_document_id, COALESCE(k.refund_id, o.refund_id) AS credit_refund_id,
    k.reverses_id AS credit_reverses_id, o.kind AS credit_reversed_kind,
    COALESCE(rf.bank_account, '')::text AS refund_bank_account,
    m.from_customer_id AS merge_from_customer_id, m.merged_at
FROM invoices.journal_facts f
LEFT JOIN invoices.invoices i ON i.id = f.invoice_id
LEFT JOIN invoices.payments p
    ON f.kind IN ('payment', 'payment_removed', 'recovered_payment', 'recovered_payment_removed') AND p.id = f.subject_id
LEFT JOIN invoices.charge_payments c ON f.kind IN ('charge_payment', 'charge_payment_removed') AND c.id = f.subject_id
LEFT JOIN invoices.write_offs w
    ON w.id = CASE WHEN f.kind IN ('write_off', 'write_off_undone') THEN f.subject_id ELSE p.write_off_id END
LEFT JOIN invoices.customer_credits k ON f.kind = 'credit' AND k.id = f.subject_id
LEFT JOIN invoices.customer_credits o ON o.id = k.reverses_id
LEFT JOIN invoices.credit_refunds rf ON rf.id = COALESCE(k.refund_id, o.refund_id)
LEFT JOIN invoices.credit_merges m ON f.kind = 'credit_merged' AND m.id = f.subject_id
LEFT JOIN invoices.bank_transactions t
    ON t.id = COALESCE(p.bank_transaction_id, c.bank_transaction_id, k.bank_transaction_id, o.bank_transaction_id)
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
-- live_until — the first moment of the next business day — less the
-- write-offs made on or before day that were not undone before it, and plus
-- what its credit notes' surplus moved to the customer by day, less what of
-- it was returned by then (I10). A recovered payment is not part of the open
-- amount.
SELECT i.id, i.number::bigint AS number, i.customer_id, i.issue_date, i.due_date, i.currency, i.exchange_rate,
    (i.gross_total - COALESCE(cr.credited, 0) - COALESCE(pd.paid, 0) + COALESCE(mv.moved, 0))::numeric AS open_amount
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
CROSS JOIN LATERAL (
    SELECT sum(k.amount) AS moved FROM invoices.customer_credits k
    WHERE k.invoice_id = i.id AND k.credit_day <= sqlc.arg(day)::date
      AND (k.kind = 'credit_note'
           OR (k.kind = 'reversed' AND EXISTS (SELECT 1 FROM invoices.customer_credits o
                                               WHERE o.id = k.reverses_id AND o.kind = 'credit_note')))
) AS mv
WHERE i.kind = 'invoice' AND i.status = 'issued' AND i.issue_date <= sqlc.arg(day)::date
  AND i.number > sqlc.arg(after_number)::bigint
  AND i.gross_total - COALESCE(cr.credited, 0) - COALESCE(pd.paid, 0) + COALESCE(mv.moved, 0) <> 0
ORDER BY i.number
LIMIT sqlc.arg(row_limit)::int;

-- name: JournalCreditsAt :many
-- JournalCreditsAt is up to row_limit customers with an id after
-- after_customer_id, in id order, each with its credit per currency at the
-- end of day where it is not zero (I16, plan reading 6): the movements dated
-- on or before day, each counted for the customer it belonged to when it was
-- written (its fact's), plus the balances merges moved into the customer
-- before live_until, less those they moved out of it. The page holds every
-- currency of the customers it names. A movement written after a merge and
-- dated before it is its fact's customer's — the survivor's — on every day,
-- deliberately (B-1 review m2): Accounting posts it on that customer's credit
-- item, and the merge's fact moves only what the absorbed customer held when
-- it merged, so counting it for the absorbed customer would leave a residue
-- on its item once the merge posts.
WITH movements AS (
    SELECT f.customer_id, k.currency, k.amount, k.amount_nok
    FROM invoices.journal_facts f
    JOIN invoices.customer_credits k ON f.kind = 'credit' AND k.id = f.subject_id
    WHERE k.credit_day <= sqlc.arg(day)::date
    UNION ALL
    SELECT m.into_customer_id, m.currency, m.amount, m.amount_nok
    FROM invoices.credit_merges m WHERE m.merged_at < sqlc.arg(live_until)::timestamptz
    UNION ALL
    SELECT m.from_customer_id, m.currency, -m.amount, -m.amount_nok
    FROM invoices.credit_merges m WHERE m.merged_at < sqlc.arg(live_until)::timestamptz
), balances AS (
    SELECT customer_id, currency, sum(amount) AS balance, sum(amount_nok) AS balance_nok
    FROM movements
    WHERE customer_id > sqlc.arg(after_customer_id)::int
    GROUP BY customer_id, currency
    HAVING sum(amount) <> 0
), page AS (
    SELECT DISTINCT customer_id FROM balances ORDER BY customer_id LIMIT sqlc.arg(row_limit)::int
)
SELECT b.customer_id::int AS customer_id, b.currency::text AS currency, b.balance::numeric(14,2) AS balance,
    b.balance_nok::numeric(14,2) AS balance_nok
FROM balances b JOIN page USING (customer_id)
ORDER BY b.customer_id, b.currency;

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
