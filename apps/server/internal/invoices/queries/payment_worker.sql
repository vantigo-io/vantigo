-- The invoices-payments worker (invoices payments and reminders design D16):
-- a due attempt is claimed under a row lease, the shape of
-- reminder_worker.sql's ClaimReminder; every later write of the claim names
-- its lease and the states the claim may still find, is made under the
-- attempt's own lock (lockPaymentAttempt) — the capture decision and the
-- registration then also under the invoice's — and ends the claim, clearing
-- the lease. A no-op means the attempt moved under the claim: abandoned, or
-- claimed again after this claim's lease ran out. Every query takes the time
-- as a parameter from Deps.Clock(), never now().

-- name: ClaimPaymentAttempt :one
-- ClaimPaymentAttempt leases the next attempt due by @now whose lease, if
-- any, has run out: ix_payment_attempts_due's predicate — a live state, a
-- release pending, a refund watch — in next_poll_at order. One statement: the
-- row lock FOR NO KEY UPDATE SKIP LOCKED is held for it alone, so it is
-- outside the lock helpers and the lock-order seam (m11), and two workers
-- never take one attempt. A parked attempt (next_poll_at NULL) is never due.
-- Each claim counts as a poll. No row is pgx.ErrNoRows.
UPDATE invoices.payment_attempts
SET lease_id = @lease_id::text, lease_until = @lease_until::timestamptz, polls = polls + 1
WHERE id = (
    SELECT a.id FROM invoices.payment_attempts a
    WHERE (a.state IN ('creating', 'created', 'authorized', 'capturing', 'capture_unknown', 'cancelling')
           OR a.release_pending OR (a.state = 'captured' AND a.refund_watch_until IS NOT NULL))
      AND a.next_poll_at <= @now::timestamptz
      AND (a.lease_until IS NULL OR a.lease_until <= @now::timestamptz)
    ORDER BY a.next_poll_at, a.id
    LIMIT 1
    FOR NO KEY UPDATE SKIP LOCKED
)
RETURNING *;

-- name: ReschedulePaymentAttempt :execrows
-- ReschedulePaymentAttempt ends a claim that changes nothing but when the
-- attempt is read again: @next_poll_at NULL parks it (a release Vipps can
-- never perform), and @last_error says why, NULL clearing it. @failed counts
-- one more failed call in a row — the backoff's — or clears the count;
-- @insufficient_funds adds Vipps' 6260 answers.
UPDATE invoices.payment_attempts
SET next_poll_at = sqlc.narg(next_poll_at)::timestamptz, last_error = sqlc.narg(last_error),
    failures = CASE WHEN @failed::boolean THEN failures + 1 ELSE 0 END,
    insufficient_funds = insufficient_funds + @insufficient_funds::integer,
    lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text;

-- name: CountCaptureAttempt :execrows
-- CountCaptureAttempt counts the Capture about to be sent, in the commit
-- before it, the capture key the call will carry already stored: the trigger
-- lets the key be replaced only while capture_attempts is 0. The lease
-- stays: the call is this claim's.
UPDATE invoices.payment_attempts
SET capture_attempts = capture_attempts + 1
WHERE id = @id AND lease_id = @lease_id::text AND state IN ('capturing', 'capture_unknown');

-- name: EndUnauthorizedAttempt :execrows
-- EndUnauthorizedAttempt records Vipps' own end of a request never
-- authorized — aborted, expired or terminated — or its definitive refusal
-- of the create (failed): the attempt finished, nothing left to poll.
UPDATE invoices.payment_attempts
SET state = @state::text, psp_reference = coalesce(sqlc.narg(psp_reference), psp_reference),
    finished_at = @now::timestamptz, next_poll_at = NULL, last_error = sqlc.narg(last_error),
    failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state IN ('creating', 'created');

-- name: RecreatedAttempt :execrows
-- RecreatedAttempt records that the worker's create under the stored create
-- key made the payment after all: created, read again at @next_poll_at.
UPDATE invoices.payment_attempts
SET state = 'created', redirect_url = @redirect_url, next_poll_at = @next_poll_at::timestamptz,
    last_error = NULL, failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state = 'creating';

-- name: AuthorizeAttempt :execrows
-- AuthorizeAttempt records the payer's approval as Vipps reads it — the
-- amount it reserved, when, and until when the capture is guaranteed — and
-- makes the attempt due at once, for the capture decision.
UPDATE invoices.payment_attempts
SET state = 'authorized', authorized_amount = @authorized_amount, authorized_at = @now::timestamptz,
    psp_reference = coalesce(sqlc.narg(psp_reference), psp_reference),
    capture_guaranteed_until = sqlc.narg(capture_guaranteed_until)::timestamptz,
    next_poll_at = @now::timestamptz, last_error = NULL, failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state IN ('creating', 'created');

