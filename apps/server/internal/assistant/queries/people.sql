-- name: GetPerson :one
SELECT * FROM assistant.person_settings WHERE user_id = @user_id;

-- name: LockPerson :one
SELECT * FROM assistant.person_settings WHERE user_id = @user_id FOR UPDATE;

-- name: EnsurePerson :exec
-- EnsurePerson makes the person's row, at version 0 and off, so a change can
-- lock it: the first write moves it to version 1.
INSERT INTO assistant.person_settings (user_id, enabled, version, updated_at)
VALUES (@user_id, false, 0, @now::timestamptz)
ON CONFLICT (user_id) DO NOTHING;

-- name: SetPersonEnabled :one
UPDATE assistant.person_settings SET enabled = @enabled, version = version + 1, updated_at = @now::timestamptz
WHERE user_id = @user_id
RETURNING *;

-- name: SetLastConversation :exec
INSERT INTO assistant.person_settings (user_id, enabled, last_conversation_id, version, updated_at)
VALUES (@user_id, false, sqlc.narg(conversation_id)::uuid, 0, @now::timestamptz)
ON CONFLICT (user_id) DO UPDATE SET last_conversation_id = EXCLUDED.last_conversation_id;

-- name: ClearLastConversation :exec
UPDATE assistant.person_settings SET last_conversation_id = NULL
WHERE user_id = @user_id AND last_conversation_id = @conversation_id::uuid;
