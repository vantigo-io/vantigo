-- The erasure sweep (manufacturing design §15, rule 19): a time log keeps its
-- operator's user id and display name as a record, and loses its free-text note
-- once identity no longer knows the operator.

-- name: ListNotedUserIDs :many
-- ListNotedUserIDs is the operators whose time logs carry a note, after a
-- given id, in id order: the sweep asks identity which of them still exist.
SELECT DISTINCT user_id::uuid AS user_id
FROM manufacturing.time_logs
WHERE user_id IS NOT NULL AND note <> '' AND user_id > @after::uuid
ORDER BY user_id
LIMIT @batch_size;

-- name: BlankNotesOfUsers :execrows
-- BlankNotesOfUsers blanks the notes of the erased operators' time logs.
UPDATE manufacturing.time_logs SET note = ''
WHERE user_id = ANY(@user_ids::uuid[]) AND note <> '';
