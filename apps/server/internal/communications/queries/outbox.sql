-- Task 11 (the outbox delivery worker, SV/OutboxJobProcessor.cs:11-282;
-- communications inventory §13, design doc §4). Every query here belongs to
-- outbox.go and to nothing else: the worker is the only reader or writer of
-- outbox_jobs after the enqueue sites in conversations_create.go /
-- conversations_reply.go put a 'pending' row there.

-- name: SelectClaimableOutboxJob :one
-- The candidate select (`:73-79`, inventory §13.2 step 4): the first row by
-- next_attempt_at ascending matching
-- `((status='pending' OR status='retry') AND next_attempt_at <= now) OR
--  (status='processing' AND lease_until < now)`.
--
-- The parenthesisation is the inventory's explicit porting note: the C# is
-- `A && B || C && D` with no parentheses, and C# `&&` binds tighter than
-- `||`, so it means `(A && B) || (C && D)` — written out here rather than
-- left to a reader's precedence intuition. The second disjunct is the
-- lease-expiry branch, and it is the ONLY branch on which the
-- possible-duplicate counter can fire (inventory §13.2, §18).
--
-- .NET's `onlyMessageId` filter is dropped with the endpoint that passed it
-- (there is no per-message drain entry point in this port), and the tenant
-- filter is dropped with tenancy.
SELECT id, message_id, status, attempts, next_attempt_at, lease_id, lease_until,
       completed_at, last_error, created_at, delivery_attempted_at
FROM communications.outbox_jobs
WHERE ((status = 'pending' OR status = 'retry') AND next_attempt_at <= @now)
   OR (status = 'processing' AND lease_until < @now)
ORDER BY next_attempt_at ASC
LIMIT 1;

-- name: ClaimOutboxJob :execrows
-- The claim (`:100-110`, inventory §13.2): one conditional UPDATE re-asserting
-- the same predicate the candidate select used, so a job another worker won in
-- between no longer matches and this update reports 0 rows — "the conditional
-- update is the lock" (inventory `:1287-1289`). There is no SELECT ... FOR
-- UPDATE and no advisory lock here; the advisory lease guards retention only
-- (design D8, inventory §14).
--
-- attempts is incremented HERE, at claim, not at failure — which is what makes
-- terminality `attempts >= max_attempts` post-increment and makes the first
-- failure's backoff 2 s rather than 1 s (inventory §13.4).
-- delivery_attempted_at is cleared to NULL on every claim, so the marker only
-- ever survives a crash, never a normal retry (D5, inventory §11).
UPDATE communications.outbox_jobs
SET status = 'processing',
    lease_id = @lease_id,
    lease_until = @lease_until,
    delivery_attempted_at = NULL,
    attempts = attempts + 1
WHERE id = @id
  AND (((status = 'pending' OR status = 'retry') AND next_attempt_at <= @now)
       OR (status = 'processing' AND lease_until < @now));

-- name: GetOutboxJob :one
-- The re-reads at `:113` (inside the claim transaction), `:174` (step 9, before
-- completion) and `:251` (MarkFailedAsync). All three re-read the row rather
-- than trusting the copy the worker already holds, because the lease may have
-- changed underneath it.
SELECT id, message_id, status, attempts, next_attempt_at, lease_id, lease_until,
       completed_at, last_error, created_at, delivery_attempted_at
FROM communications.outbox_jobs
WHERE id = @id;

-- name: GetOutboundMessageForSend :one
-- Step 1 of the send phase (`:133-135`): the message with its conversation,
-- channel and the channel's credential. A missing conversation or channel is
-- .NET's `InvalidOperationException("The message channel no longer exists.")`
-- (step 2) — here the inner joins simply return no row, which outbox.go turns
-- into that same failure.
-- Only the columns the send actually reads. rfc_message_id is deliberately NOT
-- selected even though the row exists: the envelope's Message-Id is recomputed
-- from the message id (EmailMessageId.For, inventory §15.4), and selecting the
-- stored column here would invite a future reader to "fix" the envelope to the
-- wrong source. conversation_id, channel_id and channel_provider are likewise
-- omitted rather than carried unread.
SELECT m.id, m.subject, m.text_body, m.html_body, m.channel_metadata_json,
       c.subject AS conversation_subject,
       ch.type AS channel_type, ch.address AS channel_address,
       ch.display_name AS channel_display_name,
       cred.settings_json AS credential_settings_json,
       cred.secret_ciphertext AS credential_secret_ciphertext
FROM communications.conversation_messages m
JOIN communications.conversations c ON c.id = m.conversation_id
JOIN communications.channels ch ON ch.id = c.channel_id
LEFT JOIN communications.channel_credentials cred ON cred.channel_id = ch.id
WHERE m.id = @id;

