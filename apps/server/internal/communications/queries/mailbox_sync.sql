-- The communications-mailbox-sync worker's channel_sync (Outlook connection
-- design M14; delivery D, readings 13 and 22): the claim, the lease and every
-- write of a cycle, and the row the channel operations write and read. Every
-- write of a cycle is guarded by the lease (channel_id AND lease_id), so a
-- worker that lost its lease writes nothing: 0 rows is the signal.

-- name: SelectDueChannelSync :one
-- The candidate: a row of an active Microsoft 365 channel, not stopped, due,
-- and not leased (or its lease expired). A channel E's notice marked comes
-- first, then the longest due.
SELECT s.channel_id
FROM communications.channel_sync s
JOIN communications.channels c ON c.id = s.channel_id
WHERE c.provider = 'entra' AND c.is_active
  AND s.state <> 'stopped'
  AND s.next_sync_at <= @now
  AND (s.lease_until IS NULL OR s.lease_until < @now)
ORDER BY s.notified DESC, s.next_sync_at ASC, s.channel_id ASC
LIMIT 1;

-- name: ClaimChannelSync :execrows
-- The claim: the candidate's predicate asserted again by the update itself,
-- so a row another worker took in between reports 0 rows — the conditional
-- update is the lock, as the outbox's (ClaimOutboxJob).
UPDATE communications.channel_sync s
SET lease_id = @lease_id, lease_until = @lease_until, updated_at = @now
FROM communications.channels c
WHERE s.channel_id = @channel_id
  AND c.id = s.channel_id AND c.provider = 'entra' AND c.is_active
  AND s.state <> 'stopped'
  AND s.next_sync_at <= @now
  AND (s.lease_until IS NULL OR s.lease_until < @now);

