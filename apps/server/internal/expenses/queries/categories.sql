-- name: ListCategories :many
-- ListCategories is every category, active and not, in the order the picker
-- offers them: by position and then by name, so two rows that share a
-- position still have one order.
SELECT * FROM expenses.categories ORDER BY position, name;

-- name: GetCategory :one
SELECT * FROM expenses.categories WHERE id = @id;

-- name: CategoryIDsInOrder :many
-- CategoryIDsInOrder is every category in its current order, every row held
-- for the rest of the transaction. A move reorders this list in Go and writes
-- it back with RenumberCategories, so what it renumbers is exactly what it
-- read, and two moves at once take the rows in the same order and queue.
--
-- Deactivated categories are in it: a category nobody may choose any more
-- still sits where it sat, so deactivating one does not shuffle the picker.
SELECT id FROM expenses.categories ORDER BY position, name FOR UPDATE;

-- name: RenumberCategories :exec
-- RenumberCategories writes the list back as a dense 1..n in one statement:
-- ids is the categories in their new order and WITH ORDINALITY is the number
-- each one takes. A row already carrying its number is left alone, so a move
-- at one end of the list does not touch the other.
UPDATE expenses.categories c
SET position = v.ord::integer, updated_at = @now::timestamptz
FROM unnest(@ids::integer[]) WITH ORDINALITY AS v(id, ord)
WHERE c.id = v.id AND c.position <> v.ord::integer;

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
