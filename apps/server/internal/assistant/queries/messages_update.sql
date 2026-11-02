-- name: UpdateMessageContent :exec
UPDATE assistant.messages SET content = @content::jsonb WHERE id = @id;

-- name: GetMessageByID :one
-- GetMessageByID is for the run that owns the message.
SELECT * FROM assistant.messages WHERE id = @id;
