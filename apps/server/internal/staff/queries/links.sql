-- The self-service link (design S24 as amended, §3.6): HR ties a person to an
-- existing user under the identity provider — state linked, not managed —
-- which makes the user the person themselves to this module and nothing else.

-- name: PersonLink :one
-- PersonLink is a person's link under the identity provider. No row is a
-- person linked to nobody.
SELECT external_id, state, managed
FROM staff.identity_links
WHERE person_id = @person_id AND provider = 'identity';

-- name: SetPersonLink :exec
-- SetPersonLink links a person to a user, replacing any link they had under
-- the identity provider. A user linked to another person trips
-- ux_identity_links_provider_external, which the caller answers as
-- user_linked_elsewhere. A replaced link starts over as a self-service link:
-- whatever delivery C's worker recorded about the old user is not this one's.
INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
VALUES (@person_id, 'identity', @external_id::text, 'linked', false, @now::timestamptz, @now::timestamptz)
ON CONFLICT (person_id, provider) DO UPDATE SET
    external_id = EXCLUDED.external_id,
    state = 'linked',
    managed = false,
    blocked_reason = NULL,
    last_error = NULL,
    attempts = 0,
    next_attempt_at = NULL,
    last_synced_at = NULL,
    last_spec_hash = NULL,
    updated_at = EXCLUDED.updated_at;

-- name: DeletePersonLink :exec
DELETE FROM staff.identity_links WHERE person_id = @person_id AND provider = 'identity';

-- name: PeopleLinks :many
-- PeopleLinks is the self-service links of @person_ids' people under the
-- identity provider, in any state: who among a person's managers decides
-- (deciders.go), once the directory says whose user is active.
SELECT person_id, external_id FROM staff.identity_links
WHERE person_id = ANY(@person_ids::bigint[]) AND provider = 'identity';

-- name: MailRecipients :many
-- MailRecipients is what an absence mail needs of @person_ids' people: the
-- name, the addresses Staff holds and the language (plan reading 26).
SELECT id, display_name, work_email, private_email, preferred_language
FROM staff.people WHERE id = ANY(@person_ids::bigint[]);
