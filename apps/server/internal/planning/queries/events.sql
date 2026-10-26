-- The record (design S23, §3.6): one row per write, the actor snapshotted.

-- name: InsertEvent :exec
-- InsertEvent appends one event inside the write's own transaction.
INSERT INTO planning.events (entity_kind, entity_id, person_id, project_id, kind, payload, actor_kind, actor_user_id, actor_display, occurred_at)
VALUES (@entity_kind, @entity_id, sqlc.narg(person_id), sqlc.narg(project_id), @kind, @payload, @actor_kind, sqlc.narg(actor_user_id)::uuid, sqlc.narg(actor_display), @occurred_at::timestamptz);

-- name: ListEvents :many
-- ListEvents is the record filtered by person, project and allocation (each
-- NULL for any), newest first, at most @max_events. A wish's events and the
-- terms' (terms_set, terms_cleared) are planning:manage's (S23, S14,
-- amendment 89): without @include_manage_only none is listed, whatever the
-- filter.
SELECT * FROM planning.events
WHERE (@include_manage_only::boolean OR (entity_kind <> 'wish' AND kind NOT IN ('terms_set', 'terms_cleared')))
  AND (sqlc.narg(person_id)::bigint IS NULL OR person_id = sqlc.narg(person_id)::bigint)
  AND (sqlc.narg(project_id)::integer IS NULL OR project_id = sqlc.narg(project_id)::integer)
  AND (sqlc.narg(allocation_id)::bigint IS NULL OR (entity_kind = 'allocation' AND entity_id = sqlc.narg(allocation_id)::bigint))
ORDER BY occurred_at DESC, id DESC
LIMIT @max_events::integer;
