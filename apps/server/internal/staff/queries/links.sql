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
-- whatever delivery C's worker recorded about the old user is not this one's,
-- and a claim the worker holds on it is ended — its result, which names the
-- old user, is no longer written (WriteAccountResult). Clearing a disable
-- Disable now asked for (disable_requested_at) is safe here: a link in one of
-- the worker's states, or naming a Staff-managed user, is refused
-- link_managed before this runs (links.go), so the only marked link this
-- replaces is a self-service link to a user Staff does not manage, whom
-- Deactivate refuses (account_not_managed) and only an Owner disables.
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
    synced_employee_number = NULL,
    disable_requested_at = NULL,
    lease_id = NULL,
    lease_until = NULL,
    updated_at = EXCLUDED.updated_at;

-- name: DeletePersonLink :exec
-- DeletePersonLink removes the person's link, a disable asked for with it:
-- safe for the same reason as SetPersonLink's — the unlink refuses a
-- worker's or a Staff-managed link (link_managed), and the person's delete
-- one naming any user (person_in_use).
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

-- name: LinkedPeopleOfUsers :many
-- LinkedPeopleOfUsers is the people @user_ids' users are linked to under the
-- identity provider, by user: the name and the language a mail to a holder
-- by right greets them in (notifications.go) — the person's own, before the
-- user's.
SELECT l.external_id::text AS user_id, p.display_name, p.preferred_language
FROM staff.identity_links l JOIN staff.people p ON p.id = l.person_id
WHERE l.provider = 'identity' AND l.external_id = ANY(@user_ids::text[]);
