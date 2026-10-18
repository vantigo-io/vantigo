-- The retention run (design S15 as amended by the A2 pre-flight, plan Task 5):
-- three due queries over what remains, the erases' reads and writes, the
-- deletion ledger and the retention block of GET /settings. Each due query
-- takes an optional person (or employment) so the erase judges it again on
-- the row its lock holds — one predicate, asked twice — and none crosses the
-- schema. The columns an erase nulls are not named here: they are the
-- module's columnClasses (retention.go), and the statements that null them
-- are built from it.

-- name: PersonHealthGone :one
-- PersonHealthGone is whether the person's own health cutoff has passed:
-- at least one employment, and every one ended before @cutoff (today minus
-- healthRetentionDays) — DuePeopleHealth's bound, which the schedules a case,
-- a child-sick row and a withdrawal own meet too when it comes first.
SELECT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = @person_id)
   AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = @person_id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date));

-- name: DuePeopleD :many
-- DuePeopleD is the people whose personnel administration (class D) is due
-- for erasure: not erased yet, not held, with at least one employment and
-- every one ended before @cutoff (today minus retentionGraceDays) — a person
-- with no employment is never erased. Oldest last end first, so a backlog
-- drains in the order it built up.
SELECT p.id
FROM staff.people p
WHERE p.erased_at IS NULL
  AND NOT p.legal_hold
  AND (sqlc.narg(person_id)::bigint IS NULL OR p.id = sqlc.narg(person_id)::bigint)
  AND EXISTS (SELECT 1 FROM staff.employments e WHERE e.person_id = p.id)
  AND NOT EXISTS (
      SELECT 1 FROM staff.employments e
      WHERE e.person_id = p.id AND (e.end_date IS NULL OR e.end_date >= @cutoff::date))
ORDER BY (SELECT max(e.end_date) FROM staff.employments e WHERE e.person_id = p.id), p.id
LIMIT @row_limit;