-- name: GetChannelSyncForCycle :one
-- The claimed row with what the cycle reads of its channel: the address (the
-- mailbox's own, left out of the participants) and the settings naming the
-- mailbox.
SELECT s.channel_id, s.folder, s.delta_link, s.import_from, s.state, s.attempts, s.lease_id,
       s.last_synced_at, s.restart_from,
       c.address AS channel_address, c.settings AS channel_settings
FROM communications.channel_sync s
JOIN communications.channels c ON c.id = s.channel_id
WHERE s.channel_id = @channel_id;

-- name: RenewChannelSyncLease :execrows
-- Before each message: the lease another two minutes from now.
UPDATE communications.channel_sync
SET lease_until = @lease_until, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: StoreChannelSyncPage :execrows
-- After a page whose every message has a receipt: its link kept, the failed
-- tries of its messages forgotten and the lease given back. With done, the
-- delta link — the channel is active, its failure, restart point and notice
-- forgotten, synced now and due again at next_sync_at; without, the next
-- page's link and next_sync_at now, so the drain claims it again at once.
UPDATE communications.channel_sync
SET delta_link = @delta_link,
    state = CASE WHEN sqlc.arg(done)::boolean THEN 'active' ELSE state END,
    error = CASE WHEN sqlc.arg(done)::boolean THEN NULL ELSE error END,
    attempts = CASE WHEN sqlc.arg(done)::boolean THEN 0 ELSE attempts END,
    last_synced_at = CASE WHEN sqlc.arg(done)::boolean THEN sqlc.arg(now)::timestamptz ELSE last_synced_at END,
    restart_from = CASE WHEN sqlc.arg(done)::boolean THEN NULL ELSE restart_from END,
    notified = CASE WHEN sqlc.arg(done)::boolean THEN false ELSE notified END,
    failing_item = NULL, failing_tries = 0,
    next_sync_at = @next_sync_at,
    lease_id = NULL, lease_until = NULL,
    updated_at = sqlc.arg(now)::timestamptz
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: HoldChannelSync :execrows
-- The provider unavailable (C's outbox hold): tried again at next_sync_at,
-- no attempt spent, the state kept.
UPDATE communications.channel_sync
SET error = 'provider_unavailable', next_sync_at = @next_sync_at,
    lease_id = NULL, lease_until = NULL, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: FailChannelSync :execrows
-- A failed cycle: an attempt spent, the code kept, tried again at
-- next_sync_at; state is 'failed' for a refusal no retry mends soon, else
-- the row's own.
UPDATE communications.channel_sync
SET state = COALESCE(sqlc.narg(state), state),
    error = @error,
    attempts = attempts + 1,
    next_sync_at = @next_sync_at,
    lease_id = NULL, lease_until = NULL, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: ResetChannelSyncDelta :execrows
-- The provider no longer takes the stored link: forgotten, and the instant
-- the fresh read starts from kept, so a cycle that ends before its fresh read
-- stores a link starts afresh from the same instant.
UPDATE communications.channel_sync
SET delta_link = NULL, restart_from = @restart_from, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: CountChannelSyncItemFailure :one
-- An import of item that failed: its count of failed tries, from 1 when the
-- page was held at another message.
UPDATE communications.channel_sync
SET failing_tries = CASE WHEN failing_item = @item THEN failing_tries + 1 ELSE 1 END,
    failing_item = @item, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id
RETURNING failing_tries;

-- name: InsertChannelSync :one
-- A Microsoft 365 channel's row, written in the transaction that creates or
-- switches it: it receives from import_from (from_now, last_30_days or
-- last_90_days, M14/M17). A row left from an earlier switch is started
-- afresh.
INSERT INTO communications.channel_sync (channel_id, folder, import_from, state, next_sync_at, updated_at)
VALUES (@channel_id, 'inbox', @import_from, 'pending', @now, @now)
ON CONFLICT (channel_id) DO UPDATE
SET folder = 'inbox', delta_link = NULL, import_from = excluded.import_from, state = 'pending',
    error = NULL, next_sync_at = excluded.next_sync_at, last_synced_at = NULL, attempts = 0,
    lease_id = NULL, lease_until = NULL, notified = false, restart_from = NULL,
    failing_item = NULL, failing_tries = 0, updated_at = excluded.updated_at
RETURNING channel_id, folder, delta_link, import_from, state, error, next_sync_at, last_synced_at,
          attempts, lease_id, lease_until, notified, restart_from, failing_item, failing_tries, updated_at;

-- name: DeleteChannelSync :exec
-- The way back to SMTP: the channel no longer receives, and a later switch
-- starts from its own day.
DELETE FROM communications.channel_sync WHERE channel_id = @channel_id;

-- name: GetChannelSyncByChannelIDs :many
-- ChannelResponse.sync for each channel that has a row.
SELECT channel_id, state, error, last_synced_at
FROM communications.channel_sync
WHERE channel_id = ANY(@channel_ids::uuid[]);

-- name: MarkChannelSyncNotified :execrows
-- MarkChannelSyncNotified sets notified = true and makes due at now the
-- active Microsoft 365 channels whose mailbox a cycle's change notices named
-- (M17): active or pending, not backing off (attempts = 0), and last synced
-- before the notice.
UPDATE communications.channel_sync s
SET notified = true,
    next_sync_at = LEAST(s.next_sync_at, @now::timestamptz),
    updated_at = @now::timestamptz
FROM communications.channels c,
(
    SELECT lower(n.mailbox) AS mailbox, max(n.at) AS at
    FROM (SELECT unnest(@mailboxes::text[]) AS mailbox, unnest(@ats::timestamptz[]) AS at) AS n
    GROUP BY lower(n.mailbox)
) AS notice
WHERE c.id = s.channel_id
  AND c.provider = 'entra'
  AND c.is_active
  AND lower(c.settings->>'mailbox') = notice.mailbox
  AND s.state IN ('active', 'pending')
  AND s.attempts = 0
  AND (s.last_synced_at IS NULL OR s.last_synced_at < notice.at)
  AND (NOT s.notified OR s.next_sync_at > @now::timestamptz);

-- name: UpsertMailboxMark :exec
-- Enqueues or resets a conversation's on-close Outlook mark job (M17).
INSERT INTO communications.mailbox_marks
    (conversation_id, operation, folder, status, attempts, next_attempt_at, queued_at)
VALUES (@conversation_id, @operation, @folder, 'pending', 0, @now, @now)
ON CONFLICT (conversation_id) DO UPDATE
SET operation = excluded.operation,
    folder = excluded.folder,
    status = 'pending',
    attempts = 0,
    next_attempt_at = excluded.next_attempt_at,
    queued_at = excluded.queued_at,
    lease_id = NULL,
    lease_until = NULL,
    completed_at = NULL,
    last_error = NULL;

-- name: SelectDueMailboxMark :one
-- The first due mailbox_marks job.
SELECT conversation_id
FROM communications.mailbox_marks
WHERE ((status = 'pending' OR status = 'retry') AND next_attempt_at <= @now)
   OR (status = 'processing' AND lease_until < @now)
ORDER BY next_attempt_at ASC, conversation_id ASC
LIMIT 1;

-- name: ClaimMailboxMark :execrows
-- Claims one due mailbox_marks job by conditional UPDATE, incrementing attempts.
UPDATE communications.mailbox_marks
SET status = 'processing',
    lease_id = @lease_id,
    lease_until = @lease_until,
    attempts = attempts + 1
WHERE conversation_id = @conversation_id
  AND (((status = 'pending' OR status = 'retry') AND next_attempt_at <= @now)
       OR (status = 'processing' AND lease_until < @now));

-- name: GetMailboxMarkForCycle :one
-- Reads the claimed mailbox_marks row with its conversation's channel.
SELECT m.conversation_id, m.operation, m.folder, m.status, m.attempts, m.lease_id,
       cv.channel_id, c.provider AS channel_provider, c.is_active AS channel_is_active, c.settings AS channel_settings
FROM communications.mailbox_marks m
JOIN communications.conversations cv ON cv.id = m.conversation_id
JOIN communications.channels c ON c.id = cv.channel_id
WHERE m.conversation_id = @conversation_id;

-- name: ListConversationInboundProviderIDs :many
-- Every imported inbound message's provider_message_id in the conversation,
-- oldest first.
SELECT r.provider_message_id
FROM communications.inbound_receipts r
JOIN communications.conversation_messages m ON m.id = r.message_id
WHERE r.channel_id = @channel_id
  AND m.conversation_id = @conversation_id
  AND m.direction = 'inbound'
ORDER BY r.received_at ASC, r.id ASC;

-- name: CompleteMailboxMark :execrows
-- Marks a claimed mailbox_marks job completed under its lease.
UPDATE communications.mailbox_marks
SET status = 'completed',
    completed_at = @now,
    last_error = NULL,
    lease_id = NULL,
    lease_until = NULL
WHERE conversation_id = @conversation_id AND lease_id = @lease_id;

-- name: FailMailboxMark :execrows
-- Marks a claimed mailbox_marks job for retry or terminal failure under its lease.
UPDATE communications.mailbox_marks
SET status = @status,
    last_error = @last_error,
    next_attempt_at = @next_attempt_at,
    lease_id = NULL,
    lease_until = NULL
WHERE conversation_id = @conversation_id AND lease_id = @lease_id;

-- name: HoldMailboxMark :execrows
-- Holds a claimed mailbox_marks job when the mail channels provider is
-- unavailable, without spending the attempt.
UPDATE communications.mailbox_marks
SET status = 'retry',
    last_error = 'provider_unavailable',
    next_attempt_at = @next_attempt_at,
    attempts = GREATEST(attempts - 1, 0),
    lease_id = NULL,
    lease_until = NULL
WHERE conversation_id = @conversation_id AND lease_id = @lease_id;
