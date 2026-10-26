-- The window reads of contracts.Workforce (planning design S8,
-- workforce_window.go). The people, their links, the terms chains and the
-- colleagues' absence days are read with the queries their own files hold
-- (PeopleSummaries, PeopleLinks, ListTermsOfEmployments, ColleagueAbsenceDays);
-- only the employments of a window are the contract's own.

-- name: WorkforceEmployments :many
-- WorkforceEmployments is every employment of the people with an employment
-- touching @from_day..@to_day — of @person_ids' alone when it is not NULL —
-- by person and start: the window's people, and each one's whole history so
-- the rule can ask whether they are in employment today even when today
-- lies outside the window.
SELECT e.* FROM staff.employments e
WHERE e.person_id IN (
        SELECT w.person_id FROM staff.employments w
        WHERE w.start_date <= @to_day::date AND (w.end_date IS NULL OR w.end_date >= @from_day::date))
  AND (sqlc.narg('person_ids')::bigint[] IS NULL OR e.person_id = ANY(sqlc.narg('person_ids')::bigint[]))
ORDER BY e.person_id, e.start_date, e.id;

-- name: WorkforcePreferences :many
-- WorkforcePreferences is the preferences of the people named, by person and
-- kind, for the fit the Workforce contract scores (planning design S17.6).
SELECT person_id, kind, value, weight
FROM staff.person_preferences
WHERE person_id = ANY(@person_ids::bigint[])
ORDER BY person_id, kind;
