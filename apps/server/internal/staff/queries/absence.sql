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

-- name: AbsenceTypeSelfApprovedRunning :one
-- AbsenceTypeSelfApprovedRunning is whether an absence of the type that its
-- own person approved (self_approved) has not ended by @today. Asked under
-- the type's FOR UPDATE, as AbsenceTypeInUse is.
SELECT EXISTS (
    SELECT 1 FROM staff.absences
    WHERE type_id = @id::bigint AND self_approved AND last_day >= @today::date
) AS running;

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
SELECT sqlc.embed(a), t.kind, t.unit, t.allows_percent, t.visibility
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
-- note and status. A row whose new type is not sick (@sick false) leaves its
-- case and its degree in the same statement (whole-branch review), so the
-- case it leaves can be derived again without it.
UPDATE staff.absences SET
    type_id = @type_id,
    first_day = @first_day,
    last_day = @last_day,
    hours = @hours,
    percent = @percent,
    degree_percent = CASE WHEN @sick::boolean THEN sqlc.narg(degree_percent)::numeric END,
    case_id = CASE WHEN @sick::boolean THEN case_id END,
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
SELECT count(*) FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE (sqlc.narg('person_id')::bigint IS NULL OR a.person_id = sqlc.narg('person_id')::bigint)
  AND (sqlc.narg('from_day')::date IS NULL OR a.last_day >= sqlc.narg('from_day')::date)
  AND (sqlc.narg('to_day')::date IS NULL OR a.first_day <= sqlc.narg('to_day')::date)
  AND (sqlc.narg('status')::text IS NULL OR sqlc.narg('status')::text =
       CASE WHEN @neutral::boolean AND t.visibility <> 'absent' AND a.person_id IS DISTINCT FROM sqlc.narg('self_id')::bigint
                 AND a.status = 'registered' THEN 'approved' ELSE a.status END)
  AND (@everyone::boolean OR a.person_id = ANY(@full_ids::bigint[]))
  AND NOT (@neutral::boolean AND t.visibility <> 'absent' AND a.person_id IS DISTINCT FROM sqlc.narg('self_id')::bigint
           AND (a.status NOT IN ('approved', 'registered') OR t.unit = 'hours'));

-- name: ListAbsences :many
-- ListAbsences is one page of the absences the caller may see as rows,
-- newest first: everyone's (@everyone) or every row of @full_ids' people. A
-- colleague's people are read as spans instead (ColleagueAbsenceDays). With
-- @neutral — a caller
-- who reads an HR-only type as leave, on every row but their own
-- (@self_id) — a status filter matches such a row by the status it is shown
-- as, a registration as an approval (leak 2), and an HR-only row that is not
-- approved or registered — a request, a refusal, a cancel — is not listed
-- at all: to its leave-reader every HR-only row is an approved leave they
-- never saw asked for (review H4, interim option b) — and an HR-only hour
-- row never (whole-branch review).
SELECT sqlc.embed(a), t.kind, t.visibility, t.unit
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE (sqlc.narg('person_id')::bigint IS NULL OR a.person_id = sqlc.narg('person_id')::bigint)
  AND (sqlc.narg('from_day')::date IS NULL OR a.last_day >= sqlc.narg('from_day')::date)
  AND (sqlc.narg('to_day')::date IS NULL OR a.first_day <= sqlc.narg('to_day')::date)
  AND (sqlc.narg('status')::text IS NULL OR sqlc.narg('status')::text =
       CASE WHEN @neutral::boolean AND t.visibility <> 'absent' AND a.person_id IS DISTINCT FROM sqlc.narg('self_id')::bigint
                 AND a.status = 'registered' THEN 'approved' ELSE a.status END)
  AND (@everyone::boolean OR a.person_id = ANY(@full_ids::bigint[]))
  AND NOT (@neutral::boolean AND t.visibility <> 'absent' AND a.person_id IS DISTINCT FROM sqlc.narg('self_id')::bigint
           AND (a.status NOT IN ('approved', 'registered') OR t.unit = 'hours'))
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

-- name: AbsentTypeID :one
-- AbsentTypeID is the reserved type the health erase writes in place of a
-- sick or child-sick one (plan reading 12).
SELECT id FROM staff.absence_types WHERE key = 'absent';

-- name: DecideAbsence :one
-- DecideAbsence records a decision on a request (design S12): its status,
-- who decided it and when, the decider's note, and whether the person
-- approved their own. Taken under the person's lock, after LockAbsence.
UPDATE staff.absences SET
    status = @status,
    decided_by_user_id = @decided_by_user_id,
    decided_at = @now::timestamptz,
    decision_note = @decision_note,
    self_approved = @self_approved,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: RequestedAbsences :many
