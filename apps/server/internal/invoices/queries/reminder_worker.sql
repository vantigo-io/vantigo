-- The invoices-reminders worker and a letter's own operations (invoices
-- payments and reminders design D10). A queued letter is claimed under a row
-- lease, the shape of transmissions.sql's ClaimTransmission; every later
-- write of the claim names its lease and the status the claim saw, and is a
-- no-op when either changed. A letter's facts are written only while a claim
-- holds it — or a print batch has it locked — and a failed attempt clears
-- them with the lease, so a letter carrying facts under no live lease is
-- never "being sent" (plan reading 45): status = 'queued' AND sent_on IS NOT
-- NULL AND lease_until > @now.

-- name: ClaimReminder :one
-- ClaimReminder leases the next queued letter due by @now whose lease, if
-- any, has run out — free at exactly lease_until <= @now, the complement of
-- "being sent"'s lease_until > @now, so no instant has a letter both
-- claimable and unwithdrawable, or neither (Task 11's review). One
-- conditional UPDATE over a SKIP LOCKED pick, so two workers never take one
-- letter. The Message-ID is made by the claim and kept by every later one:
-- set once, while queued (plan reading 52). No row is pgx.ErrNoRows.
UPDATE invoices.reminders
SET lease_id = @lease_id::text, lease_until = @lease_until::timestamptz,
    message_id = coalesce(message_id, @message_id::text)
WHERE status = 'queued' AND id = (
    SELECT r.id FROM invoices.reminders r
    WHERE r.status = 'queued'
      AND r.next_attempt_at <= @now::timestamptz
      AND (r.lease_until IS NULL OR r.lease_until <= @now::timestamptz)
    ORDER BY r.next_attempt_at, r.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: WriteLetterFacts :one
-- WriteLetterFacts writes a letter's facts as the engine judged them on its
-- sending day — the dispatch's today, a print batch's postOn — and clears
-- held_reason and any PDF of an earlier attempt: the PDF is rendered from
-- these facts. A queued letter's first attempt is stamped once. The caller
-- holds the invoice and then the letter (D18) and has judged the letter's
-- status and lease under those locks.
UPDATE invoices.reminders
SET sent_on = @sent_on::date, deadline = @deadline::date, regime = @regime::text,
    principal_open = @principal_open, credited = @credited, fee_kind = @fee_kind::text,
    fee = sqlc.narg(fee)::numeric, compensation = sqlc.narg(compensation)::numeric,
    charges_earlier = @charges_earlier, interest = @interest, interest_waived = @interest_waived,
    interest_paid = @interest_paid, interest_from = sqlc.narg(interest_from)::date,
    interest_segments = @interest_segments::jsonb, inkassosats = sqlc.narg(inkassosats)::numeric,
    total = @total, charge_notes = @charge_notes::text[],
    pdf_object_key = NULL, pdf_sha256 = NULL, held_reason = NULL,
    first_attempt_at = CASE WHEN status = 'queued' THEN coalesce(first_attempt_at, @now::timestamptz) ELSE first_attempt_at END
WHERE id = @id AND status IN ('queued', 'awaiting_print')
RETURNING *;

-- name: RescheduleReminderUncounted :execrows
-- RescheduleReminderUncounted puts a claimed letter back without counting
-- the try (plan readings 17, 46): an hour out when its rates or regime
-- review are missing, held_reason saying why, which attention reads; at
-- once, held_reason NULL, when Oslo midnight passed before its send, so its
-- facts are judged again on the new day. Neither attempts nor
-- first_attempt_at moves, so waiting never runs into the 48 hours. The lease
-- and the facts are cleared.
UPDATE invoices.reminders
SET next_attempt_at = @next_attempt_at::timestamptz, held_reason = sqlc.narg(held_reason)::text,
    lease_id = NULL, lease_until = NULL,
    sent_on = NULL, deadline = NULL, regime = NULL, principal_open = NULL, credited = NULL, fee_kind = NULL, fee = NULL,
    compensation = NULL, charges_earlier = NULL, interest = NULL, interest_waived = NULL, interest_paid = NULL,
    interest_from = NULL, interest_segments = NULL, inkassosats = NULL, total = NULL, charge_notes = NULL,
    pdf_object_key = NULL, pdf_sha256 = NULL
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued';

-- name: WithdrawClaimedReminder :execrows
-- WithdrawClaimedReminder is the dispatch's re-judge withdrawing the letter
-- it holds (D10): the module's reason code and no user (plan reading 44),
-- the lease and any facts of an earlier attempt cleared — a letter that was
-- never sent claims nothing.
UPDATE invoices.reminders
SET status = 'withdrawn', withdrawn_at = @withdrawn_at::timestamptz, withdrawal_reason = @withdrawal_reason::text,
    lease_id = NULL, lease_until = NULL, held_reason = NULL,
    sent_on = NULL, deadline = NULL, regime = NULL, principal_open = NULL, credited = NULL, fee_kind = NULL, fee = NULL,
    compensation = NULL, charges_earlier = NULL, interest = NULL, interest_waived = NULL, interest_paid = NULL,
    interest_from = NULL, interest_segments = NULL, inkassosats = NULL, total = NULL, charge_notes = NULL,
    pdf_object_key = NULL, pdf_sha256 = NULL
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued';

-- name: SetReminderPDF :execrows
-- SetReminderPDF records the PDF a claim rendered and stored, before it is
-- mailed — the object the mail carries.
UPDATE invoices.reminders SET pdf_object_key = @pdf_object_key::text, pdf_sha256 = @pdf_sha256::text
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued';

-- name: MarkReminderSent :one
-- MarkReminderSent is the letter mailed (D10 step 4): sent at the claim's
-- time, the lease released. The caller holds the invoice and then the
-- letter. No row is pgx.ErrNoRows: another claim, or a withdrawal after the
-- lease ran out, moved it.
UPDATE invoices.reminders
SET status = 'sent', sent_at = @sent_at::timestamptz, lease_id = NULL, lease_until = NULL, held_reason = NULL,
    last_error = NULL
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued'
RETURNING *;

-- name: FailReminderAttempt :execrows
-- FailReminderAttempt counts a failed attempt (D10): attempts + 1, the next
-- at the backoff, the reason, and the lease and the facts cleared — so a
-- stale sent_on never makes the letter look "being sent" (plan reading 45)
-- and it may be withdrawn again until the next claim.
UPDATE invoices.reminders
SET attempts = attempts + 1, next_attempt_at = @next_attempt_at::timestamptz, last_error = sqlc.narg(last_error)::text,
    first_attempt_at = coalesce(first_attempt_at, @now::timestamptz),
    lease_id = NULL, lease_until = NULL,
    sent_on = NULL, deadline = NULL, regime = NULL, principal_open = NULL, credited = NULL, fee_kind = NULL, fee = NULL,
    compensation = NULL, charges_earlier = NULL, interest = NULL, interest_waived = NULL, interest_paid = NULL,
    interest_from = NULL, interest_segments = NULL, inkassosats = NULL, total = NULL, charge_notes = NULL,
    pdf_object_key = NULL, pdf_sha256 = NULL
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued';

-- name: MarkReminderFailed :execrows
-- MarkReminderFailed is a letter still unsent 48 hours after its first
-- attempt (D10): failed, with the attempt counted, the reason, and the lease
-- and the facts cleared. Attention names it; a retry puts it back.
UPDATE invoices.reminders
SET status = 'failed', failed_at = @failed_at::timestamptz, attempts = attempts + 1,
    last_error = sqlc.narg(last_error)::text, lease_id = NULL, lease_until = NULL,
    sent_on = NULL, deadline = NULL, regime = NULL, principal_open = NULL, credited = NULL, fee_kind = NULL, fee = NULL,
    compensation = NULL, charges_earlier = NULL, interest = NULL, interest_waived = NULL, interest_paid = NULL,
    interest_from = NULL, interest_segments = NULL, inkassosats = NULL, total = NULL, charge_notes = NULL,
    pdf_object_key = NULL, pdf_sha256 = NULL
WHERE id = @id AND lease_id = @lease_id::text AND status = 'queued';

-- name: WithdrawReminder :one
-- WithdrawReminder is a person's withdrawal (D10): their reason and their
-- id. The caller holds the letter alone (D18) and has judged under that
-- lock that it may be withdrawn — queued, awaiting print, printed or
-- failed, and not being sent.
UPDATE invoices.reminders
SET status = 'withdrawn', withdrawn_at = @withdrawn_at::timestamptz, withdrawn_by_user_id = @withdrawn_by_user_id,
    withdrawal_reason = @withdrawal_reason::text, held_reason = NULL
WHERE id = @id AND status IN ('queued', 'awaiting_print', 'printed', 'failed')
RETURNING *;

-- name: RetryReminder :one
-- RetryReminder puts a failed letter back in the queue (D10): due at once,
-- its 48 hours and its backoff begun again. The caller holds the letter.
UPDATE invoices.reminders
SET status = 'queued', next_attempt_at = @now::timestamptz, first_attempt_at = NULL, attempts = 0, failed_at = NULL
WHERE id = @id AND status = 'failed'
RETURNING *;

-- name: GetReminder :one
SELECT * FROM invoices.reminders WHERE id = @id;

-- name: CountReminders :one
-- CountReminders is how many letters ListReminders' filters match.
SELECT count(*)::int FROM invoices.reminders
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(channel)::text IS NULL OR channel = sqlc.narg(channel)::text)
  AND (sqlc.narg(invoice_id)::bigint IS NULL OR invoice_id = sqlc.narg(invoice_id)::bigint)
  AND (sqlc.narg(run_id)::bigint IS NULL OR run_id = sqlc.narg(run_id)::bigint);

-- name: ListReminders :many
-- ListReminders is one page of the letters (plan reading 12), filtered by
-- status, channel, invoice and run, the newest first.
SELECT * FROM invoices.reminders
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(channel)::text IS NULL OR channel = sqlc.narg(channel)::text)
  AND (sqlc.narg(invoice_id)::bigint IS NULL OR invoice_id = sqlc.narg(invoice_id)::bigint)
  AND (sqlc.narg(run_id)::bigint IS NULL OR run_id = sqlc.narg(run_id)::bigint)
ORDER BY id DESC
LIMIT @page_size OFFSET @page_offset;
