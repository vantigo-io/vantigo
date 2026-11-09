-- Every query here filters by user_id (the Assistant design S9): a
-- conversation that is not the caller's is not found.

-- name: ListConversations :many
-- ListConversations is the caller's conversations, pinned first and then the
-- newest activity, keyset-paged on (pinned, last_activity_at, id). The cursor
-- is the last row of the previous page; the zero values start from the top.
SELECT c.*,
       EXISTS (SELECT 1 FROM assistant.runs r WHERE r.conversation_id = c.id AND r.state = 'running') AS running
FROM assistant.conversations c
WHERE c.user_id = @user_id
  AND (c.archived_at IS NOT NULL) = @archived::boolean
  AND (@query::text = '' OR c.title ILIKE '%' || @query || '%'
       OR EXISTS (SELECT 1 FROM assistant.messages m WHERE m.conversation_id = c.id AND m.search_text ILIKE '%' || @query || '%'))
  AND (NOT @has_cursor::boolean
       OR (c.pinned, c.last_activity_at, c.id) < (@cursor_pinned::boolean, @cursor_at::timestamptz, @cursor_id::uuid))
ORDER BY c.pinned DESC, c.last_activity_at DESC, c.id DESC
LIMIT @page_size;

-- name: GetConversation :one
SELECT c.*,
       EXISTS (SELECT 1 FROM assistant.runs r WHERE r.conversation_id = c.id AND r.state = 'running') AS running
FROM assistant.conversations c
WHERE c.id = @id AND c.user_id = @user_id;

-- name: CreateConversation :one
INSERT INTO assistant.conversations (id, user_id, title, origin_app, origin_route, agent_id, created_at, last_activity_at)
VALUES (@id, @user_id, '', @origin_app, @origin_route, sqlc.narg(agent_id)::uuid, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateConversation :one
UPDATE assistant.conversations SET
    title = COALESCE(sqlc.narg(title)::text, title),
    pinned = COALESCE(sqlc.narg(pinned)::boolean, pinned),
    archived_at = CASE
        WHEN sqlc.narg(archived)::boolean IS NULL THEN archived_at
        WHEN sqlc.narg(archived)::boolean THEN COALESCE(archived_at, @now::timestamptz)
        ELSE NULL END
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteConversation :one
DELETE FROM assistant.conversations WHERE id = @id AND user_id = @user_id RETURNING id;

-- name: TouchConversation :exec
UPDATE assistant.conversations SET last_activity_at = @now::timestamptz WHERE id = @id;

-- name: SetTitleIfEmpty :exec
UPDATE assistant.conversations SET title = @title WHERE id = @id AND title = '';
