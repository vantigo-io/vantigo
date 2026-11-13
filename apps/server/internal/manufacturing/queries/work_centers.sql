-- Work centers (manufacturing design M8, M9): a machine, a cell or a team at
-- one Inventory site, with its weekly pattern and its closures. Every write of
-- one locks its row first (GetWorkCenterForUpdate).

-- name: ListWorkCenters :many
-- ListWorkCenters is the work centers in one status (every status when
-- status is null), at one site when site_id is set, whose code or name
-- contains q when it is set, by code.
SELECT * FROM manufacturing.work_centers
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(site_id)::bigint IS NULL OR site_id = sqlc.narg(site_id)::bigint)
  AND (sqlc.narg(q)::text IS NULL
       OR code ILIKE '%' || sqlc.narg(q)::text || '%'
       OR name ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY code;

-- name: GetWorkCenter :one
SELECT * FROM manufacturing.work_centers WHERE id = @id;

-- name: GetWorkCenterForUpdate :one
-- GetWorkCenterForUpdate takes the work center's row FOR UPDATE: the first
-- of its own rows every write of it locks.
SELECT * FROM manufacturing.work_centers WHERE id = @id FOR UPDATE;

-- name: WorkCentersByIDs :many
-- WorkCentersByIDs is the work centers a BoM's operations name, by id.
SELECT * FROM manufacturing.work_centers WHERE id = ANY(@ids::bigint[]) ORDER BY id;

-- name: WorkCenterCodeTaken :one
-- WorkCenterCodeTaken reports whether another work center has the code
-- (work_center_code_taken); id 0 asks about a new one.
SELECT EXISTS (SELECT 1 FROM manufacturing.work_centers WHERE code = @code AND id <> @id);

-- name: InsertWorkCenter :one
INSERT INTO manufacturing.work_centers (code, name, site_id, capacity, time_efficiency, setup_minutes, cleanup_minutes,
                                        machine_cost_per_hour, labour_cost_per_hour, pattern, created_at, updated_at)
VALUES (@code, @name, @site_id, @capacity, @time_efficiency, @setup_minutes, @cleanup_minutes,
        sqlc.narg(machine_cost_per_hour), sqlc.narg(labour_cost_per_hour), @pattern, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateWorkCenter :one
-- UpdateWorkCenter replaces a work center's fields — the cost rates as the
-- caller resolved them (an absent rate is the stored one) — and moves its
-- revision on. The working state and the status are not an edit's.
UPDATE manufacturing.work_centers
SET code = @code, name = @name, site_id = @site_id, capacity = @capacity, time_efficiency = @time_efficiency,
    setup_minutes = @setup_minutes, cleanup_minutes = @cleanup_minutes,
    machine_cost_per_hour = sqlc.narg(machine_cost_per_hour), labour_cost_per_hour = sqlc.narg(labour_cost_per_hour),
    pattern = @pattern, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SetWorkCenterStatus :one
-- SetWorkCenterStatus archives or reactivates a work center and moves its
-- revision on.
UPDATE manufacturing.work_centers
SET status = @status, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteWorkCenter :execrows
-- DeleteWorkCenter deletes a work center no operation names; its closures go
-- with it.
DELETE FROM manufacturing.work_centers WHERE id = @id;

-- name: CountOperationsOfWorkCenter :one
-- CountOperationsOfWorkCenter is how many BoM operations, work orders and
-- time logs name the work center, of any status (work_center_in_use): a
-- work order or a log keeps it as an operation does.
SELECT ((SELECT count(*) FROM manufacturing.bom_operations o WHERE o.work_center_id = @work_center_id::bigint)
      + (SELECT count(*) FROM manufacturing.work_orders w WHERE w.work_center_id = @work_center_id::bigint)
      + (SELECT count(*) FROM manufacturing.time_logs l WHERE l.work_center_id = @work_center_id::bigint)
      + (SELECT count(*) FROM manufacturing.work_center_group_members m WHERE m.work_center_id = @work_center_id::bigint))::int;

-- name: CountActiveBomsAtWorkCenter :one
-- CountActiveBomsAtWorkCenter is how many active BoMs have an operation at
-- the work center: while any does, its site does not change (M12a).
SELECT count(DISTINCT b.id)::int FROM manufacturing.boms b
JOIN manufacturing.bom_operations o ON o.bom_id = b.id
WHERE o.work_center_id = @work_center_id AND b.status = 'active';

-- name: CountGroupsOfWorkCenter :one
-- CountGroupsOfWorkCenter is how many groups have the work center as a
-- member: while any does, its site does not change (M117).
SELECT count(*)::int FROM manufacturing.work_center_group_members WHERE work_center_id = @work_center_id;

-- name: Stats :one
-- Stats is the overview's counts: active work centers, the active ones
-- blocked, and active BoMs.
SELECT (SELECT count(*) FROM manufacturing.work_centers WHERE status = 'active')::int AS active_work_centers,
       (SELECT count(*) FROM manufacturing.work_centers WHERE status = 'active' AND working_state = 'blocked')::int AS blocked_work_centers,
       (SELECT count(*) FROM manufacturing.boms WHERE status = 'active')::int AS active_boms;