-- name: DuePeopleHealth :many
-- DuePeopleHealth is the people whose health content is due (plan reading 3):
-- erased or not, not held, every employment ended before @cutoff (today
-- minus healthRetentionDays), with something of class E still there — a
-- document in a restricted category, or a value of class E in an event's
-- payload that the erase has not replaced by "changed" yet.
SELECT p.id
FROM staff.people p
WHERE NOT p.legal_hold
  AND (sqlc.narg(person_id)::bigint IS NULL OR p.id = sqlc.narg(person_id)::bigint)
  AND EXISTS (SELECT 1 FROM staff.employments e WHERE e.person_id = p.id)
  AND NOT EXISTS (
      SELECT 1 FROM staff.employments e
      WHERE e.person_id = p.id AND (e.end_date IS NULL OR e.end_date >= @cutoff::date))
  AND (
      EXISTS (
          SELECT 1 FROM staff.documents d
          JOIN staff.document_categories c ON c.id = d.category_id
          WHERE d.person_id = p.id AND c.retention_class = 'E' AND d.case_id IS NULL)
      OR EXISTS (
          SELECT 1 FROM staff.events ev
          WHERE ev.person_id = p.id AND 'E' = ANY(ev.payload_classes)
            -- The events a sick-leave case, a child-sick row or the
            -- withdrawal owns are on their own schedules (delivery B,
            -- retention.go's ownedHealthEvent); whatever none owns stays here.
            AND NOT (
                (ev.payload ? 'absenceId' AND EXISTS (
                    SELECT 1 FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
                    WHERE a.id = (ev.payload ->> 'absenceId')::bigint
                      AND (a.case_id IS NOT NULL OR (t.kind = 'child_sick' AND NOT ev.payload ? 'rowCaseId'))))
                OR (ev.payload ? 'sickAbsenceId' AND EXISTS (
                    SELECT 1 FROM staff.absences a
                    WHERE a.id = (ev.payload ->> 'sickAbsenceId')::bigint AND a.case_id IS NOT NULL))
                OR (ev.payload ? 'caseId' AND EXISTS (
                    SELECT 1 FROM staff.sick_cases c WHERE c.id = (ev.payload ->> 'caseId')::bigint))
                OR (ev.payload ? 'rowCaseId' AND EXISTS (
                    SELECT 1 FROM staff.sick_cases c WHERE c.id = (ev.payload ->> 'rowCaseId')::bigint))
                OR ev.payload ? 'selfCertWithdrawal')
            AND (
                EXISTS (
                    SELECT 1 FROM jsonb_each(ev.payload -> 'fields') f
                    WHERE f.value <> to_jsonb(@blanked::text)
                      AND (NOT ev.payload ? 'classes' OR coalesce(ev.payload -> 'classes' ->> f.key, 'E') = 'E'))
                OR EXISTS (
                    SELECT 1 FROM jsonb_array_elements_text(coalesce(ev.payload -> 'typeIds', '[]'::jsonb)) WITH ORDINALITY ti(id, n)
                    WHERE coalesce(ev.payload -> 'typeClasses' ->> (ti.n::int - 1), 'E') = 'E'
                      AND NOT (ti.id::bigint = ANY(@reserved_ids::bigint[]))))))
ORDER BY (SELECT max(e.end_date) FROM staff.employments e WHERE e.person_id = p.id), p.id
LIMIT @row_limit;

-- name: DueCasesE :many
-- DueCasesE is the sick-leave cases whose health content is due (design
-- S15, delivery B Task 6): closed — by HR, or on the day after the sixteen
-- that follow their last day (last_day + 17, caseClosesAfter), whichever
-- came first — before @cutoff (today minus
-- healthRetentionDays), whatever the person's employment, of a person not
-- held, with something of class E left: a value of the case's or its
-- milestones' class E columns (@case_columns, @milestone_columns, from
-- columnClasses) not yet erased, a row still of a sick type or with a degree,
-- a document filed under it, or a health field of an event it owns not yet
-- replaced. Oldest close first.
SELECT c.id, c.person_id
FROM staff.sick_cases c
JOIN staff.people p ON p.id = c.person_id
WHERE NOT p.legal_hold
  AND (sqlc.narg(person_id)::bigint IS NULL OR c.person_id = sqlc.narg(person_id)::bigint)
  AND (least(coalesce((c.closed_at AT TIME ZONE @zone::text)::date, c.last_day + 17), c.last_day + 17) < @cutoff::date
       -- or the person's own health cutoff, when it comes first: every
       -- employment ended before @cutoff (whole-branch review)
       OR (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = c.person_id)
           AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = c.person_id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
  AND (
      EXISTS (
          SELECT 1 FROM jsonb_each(to_jsonb(c)) j
          WHERE j.key = ANY(@case_columns::text[]) AND j.value NOT IN ('null'::jsonb, 'false'::jsonb))
      OR EXISTS (
          SELECT 1 FROM staff.sick_case_milestones m, jsonb_each(to_jsonb(m)) j
          WHERE m.case_id = c.id AND j.key = ANY(@milestone_columns::text[]) AND j.value NOT IN ('null'::jsonb, 'false'::jsonb))
      OR EXISTS (
          SELECT 1 FROM staff.absences a
          WHERE a.case_id = c.id AND (NOT (a.type_id = ANY(@reserved_ids::bigint[])) OR a.degree_percent IS NOT NULL))
      OR EXISTS (SELECT 1 FROM staff.documents d WHERE d.case_id = c.id)
      -- With eraseSickCaseDates on (delivery C, Task 5b) the case itself is
      -- what is left, until its erase deletes it: a case B's erase reached
      -- before is due again.
      OR @remove_cases::boolean
      OR EXISTS (
          SELECT 1 FROM staff.events ev
          WHERE ev.person_id = c.person_id AND 'E' = ANY(ev.payload_classes)
            AND ((ev.payload ->> 'caseId')::bigint = c.id
                 OR (ev.payload ->> 'rowCaseId')::bigint = c.id
                 OR (ev.payload ->> 'absenceId')::bigint IN (SELECT a.id FROM staff.absences a WHERE a.case_id = c.id)
                 OR (ev.payload ->> 'sickAbsenceId')::bigint IN (SELECT a.id FROM staff.absences a WHERE a.case_id = c.id))
            AND (
                EXISTS (
                    SELECT 1 FROM jsonb_each(ev.payload -> 'fields') f
                    WHERE f.value <> to_jsonb(@blanked::text)
                      AND coalesce(ev.payload -> 'classes' ->> f.key, 'E') = 'E')
                OR EXISTS (
                    SELECT 1 FROM jsonb_array_elements_text(coalesce(ev.payload -> 'typeIds', '[]'::jsonb)) WITH ORDINALITY ti(id, n)
                    WHERE coalesce(ev.payload -> 'typeClasses' ->> (ti.n::int - 1), 'E') = 'E'
                      AND NOT (ti.id::bigint = ANY(@reserved_ids::bigint[]))))))
ORDER BY least(coalesce((c.closed_at AT TIME ZONE @zone::text)::date, c.last_day + 17), c.last_day + 17), c.id
LIMIT @row_limit;

-- name: EraseCaseMilestones :execrows
-- EraseCaseMilestones deletes a case's milestones, kinds and dates with them
-- (eraseSickCaseDates, Task 5b).
DELETE FROM staff.sick_case_milestones WHERE case_id = @case_id;

-- name: UnlinkCaseRows :execrows
-- UnlinkCaseRows takes a case's rows out of it before the case is deleted
-- (eraseSickCaseDates, Task 5b): the rows keep their days, as the reserved
-- absent the case's erase gave them.
UPDATE staff.absences SET case_id = NULL, revision = revision + 1, updated_at = @now::timestamptz
WHERE case_id = @case_id;

-- name: EraseSickCase :execrows
-- EraseSickCase deletes the case row itself, its dates with it
-- (eraseSickCaseDates, Task 5b), once its milestones, rows and documents no
-- longer name it.
DELETE FROM staff.sick_cases WHERE id = @id;

-- name: PersonEventsNamingCases :many
-- PersonEventsNamingCases is a person's events that name one of @case_ids as
-- their case (caseId) or their row's (rowCaseId), oldest first: what the
-- removal of a case blanks to the kind, or unlinks.
SELECT id, payload
FROM staff.events
WHERE person_id = @person_id
  AND ((payload ->> 'caseId')::bigint = ANY(@case_ids::bigint[]) OR (payload ->> 'rowCaseId')::bigint = ANY(@case_ids::bigint[]))
ORDER BY id;

-- name: CaseRowIDs :many
-- CaseRowIDs is the ids of a case's rows: the events the case's erase owns
-- name one of them.
SELECT id FROM staff.absences WHERE case_id = @case_id ORDER BY id;

-- name: MarkCaseErased :exec
-- MarkCaseErased closes an erased case where HR had not, at its derived
-- close — its last day + 17 at midnight in @zone, not the erase's instant
-- (retention re-check) — so it is never derived away or removed with its
-- rows: its dates and milestone kinds stay.
UPDATE staff.sick_cases SET closed_at = ((last_day + 17)::timestamp AT TIME ZONE @zone::text)
WHERE id = @id AND closed_at IS NULL;

-- name: SetCaseRowsAbsent :execrows
-- SetCaseRowsAbsent writes the reserved absent type in place of the sick
-- type of a case's rows (plan reading 12): the days stay, class C.
UPDATE staff.absences SET type_id = @absent_id::bigint, revision = revision + 1, updated_at = @now::timestamptz
WHERE case_id = @case_id AND type_id <> @absent_id::bigint;

-- name: CaseDocuments :many
-- CaseDocuments is the documents filed under a case, which its erase deletes.
SELECT id, object_key FROM staff.documents WHERE case_id = @case_id ORDER BY id;

-- name: MergedCaseIDs :many
-- MergedCaseIDs is the cases @case_ids took in by a merge (case_merged's
-- mergedCaseId): their events are the survivor's to erase.
SELECT DISTINCT (ev.payload -> 'fields' -> 'mergedCaseId' ->> 'to')::bigint AS merged_case_id
FROM staff.events ev
WHERE ev.person_id = @person_id AND ev.kind = 'case_merged'
  AND (ev.payload ->> 'caseId')::bigint = ANY(@case_ids::bigint[])
  AND jsonb_typeof(ev.payload -> 'fields' -> 'mergedCaseId' -> 'to') = 'number';

-- name: DuePeopleChildSick :many
-- DuePeopleChildSick is the people with a child-sick row whose year ended
-- before @cutoff — healthRetentionDays after 31 December of its last day's
-- year (plan reading 21) — not held, whatever the employment.
SELECT a.person_id
FROM staff.absences a
JOIN staff.absence_types t ON t.id = a.type_id AND t.kind = 'child_sick'
JOIN staff.people p ON p.id = a.person_id
WHERE NOT p.legal_hold
  AND (sqlc.narg(person_id)::bigint IS NULL OR a.person_id = sqlc.narg(person_id)::bigint)
  AND (make_date(EXTRACT(YEAR FROM a.last_day)::int, 12, 31) < @cutoff::date OR (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = a.person_id)
           AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = a.person_id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
GROUP BY a.person_id
ORDER BY min(a.last_day), a.person_id
LIMIT @row_limit;

-- name: DueChildSickRows :many
-- DueChildSickRows is one person's child-sick rows due, as DuePeopleChildSick
-- judges them.
SELECT a.id FROM staff.absences a
JOIN staff.absence_types t ON t.id = a.type_id AND t.kind = 'child_sick'
WHERE a.person_id = @person_id
  AND (make_date(EXTRACT(YEAR FROM a.last_day)::int, 12, 31) < @cutoff::date OR (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = a.person_id)
           AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = a.person_id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
ORDER BY a.id;

-- name: SetRowsAbsent :exec
-- SetRowsAbsent writes the reserved absent type on @ids.
UPDATE staff.absences SET type_id = @absent_id::bigint, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = ANY(@ids::bigint[]);

-- name: SeededTypesByKey :many
-- SeededTypesByKey is the seeds named by @keys with their ids: the reserved
-- types the pregnancy and health erase writes (Task 5b).
SELECT id, key FROM staff.absence_types WHERE seeded AND key = ANY(@keys::text[]) ORDER BY id;

-- name: SeededTypeIDs :many
-- SeededTypeIDs is the ids of the seeds named by @keys: the pregnancy and
-- health types the erase setting reaches (Task 5b).
SELECT id FROM staff.absence_types WHERE seeded AND key = ANY(@keys::text[]) ORDER BY id;

-- name: DuePeoplePregnancyHealth :many
-- DuePeoplePregnancyHealth is the people, not held, whatever their
-- employment, with pregnancy or health content due (erasePregnancyHealthTypes,
-- Task 5b): a row of one of @type_ids whose year ended before @cutoff —
-- healthRetentionDays after 31 December of its last day's year, as a
-- child-sick row's — or an event naming one of those types in its typeIds
-- whose row no longer has one (deleted, or corrected to another type),
-- recorded in a year in @zone that ended before @cutoff; either one sooner
-- when every employment ended before @cutoff — but a row of @floor_type_ids
-- (svangerskapspermisjon, fodselspermisjon), or an event naming one, never
-- before its year ended before @floor_cutoff, a full year ago (review L5):
-- the holiday-pay year after it still reads the leave. An erased row has the reserved
-- type and its events the reserved ids, so nothing is due twice.
SELECT p.id
FROM staff.people p
WHERE NOT p.legal_hold
  AND (sqlc.narg(person_id)::bigint IS NULL OR p.id = sqlc.narg(person_id)::bigint)
  AND (
      EXISTS (
          SELECT 1 FROM staff.absences a
          WHERE a.person_id = p.id AND a.type_id = ANY(@type_ids::bigint[])
            AND (make_date(EXTRACT(YEAR FROM a.last_day)::int, 12, 31) < @cutoff::date OR (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id)
                 AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
            AND (NOT (a.type_id = ANY(@floor_type_ids::bigint[])) OR make_date(EXTRACT(YEAR FROM a.last_day)::int, 12, 31) < @floor_cutoff::date))
      OR EXISTS (
          SELECT 1 FROM staff.events ev
          WHERE ev.person_id = p.id
            AND (make_date(EXTRACT(YEAR FROM (ev.at AT TIME ZONE @zone::text))::int, 12, 31) < @cutoff::date OR (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id)
                 AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
            AND NOT EXISTS (
                SELECT 1 FROM staff.absences a
                WHERE a.id = (ev.payload ->> 'absenceId')::bigint AND a.type_id = ANY(@type_ids::bigint[]))
            AND EXISTS (
                SELECT 1 FROM jsonb_array_elements_text(coalesce(ev.payload -> 'typeIds', '[]'::jsonb)) ti(id)
                WHERE ti.id::bigint = ANY(@type_ids::bigint[]))
            AND (make_date(EXTRACT(YEAR FROM (ev.at AT TIME ZONE @zone::text))::int, 12, 31) < @floor_cutoff::date
                 OR NOT EXISTS (
                     SELECT 1 FROM jsonb_array_elements_text(coalesce(ev.payload -> 'typeIds', '[]'::jsonb)) ti(id)
                     WHERE ti.id::bigint = ANY(@floor_type_ids::bigint[])))))
ORDER BY p.id
LIMIT @row_limit;

-- name: DuePregnancyHealthRows :many
-- DuePregnancyHealthRows is one person's rows of @type_ids due, as
-- DuePeoplePregnancyHealth judges them, the full-year floor included.
SELECT a.id FROM staff.absences a
WHERE a.person_id = @person_id AND a.type_id = ANY(@type_ids::bigint[])
  AND (make_date(EXTRACT(YEAR FROM a.last_day)::int, 12, 31) < @cutoff::date OR (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = a.person_id)
       AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = a.person_id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
  AND (NOT (a.type_id = ANY(@floor_type_ids::bigint[])) OR make_date(EXTRACT(YEAR FROM a.last_day)::int, 12, 31) < @floor_cutoff::date)
ORDER BY a.id;

-- name: SetRowsReserved :exec
-- SetRowsReserved writes a reserved type on @ids by each row's type's
-- visibility and unit (Task 5b): an HR-only row absent_hr_only, or
-- absent_hr_only_hours for an hour row — so it reads "leave" to every reader
-- who read it so — any other absent, or absent_hours; an hour row keeps its
-- hours.
UPDATE staff.absences a SET
    type_id = CASE
        WHEN t.visibility = 'hr_only' AND t.unit = 'hours' THEN @hr_only_hours_id::bigint
        WHEN t.visibility = 'hr_only' THEN @hr_only_id::bigint
        WHEN t.unit = 'hours' THEN @absent_hours_id::bigint
        ELSE @absent_id::bigint END,
    -- An HR-only row keeps its revision and updated_at (review H1): HR
    -- without staff:personal-data is sent an opaque tag of the revision, and
    -- only this erase rewrites an HR-only row, so a tag that moved would say
    -- it was a pregnancy type.
    revision = CASE WHEN t.visibility = 'hr_only' THEN a.revision ELSE a.revision + 1 END,
    updated_at = CASE WHEN t.visibility = 'hr_only' THEN a.updated_at ELSE @now::timestamptz END
FROM staff.absence_types t
WHERE t.id = a.type_id AND a.id = ANY(@ids::bigint[]);

-- name: PersonEventsNamingTypes :many
-- PersonEventsNamingTypes is a person's events whose typeIds name one of
-- @type_ids, oldest first: the pregnancy and health erase's to rewrite.
SELECT ev.id, ev.at, ev.payload
FROM staff.events ev
WHERE ev.person_id = @person_id
  AND EXISTS (
      SELECT 1 FROM jsonb_array_elements_text(coalesce(ev.payload -> 'typeIds', '[]'::jsonb)) ti(id)
      WHERE ti.id::bigint = ANY(@type_ids::bigint[]))
ORDER BY ev.id;

-- name: DuePeopleWithdrawal :many
-- DuePeopleWithdrawal is the people whose withdrawal of egenmelding is due
-- (delivery B Task 6), not held: a withdrawal on file that lapsed before
-- @cutoff, or — none in force since — a withdrawal event recorded before
-- @cutoff_at with a value not yet replaced. An event is judged by its own
-- instant: one recorded since keeps its values until its own time has run.
SELECT p.id
FROM staff.people p
WHERE NOT p.legal_hold
  AND (sqlc.narg(person_id)::bigint IS NULL OR p.id = sqlc.narg(person_id)::bigint)
  AND (
      p.self_cert_withdrawn_until < @cutoff::date
      OR (p.self_cert_withdrawn_until IS NOT NULL AND (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id)
           AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
      OR ((p.self_cert_withdrawn_until IS NULL OR p.self_cert_withdrawn_until < @cutoff::date OR (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id)
           AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
          AND EXISTS (
              SELECT 1 FROM staff.events ev, jsonb_each(ev.payload -> 'fields') f
              WHERE ev.person_id = p.id AND ev.payload ? 'selfCertWithdrawal'
                AND (ev.at < @cutoff_at::timestamptz OR (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id)
           AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = p.id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
                AND f.value <> to_jsonb(@blanked::text))))
ORDER BY p.id
LIMIT @row_limit;

-- name: DuePeopleOrphanedSickEvents :many
-- DuePeopleOrphanedSickEvents is the people, not held, whatever their
-- employment, with an absence or case event (a key absenceId, caseId,
-- rowCaseId or sickAbsenceId) that nothing owns any more — its row, its case
-- and the sykmelding it names gone — recorded before @cutoff_at (sixteen days
-- and healthRetentionDays ago: the latest its case could have closed), with
-- a health value not yet replaced (Task 6 review).
SELECT DISTINCT ev.person_id::bigint AS person_id
FROM staff.events ev
JOIN staff.people p ON p.id = ev.person_id
WHERE NOT p.legal_hold
  AND (sqlc.narg(person_id)::bigint IS NULL OR ev.person_id = sqlc.narg(person_id)::bigint)
  AND 'E' = ANY(ev.payload_classes) AND (ev.at < @cutoff_at::timestamptz OR (EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = ev.person_id)
           AND NOT EXISTS (SELECT 1 FROM staff.employments ge WHERE ge.person_id = ev.person_id AND (ge.end_date IS NULL OR ge.end_date >= @cutoff::date))))
  AND (ev.payload ? 'absenceId' OR ev.payload ? 'caseId' OR ev.payload ? 'rowCaseId' OR ev.payload ? 'sickAbsenceId')
  AND NOT ev.payload ? 'selfCertWithdrawal'
  AND NOT (
      (ev.payload ? 'absenceId' AND EXISTS (
          SELECT 1 FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id
          WHERE a.id = (ev.payload ->> 'absenceId')::bigint
                      AND (a.case_id IS NOT NULL OR (t.kind = 'child_sick' AND NOT ev.payload ? 'rowCaseId'))))
      OR (ev.payload ? 'sickAbsenceId' AND EXISTS (
          SELECT 1 FROM staff.absences a
          WHERE a.id = (ev.payload ->> 'sickAbsenceId')::bigint AND a.case_id IS NOT NULL))
      OR (ev.payload ? 'caseId' AND EXISTS (
          SELECT 1 FROM staff.sick_cases c WHERE c.id = (ev.payload ->> 'caseId')::bigint))
      OR (ev.payload ? 'rowCaseId' AND EXISTS (
          SELECT 1 FROM staff.sick_cases c WHERE c.id = (ev.payload ->> 'rowCaseId')::bigint)))
  AND (
      EXISTS (
          SELECT 1 FROM jsonb_each(ev.payload -> 'fields') f
          WHERE f.value <> to_jsonb(@blanked::text)
            AND coalesce(ev.payload -> 'classes' ->> f.key, 'E') = 'E')
      OR EXISTS (
          SELECT 1 FROM jsonb_array_elements_text(coalesce(ev.payload -> 'typeIds', '[]'::jsonb)) WITH ORDINALITY ti(id, n)
          WHERE coalesce(ev.payload -> 'typeClasses' ->> (ti.n::int - 1), 'E') = 'E'
            AND NOT (ti.id::bigint = ANY(@reserved_ids::bigint[]))))
ORDER BY person_id
LIMIT @row_limit;

-- name: DueEmploymentsA :many
-- DueEmploymentsA is the employments whose pay documentation (class A) is
-- due (plan reading 4): ended in a year before @before_year — this year
-- less the five years pay documentation is kept — of a person not held — erased or not — with something of
-- class A still there: a pay column set on one of its terms rows
-- (@a_columns, the terms' class A columns), a document of class A filed
-- against it — or filed against no employment while it is the person's last
-- one — or a field of class A (@a_fields) in one of its events' payloads not
-- yet replaced by "changed" — or, while it is the person's last one, a value
-- not yet replaced in an event of class A about a document (@document_kinds)
-- that is gone: a contract or an attest deleted by hand, which no longer says
-- which employment it was filed against. Oldest end first.
SELECT e.id, e.person_id
FROM staff.employments e
JOIN staff.people p ON p.id = e.person_id
WHERE NOT p.legal_hold
  AND e.end_date IS NOT NULL
  AND EXTRACT(YEAR FROM e.end_date)::int < @before_year::int
  AND (sqlc.narg(person_id)::bigint IS NULL OR e.person_id = sqlc.narg(person_id)::bigint)
  AND (
      EXISTS (
          SELECT 1 FROM staff.employment_terms t
          WHERE t.employment_id = e.id AND jsonb_strip_nulls(to_jsonb(t)) ?| @a_columns::text[])
      OR EXISTS (
          SELECT 1 FROM staff.documents d
          JOIN staff.document_categories c ON c.id = d.category_id
          WHERE d.person_id = e.person_id AND c.retention_class = 'A'
            AND (d.employment_id = e.id OR (d.employment_id IS NULL AND e.id = (
                SELECT l.id FROM staff.employments l WHERE l.person_id = e.person_id
                ORDER BY l.start_date DESC, l.id DESC LIMIT 1))))
      OR EXISTS (
          SELECT 1 FROM staff.events ev
          WHERE ev.person_id = e.person_id AND 'A' = ANY(ev.payload_classes)
            AND ev.payload ->> 'documentId' IS NULL
            AND ev.payload ->> 'employmentId' = e.id::text
            AND EXISTS (
                SELECT 1 FROM jsonb_each(ev.payload -> 'fields') f
                WHERE f.key = ANY(@a_fields::text[]) AND f.value <> to_jsonb(@blanked::text)))
      OR (e.id = (
              SELECT l.id FROM staff.employments l WHERE l.person_id = e.person_id
              ORDER BY l.start_date DESC, l.id DESC LIMIT 1)
          AND EXISTS (
              SELECT 1 FROM staff.events ev
              WHERE ev.person_id = e.person_id AND 'A' = ANY(ev.payload_classes)
                AND ev.kind = ANY(@document_kinds::text[])
                AND ev.payload ->> 'documentId' IS NOT NULL
                AND NOT EXISTS (
                    SELECT 1 FROM staff.documents d WHERE d.id = (ev.payload ->> 'documentId')::bigint)
                AND EXISTS (
                    SELECT 1 FROM jsonb_each(ev.payload -> 'fields') f
                    WHERE f.value <> to_jsonb(@blanked::text)))))
ORDER BY e.end_date, e.id
LIMIT @row_limit;

-- name: LastEmploymentID :one
-- LastEmploymentID is a person's last employment — the latest to start — the
-- one a document filed against no employment belongs to for the A erase.
SELECT id FROM staff.employments
WHERE person_id = @person_id
ORDER BY start_date DESC, id DESC
LIMIT 1;

-- name: PersonDocumentsOfClass :many
-- PersonDocumentsOfClass is a person's documents in categories of one
-- retention class, the D and the health erases' rows to delete — but those
-- filed under a sick-leave case, which the case's schedule deletes.
SELECT d.id, d.object_key
FROM staff.documents d
JOIN staff.document_categories c ON c.id = d.category_id
WHERE d.person_id = @person_id AND c.retention_class = @class::text AND d.case_id IS NULL
ORDER BY d.id;

-- name: EmploymentDocumentsA :many
-- EmploymentDocumentsA is the documents of class A the A erase of one
-- employment deletes: those filed against it, and those filed against no
-- employment when it is the person's last one (@is_last).
SELECT d.id, d.object_key
FROM staff.documents d
JOIN staff.document_categories c ON c.id = d.category_id
WHERE d.person_id = @person_id AND c.retention_class = 'A'
  AND (d.employment_id = @employment_id OR (d.employment_id IS NULL AND @is_last::boolean))
ORDER BY d.id;

-- name: GoneDocumentsWithEventsA :many
-- GoneDocumentsWithEventsA is the documents a person's events of class A
-- about a document (@document_kinds) name that no longer exist — a contract
-- or an attest deleted by hand — whose values the A erase of the person's
-- last employment replaces.
SELECT DISTINCT (ev.payload ->> 'documentId')::bigint AS document_id
FROM staff.events ev
WHERE ev.person_id = @person_id AND 'A' = ANY(ev.payload_classes)
  AND ev.kind = ANY(@document_kinds::text[])
  AND ev.payload ->> 'documentId' IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM staff.documents d WHERE d.id = (ev.payload ->> 'documentId')::bigint)
ORDER BY document_id;

-- name: PersonEventsOfClass :many
-- PersonEventsOfClass is a person's events whose payload touches one
-- retention class (payload_classes, written for the erase alone), oldest
-- first, for the erase to replace that class's values in.
SELECT id, at, payload, payload_classes
FROM staff.events
WHERE person_id = @person_id AND @class::text = ANY(payload_classes)
ORDER BY id;

-- name: SetEventPayload :exec
-- SetEventPayload rewrites one event's payload: the erase's only change to
-- the history, which keeps its kind, actor, instant and classes.
UPDATE staff.events SET payload = @payload WHERE id = @id;

-- name: MarkPersonErased :exec
-- MarkPersonErased sets erased_at, the D erase's last write: the record is
-- read-only from here (erased.go) until a rehire clears it.
UPDATE staff.people SET
    erased_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: DueObjectDeletions :many
-- DueObjectDeletions is the ledger rows a pass may claim: due, and not
-- claimed — or claimed by a pass whose window has run out. A hundred at a
-- time, the longest waiting first.
SELECT object_key
FROM staff.object_deletions
WHERE next_attempt_at <= @now::timestamptz
  AND (claimed_until IS NULL OR claimed_until < @now::timestamptz)
ORDER BY next_attempt_at, id
LIMIT @row_limit;

-- name: ClaimObjectDeletion :one
-- ClaimObjectDeletion claims one ledger row for @claimed_until by a
-- conditional update that asserts the candidate predicate again: of two
-- passes, on two replicas, that both selected the row, one updates it and the
-- other finds no row. It answers the attempts already failed.
UPDATE staff.object_deletions SET claimed_until = @claimed_until::timestamptz
WHERE object_key = @object_key
  AND next_attempt_at <= @now::timestamptz
  AND (claimed_until IS NULL OR claimed_until < @now::timestamptz)
RETURNING attempts;

-- name: RetentionLedgerState :one
-- RetentionLedgerState is the ledger as the retention block of GET /settings
-- counts it: the objects waiting, and those the store has refused at least
-- once.
SELECT
    count(*)::bigint AS pending,
    (count(*) FILTER (WHERE attempts >= 1))::bigint AS failing
FROM staff.object_deletions;

-- name: OldestPendingDeletion :one
-- OldestPendingDeletion is when the oldest object still in the ledger was
-- queued; no row when the ledger is empty.
SELECT created_at FROM staff.object_deletions ORDER BY created_at, id LIMIT 1;

-- name: LastErasedAt :one
-- LastErasedAt is the instant of the latest erased event; no row when the
-- run has never erased anything. Without @with_health the health kinds'
-- events, of class E, are left out (review M1): a reader without
-- staff:personal-data must not see the last erase move when only health
-- content went.
SELECT at FROM staff.events
WHERE kind = 'erased' AND (@with_health::boolean OR NOT ('E' = ANY(payload_classes)))
ORDER BY at DESC, id DESC LIMIT 1;
