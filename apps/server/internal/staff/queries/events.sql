-- The history (design S21, §3.6): one row per change, the actor snapshotted.

-- name: InsertEvent :exec
-- InsertEvent appends one event. person_id is NULL for an installation-level
-- event, such as a person deleted as created in error.
INSERT INTO staff.events (person_id, kind, actor_kind, actor_user_id, actor_display, at, payload, payload_classes)
VALUES (@person_id, @kind, @actor_kind, @actor_user_id, @actor_display, @at::timestamptz, @payload, @payload_classes::text[]);

-- name: CountPersonEvents :one
-- CountPersonEvents counts what ListPersonEvents pages through, health events
-- (those whose payload touches class E) — but the absence family's — and the
-- hold's kinds only when asked for.
SELECT count(*) FROM staff.events
WHERE person_id = @person_id
  AND (@with_health::boolean OR NOT ('E' = ANY(payload_classes)) OR kind = ANY(@absence_kinds::text[]))
  AND (@with_hold::boolean OR NOT (kind = ANY(@hold_kinds::text[])));

-- name: ListPersonEvents :many
-- ListPersonEvents is one page of a person's history, newest first; two
-- events of one instant keep the order they were written in, reversed. A
-- health event — one whose payload touches class E, such as a health
-- document's — is left out unless asked for: an audience without the health
-- group does not learn that one happened. The hold's kinds (@hold_kinds) —
-- the legal hold, the reassessment, the rehire, the export — are left out
-- the same way unless asked for (@with_hold). The absence family
-- (@absence_kinds) is listed whatever its classes (plan pre-flight I6): a
-- sick row's event is shaped field by field, each by its own group.
SELECT id, kind, actor_kind, actor_user_id, actor_display, at, payload, payload_classes
FROM staff.events
WHERE person_id = @person_id
  AND (@with_health::boolean OR NOT ('E' = ANY(payload_classes)) OR kind = ANY(@absence_kinds::text[]))
  AND (@with_hold::boolean OR NOT (kind = ANY(@hold_kinds::text[])))
ORDER BY at DESC, id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: ListAllPersonEvents :many
-- ListAllPersonEvents is a person's whole history, oldest first, for the
-- personal-data exports (design S16): no page, health events included —
-- each export shapes what its reader may read.
SELECT id, kind, actor_kind, actor_user_id, actor_display, at, payload, payload_classes
FROM staff.events
WHERE person_id = @person_id
ORDER BY at, id;
