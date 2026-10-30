-- Conversations, read state and notes: getCommunicationsConversations,
-- postCommunicationsConversations, getCommunicationsConversationsById,
-- patchCommunicationsConversationsById, postCommunicationsConversationsByIdNotes,
-- postCommunicationsConversationsByIdRead (communications inventory §1.2, §2,
-- §4; ConversationEndpoints.cs). Task 5's brief and dispatch are the
-- authority for the behaviour these back.

-- name: ListConversations :many
-- ListConversations is ListConversations (EP/ConversationEndpoints.cs:42-63):
-- every filter is optional (sqlc.narg IS NULL short-circuits it), ordered by
-- LastActivityAt descending, paginated by an already-clamped limit/offset,
-- with the filtered total riding along as a window function so one query
-- serves both the page and PaginationMetadata. unread_user_id is passed only
-- when unreadOnly=true was requested (inventory §2: ".NET only applies the
-- filter when userId.HasValue", always true through this router, so the Go
-- caller decides whether to apply it, not whether a user id exists).
SELECT c.id, c.channel_id, c.subject, c.status, c.assigned_user_id, c.customer_id,
       c.customer_association_source, c.suggested_customer_id, c.last_activity_at, c.preview_text,
       count(*) OVER () AS total_count
FROM communications.conversations c
WHERE (sqlc.narg(status)::text IS NULL OR c.status = sqlc.narg(status)::text)
  AND (sqlc.narg(assigned_user_id)::uuid IS NULL OR c.assigned_user_id = sqlc.narg(assigned_user_id)::uuid)
  AND (sqlc.narg(customer_id)::int IS NULL OR c.customer_id = sqlc.narg(customer_id)::int)
  AND (sqlc.narg(tag_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM communications.conversation_tags ct
        WHERE ct.conversation_id = c.id AND ct.tag_id = sqlc.narg(tag_id)::uuid))
  AND (sqlc.narg(unread_user_id)::uuid IS NULL OR NOT EXISTS (
        SELECT 1 FROM communications.conversation_read_states rs
        WHERE rs.conversation_id = c.id AND rs.user_id = sqlc.narg(unread_user_id)::uuid
          AND rs.last_read_at >= c.last_activity_at))
ORDER BY c.last_activity_at DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: ListConversationTagsByConversationIDs :many
-- Batch tag load for both ListConversations and GetConversation, keyed by
-- conversation_id so the caller groups rows into each item's Tags. Ordered
-- by name for a deterministic response; .NET's own ICollection iteration
-- order is unspecified, so this does not change observable behaviour beyond
-- picking one stable order.
SELECT ct.conversation_id, t.id, t.name, t.color
FROM communications.conversation_tags ct
JOIN communications.tags t ON t.id = ct.tag_id
WHERE ct.conversation_id = ANY(@conversation_ids::uuid[])
ORDER BY t.name;

-- name: ListConversationParticipantsByConversationIDs :many
-- Batch participant load, keyed by conversation_id. Ordered by the
-- participant's own created_at, a stable order .NET's DistinctBy(Id) over an
-- unordered ICollection does not guarantee either.
SELECT cp.conversation_id, p.id, p.channel_id, p.address, p.display_name, p.contact_id
FROM communications.conversation_participants cp
JOIN communications.participants p ON p.id = cp.participant_id
WHERE cp.conversation_id = ANY(@conversation_ids::uuid[])
ORDER BY p.created_at;

-- name: ListConversationCandidatesByConversationIDs :many
-- Batch customer-candidate load, keyed by conversation_id, ordered by
-- customer_id ascending exactly as ListConversations/GetConversation order
-- CandidateCustomerIds (`.OrderBy(candidate => candidate.CustomerId)`).
SELECT conversation_id, customer_id
FROM communications.conversation_customer_candidates
WHERE conversation_id = ANY(@conversation_ids::uuid[])
ORDER BY customer_id;

-- name: ListConversationReadStatesByConversationIDsForUser :many
-- Backs ListConversations' Unread derivation
-- (`!readAt.TryGetValue(item.Id, out var lastRead) || lastRead < item.LastActivityAt`):
-- one row per conversation this user has ever read, regardless of the
-- unreadOnly filter.
SELECT conversation_id, last_read_at
FROM communications.conversation_read_states
WHERE conversation_id = ANY(@conversation_ids::uuid[]) AND user_id = @user_id;

-- name: GetConversationReadState :one
-- GetConversation's own lastRead lookup for the caller.
SELECT last_read_at
FROM communications.conversation_read_states
WHERE conversation_id = @conversation_id AND user_id = @user_id;

