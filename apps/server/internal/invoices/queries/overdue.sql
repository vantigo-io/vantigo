-- The overdue list's candidates (invoices payments and reminders design
-- D12): the issued invoices whose state is overdue on @today — the same
-- invoices.document_state the list and the stats derive — and, when
-- @with_charges, also the paid ones whose sent letters claimed a charge,
-- whose outstanding the engine then judges. The engine's input is the
-- rule-input loader's (ruleinput.sql); these read only who the candidates
-- are, counted first for the 5 000 cap, so the whole set is judged before
-- the list pages it (M7).

-- name: CountOverdueCandidates :one
-- CountOverdueCandidates is how many invoices OverdueCandidates would answer.
SELECT count(*)::int
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
  AND (sqlc.narg(customer_id)::int IS NULL OR i.customer_id = sqlc.narg(customer_id)::int)
  AND (sqlc.narg(due_before)::date IS NULL OR i.due_date < sqlc.narg(due_before)::date)
  AND (invoices.document_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), i.due_date, @today::date) = 'overdue'
       OR (@with_charges::boolean
           AND invoices.document_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), i.due_date, @today::date) = 'paid'
           AND EXISTS (SELECT 1 FROM invoices.reminders r
                       WHERE r.invoice_id = i.id AND r.status = 'sent'
                         AND (r.fee > 0 OR r.compensation > 0 OR r.interest > 0))));

-- name: OverdueCandidates :many
-- OverdueCandidates is the candidates themselves, the oldest due date first,
-- with what an item shows of the invoice beside the engine's figures.
SELECT i.id, i.number, i.customer_id, i.buyer_name, i.buyer_type, i.issue_date, i.due_date
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
  AND (sqlc.narg(customer_id)::int IS NULL OR i.customer_id = sqlc.narg(customer_id)::int)
  AND (sqlc.narg(due_before)::date IS NULL OR i.due_date < sqlc.narg(due_before)::date)
  AND (invoices.document_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), i.due_date, @today::date) = 'overdue'
       OR (@with_charges::boolean
           AND invoices.document_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0), coalesce(pd.paid, 0), i.due_date, @today::date) = 'paid'
           AND EXISTS (SELECT 1 FROM invoices.reminders r
                       WHERE r.invoice_id = i.id AND r.status = 'sent'
                         AND (r.fee > 0 OR r.compensation > 0 OR r.interest > 0))))
ORDER BY i.due_date, i.number, i.id;

-- name: InvoiceCustomers :many
-- InvoiceCustomers is the customer of each issued invoice among ids: a run
-- reads its items' billing profiles, one per distinct customer, before it
-- locks anything (D10).
SELECT id, customer_id FROM invoices.invoices
WHERE id = ANY(@ids::bigint[]) AND kind = 'invoice' AND status = 'issued'
ORDER BY id;

-- name: LastBookedOn :one
-- LastBookedOn is the latest booking day of any imported bank file — the
-- bank data's freshness (D10, I3); NULL when no file was ever imported.
SELECT max(last_booked_on)::date AS last_booked_on FROM invoices.bank_files;

-- name: OcrAccounts :many
-- OcrAccounts is every account whose import format is OCR giro: payments
-- without a KID never reach such a file (D10's standing note).
SELECT account FROM invoices.bank_import_accounts WHERE format = 'ocr' ORDER BY account;

-- name: LiveHolds :many
-- LiveHolds is the live hold of each invoice among ids that has one (D11):
-- an overdue item shows it.
SELECT * FROM invoices.invoice_holds
WHERE invoice_id = ANY(@ids::bigint[]) AND lifted_at IS NULL
ORDER BY invoice_id;

-- name: LiveHandoffs :many
-- LiveHandoffs is the live hand-off of each invoice among ids that has one
-- (D11): an overdue item shows it.
SELECT * FROM invoices.collection_handoffs
WHERE invoice_id = ANY(@ids::bigint[]) AND withdrawn_on IS NULL
ORDER BY invoice_id;
