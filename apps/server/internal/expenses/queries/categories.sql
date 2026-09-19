-- name: ListCategories :many
-- ListCategories is every category, active and not, in the order the picker
-- offers them: by position and then by name, so two rows that share a
-- position still have one order.
SELECT * FROM expenses.categories ORDER BY position, name;

-- name: GetCategory :one
SELECT * FROM expenses.categories WHERE id = @id;

-- name: NextCategoryPosition :one
-- NextCategoryPosition is the position a new category takes when the caller
-- named none: last. An empty table answers 1.
SELECT coalesce(max(position), 0) + 1 FROM expenses.categories;

-- name: InsertCategory :one
-- InsertCategory adds a category. A name another row already holds, however
-- it is cased, raises 23505 on ux_categories_name_lower, which the handler
-- turns into the name field error.
INSERT INTO expenses.categories (name, active, position, created_at, updated_at)
VALUES (@name, @active, @position, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateCategory :one
-- UpdateCategory replaces a category's name, whether it may be chosen, and
-- where it sits. No row is an unknown id; a duplicate name raises 23505 the
-- same way the insert does.
UPDATE expenses.categories SET
    name = @name,
    active = @active,
    position = @position,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;
