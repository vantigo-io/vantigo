-- name: InsertMessage :one
INSERT INTO assistant.messages (id, conversation_id, run_id, role, content, context, search_text, model, input_tokens, output_tokens, created_at)
VALUES (@id, @conversation_id, sqlc.narg(run_id)::uuid, @role, @content::jsonb, sqlc.narg(context)::jsonb, @search_text,
        sqlc.narg(model), sqlc.narg(input_tokens)::integer, sqlc.narg(output_tokens)::integer, @now::timestamptz)
RETURNING *;

-- name: ListMessages :many
-- ListMessages is a conversation's messages in order. It joins the
-- conversation, so it answers nothing for a conversation that is not the
-- caller's.
SELECT m.* FROM assistant.messages m
JOIN assistant.conversations c ON c.id = m.conversation_id
WHERE m.conversation_id = @conversation_id AND c.user_id = @user_id
ORDER BY m.seq;
