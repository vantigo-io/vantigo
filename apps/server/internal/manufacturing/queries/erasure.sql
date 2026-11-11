-- The erasure sweep (manufacturing design §15, rule 19): a time log keeps its
-- operator's user id and display name as a record, and loses its free-text note
-- once identity no longer knows the person whose words it is (note_by: the
-- supervisor who corrected or added the log, or who blocked the work center).

-- name: ListNotedUserIDs :many
-- ListNotedUserIDs is the people whose words a time log's note is, after a
-- given id, in id order: the sweep asks identity which of them still exist.
SELECT DISTINCT note_by::uuid AS user_id
FROM manufacturing.time_logs
WHERE note_by IS NOT NULL AND note <> '' AND note_by > @after::uuid
ORDER BY user_id
LIMIT @batch_size;

-- name: BlankNotesOfUsers :execrows
-- BlankNotesOfUsers blanks the notes the erased people wrote. It takes the
-- rows in id order and skips those another transaction holds — a block or a
-- correction under way, which lock the logs in id order themselves — so the
-- sweep never waits on, nor deadlocks with, the shop floor; a skipped note
-- goes in the next cycle.
UPDATE manufacturing.time_logs SET note = ''
WHERE id IN (SELECT l.id FROM manufacturing.time_logs l
             WHERE l.note_by = ANY(@user_ids::uuid[]) AND l.note <> ''
             ORDER BY l.id
             FOR UPDATE SKIP LOCKED);
