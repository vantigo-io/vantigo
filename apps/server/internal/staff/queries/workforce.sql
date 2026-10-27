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

-- name: WorkforceDirectoryAccount :one
-- WorkforceDirectoryAccount is the account in an external directory of the
-- person @user_id is (calendar Outlook sync design O3): the person is found
-- through the identity link keyed on the user's id under the predicate
-- PersonForUser applies (authorize.sql; keep them identical) — in any state
-- the worker left it, disabled included, so a leaver's calendar can still be
-- cleaned up, but never one it blocked as someone else's account (mismatch,
-- account_not_managed); then that person's provider link with an external
-- id, enabled or disabled. A person with more than one such link answers the
-- oldest (the lowest id). No row is a user with no person, or a person with
-- no such link.
SELECT pl.provider, pl.external_id::text AS external_id, pl.state
FROM staff.identity_links il
JOIN staff.identity_links pl ON pl.person_id = il.person_id
WHERE il.provider = 'identity' AND il.external_id = @user_id::text
  AND (il.state = 'linked' OR coalesce(il.blocked_reason, '') NOT IN ('mismatch', 'account_not_managed'))
  AND pl.provider <> 'identity' AND pl.external_id IS NOT NULL
  AND pl.state IN ('enabled', 'disabled')
ORDER BY pl.id
LIMIT 1;
