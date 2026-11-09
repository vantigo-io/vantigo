-- name: InvoicesToAnnounceOverdue :many
-- InvoicesToAnnounceOverdue is the issued invoices whose state is overdue on
-- @today — invoices.document_state, the overdue list's and the stats' — that no
-- announcement was made for and that fell due in the last seven days (an older one is
-- a backlog, not news), oldest due date first, locked so that another replica
-- skips them: the event is published while the invoice's row is held.
SELECT i.id, i.number, i.customer_id, i.currency, i.due_date, i.gross_total
FROM invoices.invoices i
LEFT JOIN LATERAL (
    SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited
    FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
) cr ON true
LEFT JOIN LATERAL (
    SELECT coalesce(sum(p.amount), 0)::numeric(14,2) AS paid
    FROM invoices.payments p WHERE p.invoice_id = i.id AND p.removed_at IS NULL
) pd ON true
WHERE i.kind = 'invoice' AND i.status = 'issued'
  AND invoices.document_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), i.due_date, @today::date) = 'overdue'
  AND i.due_date >= (sqlc.arg(today)::date - 7)
  AND NOT EXISTS (SELECT 1 FROM invoices.overdue_announced a WHERE a.invoice_id = i.id)
ORDER BY i.due_date, i.id
LIMIT @n
FOR UPDATE OF i SKIP LOCKED;

-- name: MarkOverdueAnnounced :execrows
INSERT INTO invoices.overdue_announced (invoice_id, announced_on, announced_at)
VALUES (@invoice_id, @announced_on::date, @now::timestamptz)
ON CONFLICT (invoice_id) DO NOTHING;
