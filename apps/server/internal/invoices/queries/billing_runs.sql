-- Billing runs and their items (energy consumption billing design D10; plan
-- docs/superpowers/plans/2026-10-27-invoices-billing-runs.md, readings 1, 3,
-- 4, 7–9). A run's row precedes its items, made in one transaction; the
-- invoices-billing worker leases one queued item at a time, the shape of
-- reminder_worker.sql's ClaimReminder, and every later write of the claim
-- names its lease — 0 rows is another actor's move. Every query takes the
-- time as a parameter from Deps.Clock(), never now().

-- name: InsertBillingRun :one
-- InsertBillingRun makes a queued run. ux_billing_runs_active refuses a
-- second run while one is queued or running (23505).
INSERT INTO invoices.billing_runs (through, issue, customers, created_by_user_id, created_at)
VALUES (@through, @issue, @customers, @created_by_user_id, @created_at)
RETURNING *;

-- name: InsertBillingRunItems :exec
-- InsertBillingRunItems makes one queued item per customer, due at once.
INSERT INTO invoices.billing_run_items (run_id, customer_id, next_attempt_at)
SELECT @run_id, c, @now::timestamptz
FROM unnest(@customer_ids::int[]) AS c;

-- name: ActiveBillingRun :one
-- ActiveBillingRun is the run that is queued or running, if any. No row is
-- pgx.ErrNoRows.
SELECT id FROM invoices.billing_runs WHERE status IN ('queued', 'running');

-- name: CountBillingRuns :one
SELECT count(*)::int FROM invoices.billing_runs;

-- name: ListBillingRuns :many
-- ListBillingRuns is one page of the runs, the newest first.
SELECT * FROM invoices.billing_runs
ORDER BY id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: GetBillingRun :one
SELECT * FROM invoices.billing_runs WHERE id = @id;

-- name: BillingRunCounts :many
-- BillingRunCounts is, per run, how many of its items have each status.
SELECT run_id, status, count(*)::int AS items
FROM invoices.billing_run_items
WHERE run_id = ANY(@run_ids::bigint[])
GROUP BY run_id, status
ORDER BY run_id, status;

-- name: CountBillingRunItems :one
-- CountBillingRunItems counts a run's items, of one status when given.
SELECT count(*)::int FROM invoices.billing_run_items
WHERE run_id = @run_id AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text);

-- name: ListBillingRunItems :many
-- ListBillingRunItems is one page of a run's items, of one status when
-- given, in the order they were made.
SELECT * FROM invoices.billing_run_items
WHERE run_id = @run_id AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY id
LIMIT @page_size OFFSET @page_offset;

-- name: BillingRunDocuments :many
-- BillingRunDocuments is what still exists of the documents items made.
SELECT id, number, status FROM invoices.invoices
WHERE id = ANY(@ids::bigint[])
ORDER BY id;

-- name: CancelBillingRun :one
-- CancelBillingRun ends a queued or running run as cancelled. No row is a
-- run already done or cancelled, or none at all.
UPDATE invoices.billing_runs SET status = 'cancelled', finished_at = @now::timestamptz
WHERE id = @id AND status IN ('queued', 'running')
RETURNING *;

-- name: CancelBillingRunItems :execrows
-- CancelBillingRunItems cancels a run's queued items no live lease holds:
-- an item being carried finishes and records its outcome.
UPDATE invoices.billing_run_items
SET status = 'cancelled', finished_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
WHERE run_id = @run_id AND status = 'queued'
  AND (lease_until IS NULL OR lease_until <= @now::timestamptz);

-- name: ClaimBillingRunItem :one
-- ClaimBillingRunItem leases the oldest queued item due by @now, of a run
-- queued or running, whose lease, if any, has run out. One conditional
-- UPDATE over a SKIP LOCKED pick, so two workers never take one item. No row
-- is pgx.ErrNoRows.
UPDATE invoices.billing_run_items
SET lease_id = @lease_id::text, lease_until = @lease_until::timestamptz
WHERE status = 'queued' AND id = (
    SELECT i.id FROM invoices.billing_run_items i
    JOIN invoices.billing_runs r ON r.id = i.run_id
    WHERE i.status = 'queued'
      AND r.status IN ('queued', 'running')
      AND i.next_attempt_at <= @now::timestamptz
      AND (i.lease_until IS NULL OR i.lease_until <= @now::timestamptz)
    ORDER BY i.next_attempt_at, i.id
    LIMIT 1
    FOR UPDATE OF i SKIP LOCKED
)
RETURNING *;

