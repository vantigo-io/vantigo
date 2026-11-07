-- The communications-messenger-sync worker's messenger_sync (Messenger design
-- N3, delivery A): the claim, the lease and every write of a cycle, and the row
-- a Messenger channel is created with. Every write of a cycle is guarded by the
-- lease (channel_id AND lease_id), so a worker that lost its lease writes
-- nothing: 0 rows is the signal, as channel_sync's.

-- name: InsertMessengerSync :one
-- A Messenger channel's row, written in the transaction that creates it: it
-- receives from the first event of its Page's inbox, cursor 0, and is due now.
INSERT INTO communications.messenger_sync (channel_id, cursor, next_sync_at, state, updated_at)
VALUES (@channel_id, 0, @now, 'active', @now)
RETURNING channel_id, cursor, notified, next_sync_at, lease_id, lease_until, state, last_error,
          attempts, last_synced_at, updated_at;

-- name: GetMessengerSyncByChannelIDs :many
-- ChannelResponse.sync for each Messenger channel that has a row; error is the
-- code of the last failure.
SELECT channel_id, state, last_error AS error, last_synced_at
FROM communications.messenger_sync
WHERE channel_id = ANY(@channel_ids::uuid[]);

-- name: SelectDueMessengerSync :one
-- The candidate: a row of an active Messenger channel, not stopped, due, and
-- not leased (or its lease expired). A channel a notice marked comes first,
-- then the longest due.
SELECT s.channel_id
FROM communications.messenger_sync s
JOIN communications.channels c ON c.id = s.channel_id
WHERE c.type = 'messenger' AND c.is_active
  AND s.state <> 'stopped'
  AND s.next_sync_at <= @now
  AND (s.lease_until IS NULL OR s.lease_until < @now)
ORDER BY s.notified DESC, s.next_sync_at ASC, s.channel_id ASC
LIMIT 1;

-- name: ClaimMessengerSync :execrows
-- The claim: the candidate's predicate asserted again by the update itself, so
-- a row another worker took in between reports 0 rows — the conditional update
-- is the lock, as the outbox's (ClaimOutboxJob). The notice is taken with the
-- claim: one that arrives while the cycle runs sets notified again, and the
-- cycle's end makes the channel due at once.
UPDATE communications.messenger_sync s
SET lease_id = @lease_id, lease_until = @lease_until, notified = false, updated_at = @now
FROM communications.channels c
WHERE s.channel_id = @channel_id
  AND c.id = s.channel_id AND c.type = 'messenger' AND c.is_active
  AND s.state <> 'stopped'
  AND s.next_sync_at <= @now
  AND (s.lease_until IS NULL OR s.lease_until < @now);

-- name: GetMessengerSyncForCycle :one
-- The claimed row with what the cycle reads of its channel: the address (the
-- Page's id) and the provider it is read through.
SELECT s.channel_id, s.cursor, s.state, s.attempts, s.lease_id,
       c.address AS channel_address, c.provider AS channel_provider
FROM communications.messenger_sync s
JOIN communications.channels c ON c.id = s.channel_id
WHERE s.channel_id = @channel_id;

-- name: RenewMessengerSyncLease :execrows
-- Before each event: the lease another two minutes from now.
UPDATE communications.messenger_sync
SET lease_until = @lease_until, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: AdvanceMessengerSyncCursor :execrows
-- The cursor's advance, inside the transaction that imports the event: under
-- the lease, never backwards. 0 rows is a lost lease, and the import rolls
-- back with it.
UPDATE communications.messenger_sync
SET cursor = GREATEST(cursor, @cursor), updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: FinishMessengerSync :execrows
-- After the page and its Ack: the channel active, its failure forgotten, synced
-- now, the lease given back, and due at next_sync_at — or at once when a
-- notice arrived during the cycle, or the page was full and more is waiting.
UPDATE communications.messenger_sync
SET state = 'active', last_error = NULL, attempts = 0,
    last_synced_at = sqlc.arg(now)::timestamptz,
    next_sync_at = CASE WHEN notified OR sqlc.arg(more)::boolean THEN sqlc.arg(now)::timestamptz ELSE @next_sync_at END,
    lease_id = NULL, lease_until = NULL,
    updated_at = sqlc.arg(now)::timestamptz
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: HoldMessengerSync :execrows
-- The provider unavailable: tried again at next_sync_at, no attempt spent, the
-- state kept.
UPDATE communications.messenger_sync
SET last_error = 'provider_unavailable', next_sync_at = @next_sync_at,
    lease_id = NULL, lease_until = NULL, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: FailMessengerSync :execrows
-- A failed cycle: an attempt spent, the code kept, tried again at next_sync_at.
UPDATE communications.messenger_sync
SET last_error = @error,
    attempts = attempts + 1,
    next_sync_at = @next_sync_at,
    lease_id = NULL, lease_until = NULL, updated_at = @now
WHERE channel_id = @channel_id AND lease_id = @lease_id;

-- name: MarkMessengerSyncNotified :execrows
-- MarkMessengerSyncNotified sets notified = true and makes due at now the
-- active channels on @provider whose Page a notice named: not stopped, not
-- backing off (attempts = 0).
UPDATE communications.messenger_sync s
SET notified = true, next_sync_at = LEAST(s.next_sync_at, @now), updated_at = @now
FROM communications.channels c
WHERE c.id = s.channel_id AND c.type = 'messenger' AND c.is_active
  AND c.provider = @provider::text AND c.address = ANY(@addresses::text[])
  AND s.state <> 'stopped' AND s.attempts = 0;

-- The import of one event of a Messenger channel's inbox (Messenger design
-- N3, N3a, N5, N12): the participant's profile, the conversation a message
-- joins, the message of either direction, the answer's place and the delivery
-- watermarks. The receipts (inbound.sql's InboundReceiptExists and
-- InsertInboundReceipt) make an import idempotent by (channel, platform
-- message id), as the mailbox sync's do.

