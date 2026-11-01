-- name: DeleteExpiredConversations :execrows
-- DeleteExpiredConversations deletes whole conversations whose last activity
-- is before the cutoff, a chunk at a time (S9). Their messages, runs and
-- actions go with them.
DELETE FROM assistant.conversations
WHERE id IN (
    SELECT id FROM assistant.conversations WHERE last_activity_at < @cutoff::timestamptz ORDER BY last_activity_at LIMIT @batch_size
);

-- name: ListHeldUserIDs :many
-- ListHeldUserIDs is the users the module holds anything of, after a given
-- id, in id order: the erasure sweep asks identity which of them still exist.
SELECT user_id FROM (
    SELECT user_id FROM assistant.conversations
    UNION SELECT user_id FROM assistant.person_settings
    UNION SELECT user_id FROM assistant.runs
) held
WHERE user_id > @after::uuid
ORDER BY user_id
LIMIT @batch_size;

-- name: DeleteUserData :exec
-- DeleteUserData is everything of an erased user: conversations (messages,
-- runs and actions cascade) and the person's settings.
WITH c AS (DELETE FROM assistant.conversations WHERE user_id = ANY(@user_ids::uuid[]))
DELETE FROM assistant.person_settings WHERE user_id = ANY(@user_ids::uuid[]);
