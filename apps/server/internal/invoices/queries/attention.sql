-- The dashboard's attention items (invoices payments and reminders design
-- D12; plan reading 29): what a person should look at now, read on the pool
-- with no lock, every day a parameter from the request's one clock read.

-- name: AttentionOverdue :many
-- AttentionOverdue is the 20 most overdue invoices: the issued invoices whose
-- state is overdue on @today — invoices.document_state, the overdue list's
-- and the stats' — the oldest due date first. E moves only with the due date
-- (reminderrules.EffectiveDue), so this order is E's.
SELECT i.id, i.number, i.buyer_name, i.due_date
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
ORDER BY i.due_date, i.number, i.id
LIMIT 20;

-- name: AttentionRefundDue :many
-- AttentionRefundDue is every issued invoice that may owe the buyer money
-- back: its open amount — gross less the issued credit notes and the live
-- payments — below zero (a credit note after a payment, M16), or a live
-- charge payment, whose refund due the engine's charges formula then judges.
-- changed_at is when its money last moved: the latest credit note issued,
-- payment or charge payment registered, or waiver made.
SELECT i.id, i.number, i.buyer_name,
    (i.gross_total - coalesce(cr.credited, 0) - coalesce(pd.paid, 0))::numeric(14,2) AS open_amount,
    (coalesce(cp.live, 0) > 0)::boolean AS has_charge_payments,
    coalesce(greatest(cr.last_at, pd.last_at, cp.last_at, w.last_at), i.issued_at)::timestamptz AS changed_at
FROM invoices.invoices i
LEFT JOIN LATERAL (
    SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited, max(c.issued_at) AS last_at
    FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
) cr ON true
LEFT JOIN LATERAL (
    SELECT coalesce(sum(p.amount) FILTER (WHERE p.removed_at IS NULL), 0)::numeric(14,2) AS paid,
        max(p.registered_at) AS last_at
    FROM invoices.payments p WHERE p.invoice_id = i.id
) pd ON true
LEFT JOIN LATERAL (
    SELECT count(*) FILTER (WHERE c.removed_at IS NULL) AS live, max(c.registered_at) AS last_at
    FROM invoices.charge_payments c WHERE c.invoice_id = i.id
) cp ON true
LEFT JOIN LATERAL (
    SELECT max(w.waived_at) AS last_at FROM invoices.charge_waivers w WHERE w.invoice_id = i.id
) w ON true
WHERE i.kind = 'invoice' AND i.status = 'issued'
  AND (i.gross_total - coalesce(cr.credited, 0) - coalesce(pd.paid, 0) < 0 OR coalesce(cp.live, 0) > 0)
ORDER BY i.id;

-- name: AttentionBankFiles :many
-- AttentionBankFiles is every bank file with lines still open — pending
-- (matching stopped early), exception or duplicate — and how many: the
-- queue's open lines (ix_bank_transactions_open), counted as
-- CountBankTransactions counts them per status and file.
SELECT f.id, f.first_booked_on, f.last_booked_on, f.uploaded_at, count(*)::int AS open_lines
FROM invoices.bank_files f
JOIN invoices.bank_transactions t ON t.bank_file_id = f.id
WHERE t.status IN ('pending', 'exception', 'duplicate')
GROUP BY f.id
ORDER BY f.uploaded_at, f.id;

-- name: AttentionFailedLetters :many
-- AttentionFailedLetters is every letter whose e-mail failed for good (D10),
-- with its invoice's buyer name, the oldest failure first.
SELECT r.id, r.invoice_id, r.failed_at, i.buyer_name
FROM invoices.reminders r
JOIN invoices.invoices i ON i.id = r.invoice_id
WHERE r.status = 'failed'
ORDER BY r.failed_at, r.id;

-- name: AttentionHeldLetters :many
-- AttentionHeldLetters is the letters waiting on their rates or the regime
-- review (plan reading 46), one row per cause: how many, and the oldest
-- one's creation.
SELECT held_reason::text AS held_reason, count(*)::int AS letters, min(created_at)::timestamptz AS since
FROM invoices.reminders
WHERE status = 'queued' AND held_reason IS NOT NULL
GROUP BY held_reason
ORDER BY held_reason;

-- name: AttentionUnpostedBatches :many
-- AttentionUnpostedBatches is every print batch neither confirmed posted nor
-- reprinted two days after its posting day (plan reading 29) — the open
-- batches the paper page lists — with its printed letters counted.
SELECT b.id, b.post_on,
    (SELECT count(*) FROM invoices.reminders r WHERE r.print_batch_id = b.id AND r.status = 'printed')::int AS letters
FROM invoices.reminder_print_batches b
WHERE b.posted_on IS NULL AND b.reprinted_at IS NULL AND b.post_on + 2 <= @today::date
ORDER BY b.post_on, b.id;
