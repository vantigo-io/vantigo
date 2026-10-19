-- What one customer was invoiced and owes (customers receivables design D1):
-- this module's contracts.CustomerReceivables, read by the customers module
-- for its overview. Both queries derive each issued document's state and
-- open amount exactly as ListInvoices and InvoiceStatsNow do — the two
-- lateral joins and invoices.document_state on the business day the caller
-- passes as @today — so the figures a customer page shows can never
-- disagree with the Invoices app's own list. Drafts are never read: a draft
-- is not a sales document. Charges are never read either: the open amount is
-- the principal's, as everywhere in this module.

-- name: CustomerReceivablesByCurrency :many
-- CustomerReceivablesByCurrency is the money, one row per currency any issued
-- document of the customer is in, by ISO code: what was issued and credited
-- in the calendar year [@this_year_from, @next_year_from) and in the year
-- before it, [@last_year_from, @this_year_from), and what stands open of the
-- outstanding and the overdue invoices. A currency whose figures are all
-- zero — only paid invoices from years ago, say — is still a row.
WITH d AS (
    SELECT i.kind, i.currency, i.gross_total, i.issue_date,
           (i.gross_total - coalesce(cr.credited, 0) - coalesce(pd.paid, 0))::numeric(14,2) AS open_amount,
           invoices.document_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), i.due_date, @today::date)::text AS state
    FROM invoices.invoices i
    LEFT JOIN LATERAL (
        SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited
        FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
    ) cr ON true
    LEFT JOIN LATERAL (
        SELECT coalesce(sum(p.amount), 0)::numeric(14,2) AS paid
        FROM invoices.payments p WHERE p.invoice_id = i.id AND p.removed_at IS NULL
    ) pd ON true
    WHERE i.customer_id = @customer_id AND i.status = 'issued'
)
SELECT currency::text AS currency,
       coalesce(sum(gross_total) FILTER (WHERE kind = 'invoice' AND issue_date >= @this_year_from::date AND issue_date < @next_year_from::date), 0)::numeric(14,2) AS issued_this_year,
       coalesce(sum(gross_total) FILTER (WHERE kind = 'credit_note' AND issue_date >= @this_year_from::date AND issue_date < @next_year_from::date), 0)::numeric(14,2) AS credited_this_year,
       coalesce(sum(gross_total) FILTER (WHERE kind = 'invoice' AND issue_date >= @last_year_from::date AND issue_date < @this_year_from::date), 0)::numeric(14,2) AS issued_last_year,
       coalesce(sum(gross_total) FILTER (WHERE kind = 'credit_note' AND issue_date >= @last_year_from::date AND issue_date < @this_year_from::date), 0)::numeric(14,2) AS credited_last_year,
       coalesce(sum(open_amount) FILTER (WHERE state IN ('open', 'partially_paid', 'overdue')), 0)::numeric(14,2) AS outstanding,
       coalesce(sum(open_amount) FILTER (WHERE state = 'overdue'), 0)::numeric(14,2) AS overdue
FROM d
GROUP BY currency
ORDER BY currency;

-- name: CustomerReceivablesCounts :one
-- CustomerReceivablesCounts is the rest, across every currency: how many
-- invoices are outstanding and overdue, how many of the outstanding ones
-- carry a live hold or a live hand-off to collection, the oldest overdue due
-- date, the latest issue date of any document and the latest live payment.
-- Each date is NULL when there is none. A customer with no issued document
-- answers one row of zeros and NULLs — the aggregates over no rows.
WITH d AS (
    SELECT i.id, i.issue_date, i.due_date,
           invoices.document_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), i.due_date, @today::date)::text AS state
    FROM invoices.invoices i
    LEFT JOIN LATERAL (
        SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited
        FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
    ) cr ON true
    LEFT JOIN LATERAL (
        SELECT coalesce(sum(p.amount), 0)::numeric(14,2) AS paid
        FROM invoices.payments p WHERE p.invoice_id = i.id AND p.removed_at IS NULL
    ) pd ON true
    WHERE i.customer_id = @customer_id AND i.status = 'issued'
)
SELECT count(*) FILTER (WHERE state IN ('open', 'partially_paid', 'overdue'))::int AS outstanding_count,
       count(*) FILTER (WHERE state = 'overdue')::int AS overdue_count,
       count(*) FILTER (WHERE state IN ('open', 'partially_paid', 'overdue')
                          AND EXISTS (SELECT 1 FROM invoices.invoice_holds h WHERE h.invoice_id = d.id AND h.lifted_at IS NULL))::int AS on_hold_count,
       count(*) FILTER (WHERE state IN ('open', 'partially_paid', 'overdue')
                          AND EXISTS (SELECT 1 FROM invoices.collection_handoffs c WHERE c.invoice_id = d.id AND c.withdrawn_on IS NULL))::int AS at_collection_count,
       (min(due_date) FILTER (WHERE state = 'overdue'))::date AS oldest_overdue_due_on,
       max(issue_date)::date AS last_issued_on,
       (SELECT max(p.paid_on)
        FROM invoices.payments p
        JOIN invoices.invoices i ON i.id = p.invoice_id
        WHERE i.customer_id = @customer_id AND i.status = 'issued' AND p.removed_at IS NULL)::date AS last_payment_on
FROM d;
