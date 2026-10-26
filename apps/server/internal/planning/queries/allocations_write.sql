-- The allocations' list and writes (design S1–S7, S23): the write lane's own
-- queries beside the shared reads of allocations.sql. Every write runs under
-- the locks of locks.go, after the row it changes was read FOR UPDATE.

-- name: ListAllocationsFiltered :many
-- ListAllocationsFiltered is GET /allocations: the rows of one person, one
-- project, one status and touching @from_date..@to_date, each filter NULL for
-- any, by from date and id, at most @max_rows.
SELECT * FROM planning.allocations
WHERE (sqlc.narg(person_id)::bigint IS NULL OR person_id = sqlc.narg(person_id)::bigint)
  AND (sqlc.narg(project_id)::integer IS NULL OR project_id = sqlc.narg(project_id)::integer)
  AND (sqlc.narg(from_date)::date IS NULL OR to_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR from_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY from_date, id
LIMIT @max_rows::integer;

-- name: InsertAllocation :one
-- InsertAllocation writes a new row at revision 1, stamped by the caller.
INSERT INTO planning.allocations (
    person_id, project_id, from_date, to_date, load_unit, load, status, title, note,
    confirmed_at, created_at, created_by_user_id, updated_at, updated_by_user_id
) VALUES (
    @person_id, @project_id, @from_date, @to_date, @load_unit, @load, @status, sqlc.narg(title), sqlc.narg(note),
    sqlc.narg(confirmed_at)::timestamptz, @now::timestamptz, sqlc.narg(user_id)::uuid, @now::timestamptz, sqlc.narg(user_id)::uuid
)
RETURNING *;

-- name: UpdateAllocation :one
-- UpdateAllocation replaces a row's fields whole, as the caller decided them
-- over the row it locked, and moves the revision on.
UPDATE planning.allocations SET
    person_id = @person_id,
    project_id = @project_id,
    from_date = @from_date,
    to_date = @to_date,
    load_unit = @load_unit,
    load = @load,
    status = @status,
    title = sqlc.narg(title),
    note = sqlc.narg(note),
    confirmed_at = sqlc.narg(confirmed_at)::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = sqlc.narg(user_id)::uuid
WHERE id = @id
RETURNING *;

-- name: DeleteAllocation :exec
-- DeleteAllocation removes a row the caller locked. Its events stay.
DELETE FROM planning.allocations WHERE id = @id;
