-- Send all (invoices phase 6 design I36; plan E-1, E1–E4): a batch of send
-- jobs, one per document asked for, carried by the invoices-send worker
-- under a row lease — the billing worker's shape (billing_runs.sql). Every
-- later write of a claim names its lease: 0 rows is another actor's move.
-- Every query takes the time as a parameter from Deps.Clock(), never now().

-- name: InsertSendBatch :one
INSERT INTO invoices.send_batches (source, billing_run_id, requested_by, created_at)
VALUES (@source, sqlc.narg(billing_run_id), @requested_by, @created_at)
RETURNING *;

-- name: InsertQueuedSendJob :one
-- InsertQueuedSendJob queues one document of a batch, unless a job of
-- another batch is queued or sending for it (ux_send_jobs_live): then no row
-- is answered (pgx.ErrNoRows), and the caller records the document skipped
-- send_in_progress. A racing enqueue waits for the other's commit here.
INSERT INTO invoices.send_jobs (batch_id, invoice_id, created_at)
VALUES (@batch_id, @invoice_id, @created_at)
ON CONFLICT (invoice_id) WHERE state IN ('queued', 'sending') DO NOTHING
RETURNING *;

-- name: InsertSkippedSendJob :exec
-- InsertSkippedSendJob records a document of a batch skipped at enqueue.
INSERT INTO invoices.send_jobs (batch_id, invoice_id, state, code, detail, created_at, finished_at)
VALUES (@batch_id, @invoice_id, 'skipped', @code, @detail, @created_at, @created_at);

-- name: SendDocuments :many
-- SendDocuments is what exists of the documents a batch is asked for.
SELECT id, number, status, kind FROM invoices.invoices
WHERE id = ANY(@ids::bigint[])
ORDER BY id;

-- name: DeliveredDocuments :many
-- DeliveredDocuments is which of the documents are delivered already, on any
-- channel (plan reading E2): an e-mail logged, a transmission EHF or
-- eFaktura queued, submitted, delivered or awaiting confirmation — the live
-- ones, which RuleDeliveries' delivered-only rule does not count —, a manual
-- delivery not removed, or a send job of any batch left for printing or
-- printed: a document goes once.
SELECT DISTINCT invoice_id FROM (
    SELECT d.invoice_id FROM invoices.deliveries d WHERE d.invoice_id = ANY(@ids::bigint[])
    UNION ALL
    SELECT t.invoice_id FROM invoices.transmissions t
    WHERE t.invoice_id = ANY(@ids::bigint[]) AND t.status IN ('queued', 'submitted', 'delivered', 'unconfirmed')
    UNION ALL
    SELECT m.invoice_id FROM invoices.manual_deliveries m
    WHERE m.invoice_id = ANY(@ids::bigint[]) AND m.removed_at IS NULL
    UNION ALL
    SELECT j.invoice_id FROM invoices.send_jobs j
    WHERE j.invoice_id = ANY(@ids::bigint[]) AND j.state IN ('awaiting_print', 'printed')
) AS delivered;

-- name: UncertainDocuments :many
-- UncertainDocuments is which of the documents a send job of any batch may
-- have mailed without a delivery to show for it: uncertain, or failed
-- mail_failed. They are never sent again by send all (plan reading E4).
SELECT DISTINCT invoice_id FROM invoices.send_jobs
WHERE invoice_id = ANY(@ids::bigint[])
  AND (state = 'uncertain' OR (state = 'failed' AND code = 'mail_failed'));

-- name: BillingRunInvoiceIDs :many
-- BillingRunInvoiceIDs is every document a run's items made, in order.
SELECT DISTINCT unnest(invoice_ids)::bigint AS invoice_id
FROM invoices.billing_run_items
WHERE run_id = @run_id
ORDER BY 1;

-- name: GetSendBatch :one
SELECT * FROM invoices.send_batches WHERE id = @id;

-- name: SendBatchesOfRun :many
-- SendBatchesOfRun is a run's batches, the newest first.
SELECT * FROM invoices.send_batches WHERE billing_run_id = @billing_run_id ORDER BY id DESC;

-- name: SendBatchCounts :many
-- SendBatchCounts is, per batch, how many of its jobs are in each state.
SELECT batch_id, state, count(*)::int AS jobs
FROM invoices.send_jobs
WHERE batch_id = ANY(@batch_ids::bigint[])
GROUP BY batch_id, state
ORDER BY batch_id, state;

-- name: SendJobsOfBatch :many
-- SendJobsOfBatch is a batch's jobs in the order asked for, each with what
-- still exists of its document.
SELECT j.*, i.number AS invoice_number, i.customer_id AS customer_id, i.kind AS invoice_kind
FROM invoices.send_jobs j
LEFT JOIN invoices.invoices i ON i.id = j.invoice_id
WHERE j.batch_id = @batch_id
ORDER BY j.id;

-- name: ClaimSendJob :one
-- ClaimSendJob leases the oldest job that is queued and due, or sending with
-- its lease run out — a worker that stopped mid-send. One conditional UPDATE
-- over a SKIP LOCKED pick, so two workers never take one job. It answers the
-- state and the started_at the job had before this claim, by which a
-- reclaimed e-mail is judged (plan reading E4). No row is pgx.ErrNoRows.
WITH pick AS (
    SELECT j.id, j.state AS prev_state, j.started_at AS prev_started_at
    FROM invoices.send_jobs j
    WHERE j.state IN ('queued', 'sending')
      AND (j.lease_until IS NULL OR j.lease_until <= @now::timestamptz)
    ORDER BY j.id
    LIMIT 1
    FOR UPDATE OF j SKIP LOCKED
)
UPDATE invoices.send_jobs s
SET state = 'sending', attempts = s.attempts + 1, started_at = @now::timestamptz,
    lease_id = @lease_id::text, lease_until = @lease_until::timestamptz
