-- The allocations (design S1–S5, §3.2): the reads every view and the write
-- lane share. A row "touches" a window when its period overlaps it:
-- from_date on or before the window's last day and to_date on or after its
-- first. The writes are the write lane's own file.

-- name: GetAllocation :one
-- GetAllocation reads one allocation. No row is an unknown id.
SELECT * FROM planning.allocations WHERE id = @id;

-- name: LockAllocation :one
-- LockAllocation reads one allocation and holds it for the rest of the
-- transaction, after the locks of locks.go, so a change judges its If-Match
-- revision against the row it then writes.
SELECT * FROM planning.allocations WHERE id = @id FOR UPDATE;

-- name: AllocationsInWindow :many
-- AllocationsInWindow is every allocation touching @from_date..@to_date, of
-- the people in @person_ids when given (NULL is everyone), by person, from
-- date and id: the window loadWindow reads for the board, the timeline, the
-- unbooked list and the stats.
SELECT * FROM planning.allocations
WHERE from_date <= @to_date::date AND to_date >= @from_date::date
  AND (sqlc.narg(person_ids)::bigint[] IS NULL OR person_id = ANY(sqlc.narg(person_ids)::bigint[]))
ORDER BY person_id, from_date, id;

-- name: ProjectAllocationsInWindow :many
-- ProjectAllocationsInWindow is every allocation of one project touching
-- @from_date..@to_date, by from date, person and id: a project's plan.
SELECT * FROM planning.allocations
WHERE project_id = @project_id
  AND from_date <= @to_date::date AND to_date >= @from_date::date
ORDER BY from_date, person_id, id;

-- name: PersonConfirmedAllocationsInWindow :many
-- PersonConfirmedAllocationsInWindow is one person's confirmed allocations
-- touching @from_date..@to_date, read under the person's lock: what the
-- overbooking judgement of S7 is made on.
SELECT * FROM planning.allocations
WHERE person_id = @person_id AND status = 'confirmed'
  AND from_date <= @to_date::date AND to_date >= @from_date::date
ORDER BY from_date, id;
