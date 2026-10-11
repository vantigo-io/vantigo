-- The history (design S21, §3.6): one row per change, the actor snapshotted.

-- name: InsertEvent :exec
-- InsertEvent appends one event. person_id is NULL for an installation-level
-- event, such as a person deleted as created in error.
INSERT INTO staff.events (person_id, kind, actor_kind, actor_user_id, actor_display, at, payload, payload_classes)
VALUES (@person_id, @kind, @actor_kind, @actor_user_id, @actor_display, @at::timestamptz, @payload, @payload_classes::text[]);

-- name: CountPersonEvents :one
-- CountPersonEvents counts what ListPersonEvents pages through, health events
-- (those whose payload touches class E) only when asked for.
SELECT count(*) FROM staff.events
WHERE person_id = @person_id AND (@with_health::boolean OR NOT ('E' = ANY(payload_classes)));

-- name: ListPersonEvents :many
-- ListPersonEvents is one page of a person's history, newest first; two
-- events of one instant keep the order they were written in, reversed. A
-- health event — one whose payload touches class E, such as a health
-- document's — is left out unless asked for: an audience without the health
-- group does not learn that one happened.
SELECT id, kind, actor_kind, actor_user_id, actor_display, at, payload, payload_classes
FROM staff.events
WHERE person_id = @person_id AND (@with_health::boolean OR NOT ('E' = ANY(payload_classes)))
ORDER BY at DESC, id DESC
LIMIT @page_size OFFSET @page_offset;