-- name: UpsertConversationReadState :exec
-- MarkRead (`:84-94`): FindAsync-then-update-or-add collapsed into one
-- upsert on the (conversation_id, user_id) primary key.
INSERT INTO communications.conversation_read_states (conversation_id, user_id, last_read_at)
VALUES (@conversation_id, @user_id, @last_read_at)
ON CONFLICT (conversation_id, user_id) DO UPDATE SET last_read_at = EXCLUDED.last_read_at;

-- name: GetConversationByID :one
-- GetConversation's/PATCH's/notes'/read's shared existence-and-detail
-- lookup. No channel join: replyRecipientsOf (conversations.go) answers a
-- constant rather than reading the channel mailbox — see its own comment
-- for why (design §1.1: no production path in this port ever writes a
-- direction='inbound' message).
SELECT c.id, c.channel_id, c.subject, c.status, c.assigned_user_id, c.customer_id,
       c.customer_association_source, c.suggested_customer_id, c.suggested_customer_confidence,
       c.suggested_customer_reasoning, c.last_activity_at, c.preview_text, c.created_at
FROM communications.conversations c
WHERE c.id = @id;

-- name: GetConversationChannelDetail :one
-- GetConversation's channel provider, address and display name (M13, M20).
SELECT ch.provider, ch.address, ch.display_name
FROM communications.conversations c
JOIN communications.channels ch ON ch.id = c.channel_id
WHERE c.id = @id;

-- name: GetConversationMessagesByConversationID :many
-- GetConversation's message list, ordered OccurredAt ascending
-- (`item.Messages.OrderBy(message => message.OccurredAt)`), each left-joined
-- to its participant (null for every internal_note and outbound message),
-- with whether Vantigo keeps its original, the two flags an inbound
-- message's metadata carries, and the personal-sending fields (M20).
SELECT m.id, m.direction, m.author_user_id, m.subject, m.text_body, m.html_body,
       m.occurred_at, m.created_at,
       m.send_as, m.from_address, m.from_display_name,
       (m.raw_payload_storage_key IS NOT NULL)::boolean AS has_raw_payload,
       COALESCE((m.channel_metadata_json->>'autoSubmitted')::boolean, false)::boolean AS auto_submitted,
       COALESCE((m.channel_metadata_json->>'unreadable')::boolean, false)::boolean AS unreadable,
       p.id AS participant_id, p.channel_id AS participant_channel_id, p.address AS participant_address,
       p.display_name AS participant_display_name, p.contact_id AS participant_contact_id
FROM communications.conversation_messages m
LEFT JOIN communications.participants p ON p.id = m.participant_id
WHERE m.conversation_id = @conversation_id
ORDER BY m.occurred_at ASC;

