-- name: PersonForUser :one
-- PersonForUser is the person a signed-in user is, through the self-service
-- link (design S24): the identity provider's link keyed on the user's id,
-- whatever state the link is in, so delivery C's states never take the Self
-- audience away from a person. No row is a user linked to nobody.
SELECT person_id FROM staff.identity_links
WHERE provider = 'identity' AND external_id = @external_id;