FROM pick
WHERE s.id = pick.id
RETURNING s.*, pick.prev_state::text AS prev_state, pick.prev_started_at AS prev_started_at;

-- name: SetSendJobChannel :execrows
-- SetSendJobChannel records the channel the claimed job goes by, before it
-- is tried, so a reclaim knows it.
UPDATE invoices.send_jobs SET channel = @channel::text
WHERE id = @id AND lease_id = @lease_id::text AND state = 'sending';

-- name: LockSendJob :one
-- LockSendJob takes a job FOR UPDATE, after its document's lock.
SELECT * FROM invoices.send_jobs WHERE id = @id FOR UPDATE;

-- name: LockSendBatch :exec
-- LockSendBatch serializes the workers that end the jobs of one batch. Each
-- of them ends its job and asks FinishSendBatch whether any job is still
-- live, in its own transaction: without this lock the last two to end, at the
-- same time, each still see the other's job as sending (neither commit is
-- visible) and neither finishes the batch. FOR NO KEY UPDATE does not
-- hold up a job being queued for the batch (its foreign key takes FOR KEY SHARE).
SELECT 1 FROM invoices.send_batches WHERE id = @id FOR NO KEY UPDATE;

-- name: CompleteSendJob :execrows
-- CompleteSendJob ends the claimed job with its outcome.
UPDATE invoices.send_jobs
SET state = @state::text, channel = sqlc.narg(channel)::text, code = sqlc.narg(code)::text, detail = @detail::text,
    transmission_id = sqlc.narg(transmission_id)::bigint, delivery_id = sqlc.narg(delivery_id)::bigint,
    finished_at = @now::timestamptz, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state = 'sending';

-- name: RetrySendJob :execrows
-- RetrySendJob puts the claimed job back after an unavailability, due at
-- @not_before, with the reason it waits and its channel cleared: nothing left
-- the process, so the next claim chooses afresh and is never judged a reclaim.
UPDATE invoices.send_jobs
SET state = 'queued', channel = NULL, code = @code::text, detail = @detail::text, lease_id = NULL, lease_until = @not_before::timestamptz
WHERE id = @id AND lease_id = @lease_id::text AND state = 'sending';

-- name: ReleaseSendJob :execrows
-- ReleaseSendJob puts the claimed job back, due at once and uncounted: the
-- worker was stopped before anything left the process; its channel cleared
-- as a retry's.
UPDATE invoices.send_jobs
SET state = 'queued', channel = NULL, attempts = attempts - 1, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state = 'sending';

-- name: FinishSendBatch :execrows
-- FinishSendBatch marks a batch finished once none of its jobs is live.
UPDATE invoices.send_batches b SET finished_at = @now::timestamptz
WHERE b.id = @id AND b.finished_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM invoices.send_jobs j WHERE j.batch_id = b.id AND j.state IN ('queued', 'sending'));

-- name: FinishIdleSendBatches :many
-- FinishIdleSendBatches is the sweep: every unfinished batch with no live job
-- left — its last outcome written, its FinishSendBatch lost — is finished.
UPDATE invoices.send_batches b SET finished_at = @now::timestamptz
WHERE b.finished_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM invoices.send_jobs j WHERE j.batch_id = b.id AND j.state IN ('queued', 'sending'))
RETURNING b.id;

-- name: EmailDeliveryAfter :one
-- EmailDeliveryAfter is the first e-mail of a document logged at or after a
-- moment: the evidence a reclaimed e-mail job was sent. No row is
-- pgx.ErrNoRows.
SELECT id FROM invoices.deliveries
WHERE invoice_id = @invoice_id AND sent_at >= @after::timestamptz
ORDER BY sent_at, id
LIMIT 1;

-- name: LiveTransmissionOf :one
-- LiveTransmissionOf is a document's live transmission on a channel. No row
-- is pgx.ErrNoRows.
SELECT id FROM invoices.transmissions
WHERE invoice_id = @invoice_id AND channel = @channel::text AND status IN ('queued', 'submitted', 'delivered', 'unconfirmed')
ORDER BY id DESC
LIMIT 1;

-- name: AwaitingPrintJobs :many
-- AwaitingPrintJobs is a batch's jobs left for printing, in order.
SELECT * FROM invoices.send_jobs
WHERE batch_id = @batch_id AND state = 'awaiting_print'
ORDER BY id;

-- name: MarkSendJobPrinted :execrows
-- MarkSendJobPrinted ends a job left for printing: posted.
UPDATE invoices.send_jobs SET state = 'printed', finished_at = @now::timestamptz
WHERE id = @id AND state = 'awaiting_print';

-- name: AttentionSendBatches :many
-- AttentionSendBatches is every batch made since @since with jobs failed or
-- uncertain, and how many: the newest first.
SELECT b.id, b.created_at, count(*)::int AS not_sent
FROM invoices.send_batches b
JOIN invoices.send_jobs j ON j.batch_id = b.id
WHERE b.created_at >= @since::timestamptz AND j.state IN ('failed', 'uncertain')
GROUP BY b.id, b.created_at
ORDER BY b.id DESC;
