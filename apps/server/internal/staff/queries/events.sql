-- The history (design S21, §3.6): one row per change, the actor snapshotted.

-- name: InsertEvent :exec
-- InsertEvent appends one event. person_id is NULL for an installation-level
-- event, such as a person deleted as created in error.
INSERT INTO staff.events (person_id, kind, actor_kind, actor_user_id, actor_display, at, payload, payload_classes)
VALUES (@person_id, @kind, @actor_kind, @actor_user_id, @actor_display, @at::timestamptz, @payload, @payload_classes::text[]);

-- name: CountPersonEvents :one
SELECT count(*) FROM staff.events WHERE person_id = @person_id;

-- name: ListPersonEvents :many
-- ListPersonEvents is one page of a person's history, newest first; two
-- events of one instant keep the order they were written in, reversed.
SELECT id, kind, actor_kind, actor_user_id, actor_display, at, payload
FROM staff.events
WHERE person_id = @person_id
ORDER BY at DESC, id DESC
LIMIT @page_size OFFSET @page_offset;