-- name: FindParticipantProfile :one
-- When the participant's profile — its name — was last read from the platform.
SELECT fetched_at
FROM communications.participant_profiles
WHERE participant_id = @participant_id;

-- name: UpsertParticipantProfile :exec
INSERT INTO communications.participant_profiles (participant_id, fetched_at)
VALUES (@participant_id, @fetched_at)
ON CONFLICT (participant_id) DO UPDATE SET fetched_at = excluded.fetched_at;

-- name: SetParticipantDisplayName :exec
-- A name the platform gave, on a participant: the profile's read.
UPDATE communications.participants SET display_name = @display_name WHERE id = @id;

-- name: FindOpenMessengerConversation :one
-- Threading by participant (N3a): the newest open conversation of the channel
-- the participant is in. A closed or archived conversation is never joined.
SELECT c.id, c.customer_id
FROM communications.conversations c
JOIN communications.conversation_participants cp ON cp.conversation_id = c.id
WHERE c.channel_id = @channel_id AND cp.participant_id = @participant_id AND c.status = 'open'
ORDER BY c.last_activity_at DESC, c.id DESC
LIMIT 1
FOR UPDATE OF c;

-- name: InsertMessengerMessage :exec
-- A message the import writes: inbound — the participant's, with its
-- participant — or outbound — an echo of the Page's own, sent from the
-- platform, with none. It has no subject, no e-mail body and no Message-Id.
INSERT INTO communications.conversation_messages
    (id, conversation_id, direction, participant_id, text_body,
     channel_metadata_json, occurred_at, created_at)
VALUES (@id, @conversation_id, @direction, @participant_id, @text_body,
        @channel_metadata_json, @occurred_at, @created_at);

-- name: OutboundMessageInConversation :one
-- Whether an answer's message is one this conversation sent: the answer is
-- placed under it only then (N5).
SELECT EXISTS (
    SELECT 1 FROM communications.conversation_messages
    WHERE id = @id AND conversation_id = @conversation_id AND direction = 'outbound'
);

-- name: MessengerPartExists :one
-- Whether a platform message id is one of the parts Vantigo sent: an echo of it
-- is no message of the Page's own from elsewhere (N12).
SELECT EXISTS (
    SELECT 1 FROM communications.messenger_parts WHERE platform_message_id = @platform_message_id
);

-- name: MessengerPartInFlight :one
-- Whether the channel has a part whose Send began after @since and has not been
-- recorded as sent: its echo may arrive before the id is written, so an echo
-- that matches no part is held a moment, not imported (N12).
SELECT EXISTS (
    SELECT 1
    FROM communications.messenger_parts p
    JOIN communications.conversation_messages m ON m.id = p.message_id
    JOIN communications.conversations c ON c.id = m.conversation_id
    WHERE c.channel_id = @channel_id AND p.sent_at IS NULL AND p.attempted_at > @since
);

-- name: AdvanceMessengerDeliveries :many
-- The watermark of a delivered or read event (N2): every delivery of an
-- outbound message in the participant's conversations of the channel whose
-- newest part the platform stamped at or before it — a part's platform time,
-- else the time its Send began — and that is in one of from_statuses, moves to
-- status. A message with no part, or still being sent (its deliveries are
-- 'sending'), is left for the watermark kept per participant to reach when the
-- send completes. Monotonic — a delivery already further along is not in
-- from_statuses — and so idempotent. It answers the deliveries it moved, for
-- their events and the conversations to notify.
UPDATE communications.message_deliveries d
SET status = @status
FROM communications.conversation_messages m
JOIN communications.conversations c ON c.id = m.conversation_id
WHERE d.message_id = m.id
  AND c.channel_id = @channel_id
  AND EXISTS (
      SELECT 1 FROM communications.conversation_participants cp
      WHERE cp.conversation_id = c.id AND cp.participant_id = @participant_id)
  AND (SELECT max(COALESCE(p.platform_at, p.attempted_at))
       FROM communications.messenger_parts p WHERE p.message_id = m.id) <= @watermark
  AND d.status = ANY(@from_statuses::text[])
RETURNING d.id, d.message_id, m.conversation_id;

-- name: UpsertMessengerWatermark :exec
-- The newest watermark of a kind a participant has had, never backwards.
INSERT INTO communications.messenger_watermarks (participant_id, kind, watermark)
VALUES (@participant_id, @kind, @watermark)
ON CONFLICT (participant_id, kind) DO UPDATE SET watermark = GREATEST(messenger_watermarks.watermark, excluded.watermark);

-- name: ListMessengerWatermarks :many
SELECT kind, watermark FROM communications.messenger_watermarks WHERE participant_id = @participant_id;

-- name: GetMessageComponents :one
-- The components of a message, for an answer to be checked against its question.
SELECT components FROM communications.conversation_messages WHERE id = @id;

-- name: FindParticipantIDByAddress :one
SELECT id FROM communications.participants WHERE channel_id = @channel_id AND address = @address;

-- name: LatestShareEmailQuestion :one
-- The newest message of the conversation that asked the person for an e-mail
-- address: a user_email quick reply carries no payload of ours, so the address
-- that comes back is placed under it (N5).
SELECT id
FROM communications.conversation_messages
WHERE conversation_id = @conversation_id AND direction = 'outbound'
  AND components->>'shareEmail' = 'true'
ORDER BY occurred_at DESC, id DESC
LIMIT 1;
