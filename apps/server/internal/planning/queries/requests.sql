-- The requests (design S16, §3.4): a project's need for a person not yet
-- named, its competence requirements, and the allocation that fills it. A
-- request "touches" a window as an allocation does: from_date on or before
-- the window's last day and to_date on or after its first. Every write runs
-- under the project's lock of locks.go — a fill and the reopen on a delete
-- under the person's too — after the request it changes was read FOR UPDATE.

-- name: GetRequest :one
-- GetRequest reads one request. No row is an unknown id.
SELECT * FROM planning.requests WHERE id = @id;

-- name: LockRequest :one
-- LockRequest reads one request and holds it for the rest of the
-- transaction, after the locks of locks.go, so a write judges its revision
-- and its status against the row it then writes.
SELECT * FROM planning.requests WHERE id = @id FOR UPDATE;

-- name: ListRequestsFiltered :many
-- ListRequestsFiltered is GET /requests: the requests of one project, one
-- status and touching @from_date..@to_date, each filter NULL for any, by
-- from date and id, at most @max_rows.
SELECT * FROM planning.requests
WHERE (sqlc.narg(project_id)::integer IS NULL OR project_id = sqlc.narg(project_id)::integer)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR to_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR from_date <= sqlc.narg(to_date)::date)
ORDER BY from_date, id
LIMIT @max_rows::integer;

-- name: RequestsOfProjects :many
-- RequestsOfProjects is the requests of the projects in @project_ids whose
-- status is one of @statuses, by project, from date and id: the intake's
-- open and filled requests of the planned projects (S18).
SELECT * FROM planning.requests
WHERE project_id = ANY(@project_ids::integer[]) AND status = ANY(@statuses::text[])
ORDER BY project_id, from_date, id;

-- name: RequestsOfProjectInWindow :many
-- RequestsOfProjectInWindow is one project's open and filled requests
-- touching @from_date..@to_date, by from date and id: a project's plan, its
-- demand and its requests (S11). A withdrawn request is not the plan.
SELECT * FROM planning.requests
WHERE project_id = @project_id AND status IN ('open', 'filled')
  AND from_date <= @to_date::date AND to_date >= @from_date::date
ORDER BY from_date, id;

-- name: RequirementsOfRequests :many
-- RequirementsOfRequests is the requirements of the requests in
-- @request_ids, by request and id — the order they were written in.
SELECT * FROM planning.request_requirements
WHERE request_id = ANY(@request_ids::bigint[])
ORDER BY request_id, id;

-- name: AllocationsFillingRequests :many
-- AllocationsFillingRequests is the allocations that name a request of
-- @request_ids, one at most a request (ux_allocations_request): who fills it.
SELECT * FROM planning.allocations
WHERE request_id = ANY(@request_ids::bigint[])
ORDER BY request_id;

-- name: InsertRequest :one
-- InsertRequest writes a new open request at revision 1, stamped by the
-- caller.
INSERT INTO planning.requests (
    project_id, title, from_date, to_date, hours_per_week, status, note,
    created_at, created_by_user_id, updated_at, updated_by_user_id
) VALUES (
    @project_id, @title, @from_date, @to_date, @hours_per_week, 'open', sqlc.narg(note),
    @now::timestamptz, sqlc.narg(user_id)::uuid, @now::timestamptz, sqlc.narg(user_id)::uuid
)
RETURNING *;

-- name: UpdateRequest :one
-- UpdateRequest replaces an open request's fields whole, as the caller
-- decided them over the row it locked, and moves the revision on. The
-- status is SetRequestStatus's.
UPDATE planning.requests SET
    project_id = @project_id,
    title = @title,
    from_date = @from_date,
    to_date = @to_date,
    hours_per_week = @hours_per_week,
    note = sqlc.narg(note),
    revision = revision + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = sqlc.narg(user_id)::uuid
WHERE id = @id
RETURNING *;

-- name: SetRequestStatus :one
-- SetRequestStatus moves a request the caller locked to @status — filled by
-- a fill, open again when its filling row is deleted, withdrawn — and its
-- revision on.
UPDATE planning.requests SET
    status = @status,
    revision = revision + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = sqlc.narg(user_id)::uuid
WHERE id = @id
RETURNING *;

-- name: InsertRequirement :exec
-- InsertRequirement writes one requirement of a request.
INSERT INTO planning.request_requirements (request_id, competence_id, min_level)
VALUES (@request_id, @competence_id, sqlc.narg(min_level));

-- name: DeleteRequirements :exec
-- DeleteRequirements removes every requirement of a request, before a
-- replace writes the new ones.
DELETE FROM planning.request_requirements WHERE request_id = @request_id;

-- The reads of the candidates, the intake, the attention item and the stats
-- (design S17, S18, S22): what they read of the module's own rows beyond the
-- window loadWindow reads. None runs inside a transaction.

-- name: ConfirmedAllocationsCovering :many
-- ConfirmedAllocationsCovering is the confirmed allocations of the people in
-- @person_ids covering @day, by person, from date and id: a candidate's
-- current project is the first of theirs (S17's fairness signal).
SELECT * FROM planning.allocations
WHERE person_id = ANY(@person_ids::bigint[]) AND status = 'confirmed'
  AND from_date <= @day::date AND to_date >= @day::date
ORDER BY person_id, from_date, id;

-- name: AllocationsOfProjects :many
-- AllocationsOfProjects is every allocation of the projects in
-- @project_ids, by project, from date and id: the intake's tentative and
-- confirmed hours a week per project (S18), over all its rows.
SELECT * FROM planning.allocations
WHERE project_id = ANY(@project_ids::integer[])
ORDER BY project_id, from_date, id;

-- name: OpenRequestsStartingBy :many
-- OpenRequestsStartingBy is the open requests whose first day is on or
-- before @last_day — those within the lead and those already started — by
-- from date and id: the requestsOpen attention item (S22).
SELECT * FROM planning.requests
WHERE status = 'open' AND from_date <= @last_day::date
ORDER BY from_date, id;

-- name: CountOpenRequests :one
-- CountOpenRequests is how many requests are open: the stats' openRequests
-- (S22).
SELECT count(*) FROM planning.requests WHERE status = 'open';