-- name: ListMessageDeliveriesForSend :many
-- Every delivery of the message, in a stable order. The worker filters the
-- sendable ones itself (IsSendable, `:22-23`: status not in relay_accepted,
-- cancelled, suppressed) rather than filtering in SQL, because the same list is
-- needed both to decide "nothing sendable" (step 3) and to build the envelope's
-- to/cc/bcc partition (inventory §15.1).
SELECT id, recipient_address, recipient_type, status, attempts
FROM communications.message_deliveries
WHERE message_id = @message_id
ORDER BY created_at ASC, id ASC;

-- name: MarkMessageDeliverySending :exec
-- The claim transaction's per-delivery write (`:113-121`): Attempts++ and
-- Status = "sending" for each sendable delivery, still inside the claim
-- transaction.
UPDATE communications.message_deliveries
SET status = 'sending', attempts = attempts + 1
WHERE id = @id;

-- name: ListMessageAttachmentsForSend :many
-- Step 4's gate (`:145-146`) and the envelope's attachment list (inventory
-- §15.1, §15.5). scan_status comes back so the worker can refuse a message
-- carrying any non-clean attachment; storage_key is what the object store is
-- read by.
SELECT id, file_name, content_type, content_id, is_inline, storage_key, scan_status
FROM communications.message_attachments
WHERE message_id = @message_id
ORDER BY created_at ASC, id ASC;

-- name: CountSuppressedRecipients :one
-- Step 5's re-check (`:148-156`): the worker's own suppression lookup, run for
-- email channels only, over the normalised (Trim().ToUpperInvariant(), spec D7)
-- recipient addresses of every sendable delivery. One hit cancels the whole job
-- (step 6 is all-or-nothing).
SELECT count(*) FROM communications.suppressions
WHERE normalized_email_address = ANY(@addresses::text[]);

-- name: StampOutboxDeliveryAttempted :execrows
-- Step 7 (`:167-169`): the bare marker write, guarded by the lease, with NO
-- enclosing transaction so it auto-commits BEFORE the external send. .NET does
-- not check its rows-affected — on an already-stolen lease the write silently
-- no-ops and the send happens anyway (inventory `:1320-1323`). :execrows here
-- so outbox.go can say in one place that it deliberately ignores the count.
UPDATE communications.outbox_jobs
SET delivery_attempted_at = @delivery_attempted_at
WHERE id = @id AND lease_id = @lease_id;

-- name: FinishOutboxJob :exec
-- Steps 11 and 6's job write, and CompleteWithoutSendingAsync's (`:176-180`,
-- `:212`, `:228`): the terminal status ('completed' or 'cancelled'), the
-- completion stamp, and the lease cleared. delivery_attempted_at is
-- deliberately NOT cleared — only the next claim clears it (inventory
-- `:1320`), so a completed job keeps the stamp.
UPDATE communications.outbox_jobs
SET status = @status, completed_at = @completed_at, lease_id = NULL, lease_until = NULL
WHERE id = @id;

-- name: FailOutboxJob :exec
-- MarkFailedAsync's job write (`:256-266`): 'retry' or 'failed', the constant
-- LastError, the backoff, and the lease cleared. next_attempt_at is written
-- even for a terminal job — it is simply never queried again, because the claim
-- predicate requires pending/retry/expired-processing (inventory §13.4).
UPDATE communications.outbox_jobs
SET status = @status, last_error = @last_error, next_attempt_at = @next_attempt_at,
    lease_id = NULL, lease_until = NULL
WHERE id = @id;

-- name: MarkMessageDeliveryAccepted :exec
-- Step 12 (`:181-194`): the per-delivery success write.
UPDATE communications.message_deliveries
SET status = 'relay_accepted', last_error = NULL, accepted_at = @accepted_at
WHERE id = @id;

-- name: MarkMessageDeliverySuppressed :exec
-- CancelSuppressedAsync's per-delivery write (`:234`): status 'suppressed' and
-- LastError explicitly cleared to NULL.
UPDATE communications.message_deliveries
SET status = 'suppressed', last_error = NULL
WHERE id = @id;

-- name: MarkMessageDeliveryFailed :exec
-- MarkFailedAsync's per-delivery write (`:268`): 'retrying' or
-- 'submission_failed', with the same constant error text the job row carries.
UPDATE communications.message_deliveries
SET status = @status, last_error = @last_error
WHERE id = @id;

-- name: InsertMessageEventWithData :exec
-- The worker's own event writes. relay_accepted (`:191`) and suppressed
-- (`:241`) carry no data_json; retrying / submission_failed (`:275`) carry
-- {"error":"Outbound delivery failed."}. conversations.sql's InsertMessageEvent
-- is the enqueue sites' four-column form and is left alone.
INSERT INTO communications.message_events (id, message_id, delivery_id, event_type, occurred_at, data_json)
VALUES (@id, @message_id, @delivery_id, @event_type, @occurred_at, @data_json);
