-- name: InsertEvent :exec
-- InsertEvent appends one row to the record (I31), inside the write's own
-- transaction.
INSERT INTO inventory.events (entity_kind, entity_id, site_id, kind, payload, actor_kind, actor_user_id, actor_display, occurred_at)
VALUES (@entity_kind, @entity_id, sqlc.narg(site_id), @kind, @payload, @actor_kind, sqlc.narg(actor_user_id), sqlc.narg(actor_display), @occurred_at);
