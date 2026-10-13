-- On-site Vipps payments, the day's list and its reconciliation (invoices
-- payments and reminders design D17). Every query takes the time as a
-- parameter from Deps.Clock(), never now(). An on-site request is made by
-- the attempt's create (attempts.sql) under its invoice's lock; a cancel
-- locks the attempt alone (lockPaymentAttempt); the list and the
-- reconciliation take no lock.

-- name: LiveOnSiteAttempt :one
-- LiveOnSiteAttempt is an invoice's live on-site attempt — the one
-- ux_payment_attempts_on_site_live allows: creating, created, authorized,
-- capturing or capture_unknown, parked or not. Read under the invoice's
-- lock by a request, and on the pool by a quick invoice's replay.
SELECT * FROM invoices.payment_attempts
WHERE invoice_id = @invoice_id AND flow = 'on_site'
  AND state IN ('creating', 'created', 'authorized', 'capturing', 'capture_unknown')
ORDER BY id DESC
LIMIT 1;

-- name: CancelOnSiteAttempt :one
-- CancelOnSiteAttempt moves an on-site request Vipps created and nobody
-- was seen to approve to cancelling, due at once: the payments worker
-- cancels it at Vipps. The caller holds the attempt's lock; only from
-- created, so a worker that learned it was approved meanwhile wins.
UPDATE invoices.payment_attempts
SET state = 'cancelling', next_poll_at = @now::timestamptz, failures = 0
WHERE id = @id AND flow = 'on_site' AND state = 'created'
RETURNING *;

-- name: VippsPaymentsBetween :many
-- VippsPaymentsBetween is every attempt captured from @day_start to before
-- @day_end — one Oslo day — with its invoice's number and buyer, oldest
-- first.
SELECT a.id, a.reference, a.flow, a.captured_amount, a.captured_at,
       i.id AS invoice_id, i.number AS invoice_number, i.buyer_name
FROM invoices.payment_attempts a
JOIN invoices.invoices i ON i.id = a.invoice_id
WHERE a.captured_at >= @day_start::timestamptz AND a.captured_at < @day_end::timestamptz
  AND a.captured_amount IS NOT NULL
ORDER BY a.captured_at, a.id;

-- name: VippsDayReconciliation :one
-- VippsDayReconciliation is the day's reconciliation record; no row is
-- pgx.ErrNoRows.
SELECT * FROM invoices.vipps_day_reconciliations WHERE day = @day;

-- name: InsertVippsDayReconciliation :one
-- InsertVippsDayReconciliation records a day reconciled with its totals as
-- they stand, in one statement — the payments captured from @day_start
-- to before @day_end counted and summed per flow — once: a day reconciled before
-- inserts nothing (pgx.ErrNoRows).
INSERT INTO invoices.vipps_day_reconciliations (
    day, payments, total, on_site_total, pay_link_total, reconciled_by_user_id, reconciled_at, note
)
SELECT @day::date, count(*)::integer,
       coalesce(sum(captured_amount), 0),
       coalesce(sum(captured_amount) FILTER (WHERE flow = 'on_site'), 0),
       coalesce(sum(captured_amount) FILTER (WHERE flow = 'pay_link'), 0),
       @reconciled_by_user_id::uuid, @now::timestamptz, @note::text
FROM invoices.payment_attempts
WHERE captured_at >= @day_start::timestamptz AND captured_at < @day_end::timestamptz AND captured_amount IS NOT NULL
ON CONFLICT (day) DO NOTHING
RETURNING *;
