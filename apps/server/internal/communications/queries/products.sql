-- A product in a message (Messenger design N16-N18): the card attachments a
-- reply copies from a product's images, and the public route that serves them.

-- name: InsertCardAttachment :exec
-- A card's image: the bytes of a product image, copied into this module's
-- storage by the reply (N16), an attachment of role 'card'. Always clean: it is
-- a file Vantigo normalised itself, not an upload.
INSERT INTO communications.message_attachments
    (id, message_id, file_name, content_type, size_bytes, content_hash, storage_key, scan_status, is_inline, role, created_at)
VALUES (@id, @message_id, @file_name, @content_type, @size_bytes, @content_hash, @storage_key, 'clean', false, 'card', @created_at);

-- name: GetPublicCardAttachment :one
-- What the public media route serves (N18): a card attachment of an outbound
-- message on a Messenger channel, with the channel's provider so the route can
-- ask whether that provider wants public media. Anything else is no row.
SELECT a.id, a.content_type, a.size_bytes, a.storage_key, ch.provider
FROM communications.message_attachments a
JOIN communications.conversation_messages m ON m.id = a.message_id
JOIN communications.conversations c ON c.id = m.conversation_id
JOIN communications.channels ch ON ch.id = c.channel_id
WHERE a.id = @id AND a.role = 'card' AND m.direction = 'outbound' AND ch.type = 'messenger';