-- name: DecideAttemptCapture :execrows
-- DecideAttemptCapture is the capture decision's "capture": the amount
-- reserved — counted in the invoice's open amount from this commit — and the
-- capture key made, both before any call; due at once. The caller holds the
-- attempt and then its invoice.
UPDATE invoices.payment_attempts
SET state = 'capturing', reserved_amount = @reserved_amount, capture_key = @capture_key,
    next_poll_at = @now::timestamptz, last_error = NULL, failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state = 'authorized';

-- name: DecideAttemptCancel :execrows
-- DecideAttemptCancel is the decision's "nothing to capture" — from
-- authorized, or a re-decision of a capture never sent: cancelling, any
-- reservation released; due at once. The caller holds the attempt and then
-- its invoice.
UPDATE invoices.payment_attempts
SET state = 'cancelling', reserved_amount = NULL,
    next_poll_at = @now::timestamptz, failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text
  AND (state = 'authorized' OR (state = 'capturing' AND capture_attempts = 0));

-- name: RedecideAttemptCapture :execrows
-- RedecideAttemptCapture lowers a reservation whose capture was never sent
-- (m3) — a credit note or a payment landed since the decision — with a new
-- capture key, under the decision's locks. The lease stays: the claim goes
-- on to the capture.
UPDATE invoices.payment_attempts
SET reserved_amount = @reserved_amount, capture_key = @capture_key
WHERE id = @id AND lease_id = @lease_id::text AND state = 'capturing' AND capture_attempts = 0;

-- name: AttemptCaptureUnknown :execrows
-- AttemptCaptureUnknown records a capture whose outcome has been unknown for
-- seven days: capture_unknown, the reservation kept, read again at
-- @next_poll_at.
UPDATE invoices.payment_attempts
SET state = 'capture_unknown', next_poll_at = @next_poll_at::timestamptz, last_error = sqlc.narg(last_error),
    failures = failures + 1, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state = 'capturing';

-- name: RegisterAttemptCaptured :execrows
-- RegisterAttemptCaptured is the registration's attempt half, in the
-- transaction that inserts its payment: captured, the reservation released
-- as the payment takes its place, the uncaptured rest's release pending when
-- Vipps authorized more and none was released, the refund watch set. Money
-- Vipps shows captured is always registered: from a capture in progress or
-- unknown, and from a failed capture or a cancel that it landed on after all
-- (00046 lets capture_failed become captured with a captured amount). The
-- caller holds the attempt and then its invoice.
UPDATE invoices.payment_attempts
SET state = 'captured', captured_amount = @captured_amount, captured_at = @now::timestamptz,
    reserved_amount = NULL, release_pending = @release_pending,
    refund_watch_until = @refund_watch_until::timestamptz, finished_at = @now::timestamptz,
    next_poll_at = @next_poll_at::timestamptz, last_error = NULL, failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state IN ('capturing', 'capture_unknown', 'capture_failed', 'cancelling');

-- name: FailAttemptCapture :execrows
-- FailAttemptCapture records a definitive capture failure: nothing was
-- taken and nothing can be — capture_failed, the reservation released, the
-- authorized amount's release pending and due at once; @insufficient_funds
-- counts the 6260 that ended it.
UPDATE invoices.payment_attempts
SET state = 'capture_failed', reserved_amount = NULL, release_pending = true, finished_at = @now::timestamptz,
    insufficient_funds = insufficient_funds + @insufficient_funds::integer,
    next_poll_at = @now::timestamptz, last_error = sqlc.narg(last_error), failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state IN ('capturing', 'capture_unknown');

-- name: AttemptCancelled :execrows
-- AttemptCancelled ends a cancelling attempt: what Vipps released recorded.
UPDATE invoices.payment_attempts
SET state = 'cancelled', cancelled_amount = @cancelled_amount, finished_at = @now::timestamptz,
    next_poll_at = NULL, last_error = NULL, failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state = 'cancelling';

-- name: AttemptReleased :execrows
-- AttemptReleased ends a pending release of a captured or failed attempt's
-- uncaptured rest: what Vipps released recorded, read again at
-- @next_poll_at (a captured attempt's refund watch) or never.
UPDATE invoices.payment_attempts
SET release_pending = false, cancelled_amount = @cancelled_amount,
    next_poll_at = sqlc.narg(next_poll_at)::timestamptz, last_error = NULL, failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND release_pending;

-- name: RecordAttemptRefund :execrows
-- RecordAttemptRefund is the refund watch's (D16): a captured attempt Vipps
-- shows refunded more than recorded — a refund made in the Vipps portal —
-- the amount stored, read again at @next_poll_at or, the watch over, never.
-- No payment moves: the person removes it (vippsRefundSeen).
UPDATE invoices.payment_attempts
SET refunded_amount = @refunded_amount, next_poll_at = sqlc.narg(next_poll_at)::timestamptz,
    last_error = NULL, failures = 0, lease_id = NULL, lease_until = NULL
WHERE id = @id AND lease_id = @lease_id::text AND state = 'captured';
