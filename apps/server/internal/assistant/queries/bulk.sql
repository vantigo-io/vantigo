-- The bulk jobs (the Assistant design S13): every query that serves the person
-- filters by user_id, so another person's job is not found. The runner's own
-- queries name the job by id: it owns the id, and a claim says which replica
-- runs it.

-- name: InsertBulkJob :one
INSERT INTO assistant.bulk_jobs (id, pending_action_id, run_id, conversation_id, user_id, operation_id, selector, template, total, processed, skipped, created_at, updated_at)
VALUES (@id, @pending_action_id, @run_id, @conversation_id, @user_id, @operation_id, @selector::jsonb, @template::jsonb, @total, @skipped, @skipped, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: InsertBulkItems :exec
-- InsertBulkItems freezes the items the selector found, in order. An item the
-- pre-check excluded is stored skipped, with its reason.
INSERT INTO assistant.bulk_job_items (job_id, seq, item, status, problem_code)
SELECT @job_id::uuid, (@seqs::int[])[g], (@items::jsonb[])[g], (@statuses::text[])[g], NULLIF((@codes::text[])[g], '')
FROM generate_series(1, cardinality(@seqs::int[])) AS g;

-- name: GetBulkJob :one
SELECT * FROM assistant.bulk_jobs WHERE id = @id AND user_id = @user_id;

-- name: GetBulkJobByID :one
-- GetBulkJobByID is for the job's own goroutine, which owns its id.
SELECT * FROM assistant.bulk_jobs WHERE id = @id;

-- name: GetBulkJobByAction :one
SELECT * FROM assistant.bulk_jobs WHERE pending_action_id = @pending_action_id AND user_id = @user_id;

-- name: ListBulkJobs :many
-- ListBulkJobs is a conversation's jobs, for the caller's own conversation only.
SELECT j.* FROM assistant.bulk_jobs j
JOIN assistant.conversations c ON c.id = j.conversation_id
WHERE j.conversation_id = @conversation_id AND c.user_id = @user_id
ORDER BY j.created_at, j.id;

-- name: StartBulkJob :one
-- StartBulkJob is the person's Start: the canary begins, under this replica's
-- claim and the grant issued from the Start request.
UPDATE assistant.bulk_jobs SET
    state = 'canary', grant_id = @grant_id, grant_expires_at = @grant_expires_at::timestamptz,
    claimed_by = @claimed_by, claimed_until = @claimed_until::timestamptz,
    started_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE pending_action_id = @pending_action_id AND user_id = @user_id AND state = 'previewed'
RETURNING *;

-- name: ContinueBulkJob :one
-- ContinueBulkJob is the person's Continue: the job runs on, under this
-- replica's claim and a grant issued from the Continue request.
UPDATE assistant.bulk_jobs SET
    state = 'running', grant_id = @grant_id, grant_expires_at = @grant_expires_at::timestamptz,
    claimed_by = @claimed_by, claimed_until = @claimed_until::timestamptz,
    pause_reason = NULL, stop_requested = false, updated_at = @now::timestamptz
WHERE id = @id AND user_id = @user_id AND state = 'awaiting_continue'
RETURNING *;

-- name: ClaimBulkJob :one
-- ClaimBulkJob takes a job whose claim lapsed (or never was) for this replica,
-- once: the conditional update lets one replica win. In the same statement every
-- item the dead replica had in flight becomes unknown and is counted processed:
-- its call may or may not have been made, and no replica calls it again (S17).
WITH c AS (
    SELECT b.id FROM assistant.bulk_jobs b
    WHERE b.id = @id AND b.state IN ('canary', 'running', 'stopping')
      AND (b.claimed_by IS NULL OR b.claimed_until < @now::timestamptz)
    FOR UPDATE OF b
), swept AS (
    UPDATE assistant.bulk_job_items i SET status = 'unknown', done_at = @now::timestamptz
    FROM c
    WHERE i.job_id = c.id AND i.status = 'in_flight'
    RETURNING i.seq
)
UPDATE assistant.bulk_jobs j SET
    claimed_by = @claimed_by, claimed_until = @claimed_until::timestamptz, updated_at = @now::timestamptz,
    processed = j.processed + (SELECT count(*) FROM swept)::int,
    unknown = j.unknown + (SELECT count(*) FROM swept)::int
FROM c
WHERE j.id = c.id
RETURNING j.*;

-- name: ListLapsedBulkJobs :many
-- ListLapsedBulkJobs is the running jobs nobody holds a claim on, for the
-- replica that sees them to take over; user_id narrows it to one person's.
SELECT * FROM assistant.bulk_jobs
WHERE state IN ('canary', 'running', 'stopping')
  AND (claimed_by IS NULL OR claimed_until < @now::timestamptz)
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
ORDER BY updated_at
LIMIT @batch_size;

-- name: MarkBulkItemInFlight :one
-- MarkBulkItemInFlight is the statement that commits before an item's call goes
-- out (S17): from then on, a replica that dies leaves the item in_flight, and the
-- one that takes the job over marks it unknown and never calls it. It answers
-- nothing (ErrNoRows) when this replica no longer holds the claim or the item is
-- not pending, and then the item is not called.
UPDATE assistant.bulk_job_items i SET status = 'in_flight', in_flight_at = @now::timestamptz
FROM assistant.bulk_jobs j
WHERE i.job_id = @job_id AND i.seq = @seq AND i.status = 'pending'
  AND j.id = i.job_id AND j.claimed_by = @claimed_by AND j.state IN ('canary', 'running', 'stopping')
RETURNING i.seq;

-- name: ReleaseBulkItem :exec
-- ReleaseBulkItem puts an item back to pending when its call was never made (the
-- dispatcher refused it before it went out), so the job that ends leaves it as it
-- was.
UPDATE assistant.bulk_job_items i SET status = 'pending', in_flight_at = NULL
FROM assistant.bulk_jobs j
WHERE i.job_id = @job_id AND i.seq = @seq AND i.status = 'in_flight'
  AND j.id = i.job_id AND j.claimed_by = @claimed_by;

-- name: RecordBulkItem :one
-- RecordBulkItem records how one item went, counts it, and renews the claim, in
-- one statement; it answers nothing (ErrNoRows) when this replica no longer
-- holds the claim or the item is not in flight (a replica that took the job over
-- has marked it unknown), so a job is never counted twice and a replica that lost
-- the claim stops at once.
WITH item AS (
    UPDATE assistant.bulk_job_items i SET
        status = @status, http_status = sqlc.narg(http_status)::integer, problem_code = sqlc.narg(problem_code)::text,
        problem_title = sqlc.narg(problem_title)::text, done_at = @now::timestamptz
    FROM assistant.bulk_jobs j
    WHERE i.job_id = @job_id AND i.seq = @seq AND i.status = 'in_flight'
      AND j.id = i.job_id AND j.claimed_by = @claimed_by
    RETURNING i.status
)
UPDATE assistant.bulk_jobs j SET
    processed = processed + 1,
    succeeded = succeeded + CASE WHEN item.status = 'succeeded' THEN 1 ELSE 0 END,
    failed = failed + CASE WHEN item.status = 'failed' THEN 1 ELSE 0 END,
    skipped = skipped + CASE WHEN item.status = 'skipped' THEN 1 ELSE 0 END,
    unknown = unknown + CASE WHEN item.status = 'unknown' THEN 1 ELSE 0 END,
    claimed_until = @claimed_until::timestamptz, updated_at = @now::timestamptz
FROM item
WHERE j.id = @job_id AND j.claimed_by = @claimed_by
RETURNING j.*;

-- name: TouchBulkClaim :one
-- TouchBulkClaim renews the claim between chunks and reads what the runner must
-- know to go on: it answers nothing when the claim is no longer this replica's.
UPDATE assistant.bulk_jobs SET claimed_until = @claimed_until::timestamptz, updated_at = @now::timestamptz
WHERE id = @id AND claimed_by = @claimed_by AND state IN ('canary', 'running', 'stopping')
RETURNING *;

-- name: ListPendingBulkItems :many
-- ListPendingBulkItems is the next items still to run, in order; below limits
-- them to the canary's.
SELECT * FROM assistant.bulk_job_items
WHERE job_id = @job_id AND status = 'pending' AND seq < @below
ORDER BY seq
LIMIT @batch_size;

-- name: PauseBulkJob :one
-- PauseBulkJob has a job wait for the person's Continue.
UPDATE assistant.bulk_jobs SET
    state = 'awaiting_continue', pause_reason = sqlc.narg(pause_reason)::text, claimed_by = NULL, claimed_until = NULL, updated_at = @now::timestamptz
WHERE id = @id AND claimed_by = @claimed_by AND state IN ('canary', 'running', 'stopping')
RETURNING *;

-- name: RequestBulkStop :one
-- RequestBulkStop is the person's Stop of a job that is running: it takes effect
-- between chunks.
UPDATE assistant.bulk_jobs SET
    stop_requested = true,
    state = CASE WHEN state IN ('canary', 'running') THEN 'stopping' ELSE state END,
    updated_at = @now::timestamptz
WHERE id = @id AND user_id = @user_id AND state IN ('canary', 'running', 'stopping')
RETURNING *;

-- name: EndBulkJob :one
-- EndBulkJob ends a job once, redacting the plan: the filters and the values go,
-- the operations, the names and the binding stay. A running job is ended by the
-- replica that claims it; one nobody runs (a preview, a job waiting for
-- Continue) by whoever the person asked.
UPDATE assistant.bulk_jobs SET
    state = @state, error_code = sqlc.narg(error_code)::text, ended_at = @now::timestamptz, updated_at = @now::timestamptz,
    claimed_by = NULL, claimed_until = NULL, stop_requested = false, pause_reason = NULL,
    selector = assistant.redact_bulk_selector(selector), template = assistant.redact_bulk_template(template)
WHERE id = @id AND state IN ('previewed', 'canary', 'awaiting_continue', 'running', 'stopping')
  AND (claimed_by IS NULL OR claimed_by = sqlc.narg(claimed_by)::text)
RETURNING *;

-- name: DeleteBulkItems :exec
-- DeleteBulkItems drops the items of a plan nobody started.
DELETE FROM assistant.bulk_job_items WHERE job_id = @job_id;

-- name: ListBulkFailures :many
-- ListBulkFailures is the items that went wrong: the unknown ones first, then the
-- failed, each in order.
SELECT * FROM assistant.bulk_job_items WHERE job_id = @job_id AND status IN ('failed', 'unknown')
ORDER BY (status = 'unknown') DESC, seq LIMIT @batch_size;

-- name: ListBulkCanary :many
-- ListBulkCanary is the items the canary ran, in order, with how each went.
SELECT * FROM assistant.bulk_job_items WHERE job_id = @job_id AND seq < @below AND status <> 'pending' ORDER BY seq;

-- name: ExpireBulkJobs :many
-- ExpireBulkJobs expires the previews and the jobs waiting for Continue that
-- nobody went on with, redacts their plans and ends the runs that waited for
-- them. It is one statement and takes no second connection.
WITH j AS (
    UPDATE assistant.bulk_jobs SET
        state = 'expired', ended_at = @now::timestamptz, updated_at = @now::timestamptz, pause_reason = NULL,
        selector = assistant.redact_bulk_selector(selector), template = assistant.redact_bulk_template(template)
    WHERE state IN ('previewed', 'awaiting_continue') AND updated_at < @cutoff::timestamptz
    RETURNING id, pending_action_id, run_id
), p AS (
    UPDATE assistant.pending_actions pa SET
        status = CASE WHEN pa.status = 'pending' THEN 'expired' ELSE pa.status END,
        outcome = COALESCE(pa.outcome, '{"ok":false,"reason":"expired"}'::jsonb),
        request = assistant.redact_request(pa.request),
        preview = assistant.redact_preview(pa.preview),
        decided_at = COALESCE(pa.decided_at, @now::timestamptz)
    FROM j WHERE pa.id = j.pending_action_id
    RETURNING pa.id
)
UPDATE assistant.runs r SET state = 'expired', ended_at = @now::timestamptz
FROM j
WHERE r.id = j.run_id AND r.state = 'awaiting_confirmation'
RETURNING r.id;

-- name: GetBulkJobByRun :one
-- GetBulkJobByRun is the bulk job of a run that waits for the person.
SELECT * FROM assistant.bulk_jobs WHERE run_id = @run_id AND user_id = @user_id;

-- name: CountPendingBulkItems :one
-- CountPendingBulkItems is how many items have yet to run.
SELECT count(*)::int FROM assistant.bulk_job_items WHERE job_id = @job_id AND status = 'pending';
