-- One inbound message's import (Outlook connection design M14, M15; delivery
-- D, readings 14, 18, 19, 21 and 23): the receipts that make it idempotent,
-- our own sends recognised, threading's finds, the participants by address in
-- any case, the message and its unscanned attachments, and the linker's
-- writes.

-- name: InboundReceiptExists :one
-- A message the worker already saw in this channel, by the provider's id
-- (case-sensitive, as Graph's ids are).
SELECT EXISTS (
    SELECT 1 FROM communications.inbound_receipts
    WHERE channel_id = @channel_id AND provider_message_id = @provider_message_id
);

-- name: InsertInboundReceipt :exec
-- ux_inbound_receipts_provider decides a race between two holders;
-- ux_inbound_receipts_rfc two imports of one Message-Id.
INSERT INTO communications.inbound_receipts
    (id, channel_id, provider_message_id, rfc_message_id, message_id, outcome, reason, received_at, created_at)
VALUES (@id, @channel_id, @provider_message_id, @rfc_message_id, @message_id, @outcome, @reason, @received_at, @created_at);

-- name: InboundReceiptByRfcID :one
-- The import a Message-Id already has in this channel: a later copy of it is
-- a duplicate pointing at this message (NULL once retention deleted it).
SELECT message_id
FROM communications.inbound_receipts
WHERE channel_id = @channel_id AND rfc_message_id = @rfc_message_id AND outcome = 'imported';

-- name: NewestInboundReceipt :one
-- The restart point of an expired delta: the latest receivedDateTime the
-- channel has a receipt for.
SELECT received_at
FROM communications.inbound_receipts
WHERE channel_id = @channel_id
ORDER BY received_at DESC
LIMIT 1;

-- name: MessageExists :one
-- Whether one of our own Message-Ids names a message of this database: an
-- outbound copy Exchange delivered back is skipped, another installation's
-- id with the same suffix is not.
SELECT EXISTS (SELECT 1 FROM communications.conversation_messages WHERE id = @id);

-- name: FindConversationsByRfcMessageIDs :many
-- Threading's first two steps: every message of the channel, in any
-- direction, whose Message-Id is one of ids — In-Reply-To's and the
-- References' — newest first, so the caller takes, for the first id that
-- matches in its order, the newest message's conversation.
SELECT m.rfc_message_id::text AS rfc_message_id, c.id AS conversation_id
FROM communications.conversation_messages m
JOIN communications.conversations c ON c.id = m.conversation_id
WHERE c.channel_id = @channel_id AND m.rfc_message_id = ANY(@ids::text[])
ORDER BY m.occurred_at DESC, m.id DESC;

-- name: FindConversationByGraphConversation :one
-- Threading's third step: the conversation of the newest inbound message of
-- the channel in Graph's conversation, received since @since.
SELECT c.id
FROM communications.conversation_messages m
JOIN communications.conversations c ON c.id = m.conversation_id
WHERE c.channel_id = @channel_id AND m.direction = 'inbound'
  AND m.channel_metadata_json->>'graphConversationId' = @graph_conversation_id::text
  AND m.occurred_at >= @since
ORDER BY m.occurred_at DESC, m.id DESC
LIMIT 1;

-- name: LockConversationForInbound :one
-- The matched conversation re-read inside the import's transaction, locked
-- so its status and customer are decided on what is committed.
SELECT id, status, customer_id
FROM communications.conversations
WHERE id = @id
FOR UPDATE;

-- name: ReopenConversation :exec
-- A closed conversation a person answered is open again.
UPDATE communications.conversations SET status = 'open' WHERE id = @id AND status = 'closed';

-- name: UpdateConversationActivityForInbound :exec
-- The conversation's activity from a received message: the subject filled
-- once, the last activity and the preview advanced only by a message newer
-- than the last.
UPDATE communications.conversations
SET subject = COALESCE(subject, @subject),
    preview_text = CASE WHEN @occurred_at >= last_activity_at THEN @preview_text ELSE preview_text END,
    last_activity_at = GREATEST(last_activity_at, @occurred_at)
WHERE id = @id;

-- name: FindParticipantByChannelAndAddressFold :one
-- The import's participant lookup in any case: the composer stores what the
-- user typed, and ux_participants_channel_id_address is case-sensitive.
SELECT id, channel_id, address, display_name, contact_id, created_at
FROM communications.participants
WHERE channel_id = @channel_id AND lower(address) = lower(@address)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: InsertInboundMessage :exec
INSERT INTO communications.conversation_messages
    (id, conversation_id, direction, participant_id, subject, text_body, html_body,
     channel_metadata_json, raw_payload_storage_key, occurred_at, created_at, rfc_message_id)
VALUES (@id, @conversation_id, 'inbound', @participant_id, @subject, @text_body, @html_body,
        @channel_metadata_json, @raw_payload_storage_key, @occurred_at, @created_at, @rfc_message_id);

-- name: InsertInboundMessageAttachment :exec
-- A received attachment: unscanned, as nothing scans it (the composer's
-- InsertMessageAttachment writes 'clean').
INSERT INTO communications.message_attachments
    (id, message_id, file_name, content_type, size_bytes, content_hash, content_id, storage_key, scan_status, is_inline, created_at)
VALUES (@id, @message_id, @file_name, @content_type, @size_bytes, @content_hash, @content_id, @storage_key, 'unscanned', @is_inline, @created_at);

-- name: SetConversationAutomaticCustomer :exec
-- The linker's one customer, on a conversation that has none.
UPDATE communications.conversations
SET customer_id = @customer_id, customer_association_source = 'automatic'
WHERE id = @id AND customer_id IS NULL;

-- name: InsertConversationCandidate :exec
INSERT INTO communications.conversation_customer_candidates (conversation_id, customer_id, created_at)
VALUES (@conversation_id, @customer_id, @created_at)
ON CONFLICT DO NOTHING;

-- name: SetParticipantContact :exec
-- The linker's one contact, on a participant that has none.
UPDATE communications.participants SET contact_id = @contact_id WHERE id = @id AND contact_id IS NULL;

-- name: SweepInboundReceipts :execrows
-- The receipts' retention (M16): one whose message is gone, or that never had
-- one, once older than the window — except each channel's newest, the
-- restart point of an expired delta.
DELETE FROM communications.inbound_receipts r
WHERE r.message_id IS NULL AND r.created_at < @cutoff
  AND r.id <> (
      SELECT n.id FROM communications.inbound_receipts n
      WHERE n.channel_id = r.channel_id
      ORDER BY n.received_at DESC, n.id DESC
      LIMIT 1
  );

-- name: GetMessageRawPayloadKey :one
-- The raw download's lookup: the key of the message's original, NULL for a
-- message Vantigo keeps none of.
SELECT raw_payload_storage_key FROM communications.conversation_messages WHERE id = @id;
