-- Absence (design S8, §3.5, delivery B): the absence types and the
-- absences. The cases arrive with their task. Every write of an absence runs
-- under its person's lock (withPersonLock); the type it names is then taken
-- FOR KEY SHARE, so a change widening the type's visibility (which holds it
-- FOR UPDATE) and the absence are judged one after the other.

-- name: ListAbsenceTypes :many
-- ListAbsenceTypes is the types in their order; with_hidden false leaves
-- out the hidden ones, the reserved absent among them.
SELECT * FROM staff.absence_types
WHERE @with_hidden::boolean OR NOT hidden
ORDER BY sort, id;

-- name: LockAbsenceType :one
-- LockAbsenceType holds one type for a change of its own, so the If-Match
-- revision is judged against the row the change then writes.
SELECT * FROM staff.absence_types WHERE id = @id FOR UPDATE;

-- name: NextAbsenceTypeSort :one
-- NextAbsenceTypeSort is the place after every type there is.
SELECT (coalesce(max(sort), 0) + 1)::integer AS next FROM staff.absence_types;

-- name: InsertAbsenceType :one
INSERT INTO staff.absence_types (
    key, name_en, name_nb, kind, unit, paid, draws_holiday, allows_percent, needs_decision,
    visibility, amelding_leave, hidden, sort, created_at, updated_at
) VALUES (
    @key, @name_en, @name_nb, @kind, @unit, @paid, @draws_holiday, @allows_percent, @needs_decision,
    @visibility, @amelding_leave, @hidden, @sort, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateAbsenceType :one
-- UpdateAbsenceType replaces a type's names, flags, visibility, mapping,
-- hidden and sort. Its key, kind and unit never change; a seed's flags are
-- the caller's to keep as they are.
UPDATE staff.absence_types SET
    name_en = @name_en,
    name_nb = @name_nb,
    paid = @paid,
    draws_holiday = @draws_holiday,
    allows_percent = @allows_percent,
    needs_decision = @needs_decision,
    visibility = @visibility,
    amelding_leave = @amelding_leave,
    hidden = @hidden,
    sort = @sort,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: AbsenceTypeInUse :one
-- AbsenceTypeInUse is whether any absence names the type. Asked under the
-- type's FOR UPDATE, which an absence's foreign key on it (KEY SHARE) waits
-- for, so no absence names it between the answer and the change.
SELECT EXISTS (SELECT 1 FROM staff.absences WHERE type_id = @id::bigint) AS in_use;

-- name: GetAbsenceType :one
SELECT * FROM staff.absence_types WHERE id = @id;

-- name: ShareAbsenceType :one
-- ShareAbsenceType reads the type an absence is about to name and holds it
-- FOR KEY SHARE until the transaction ends: a change of the type's own
-- (LockAbsenceType, FOR UPDATE) waits for it, so the type is not widened
-- from hr_only between this read and the absence's write. It is taken under
-- the person's lock.
SELECT * FROM staff.absence_types WHERE id = @id FOR KEY SHARE;

-- name: GetAbsence :one
-- GetAbsence is one absence with its type's kind and visibility.
SELECT sqlc.embed(a), t.kind, t.visibility, t.unit
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.id = @id;

-- name: LockAbsence :one
-- LockAbsence takes one absence after its person's lock, for a change of its
-- own.
SELECT * FROM staff.absences WHERE id = @id FOR NO KEY UPDATE;

-- name: OverlappingAbsences :many
-- OverlappingAbsences is the person's requested, approved and registered rows
-- touching first..last, but exclude — the rows overlap is judged against
-- (plan reading 17), read under the person's lock.
SELECT sqlc.embed(a), t.kind, t.unit, t.allows_percent
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.person_id = @person_id AND a.id <> @exclude::bigint
  AND a.status IN ('requested', 'approved', 'registered')
  AND a.first_day <= @last_day::date AND a.last_day >= @first_day::date;

-- name: InsertAbsence :one
INSERT INTO staff.absences (
    person_id, type_id, first_day, last_day, hours, percent, status, note, degree_percent,
    requested_by_user_id, created_at, updated_at
) VALUES (
    @person_id, @type_id, @first_day, @last_day, @hours, @percent, @status, @note, @degree_percent,
    @requested_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateAbsence :one
-- UpdateAbsence replaces an absence's type, days, hours, percent, degree,
-- note and status.
UPDATE staff.absences SET
    type_id = @type_id,
    first_day = @first_day,
    last_day = @last_day,
    hours = @hours,
    percent = @percent,
    degree_percent = @degree_percent,
    note = @note,
    status = @status,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SetAbsenceStatus :one
UPDATE staff.absences SET status = @status, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteAbsence :exec
DELETE FROM staff.absences WHERE id = @id;

-- name: CountAbsences :one
-- CountAbsences counts what ListAbsences pages through.
SELECT count(*) FROM staff.absences a
WHERE (sqlc.narg('person_id')::bigint IS NULL OR a.person_id = sqlc.narg('person_id')::bigint)
  AND (sqlc.narg('from_day')::date IS NULL OR a.last_day >= sqlc.narg('from_day')::date)
  AND (sqlc.narg('to_day')::date IS NULL OR a.first_day <= sqlc.narg('to_day')::date)
  AND (sqlc.narg('status')::text IS NULL OR a.status = sqlc.narg('status')::text)
  AND (@everyone::boolean OR a.person_id = ANY(@full_ids::bigint[])
       OR (a.person_id = ANY(@colleague_ids::bigint[]) AND a.status IN ('approved', 'registered')));

-- name: ListAbsences :many
-- ListAbsences is one page of the absences the caller may see, newest first:
-- everyone's (@everyone), every row of @full_ids' people, and the approved
-- and registered rows of @colleague_ids' — a colleague is shown that a
-- person is away, never a request or a refusal.
SELECT sqlc.embed(a), t.kind, t.visibility, t.unit
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE (sqlc.narg('person_id')::bigint IS NULL OR a.person_id = sqlc.narg('person_id')::bigint)
  AND (sqlc.narg('from_day')::date IS NULL OR a.last_day >= sqlc.narg('from_day')::date)
  AND (sqlc.narg('to_day')::date IS NULL OR a.first_day <= sqlc.narg('to_day')::date)
  AND (sqlc.narg('status')::text IS NULL OR a.status = sqlc.narg('status')::text)
  AND (@everyone::boolean OR a.person_id = ANY(@full_ids::bigint[])
       OR (a.person_id = ANY(@colleague_ids::bigint[]) AND a.status IN ('approved', 'registered')))
ORDER BY a.first_day DESC, a.id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: PeopleActiveOn :many
-- PeopleActiveOn is everyone with an employment in force on @today: the
-- people staff:view sees (plan reading 15).
SELECT DISTINCT e.person_id FROM staff.employments e
WHERE e.start_date <= @today::date AND (e.end_date IS NULL OR e.end_date >= @today::date);

-- name: HolidayAbsences :many
-- HolidayAbsences is the person's day rows of the types that draw on the
-- holiday account, in any status: what the account counts.
SELECT a.first_day, a.last_day, a.status, a.percent, a.degree_percent, t.kind
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.person_id = @person_id AND t.draws_holiday AND t.unit = 'days';
