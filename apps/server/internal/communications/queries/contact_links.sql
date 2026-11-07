-- A participant's link to a contact of Customers (Messenger design N19-N22,
-- delivery D): the link is participants.contact_id, set by an explicit act;
-- what is only suggested or referred sits in participant_contact_hints.

-- name: GetParticipantWithChannel :one
-- The participant and the channel it belongs to, for linking it (N20) and for
-- writing to it from a customer (N22).
SELECT p.id, p.channel_id, p.address, p.display_name, p.contact_id,
       ch.type AS channel_type, ch.provider AS channel_provider, ch.is_active AS channel_is_active
FROM communications.participants p
JOIN communications.channels ch ON ch.id = p.channel_id
WHERE p.id = @id;

-- name: LockParticipant :one
-- A link is an explicit act and a ref never overrides one, so the two are
-- serialised on the participant's row.
SELECT id FROM communications.participants WHERE id = @id FOR UPDATE;

-- name: SetParticipantLinkedContact :exec
-- The explicit link (N20): sets or replaces the contact, whatever it was.
UPDATE communications.participants SET contact_id = @contact_id WHERE id = @id;

-- name: ClearParticipantContact :exec
UPDATE communications.participants SET contact_id = NULL WHERE id = @id;

-- name: LinkParticipantContactIfNone :execrows
-- A ref's link (N21): only a participant with no contact gets one. 0 rows is
-- the participant that already has one.
UPDATE communications.participants SET contact_id = @contact_id WHERE id = @id AND contact_id IS NULL;

-- name: GetParticipantContact :one
SELECT contact_id FROM communications.participants WHERE id = @id;

-- name: DeleteParticipantContactHints :exec
DELETE FROM communications.participant_contact_hints WHERE participant_id = @participant_id;

-- name: UpsertSuggestedContact :exec
INSERT INTO communications.participant_contact_hints (participant_id, suggested_contact_id, updated_at)
VALUES (@participant_id, @contact_id, @now)
ON CONFLICT (participant_id) DO UPDATE
SET suggested_contact_id = EXCLUDED.suggested_contact_id, updated_at = EXCLUDED.updated_at;

-- name: UpsertReferralContact :exec
INSERT INTO communications.participant_contact_hints (participant_id, referral_contact_id, updated_at)
VALUES (@participant_id, @contact_id, @now)
ON CONFLICT (participant_id) DO UPDATE
SET referral_contact_id = EXCLUDED.referral_contact_id, updated_at = EXCLUDED.updated_at;

-- name: ClearSuggestedContact :exec
-- A suggestion is moot once the participant has a contact. The row goes with it
-- when it holds nothing else.
UPDATE communications.participant_contact_hints SET suggested_contact_id = NULL WHERE participant_id = @participant_id;

-- name: DeleteEmptyParticipantContactHints :exec
DELETE FROM communications.participant_contact_hints
WHERE participant_id = @participant_id AND suggested_contact_id IS NULL AND referral_contact_id IS NULL;

-- name: GetParticipantContactHints :one
SELECT suggested_contact_id, referral_contact_id
FROM communications.participant_contact_hints
WHERE participant_id = @participant_id;

-- name: GetConversationMessengerParticipant :one
-- A messenger conversation's participant, the one the thread header is about.
SELECT p.id, p.contact_id
FROM communications.conversation_participants cp
JOIN communications.participants p ON p.id = cp.participant_id
WHERE cp.conversation_id = @conversation_id
ORDER BY p.created_at, p.id
LIMIT 1;

-- name: ParticipantLatestInbound :one
-- The participant's newest message across all its conversations: the instant the
-- reply window counts from (N6, N22). An echo is outbound and never counts.
SELECT occurred_at
FROM communications.conversation_messages
WHERE participant_id = @participant_id AND direction = 'inbound'
ORDER BY occurred_at DESC, id DESC
LIMIT 1;

-- name: DeleteConversationParticipantLinks :exec
-- A conversation started from a customer that never got its first message (N22):
-- taken back whole.
DELETE FROM communications.conversation_participants WHERE conversation_id = @conversation_id;

-- name: DeleteConversationWithoutMessages :exec
DELETE FROM communications.conversations c
WHERE c.id = @id
  AND NOT EXISTS (SELECT 1 FROM communications.conversation_messages m WHERE m.conversation_id = c.id);

-- name: ListActiveMessengerChannels :many
-- The channels a contact's Messenger link can be made for (N21), and the
-- columns of a contact's channel entries (N22).
SELECT id, provider, address, display_name
FROM communications.channels
WHERE type = 'messenger' AND is_active
ORDER BY lower(coalesce(display_name, address)), id;

-- name: ListMessengerParticipantsByContacts :many
-- The participants linked to the contacts, one per contact and active Messenger
-- channel, with the participant's newest inbound message (N22). The page-scoped
-- id (address) is not selected: it never leaves Communications.
SELECT p.contact_id, p.id AS participant_id, p.channel_id, max(m.occurred_at) AS last_inbound_at
FROM communications.participants p
JOIN communications.channels ch ON ch.id = p.channel_id
LEFT JOIN communications.conversation_messages m ON m.participant_id = p.id AND m.direction = 'inbound'
WHERE p.contact_id = ANY(@contact_ids::int[]) AND ch.type = 'messenger' AND ch.is_active
GROUP BY p.id
ORDER BY p.contact_id, p.created_at, p.id;
