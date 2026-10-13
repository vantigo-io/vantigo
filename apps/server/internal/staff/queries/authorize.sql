-- name: PersonForUser :one
-- PersonForUser is the person a signed-in user is, through the self-service
-- link (design S24): the identity provider's link keyed on the user's id,
-- whatever state the link is in, so delivery C's states never take the Self
-- audience away from a person. No row is a user linked to nobody.
SELECT person_id FROM staff.identity_links
WHERE provider = 'identity' AND external_id = @external_id;

-- name: NamedAsManager :one
-- NamedAsManager is whether any terms row or department names the person as
-- manager — the cheap question GET /meta asks before the one manager rule's
-- walk (managesAnyoneToday): a person nothing names manages nobody, whatever
-- the day.
SELECT (EXISTS (SELECT 1 FROM staff.employment_terms WHERE manager_person_id = @person_id::bigint)
    OR EXISTS (SELECT 1 FROM staff.departments WHERE manager_person_id = @person_id::bigint))::boolean AS named;
