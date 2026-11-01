-- name: UpdateMessageContent :exec
UPDATE assistant.messages SET content = @content::jsonb WHERE id = @id;