-- name: ListMessageAttachmentsByMessageIDs :many
-- Batch attachment load for GetConversation's messages. Task 5 never writes
-- a row here (attachments are task 6's scope); kept for symmetry with the
-- other message-keyed batch loads and so a message that somehow carries
-- attachments still renders them.
SELECT id, message_id, file_name, content_type, size_bytes, content_id, scan_status, is_inline, created_at
FROM communications.message_attachments
WHERE message_id = ANY(@message_ids::uuid[]);

-- name: ListMessageDeliveriesByMessageIDs :many
-- Batch delivery load for GetConversation's messages
-- (`item.Deliveries.Select(...)`), with the terminal or hold code from the
-- latest failure event (or last_error when none) while the delivery is
-- retrying or submission_failed; '' otherwise (the handler answers null).
SELECT d.id, d.message_id, d.recipient_address, d.recipient_type, d.status, d.attempts, d.accepted_at,
       COALESCE(CASE
           WHEN d.status IN ('retrying', 'submission_failed') THEN COALESCE(
               (SELECT CASE WHEN pg_input_is_valid(e.data_json, 'jsonb')
                            THEN e.data_json::jsonb->>'code' END
                FROM communications.message_events e
                WHERE e.delivery_id = d.id AND e.event_type = d.status
                ORDER BY e.occurred_at DESC, e.id DESC
                LIMIT 1),
               d.last_error
           )
       END, '')::text AS error
FROM communications.message_deliveries d
WHERE d.message_id = ANY(@message_ids::uuid[]);

-- name: UpdateConversationActivity :one
-- AddNote's `conversation.LastActivityAt = now; conversation.PreviewText = ...`,
-- collapsed with its existence check: zero rows back means the conversation
-- id did not exist, the same 404 AddNote answers after ValidateNote passes.
UPDATE communications.conversations
SET last_activity_at = @last_activity_at, preview_text = @preview_text
WHERE id = @id
RETURNING id;

-- name: InsertConversationMessage :one
-- Shared by AddNote (direction=internal_note), CreateConversation and Reply
-- (direction=outbound): ConversationMessage's full column set for each.
INSERT INTO communications.conversation_messages
 (id, conversation_id, direction, participant_id, author_user_id, subject, text_body, html_body,
  channel_metadata_json, occurred_at, created_at, rfc_message_id, send_as, from_address, from_display_name)
VALUES (@id, @conversation_id, @direction, @participant_id, @author_user_id, @subject, @text_body, @html_body,
  @channel_metadata_json, @occurred_at, @created_at, @rfc_message_id,
  COALESCE(NULLIF(@send_as::text, ''), 'channel'), @from_address, @from_display_name)
RETURNING id;

-- name: UpdateConversationFields :one
-- UpdateConversation's (PATCH) field assignment (`:349-383`), already
-- resolved to final values in Go against the ordering rules the brief and
-- dispatch pin — this query only ever receives values already decided.
UPDATE communications.conversations
SET status = @status,
    assigned_user_id = @assigned_user_id,
    customer_id = @customer_id,
    customer_association_source = @customer_association_source,
    suggested_customer_id = @suggested_customer_id,
    suggested_customer_confidence = @suggested_customer_confidence,
    suggested_customer_reasoning = @suggested_customer_reasoning
WHERE id = @id
RETURNING id, status, assigned_user_id, customer_id, customer_association_source, suggested_customer_id;

-- name: DeleteConversationCustomerCandidates :exec
-- UpdateConversation's `conversation.CustomerCandidates.Clear()`, run only
-- when the patch actually touches customerId.
DELETE FROM communications.conversation_customer_candidates WHERE conversation_id = @conversation_id;

-- name: ListConversationCandidateIDs :many
-- The single-conversation form of ListConversationCandidatesByConversationIDs,
-- for PATCH's response (CandidateCustomerIds after the clear above, when it
-- ran; unchanged otherwise).
SELECT customer_id FROM communications.conversation_customer_candidates
WHERE conversation_id = @conversation_id ORDER BY customer_id;

-- name: GetActiveChannelByID :one
-- CreateConversation's explicit-channelId resolution
-- (`db.Channels.SingleOrDefaultAsync(item => item.Id == request.ChannelId && item.IsActive)`).
SELECT id, type, address FROM communications.channels WHERE id = @id AND is_active;

-- name: GetDefaultActiveEmailChannel :one
-- CreateConversation's implicit-channel resolution
-- (`OrderByDescending(IsDefault).ThenBy(CreatedAt).FirstOrDefault()`), relying
-- on ux_channels_type_is_default for the same determinism inventory §10
-- item 1 notes for the composer's fallback.
SELECT id, type, address FROM communications.channels
WHERE type = 'email' AND is_active
ORDER BY is_default DESC, created_at ASC
LIMIT 1;

-- name: InsertConversation :one
-- CreateConversation's conversation-row insert (`:238-262`). status is
-- always 'open' at creation (the DDL default, D4).
INSERT INTO communications.conversations
 (id, channel_id, subject, status, customer_id, customer_association_source, last_activity_at, preview_text, created_at)
VALUES (@id, @channel_id, @subject, 'open', @customer_id, @customer_association_source, @last_activity_at, @preview_text, @created_at)
RETURNING id, channel_id, subject, status, customer_id, customer_association_source, last_activity_at, preview_text, created_at;

-- name: GetIdempotencyRecordByKey :one
-- CreateConversation's/Reply's shared replay lookup
-- (`db.IdempotencyRecords.SingleOrDefaultAsync(item => item.Key == key)`);
-- the key is global (ux_idempotency_records_key), not per-user.
SELECT id, key, payload_fingerprint, conversation_id, message_id, created_at
FROM communications.idempotency_records WHERE key = @key;

-- name: InsertIdempotencyRecord :exec
INSERT INTO communications.idempotency_records (id, key, payload_fingerprint, conversation_id, message_id, created_at)
VALUES (@id, @key, @payload_fingerprint, @conversation_id, @message_id, @created_at);

-- name: InsertOutboxJob :exec
-- CreateConversation queues delivery the same way every message-producing
-- endpoint does; task 5 only ever writes this row — the worker that drains
-- it is a later task's scope (design doc §4).
INSERT INTO communications.outbox_jobs (id, message_id, status, attempts, next_attempt_at, created_at)
VALUES (@id, @message_id, 'pending', 0, @next_attempt_at, @created_at);

