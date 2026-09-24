-- name: CustomerConversationsForExport :many
-- CustomerConversationsForExport and the two reads after it are a private
-- person's correspondence for their export (customers GDPR design D2,
-- contracts.CustomerPersonalData): every conversation about the customer,
-- oldest first, then every message of them — internal notes included, they are
-- about the person too — with both bodies, and every attachment's name. customer_personal_data.go
-- reads the three in one read-only snapshot. ix_conversations_customer_id
-- finds the conversations.
SELECT id, subject, status, created_at, last_activity_at
FROM communications.conversations
WHERE customer_id = @customer_id::integer
ORDER BY created_at, id;

-- name: CustomerMessagesForExport :many
SELECT m.id, m.conversation_id, m.direction, m.subject, m.text_body, m.html_body, m.occurred_at
FROM communications.conversation_messages m
JOIN communications.conversations c ON c.id = m.conversation_id
WHERE c.customer_id = @customer_id::integer
ORDER BY m.occurred_at, m.id;

-- name: CustomerAttachmentNamesForExport :many
SELECT a.message_id, a.file_name
FROM communications.message_attachments a
JOIN communications.conversation_messages m ON m.id = a.message_id
JOIN communications.conversations c ON c.id = m.conversation_id
WHERE c.customer_id = @customer_id::integer
ORDER BY a.created_at, a.id;

-- name: CustomerConversationMessageIDs :many
-- CustomerConversationMessageIDs is where a person's anonymisation starts in
-- this module (customers GDPR design D2): every message of every conversation
-- about the customer, handed to deleteMessages — the retention batch's own
-- ordered deletes — inside the customers module's transaction.
SELECT m.id
FROM communications.conversation_messages m
JOIN communications.conversations c ON c.id = m.conversation_id
WHERE c.customer_id = @customer_id::integer
ORDER BY m.id;

-- name: CustomerConversationUploadKeys :many
-- The staged uploads of those conversations: their rows go with the
-- conversation (attachment_uploads cascades), so their objects are queued for
-- deletion first. An 'expired' upload's object was queued when it expired.
SELECT u.storage_key
FROM communications.attachment_uploads u
JOIN communications.conversations c ON c.id = u.conversation_id
WHERE c.customer_id = @customer_id::integer AND u.scan_status <> 'expired'
ORDER BY u.id;

-- name: DeleteCustomerConversations :execrows
-- The conversations themselves, once their messages are gone: participants'
-- links, tags, read states, idempotency records, AI interactions, staged
-- uploads and candidate rows all cascade.
DELETE FROM communications.conversations WHERE customer_id = @customer_id::integer;

-- name: ClearCustomerSuggestions :execrows
-- A suggestion naming the person on somebody else's conversation goes too, with
-- the reasoning that may name them.
UPDATE communications.conversations
SET suggested_customer_id = NULL, suggested_customer_confidence = NULL, suggested_customer_reasoning = NULL
WHERE suggested_customer_id = @customer_id::integer;

-- name: DeleteCustomerCandidates :execrows
-- And a candidate row naming the person on a conversation that is not theirs.
DELETE FROM communications.conversation_customer_candidates WHERE customer_id = @customer_id::integer;
