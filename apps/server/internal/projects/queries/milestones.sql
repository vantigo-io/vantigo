-- The billing milestone queries (design §3.2, §3.3). Three things run through
-- them. A milestone is addressed by its own id, not by its project's, so the
-- read that resolves "which project is this about" comes first. Every write
-- takes the *project's* row lock before anything else (LockProject,
-- queries/projects.sql), because what a milestone may be — it needs a
-- currency, and a percent one needs a fixed price — is decided from the
-- project, and only one lock held by every such writer serialises them.
-- Ordering is a third lock again (AcquireMilestoneOrderLock), taken last and
-- only by the writes that decide a position.
--
-- The file opens with the two reads the project's own guards need, which came
-- before there was any milestone operation to create rows through.

-- name: CountNonCancelledMilestones :one
-- CountNonCancelledMilestones is D13's currency guard, extended to
-- milestones: any milestone that is not cancelled carries an amount in the
-- project's currency (a flat one, or a percent of the fixed price, itself in
-- that currency), so the currency cannot be cleared or changed while one
-- exists. A cancelled milestone does not count — it bills nothing.
SELECT count(*) FROM projects.billing_milestones
WHERE project_id = @project_id AND status <> 'cancelled';

-- name: ListOpenPercentMilestoneNames :many
-- ListOpenPercentMilestoneNames is the fixed-price guard's own read: a
-- percent milestone that is still 'planned' or 'ready' depends on the
-- project's fixed price to resolve its amount, so removing that price (or
-- moving the project off fixed-price billing) is refused while any exist.
-- Invoiced and cancelled ones are excluded — an invoiced milestone's amount
-- is already frozen, and a cancelled one bills nothing. The order is
-- position, the project's own manual order, so the names a refusal lists
-- read the way the invoice plan does.
SELECT name FROM projects.billing_milestones
WHERE project_id = @project_id AND percent IS NOT NULL AND status IN ('planned', 'ready')
ORDER BY position, id;

-- name: GetMilestone :one
-- GetMilestone fetches one milestone by id. It is the read that resolves
-- which project a /projects/milestones/{milestoneId} request is about, and an
-- unknown milestone and a milestone on an invisible project answer the same
-- bare 404, so the row is loaded first and discarded after — tasks' GetTask
-- does exactly this for the same reason.
SELECT * FROM projects.billing_milestones WHERE id = @id;

-- name: LockMilestone :one
-- LockMilestone is GetMilestone with the row held for the rest of the
-- transaction. Every write decides against the row it returns rather than
-- against the one the handler read: whether this request is the move that
-- freezes the amount, whether the revision the caller sent is still current,
-- and whether the milestone is still editable at all are all questions
-- another writer can answer differently in between. It is taken *after* the
-- project's own lock, never before, so two writers that hold both can only
-- queue and never deadlock.
SELECT * FROM projects.billing_milestones WHERE id = @id FOR UPDATE;

-- name: ListProjectMilestones :many
-- ListProjectMilestones is a whole project's plan in the order it is read:
-- the manual position, with cancelled milestones after everything else
-- whatever number they carry. A cancelled milestone keeps its position — it
-- is still part of the 1..n the plan is renumbered as — and only sorts last,
-- because what it says is history rather than something still to bill.
SELECT * FROM projects.billing_milestones
WHERE project_id = @project_id
ORDER BY (status = 'cancelled'), position, id;

-- name: AcquireMilestoneOrderLock :exec
-- AcquireMilestoneOrderLock is the serialisation point of every write that
-- decides a position inside one project: a create appending after the last
-- milestone, a move renumbering the plan, and a delete closing the gap it
-- leaves. A row lock cannot cover a create — the number two concurrent
-- creates race for is the gap after the last row, and a gap has no row to
-- lock — so the lock is taken on the project instead, for the rest of the
-- transaction. It is a two-argument advisory lock in class 10 (tasks use 9),
-- which is a lock space of its own: it can never collide with identity's
-- single-argument ones, and it never substitutes for the project's row lock.
SELECT pg_advisory_xact_lock(@lock_class::integer, @project_id::integer);

