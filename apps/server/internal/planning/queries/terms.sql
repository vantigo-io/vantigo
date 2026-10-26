-- The terms of an assignment (design S14, §3.3) and what the matcher and the
-- non-compete read of them (S15, S17.3–5): the terms lane's own queries. The
-- writes run under the person's lock (locks.go), after the allocation and
-- its terms row were read FOR UPDATE.

-- name: TermsOfAllocations :many
-- TermsOfAllocations is the terms rows of the allocations of @ids, by
-- allocation id: what every response derives its dates from (S15). No lock.
SELECT * FROM planning.allocation_terms
WHERE allocation_id = ANY(@ids::bigint[])
ORDER BY allocation_id;

-- name: LockTerms :one
-- LockTerms reads one allocation's terms row and holds it for the rest of
-- the transaction, so a PUT judges its If-Match against the row it then
-- writes. No row is an allocation with no terms.
SELECT * FROM planning.allocation_terms WHERE allocation_id = @allocation_id FOR UPDATE;

-- name: InsertTerms :one
-- InsertTerms writes an allocation's first terms row at revision 1, stamped
-- by the caller.
INSERT INTO planning.allocation_terms (
    allocation_id, notice_count, notice_unit, notice_given_on, minimum_until,
    replacement_needs_approval, handover_days, non_compete_until, non_compete_customer_ids,
    reference, revision, created_at, created_by_user_id, updated_at, updated_by_user_id
) VALUES (
    @allocation_id, sqlc.narg(notice_count), sqlc.narg(notice_unit), sqlc.narg(notice_given_on)::date,
    sqlc.narg(minimum_until)::date, @replacement_needs_approval, sqlc.narg(handover_days),
    sqlc.narg(non_compete_until)::date, @non_compete_customer_ids::integer[], sqlc.narg(reference),
    1, @now::timestamptz, sqlc.narg(user_id)::uuid, @now::timestamptz, sqlc.narg(user_id)::uuid
)
RETURNING *;

-- name: UpdateTerms :one
-- UpdateTerms replaces an allocation's terms whole, as the caller decided
-- them over the row it locked, and moves the revision on.
UPDATE planning.allocation_terms SET
    notice_count = sqlc.narg(notice_count),
    notice_unit = sqlc.narg(notice_unit),
    notice_given_on = sqlc.narg(notice_given_on)::date,
    minimum_until = sqlc.narg(minimum_until)::date,
    replacement_needs_approval = @replacement_needs_approval,
    handover_days = sqlc.narg(handover_days),
    non_compete_until = sqlc.narg(non_compete_until)::date,
    non_compete_customer_ids = @non_compete_customer_ids::integer[],
    reference = sqlc.narg(reference),
    revision = revision + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = sqlc.narg(user_id)::uuid
WHERE allocation_id = @allocation_id
RETURNING *;

-- name: DeleteTerms :one
-- DeleteTerms removes an allocation's terms row and answers it, for the
-- record of what was cleared. No row is an allocation with no terms.
DELETE FROM planning.allocation_terms WHERE allocation_id = @allocation_id
RETURNING *;

-- name: PersonNonCompetes :many
-- PersonNonCompetes is one person's confirmed allocations whose terms carry
-- a non-compete in force on or after @from_date, read under the person's
-- lock (S17.3, S23): the binding row, its until date and the customers it
-- names, by until date and id.
SELECT a.id, t.non_compete_until, t.non_compete_customer_ids
FROM planning.allocations a
JOIN planning.allocation_terms t ON t.allocation_id = a.id
WHERE a.person_id = @person_id AND a.status = 'confirmed'
  AND t.non_compete_until >= @from_date::date
ORDER BY t.non_compete_until, a.id;

-- name: PersonAssignmentsFrom :many
-- PersonAssignmentsFrom is the confirmed allocations of @person_ids that
-- carry terms which could bind on or after @from_date (S17.3, S17.4, plan
-- reading 5): the row still running, a minimum term or a non-compete
-- reaching the day, or notice given — the rows the candidates' exclusion and
-- flags read, by person, from date and id. No lock: the matcher is a read.
SELECT sqlc.embed(a), sqlc.embed(t)
FROM planning.allocations a
JOIN planning.allocation_terms t ON t.allocation_id = a.id
WHERE a.person_id = ANY(@person_ids::bigint[]) AND a.status = 'confirmed'
  AND (a.to_date >= @from_date::date OR t.minimum_until >= @from_date::date
       OR t.non_compete_until >= @from_date::date OR t.notice_given_on IS NOT NULL)
ORDER BY a.person_id, a.from_date, a.id;

-- name: CandidateWishes :many
-- CandidateWishes is the open wishes of @person_ids (S17.5) — the table
-- holds open wishes only, since resolved and withdrawn ones are deleted —
-- by person, from date and id: the candidates' wishes flag and sort key.
SELECT * FROM planning.wishes
WHERE person_id = ANY(@person_ids::bigint[])
ORDER BY person_id, from_date, id;
