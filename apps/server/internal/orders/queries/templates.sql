-- name: InsertTemplate :one
-- InsertTemplate writes a quote template (O23); its lines follow it.
INSERT INTO orders.templates (name, intro_text, closing_text, validity_days, discount_visible, archived, revision, created_at, updated_at)
VALUES (sqlc.arg(name), sqlc.arg(intro_text), sqlc.arg(closing_text), sqlc.narg(validity_days),
        sqlc.arg(discount_visible), sqlc.arg(archived), 1, sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: GetTemplate :one
SELECT * FROM orders.templates WHERE id = sqlc.arg(id);

-- name: LockTemplate :one
SELECT * FROM orders.templates WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateTemplate :one
-- UpdateTemplate replaces a template's fields and bumps its revision.
UPDATE orders.templates
SET name = sqlc.arg(name), intro_text = sqlc.arg(intro_text), closing_text = sqlc.arg(closing_text),
    validity_days = sqlc.narg(validity_days), discount_visible = sqlc.arg(discount_visible),
    archived = sqlc.arg(archived), revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteTemplate :execrows
DELETE FROM orders.templates WHERE id = sqlc.arg(id);

-- name: ListTemplates :many
-- ListTemplates is every template by name, the archived ones only when asked.
SELECT * FROM orders.templates
WHERE sqlc.arg(include_archived)::boolean OR NOT archived
ORDER BY lower(name), id;