-- name: MaxMilestonePosition :one
-- MaxMilestonePosition is the number a create appends after, 0 for the first
-- milestone of a project.
SELECT coalesce(max(position), 0)::integer FROM projects.billing_milestones
WHERE project_id = @project_id;

-- name: MilestoneIDs :many
-- MilestoneIDs is one project's plan in its current order, every row held for
-- the rest of the transaction. A move reorders this list in Go and writes it
-- back with RenumberMilestones, so what it renumbers is exactly what it read.
-- The order is the stored one, cancelled milestones in their own place rather
-- than at the end: the listing moves them for the reader, the numbering does
-- not move them at all.
SELECT id FROM projects.billing_milestones
WHERE project_id = @project_id
ORDER BY position, id
FOR UPDATE;

-- name: RenumberMilestones :exec
-- RenumberMilestones writes a plan's order back as 1..n in one statement: ids
-- is the plan in its new order and WITH ORDINALITY is the number each one
-- takes. A row already carrying its number is left alone, so a move that only
-- reordered part of the plan does not touch the rest of it — and no
-- revision moves, because where a milestone sits is not a field of its form.
UPDATE projects.billing_milestones m
SET position = v.ord::integer, updated_at = @now::timestamptz
FROM unnest(@ids::integer[]) WITH ORDINALITY AS v(id, ord)
WHERE m.id = v.id AND m.position <> v.ord::integer;

-- name: InsertMilestone :one
-- InsertMilestone creates one milestone. created_at and updated_at are the
-- same instant on creation, supplied by the caller from Deps.Clock(); status
-- and revision take the column defaults ('planned', 1), because a milestone
-- is always created planned and never carries a revision yet; position is the
-- number computed under the project's ordering lock.
INSERT INTO projects.billing_milestones (
    project_id, name, description, planned_date, amount, percent, position,
    created_by_user_id, created_at, updated_at
) VALUES (
    @project_id, @name, @description, @planned_date, @amount, @percent, @position,
    @created_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateMilestone :one
-- UpdateMilestone applies one content edit. It carries no revision predicate:
-- the handler holds the row under LockMilestone and has already compared the
-- revision against it, which is a comparison nothing can win a race against
-- while that lock is held — and it lets the 409 name the current revision
-- from the locked row rather than from a second read taken after a rollback.
-- What the edit does not touch is where the milestone sits and what status it
-- is in: position is the move's and status is the status operation's.
UPDATE projects.billing_milestones SET
    name = @name,
    description = @description,
    planned_date = @planned_date,
    amount = @amount,
    percent = @percent,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: UpdateMilestoneStatus :one
-- UpdateMilestoneStatus applies one status move. Every stamp is passed
-- explicitly rather than computed in SQL, because which of them a move sets
-- and which it clears is design §3.2's table, and that table is decided in Go
-- against the locked row: → ready stamps ready_at/by and ready → planned
-- clears them, → invoiced stamps invoiced_at/by and freezes invoiced_amount
-- while the undo clears those and the reference and the date with them.
--
-- ever_moved is set by every move and never unset: it is what tells a
-- milestone that came back to 'planned' from one that was never anything
-- else, and only the second may be deleted.
UPDATE projects.billing_milestones SET
    status = @status,
    ready_at = @ready_at,
    ready_by_user_id = @ready_by_user_id,
    invoiced_at = @invoiced_at,
    invoiced_by_user_id = @invoiced_by_user_id,
    invoice_reference = @invoice_reference,
    invoice_date = @invoice_date,
    invoiced_amount = @invoiced_amount,
    ever_moved = true,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteMilestone :execrows
-- DeleteMilestone removes one milestone. Only a milestone that is still
-- planned and has never moved reaches it (design §3.2) — anything that was
-- ever ready, invoiced or cancelled is cancelled rather than removed, so the
-- plan keeps the record. The row count is what decides the 404: two deletes
-- racing must not both answer 204.
DELETE FROM projects.billing_milestones WHERE id = @id;
