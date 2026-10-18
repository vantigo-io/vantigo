-- The dashboard's reads (design S20): what GET /stats and GET
-- /stats/attention judge, each read inside one snapshot with the employments,
-- the terms and the manager graph, and judged in Go on the caller's today.

-- name: StatsPeople :many
-- StatsPeople is everyone, with what the figures and the reassessment read:
-- the name (class C, which survives the erase), and the erase and its
-- reassessment.
SELECT id, display_name, erased_at, reassessed_at FROM staff.people ORDER BY id;

-- name: SignedContractEmployments :many
-- SignedContractEmployments is the employments a signed document of the
-- contract category is filed for — a document with no employment attributes
-- its signature to none.
SELECT DISTINCT d.employment_id::bigint AS employment_id
FROM staff.documents d
JOIN staff.document_categories c ON c.id = d.category_id
WHERE c.key = 'contract' AND d.signed_at IS NOT NULL AND d.employment_id IS NOT NULL;

-- name: FailingDeletions :one
-- FailingDeletions is the ledger rows the store has refused at least once
-- that were queued before @before — the attention item's rows — and when the
-- oldest of them was queued (@before when there is none).
SELECT
    count(*)::bigint AS failing,
    coalesce(min(created_at), @before::timestamptz)::timestamptz AS oldest
FROM staff.object_deletions
WHERE attempts >= 1 AND created_at < @before::timestamptz;

-- name: AbsencesBetween :many
-- AbsencesBetween is every approved or registered absence touching
-- @from_day..@to_day with what its type says of it: what the payroll file's
-- totals and leave rows, the sick-leave rate and the holiday items count
-- (delivery B Task 6). A request, a refusal and a cancel count nothing.
SELECT a.id, a.person_id, a.type_id, a.first_day, a.last_day, a.hours, a.percent, a.degree_percent, a.status,
       t.kind, t.unit, t.allows_percent, t.amelding_leave
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.status IN ('approved', 'registered')
  AND a.first_day <= @to_day::date AND a.last_day >= @from_day::date
ORDER BY a.person_id, a.first_day, a.id;

-- name: CountOpenSickCases :one
-- CountOpenSickCases is the cases open on @today: not closed by HR, and
-- whose last day is at most sixteen days before it (reading 5).
SELECT count(*)::int AS open FROM staff.sick_cases
WHERE closed_at IS NULL AND last_day + 16 >= @today::date;

-- name: OpenCaseMilestones :many
-- OpenCaseMilestones is the milestones of the cases open on @today that are
-- neither done nor waived and have a date, with their case's last day and
-- person: the attention item sickMilestoneDue judges them.
SELECT m.case_id, m.kind, m.due_date, c.person_id, c.last_day, p.display_name
FROM staff.sick_case_milestones m
JOIN staff.sick_cases c ON c.id = m.case_id
JOIN staff.people p ON p.id = c.person_id
WHERE c.closed_at IS NULL AND c.last_day + 16 >= @today::date
  AND m.due_date IS NOT NULL AND m.done_at IS NULL AND NOT m.waived
ORDER BY m.due_date, m.case_id, m.kind;

-- name: StalledHROnlyRequests :one
-- StalledHROnlyRequests is the requests of an HR-only type made before
-- @before and still undecided, and when the oldest was made (@before when
-- there is none): the attention item hrOnlyAwaitingDecision. The person
-- @sole_self_id names — the caller, when they are the sole active holder of
-- staff:personal-data and so decide their own (delivery C) — is left out:
-- their own request is one they may decide, not one stalled.
SELECT count(*)::int AS waiting, coalesce(min(a.created_at), @before::timestamptz)::timestamptz AS oldest
FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
WHERE a.status = 'requested' AND t.visibility <> 'absent' AND a.created_at < @before::timestamptz
  AND a.person_id IS DISTINCT FROM sqlc.narg('sole_self_id')::bigint;
