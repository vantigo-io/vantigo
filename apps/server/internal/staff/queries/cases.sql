-- Sickness (design S10, S11, §3.5, delivery B Task 5): the sick-leave cases
-- and their milestones, egenmelding's counters and the care days. A case is
-- derived from its person's sick rows and stored under the person's lock
-- (design §4 Locking: the person, their absences, then their cases and
-- milestones, then their documents); every query here that writes runs in
-- that lock.

-- name: PersonSickRows :many
-- PersonSickRows is the person's registered egenmelding and sykmelding rows,
-- oldest first: what the cases are derived from.
SELECT a.id, a.first_day, a.last_day, a.case_id, a.type_id, t.kind
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.person_id = @person_id AND t.kind IN ('sick_self', 'sick_certified') AND a.status = 'registered'
ORDER BY a.first_day, a.id;

-- name: LockPersonCases :many
-- LockPersonCases holds the person's cases, oldest first, after their
-- absences.
SELECT * FROM staff.sick_cases WHERE person_id = @person_id ORDER BY first_day, id FOR NO KEY UPDATE;

-- name: InsertSickCase :one
INSERT INTO staff.sick_cases (person_id, first_day, last_day, created_at, updated_at)
VALUES (@person_id, @first_day, @last_day, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: SetSickCaseDays :exec
-- SetSickCaseDays stores a case's days as its rows say, and clears HR's
-- close when a row joined it after (the case reopened).
UPDATE staff.sick_cases SET
    first_day = @first_day,
    last_day = @last_day,
    closed_at = CASE WHEN @reopen::boolean THEN NULL ELSE closed_at END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: SetAbsenceCase :exec
-- SetAbsenceCase names the case a sick row belongs to: derived, so the row's
-- revision stays.
UPDATE staff.absences SET case_id = @case_id WHERE id = @id;

-- name: MoveCaseDocuments :exec
-- MoveCaseDocuments files a case's documents under another, or none.
UPDATE staff.documents SET case_id = sqlc.narg('to_case')::bigint WHERE case_id = @from_case;

-- name: DeleteCaseMilestones :exec
DELETE FROM staff.sick_case_milestones WHERE case_id = @case_id;

-- name: DeleteSickCase :exec
DELETE FROM staff.sick_cases WHERE id = @id;

-- name: SetMilestoneDue :exec
-- SetMilestoneDue stores a milestone's date, creating it when missing; its
-- done day, waiver and note are never touched.
INSERT INTO staff.sick_case_milestones (case_id, kind, due_date, created_at, updated_at)
VALUES (@case_id, @kind, @due_date, @now::timestamptz, @now::timestamptz)
ON CONFLICT (case_id, kind) DO UPDATE SET due_date = EXCLUDED.due_date, updated_at = EXCLUDED.updated_at
WHERE staff.sick_case_milestones.due_date IS DISTINCT FROM EXCLUDED.due_date;

-- name: GetSickCase :one
SELECT * FROM staff.sick_cases WHERE id = @id;

-- name: LockSickCase :one
-- LockSickCase holds one case for a change of its own, after the person.
SELECT * FROM staff.sick_cases WHERE id = @id FOR NO KEY UPDATE;

-- name: CaseMilestones :many
-- CaseMilestones is the milestones of @case_ids' cases.
SELECT * FROM staff.sick_case_milestones WHERE case_id = ANY(@case_ids::bigint[]) ORDER BY case_id, id;

-- name: LockMilestone :one
SELECT * FROM staff.sick_case_milestones WHERE case_id = @case_id AND kind = @kind FOR NO KEY UPDATE;

-- name: CaseTypeIDs :many
-- CaseTypeIDs is the types of a case's rows, which its events carry.
SELECT DISTINCT type_id FROM staff.absences WHERE case_id = @case_id ORDER BY type_id;

-- name: UpdateCaseNotes :one
UPDATE staff.sick_cases SET
    accommodation_note = @accommodation_note,
    max_benefit_estimate = @max_benefit_estimate,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: CloseSickCase :one
UPDATE staff.sick_cases SET closed_at = @now::timestamptz, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: MarkMilestoneDone :one
UPDATE staff.sick_case_milestones SET
    done_at = @done_at::timestamptz,
    done_by_user_id = @by_user_id,
    waived = false,
    waived_reason = NULL,
    waived_by_user_id = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE case_id = @case_id AND kind = @kind
RETURNING *;

-- name: WaiveMilestone :one
UPDATE staff.sick_case_milestones SET
    waived = true,
    waived_reason = @reason,
    waived_by_user_id = @by_user_id,
    done_at = NULL,
    done_by_user_id = NULL,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE case_id = @case_id AND kind = @kind
RETURNING *;

-- name: SetMilestoneNote :one
UPDATE staff.sick_case_milestones SET note = @note, revision = revision + 1, updated_at = @now::timestamptz
WHERE case_id = @case_id AND kind = @kind
RETURNING *;

-- name: CountCases :one
-- CountCases counts what ListCases pages through.
SELECT count(*) FROM staff.sick_cases c
WHERE (sqlc.narg('person_id')::bigint IS NULL OR c.person_id = sqlc.narg('person_id')::bigint)
  AND (@everyone::boolean OR c.person_id = ANY(@person_ids::bigint[]))
  AND (@status::text = 'all'
       OR (@status::text = 'open') = (c.closed_at IS NULL AND c.last_day + 16 >= @today::date));

-- name: ListCases :many
-- ListCases is one page of the cases the caller reads, newest first:
-- everyone's (@everyone) or @person_ids' people's. Open is a case HR has not
-- closed whose last day is at most sixteen days before @today (the join's
-- bound, plan reading 5).
SELECT * FROM staff.sick_cases c
WHERE (sqlc.narg('person_id')::bigint IS NULL OR c.person_id = sqlc.narg('person_id')::bigint)
  AND (@everyone::boolean OR c.person_id = ANY(@person_ids::bigint[]))
  AND (@status::text = 'all'
       OR (@status::text = 'open') = (c.closed_at IS NULL AND c.last_day + 16 >= @today::date))
ORDER BY c.first_day DESC, c.id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: PersonCaseExists :one
-- PersonCaseExists is whether case_id is one of the person's.
SELECT EXISTS (SELECT 1 FROM staff.sick_cases WHERE id = @case_id AND person_id = @person_id) AS found;

-- name: SetDocumentCase :exec
-- SetDocumentCase files a document under a case, or none.
UPDATE staff.documents SET case_id = sqlc.narg('case_id')::bigint WHERE id = @id;

-- name: SetSelfCertWithdrawal :exec
-- SetSelfCertWithdrawal records or lifts the withdrawal of a person's right
-- to egenmelding (ftrl. § 8-27).
UPDATE staff.people SET self_cert_withdrawn_until = @until, self_cert_withdrawn_reason = @reason, updated_at = @now::timestamptz
WHERE id = @id;

-- name: ChildSickRows :many
-- ChildSickRows is the person's registered child-sick rows touching
-- @from_day..@to_day: the care days used.
SELECT a.first_day, a.last_day
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.person_id = @person_id AND t.kind = 'child_sick' AND a.status = 'registered'
  AND a.first_day <= @to_day::date AND a.last_day >= @from_day::date;

-- name: InsertSplitAbsence :one
-- InsertSplitAbsence is the part of a row after a sykmelding that split it:
-- a copy of the row from @first_day, its decision kept.
INSERT INTO staff.absences (
    person_id, type_id, first_day, last_day, hours, percent, status, note, case_id, degree_percent,
    requested_by_user_id, decided_by_user_id, decided_at, decision_note, self_approved, created_at, updated_at
)
SELECT src.person_id, src.type_id, @first_day::date, src.last_day, src.hours, src.percent, src.status, src.note, src.case_id,
       src.degree_percent, src.requested_by_user_id, src.decided_by_user_id, src.decided_at, src.decision_note, src.self_approved,
       @now::timestamptz, @now::timestamptz
FROM staff.absences src WHERE src.id = @source::bigint
RETURNING *;

-- name: SetAbsenceDays :one
-- SetAbsenceDays moves a row's days, as a sykmelding over it trims it.
UPDATE staff.absences SET first_day = @first_day, last_day = @last_day, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: PersonSelfCertWithdrawal :one
-- PersonSelfCertWithdrawal is the withdrawal of the person's right to
-- egenmelding: the last day and the reason, null when none.
SELECT self_cert_withdrawn_until, self_cert_withdrawn_reason FROM staff.people WHERE id = @id;