-- name: InsertMessageDelivery :one
INSERT INTO communications.message_deliveries
 (id, message_id, recipient_address, recipient_type, recipient_participant_id, status, attempts, created_at)
VALUES (@id, @message_id, @recipient_address, @recipient_type, @recipient_participant_id, 'queued', 0, @created_at)
RETURNING id;

-- name: InsertMessageEvent :exec
-- AddQueuedEvents' two event shapes (one per delivery, plus one
-- message-level "message_queued") both go through this one insert.
INSERT INTO communications.message_events (id, message_id, delivery_id, event_type, occurred_at)
VALUES (@id, @message_id, @delivery_id, @event_type, @occurred_at);

-- name: FindParticipantByChannelAndAddress :one
-- AddGenericDeliveriesAsync's find-or-create-by-address lookup, both the
-- simple to/cc branch and the generic recipients branch.
SELECT id, channel_id, address, display_name, contact_id, created_at
FROM communications.participants WHERE channel_id = @channel_id AND address = @address;

-- name: FindParticipantByIDAndChannel :one
-- AddGenericDeliveriesAsync's find-by-participantId branch
-- (`db.Participants.SingleOrDefaultAsync(item => item.Id == participantId && item.ChannelId == channel.Id)`).
SELECT id, channel_id, address, display_name, contact_id, created_at
FROM communications.participants WHERE id = @id AND channel_id = @channel_id;

-- name: InsertParticipant :one
-- The insert half of findOrCreateParticipantByAddress. The ON CONFLICT is
-- task 14's fix for ux_participants_channel_id_address, the module's last
-- known-open unguarded unique constraint: the Go lookup-then-insert (like
-- .NET's own FirstOrDefaultAsync-then-Add) is not atomic, so two concurrent
-- conversation creates naming the same new address on the same channel both
-- miss the lookup and both insert. The loser's 23505 aborts its whole
-- transaction — conversation, message, deliveries, outbox job and all — and
-- escapes to httpx.WriteError's host-wide fallback as a bare RFC 7807 409,
-- which is neither this module's error vocabulary nor a status
-- postCommunicationsConversations declares.
--
-- DO UPDATE rather than DO NOTHING, and the difference matters: DO NOTHING
-- returns no row when it conflicts, so the caller would need a follow-up
-- SELECT, and that SELECT finds nothing at all if the conflicting
-- transaction rolled back instead of committing. DO UPDATE always yields the
-- live row — the winner's if there is one, this statement's own insert
-- otherwise — in a single round trip with no second failure mode.
--
-- The SET is a deliberate no-op write of the conflict key itself.
-- display_name and contact_id are NOT touched: .NET never assigns either on
-- an existing Participant (findOrCreateParticipantByAddress's own comment),
-- so a race must not become the one path that overwrites them.
INSERT INTO communications.participants (id, channel_id, address, display_name, contact_id, created_at)
VALUES (@id, @channel_id, @address, @display_name, @contact_id, @created_at)
ON CONFLICT (channel_id, address) DO UPDATE SET address = excluded.address
RETURNING id, channel_id, address, display_name, contact_id, created_at;

-- name: InsertConversationParticipant :exec
-- Idempotent on (conversation_id, participant_id) exactly like the generic
-- branch's own `if (!conversation.Participants.Any(...))` guard, and also
-- covers the simple to/cc branch, which never guards but can never collide
-- within one valid request either (AddDuplicateRecipientError already
-- rejects a repeated to/cc address at 400).
INSERT INTO communications.conversation_participants (conversation_id, participant_id, role)
VALUES (@conversation_id, @participant_id, 'participant')
ON CONFLICT DO NOTHING;

-- Task 7 (Reply / QueueOutboundAsync, `:264-338`): the composer's own
-- lookups. GetConversationForReply's channel join replaces the two-query
-- shape (conversation, then channel) task 5's create path uses, matching
-- .NET's single `Include(item => item.Channel)` eager load at `:268`.

-- name: GetConversationForReply :one
-- QueueOutboundAsync's own conversation+channel load (`:268`): id and
-- current subject (BuildOutboundMessage's `request.Subject ?? conversation.Subject`,
-- `:282`, and its own fill-once `conversation.Subject ??= subject`, `:389`,
-- applied by UpdateConversationActivityForReply below), plus the channel's
-- is_active (step 5's 422 channel_inactive, checked here rather than a
-- second round trip) and type (step 9's `conversation.Channel.Type == "email"`
-- suppression gate), and its provider (the size budget of a Microsoft 365
-- channel, M13).
SELECT c.id, c.subject, c.channel_id, ch.is_active AS channel_is_active, ch.type AS channel_type,
       ch.provider AS channel_provider
FROM communications.conversations c
JOIN communications.channels ch ON ch.id = c.channel_id
WHERE c.id = @id;

-- name: ListSuppressedAddresses :many
-- QueueOutboundAsync's suppression check (`:301-302`): every address in
-- destinations that has a live suppression row, keyed by the already-
-- normalised (uppercased, D7) form both sides compare on.
SELECT normalized_email_address
FROM communications.suppressions
WHERE normalized_email_address = ANY(@addresses::text[]);

-- name: UpdateConversationActivityForReply :exec
-- BuildOutboundMessage's tracked-entity mutation for Reply (`:389`):
-- `conversation.Subject ??= subject` — fill-once, matched here with
-- COALESCE so an already-set subject is never overwritten — plus
-- LastActivityAt and PreviewText, which always advance. Run only from
-- inside queueReply's transaction, after every refusal check (steps 8, 9,
-- 10) has passed: inventory §19.2 item 16 warns that .NET applies these
-- same three fields to the *tracked* entity before its own refusal checks,
-- but nothing persists on those paths because SaveChangesAsync is never
-- reached — a Go port writing SQL directly must not apply them early either.
UPDATE communications.conversations
SET subject = COALESCE(subject, @message_subject), last_activity_at = @last_activity_at, preview_text = @preview_text
WHERE id = @id;

-- name: GetReplyRecipientsSource :one
-- Who a reply answers (M15, inventory §4.3, §4.4; ReplyRecipients
-- `:446-455`, which QueueOutboundAsync inlines at `:277`, `:283`): the latest
-- inbound message by occurred_at, ties by id, with a participant — none for
-- a conversation nothing was received in, step 7's 422 recipients_missing —,
-- its sender's address, its metadata (the Cc reply-all answers, its
-- References) and Message-Id, and the channel's own address and id.
SELECT m.rfc_message_id, m.channel_metadata_json, p.address AS sender_address,
       c.channel_id, ch.address AS channel_address
