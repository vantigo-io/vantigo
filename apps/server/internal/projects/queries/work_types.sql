-- name: ListWorkTypes :many
-- ListWorkTypes is every work type of one project (work types design D1),
-- deactivated ones included — an entry that picked one still names it — the
-- active ones first, each half by name without regard to case. The
-- directory's DirectoryWorkTypes answers in the same order, so the Billing
-- tab and Time's picker never disagree about it.
SELECT * FROM projects.work_types
WHERE project_id = @project_id
ORDER BY active DESC, lower(name), id;

-- name: InsertWorkType :one
-- InsertWorkType adds one type to a project. created_at and updated_at are
-- the same instant, supplied by the caller from Deps.Clock(); active takes the
-- column default (true). A name the project already has, in any case, raises
-- 23505 on ux_work_types_project_id_name, which the handler answers 409.
INSERT INTO projects.work_types (
    project_id, name, bill_multiplier_percent, cost_multiplier_percent, created_at, updated_at
) VALUES (
    @project_id, @name, @bill_multiplier_percent, @cost_multiplier_percent, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: LockWorkType :one
-- LockWorkType is one type of one project, locked for the rest of the
-- change's transaction, so the timeline names the fields that moved against
-- the row nobody else can move meanwhile. Scoping by project_id is the 404
-- for a type that exists on somebody else's project.
SELECT * FROM projects.work_types
WHERE id = @id AND project_id = @project_id
FOR UPDATE;

-- name: UpdateWorkType :one
-- UpdateWorkType applies one change under that lock. `active` is nullable
-- because a PUT may leave it out, which leaves the type as it stands —
-- coalesce, not a second statement, the billing line's UpdateBillingLine
-- shape.
UPDATE projects.work_types SET
    name = @name,
    bill_multiplier_percent = @bill_multiplier_percent,
    cost_multiplier_percent = @cost_multiplier_percent,
    active = coalesce(sqlc.narg(active)::boolean, active),
    updated_at = @now::timestamptz
WHERE id = @id AND project_id = @project_id
RETURNING *;