-- RequestedAbsences is the requests the caller decides, oldest first: the
-- approvals inbox (plan Task 4). A request of a plain type is
-- @person_ids' people's — everyone's with @everyone; one of an HR-only type
-- is decided by staff:personal-data alone (@personal_data, review H4). Never
-- the caller's own (@self_id), but a plain one when they approve their own
-- (@self_approves, B's by-right self-approver) and an HR-only one when they
-- are the sole active holder of staff:personal-data (@sole_personal_data,
-- delivery C). The partial index on requested rows serves it.
SELECT sqlc.embed(a), t.kind, t.visibility, t.unit
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.status = 'requested'
  AND ((t.visibility = 'absent' AND (@everyone::boolean OR a.person_id = ANY(@person_ids::bigint[])))
       OR (t.visibility <> 'absent' AND @personal_data::boolean))
  AND (a.person_id IS DISTINCT FROM sqlc.narg('self_id')::bigint
       OR (t.visibility = 'absent' AND @self_approves::boolean)
       OR (t.visibility <> 'absent' AND @sole_personal_data::boolean))
ORDER BY a.created_at, a.id
LIMIT @page_size OFFSET @page_offset;

-- name: CountRequestedAbsences :one
-- CountRequestedAbsences counts what RequestedAbsences pages through.
SELECT count(*) FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.status = 'requested'
  AND ((t.visibility = 'absent' AND (@everyone::boolean OR a.person_id = ANY(@person_ids::bigint[])))
       OR (t.visibility <> 'absent' AND @personal_data::boolean))
  AND (a.person_id IS DISTINCT FROM sqlc.narg('self_id')::bigint
       OR (t.visibility = 'absent' AND @self_approves::boolean)
       OR (t.visibility <> 'absent' AND @sole_personal_data::boolean));

-- name: ColleagueAbsenceDays :many
-- ColleagueAbsenceDays is the approved and registered day rows of
-- @person_ids' people touching the optional range, by person and first day:
-- what a colleague reads, coalesced into spans (whole-branch review) — never
-- a row id, a request or an hour row.
SELECT a.person_id, a.first_day, a.last_day
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.person_id = ANY(@person_ids::bigint[])
  AND a.status IN ('approved', 'registered') AND t.unit = 'days'
  AND (sqlc.narg('from_day')::date IS NULL OR a.last_day >= sqlc.narg('from_day')::date)
  AND (sqlc.narg('to_day')::date IS NULL OR a.first_day <= sqlc.narg('to_day')::date)
ORDER BY a.person_id, a.first_day, a.last_day;

-- name: CalendarAbsences :many
-- CalendarAbsences is the requested, approved and registered rows of
-- @person_ids' people touching @from_day..@to_day: the calendar's bars.
SELECT sqlc.embed(a), t.kind, t.visibility, t.unit
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.person_id = ANY(@person_ids::bigint[])
  AND a.status IN ('requested', 'approved', 'registered')
  AND a.first_day <= @to_day::date AND a.last_day >= @from_day::date
ORDER BY a.person_id, a.first_day, a.id;

-- name: PeopleEmployedBetween :many
-- PeopleEmployedBetween is everyone with an employment touching
-- @from_day..@to_day: the calendar's people.
SELECT DISTINCT e.person_id FROM staff.employments e
WHERE e.start_date <= @to_day::date AND (e.end_date IS NULL OR e.end_date >= @from_day::date);

-- name: SelfApprovedAbsences :many
-- SelfApprovedAbsences is every approved row its person approved
-- themselves that has not ended by @today: the attention item selfApproved.
-- Only a type of visibility absent: self-approval never reaches an HR-only
-- type, and the item, read by staff:manage without staff:personal-data,
-- must never name one (Task 6).
SELECT a.id, a.person_id, a.decided_at, p.display_name
FROM staff.absences a
JOIN staff.absence_types t ON t.id = a.type_id AND t.visibility = 'absent'
JOIN staff.people p ON p.id = a.person_id
WHERE a.self_approved AND a.status = 'approved' AND a.last_day >= @today::date
ORDER BY a.decided_at, a.id;

-- name: HROnlySelfApprovedAbsences :many
-- HROnlySelfApprovedAbsences is every approved row of an HR-only type its
-- person approved as the sole active holder of staff:personal-data that has
-- not ended by @today: the attention item hrOnlySelfApproved, read by
-- staff:personal-data alone (delivery C). SelfApprovedAbsences keeps
-- leaving these out for selfApproved, which staff:manage reads.
SELECT a.id, a.person_id, a.decided_at, p.display_name
FROM staff.absences a
JOIN staff.absence_types t ON t.id = a.type_id AND t.visibility <> 'absent'
JOIN staff.people p ON p.id = a.person_id
WHERE a.self_approved AND a.status = 'approved' AND a.last_day >= @today::date
ORDER BY a.decided_at, a.id;

-- name: PersonAbsences :many
-- PersonAbsences is every absence of the person in any status, newest
-- first, with its type's kind, visibility and unit: the personal-data file's
-- (design S16).
SELECT sqlc.embed(a), t.kind, t.visibility, t.unit
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.person_id = @person_id
ORDER BY a.first_day DESC, a.id DESC;

-- name: PeopleAwayOn :many
-- PeopleAwayOn is which of @person_ids — one page of the directory — have an
-- approved or registered absence of whole or part days covering @today, of
-- any type: People's "on leave today" (design §7, plan M3), the rows GET
-- /stats counts. Scoped to the page, by the (person_id, first_day) index.
SELECT DISTINCT a.person_id
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.person_id = ANY(@person_ids::bigint[])
  AND a.status IN ('approved', 'registered')
  AND a.first_day <= @today::date AND a.last_day >= @today::date
  AND t.unit <> 'hours'
ORDER BY a.person_id;