FROM communications.conversation_messages m
JOIN communications.participants p ON p.id = m.participant_id
JOIN communications.conversations c ON c.id = m.conversation_id
JOIN communications.channels ch ON ch.id = c.channel_id
WHERE m.conversation_id = @conversation_id AND m.direction = 'inbound'
ORDER BY m.occurred_at DESC, m.id DESC
LIMIT 1;

-- name: ListParticipantsByChannelAndAddressesFold :many
-- Reply-all's Cc as participant rows: each of addresses (lower case) in the
-- channel, in any case, newest first so the caller keeps one per address.
SELECT id, channel_id, address, display_name, contact_id, created_at
FROM communications.participants
WHERE channel_id = @channel_id AND lower(address) = ANY(@addresses::text[])
ORDER BY created_at DESC, id DESC;

-- name: GetConversationMessageForRetry :one
-- Lookup for POST /conversations/{id}/messages/{messageId}/retry (M20):
-- the message in the conversation, its channel's active flag, and its outbox
-- job's id and status.
SELECT m.id, m.conversation_id, m.direction, m.author_user_id, m.send_as,
       ch.is_active AS channel_is_active,
       j.id AS job_id, j.status AS job_status
FROM communications.conversation_messages m
JOIN communications.conversations c ON c.id = m.conversation_id
JOIN communications.channels ch ON ch.id = c.channel_id
LEFT JOIN communications.outbox_jobs j ON j.message_id = m.id
WHERE m.id = @message_id AND m.conversation_id = @conversation_id;

-- name: RetryFailedOutboxJob :execrows
-- Re-queues a terminally failed outbox job for immediate retry (M20).
UPDATE communications.outbox_jobs
SET status = 'pending',
    attempts = 0,
    next_attempt_at = @now,
    completed_at = NULL,
    last_error = NULL,
    lease_id = NULL,
    lease_until = NULL,
    delivery_attempted_at = NULL
WHERE id = @id AND status = 'failed';

-- name: ResetFailedMessageDeliveries :many
-- Resets submission_failed deliveries of a retried outbound message back to
-- queued (M20).
UPDATE communications.message_deliveries
SET status = 'queued',
    attempts = 0,
    last_error = NULL
WHERE message_id = @message_id AND status = 'submission_failed'
RETURNING id;
