-- The dashboard's attention items (invoices payments and reminders design
-- D12; plan reading 29): what a person should look at now, read on the pool
-- with no lock, every day a parameter from the request's one clock read.

-- name: AttentionOverdue :many
-- AttentionOverdue is the 20 most overdue invoices: the issued invoices whose
-- state is overdue on @today — invoices.document_state, the overdue list's
-- and the stats' — once the live Vipps reservations (D2, D16) are counted
-- with the payments, as the overdue list's engine counts them, so an
-- invoice a capture in progress holds whole is no item (m14); the oldest
-- due date first. E moves only with the due date
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
LEFT JOIN LATERAL (
    SELECT coalesce(sum(a.reserved_amount), 0)::numeric(14,2) AS reserved
    FROM invoices.payment_attempts a
    WHERE a.invoice_id = i.id AND a.state IN ('capturing', 'capture_unknown')
) rs ON true
WHERE i.kind = 'invoice' AND i.status = 'issued'
  AND invoices.document_state(i.kind, i.status, i.gross_total, coalesce(cr.credited, 0),
      coalesce(pd.paid, 0) + coalesce(rs.reserved, 0), i.due_date, @today::date) = 'overdue'
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

-- name: AttentionCaptureUnknown :many
-- AttentionCaptureUnknown is paymentCaptureUnknown's attempts (D12, D16):
-- every capture whose outcome is unknown, and every attempt whose money
-- Vipps shows captured but whose registration failed — its last_error
-- starting with @registration_failed, the worker's own words — with the
-- invoice's buyer name.
SELECT a.id, a.invoice_id, coalesce(a.authorized_at, a.created_at)::timestamptz AS occurred_at, i.buyer_name
FROM invoices.payment_attempts a
JOIN invoices.invoices i ON i.id = a.invoice_id
WHERE a.state = 'capture_unknown'
   OR (a.state IN ('capturing', 'capture_failed', 'cancelling')
       AND starts_with(coalesce(a.last_error, ''), @registration_failed::text))
ORDER BY a.id;

-- name: AttentionCaptureFailed :many
-- AttentionCaptureFailed is paymentCaptureFailed's attempts (D12, plan
-- reading 18): every failed capture finished since @since (14 days back)
-- whose invoice is still open — gross less the issued credit notes and the
-- live payments above zero — and of which no later attempt captured; one
-- whose registration failed is paymentCaptureUnknown's instead.
SELECT a.id, a.invoice_id, a.finished_at::timestamptz AS occurred_at, i.buyer_name
FROM invoices.payment_attempts a
JOIN invoices.invoices i ON i.id = a.invoice_id
LEFT JOIN LATERAL (
    SELECT coalesce(sum(c.gross_total), 0)::numeric(14,2) AS credited
    FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued'
) cr ON true
LEFT JOIN LATERAL (
    SELECT coalesce(sum(p.amount), 0)::numeric(14,2) AS paid
    FROM invoices.payments p WHERE p.invoice_id = i.id AND p.removed_at IS NULL
) pd ON true
WHERE a.state = 'capture_failed' AND a.finished_at >= @since::timestamptz
  AND NOT starts_with(coalesce(a.last_error, ''), @registration_failed::text)
  AND i.gross_total - coalesce(cr.credited, 0) - coalesce(pd.paid, 0) > 0
  AND NOT EXISTS (
      SELECT 1 FROM invoices.payment_attempts b
      WHERE b.invoice_id = a.invoice_id AND b.id <> a.id AND b.state = 'captured' AND b.captured_at >= a.finished_at)
ORDER BY a.id;

-- name: AttentionStrandedCandidates :many
-- AttentionStrandedCandidates is what paymentReservationStranded judges
-- (D1, D12, I20): every reservation — capturing or capture_unknown, stranded
-- when Vipps is switched off or its sales unit is no longer the stored one,
-- which the caller judges — and every attempt parked for a person: a create
-- given up, or a release Vipps refuses for good (next_poll_at NULL on a row
-- the worker would otherwise poll).
SELECT a.id, a.invoice_id, a.msn, (a.next_poll_at IS NULL)::boolean AS parked,
    coalesce(a.authorized_at, a.created_at)::timestamptz AS occurred_at, i.buyer_name
FROM invoices.payment_attempts a
JOIN invoices.invoices i ON i.id = a.invoice_id
WHERE a.state IN ('capturing', 'capture_unknown')
   OR (a.next_poll_at IS NULL
       AND (a.state IN ('creating', 'created', 'authorized', 'cancelling') OR a.release_pending))
ORDER BY a.id;

-- name: AttentionRefundSeen :many
-- AttentionRefundSeen is vippsRefundSeen's attempts (D16, I6): every
-- captured attempt Vipps shows refunded — the refund watch stored it —
-- whose payment is still live. Removing the payment clears it.
SELECT a.id, a.invoice_id, a.captured_at::timestamptz AS occurred_at, i.buyer_name
FROM invoices.payment_attempts a
JOIN invoices.invoices i ON i.id = a.invoice_id
WHERE a.state = 'captured' AND a.refunded_amount > 0
  AND EXISTS (SELECT 1 FROM invoices.payments p WHERE p.payment_attempt_id = a.id AND p.removed_at IS NULL)
ORDER BY a.id;
