-- name: PersonForUser :one
-- PersonForUser is the person a signed-in user is, through the self-service
-- link (design S24): the identity provider's link keyed on the user's id, in
-- whatever state the accounts worker left it — pending, disabled, failed —
-- but one the worker blocked as someone else's account: a worker link whose
-- user carries another employee number (mismatch) or whom Staff no longer
-- manages (account_not_managed) gives its user no Self, nor anything else of
-- the person (privacy review M1). HR's self-service link (linked) never
-- carries either reason. No row is a user linked to nobody. links.sql's
-- PeopleLinks and LinkedPeopleOfUsers, and workforce.sql's
-- WorkforceDirectoryAccount, apply the same predicate (sqlc shares no
-- fragment); keep the four identical.
SELECT person_id FROM staff.identity_links
WHERE provider = 'identity' AND external_id = @external_id::text
  AND (state = 'linked' OR coalesce(blocked_reason, '') NOT IN ('mismatch', 'account_not_managed'));

-- name: NamedAsManager :one
-- NamedAsManager is whether any terms row or department names the person as
-- manager — the cheap question GET /meta asks before the one manager rule's
-- walk (managesAnyoneToday): a person nothing names manages nobody, whatever
-- the day.
SELECT (EXISTS (SELECT 1 FROM staff.employment_terms WHERE manager_person_id = @person_id::bigint)
    OR EXISTS (SELECT 1 FROM staff.departments WHERE manager_person_id = @person_id::bigint))::boolean AS named;