-- name: StartBillingRun :exec
-- StartBillingRun moves a queued run to running at its first claim.
UPDATE invoices.billing_runs SET status = 'running', started_at = @now::timestamptz
WHERE id = @id AND status = 'queued';

-- name: AddBillingRunItemInvoice :execrows
-- AddBillingRunItemInvoice records a document the claimed item made, at
-- once, so an attempt after an error knows it.
UPDATE invoices.billing_run_items SET invoice_ids = array_append(invoice_ids, @invoice_id::bigint)
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued';

-- name: CompleteBillingRunItem :execrows
-- CompleteBillingRunItem ends the claimed item with its outcome: drafted or
-- issued without a code, skipped or failed with one.
UPDATE invoices.billing_run_items
SET status = @status::text, code = sqlc.narg(code)::text, detail = sqlc.narg(detail)::text,
    finished_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued';

-- name: RetryBillingRunItem :execrows
-- RetryBillingRunItem counts a failed attempt of the claimed item and puts it
-- back, due at @next_attempt_at, its lease cleared — or, when its run was
-- cancelled while the item was carried, ends it cancelled at @now: a claim
-- never picks an item of a cancelled run again, so put back it would stay
-- queued for ever (plan reading 9).
UPDATE invoices.billing_run_items i
SET attempts = i.attempts + 1, next_attempt_at = @next_attempt_at::timestamptz, lease_id = NULL, lease_until = NULL,
    status = CASE WHEN r.status = 'cancelled' THEN 'cancelled' ELSE i.status END,
    finished_at = CASE WHEN r.status = 'cancelled' THEN @now::timestamptz ELSE i.finished_at END
FROM invoices.billing_runs r
WHERE r.id = i.run_id AND i.id = @id AND i.lease_id = @lease_id::text AND i.status = 'queued';

-- name: FailBillingRunItem :execrows
-- FailBillingRunItem counts the last failed attempt and fails the item.
UPDATE invoices.billing_run_items
SET attempts = attempts + 1, status = 'failed', code = @code::text, detail = @detail::text,
    finished_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued';

-- name: FinishBillingRun :execrows
-- FinishBillingRun marks a running run done once no item of it is queued.
UPDATE invoices.billing_runs r SET status = 'done', finished_at = @now::timestamptz
WHERE r.id = @id AND r.status = 'running'
  AND NOT EXISTS (SELECT 1 FROM invoices.billing_run_items i WHERE i.run_id = r.id AND i.status = 'queued');

-- name: FinishIdleBillingRuns :execrows
-- FinishIdleBillingRuns marks done every running run with no queued item
-- left: the sweep at the start of each cycle, for a run whose last item was
-- recorded but whose FinishBillingRun never landed (a crash, a restart, a
-- failed write) — it would otherwise stay running and refuse every new run.
UPDATE invoices.billing_runs r SET status = 'done', finished_at = @now::timestamptz
WHERE r.status = 'running'
  AND NOT EXISTS (SELECT 1 FROM invoices.billing_run_items i WHERE i.run_id = r.id AND i.status = 'queued');

-- name: CancelAbandonedBillingRunItems :execrows
-- CancelAbandonedBillingRunItems cancels the queued items of cancelled runs
-- whose lease, if any, has run out: an item a claim was carrying when its
-- run was cancelled, and whose claim never recorded an outcome. No claim
-- takes an item of a cancelled run, so without this sweep it stays queued.
UPDATE invoices.billing_run_items i
SET status = 'cancelled', finished_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
FROM invoices.billing_runs r
WHERE r.id = i.run_id AND r.status = 'cancelled' AND i.status = 'queued'
  AND (i.lease_until IS NULL OR i.lease_until <= @now::timestamptz);

-- name: ReleaseBillingRunItem :execrows
-- ReleaseBillingRunItem puts the claimed item back, due at once, its lease
-- cleared and no attempt counted: the worker was stopped while carrying it.
UPDATE invoices.billing_run_items
SET next_attempt_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued';
