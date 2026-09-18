-- The checklist queries (design §3.1, §4.1). A checklist is a task's own
-- sibling group: the items are numbered 1..n inside one task, an add appends
-- after the last of them, and every write that decides a number renumbers the
-- whole group in one statement — the same shape tasks.sql gives a project's
-- task ordering, under the same per-project advisory lock
-- (AcquireTaskOrderLock), because a checklist belongs to a task and a task
-- belongs to a project.
--
-- Every item query is scoped by task_id as well as by id. That is not
-- belt-and-braces: an item is addressed under the task in its path, and an
-- item of another task must answer the same bare 404 an unknown one does, or a
-- caller could learn which ids exist by trying them.

-- name: ListChecklistItems :many
-- ListChecklistItems is one task's checklist in the order it is read in, which
-- is the order ix_task_checklist_items_task_id_position is built for. The id
-- breaks ties so the list is stable while a renumbering is in flight.
SELECT * FROM projects.task_checklist_items
WHERE task_id = @task_id
ORDER BY position, id;

-- name: GetChecklistItem :one
-- GetChecklistItem is one item of one task, which is how a change reads back
-- the number a renumbering gave it.
SELECT * FROM projects.task_checklist_items
WHERE id = @id AND task_id = @task_id;

-- name: LockChecklistItem :one
-- LockChecklistItem is GetChecklistItem with the row held for the rest of the
-- transaction: a change carries only the fields it means to move, so what the
-- others become is decided from the row nobody else can edit underneath.
SELECT * FROM projects.task_checklist_items
WHERE id = @id AND task_id = @task_id
FOR UPDATE;

-- name: MaxChecklistPosition :one
-- MaxChecklistPosition is the number an add appends after, 0 for the first
-- item of a task. It is read under the project's ordering lock, because two
-- adds racing for the end of one checklist would otherwise both read the same
-- maximum — and no row lock can cover the gap after the last row.
SELECT coalesce(max(position), 0)::integer FROM projects.task_checklist_items
WHERE task_id = @task_id;

-- name: ChecklistItemIDs :many
-- ChecklistItemIDs is one task's checklist in its current order, every row
-- held for the rest of the transaction. A move reorders this list in Go and
-- writes it back with RenumberChecklistItems, so what it renumbers is exactly
-- what it read.
SELECT id FROM projects.task_checklist_items
WHERE task_id = @task_id
ORDER BY position, id
FOR UPDATE;

-- name: RenumberChecklistItems :exec
-- RenumberChecklistItems writes a checklist's order back as 1..n in one
-- statement: ids is the list in its new order and WITH ORDINALITY is the
-- number each one takes. A row already carrying its number is left alone, so a
-- move that only reordered part of a list does not touch the rest of it.
UPDATE projects.task_checklist_items i
SET position = v.ord::integer, updated_at = @now::timestamptz
FROM unnest(@ids::bigint[]) WITH ORDINALITY AS v(id, ord)
WHERE i.id = v.id AND i.position <> v.ord::integer;

-- name: InsertChecklistItem :one
-- InsertChecklistItem adds one item to a task. created_at and updated_at are
-- the same instant on creation, supplied by the caller from Deps.Clock(); an
-- item is always added open, and position is the number computed under the
-- project's ordering lock.
INSERT INTO projects.task_checklist_items (task_id, text, done, position, created_at, updated_at)
VALUES (@task_id, @text, false, @position, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateChecklistItem :one
-- UpdateChecklistItem applies one change to an item under that lock. It
-- carries no revision guard: an item is a line of text and a tick box, and the
-- row lock already serialises two people editing the same one. Where the item
-- sits is not written here — the number is RenumberChecklistItems', which runs
-- over the whole list afterwards, so there is never a moment where two items
-- both claim one number.
UPDATE projects.task_checklist_items SET
    text = @text,
    done = @done,
    updated_at = @now::timestamptz
WHERE id = @id AND task_id = @task_id
RETURNING *;

-- name: DeleteChecklistItem :execrows
-- DeleteChecklistItem removes one item of one task. The row count is what
-- decides the 404: two deletes racing must not both answer 204, and the
-- surviving items are renumbered by the same transaction.
DELETE FROM projects.task_checklist_items WHERE id = @id AND task_id = @task_id;
