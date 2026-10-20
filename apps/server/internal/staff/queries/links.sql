-- The self-service link (design S24 as amended, §3.6): HR ties a person to an
-- existing user under the identity provider — state linked, not managed —
-- which makes the user the person themselves to this module and nothing else.

-- name: PersonLink :one
-- PersonLink is a person's link under the identity provider, with the
-- reason the worker left on it. No row is a person linked to nobody.
SELECT external_id, state, managed, blocked_reason
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
-- the worker's states — but one the worker left account_not_managed, which HR
-- repairs (links.go) — or naming a Staff-managed user, is refused
-- link_managed before this runs, so the only marked link this replaces names
-- a user Staff does not manage, whom Deactivate refuses
-- (account_not_managed) and only an Owner disables.
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
-- identity provider, in any state the worker left them but one it blocked
-- as someone else's account — the predicate PersonForUser applies
-- (authorize.sql; keep the two identical): a worker link blocked mismatch or
-- account_not_managed names no user of the person's, so no active one. Who
-- among a person's managers decides (deciders.go), once the directory says
-- whose user is active, and the person's own user a holders' mail leaves
-- out (notifications.go).
SELECT person_id, external_id FROM staff.identity_links
WHERE person_id = ANY(@person_ids::bigint[]) AND provider = 'identity'
  AND (state = 'linked' OR coalesce(blocked_reason, '') NOT IN ('mismatch', 'account_not_managed'));

-- name: MailRecipients :many
-- MailRecipients is what an absence mail needs of @person_ids' people: the
-- name, the addresses Staff holds and the language (plan reading 26).
SELECT id, display_name, work_email, private_email, preferred_language
FROM staff.people WHERE id = ANY(@person_ids::bigint[]);

-- name: LinkedPeopleOfUsers :many
-- LinkedPeopleOfUsers is the people @user_ids' users are linked to under the
-- identity provider, by user: the name and the language a mail to a holder
-- by right greets them in (notifications.go) — the person's own, before the
-- user's. A worker link blocked as someone else's account is no link of the
-- user's — the predicate PersonForUser applies (authorize.sql; keep the
-- three identical) — so its user is greeted as identity names them.
SELECT l.external_id::text AS user_id, p.display_name, p.preferred_language
FROM staff.identity_links l JOIN staff.people p ON p.id = l.person_id
WHERE l.provider = 'identity' AND l.external_id = ANY(@user_ids::text[])
  AND (l.state = 'linked' OR coalesce(l.blocked_reason, '') NOT IN ('mismatch', 'account_not_managed'));

-- The provider links (Entra design D7, delivery B): a person's second row in
-- staff.identity_links, its provider an IdentityProvisioner's key ('entra'),
-- which the accounts worker writes under the claim it holds on the person's
-- identity link — the identity link stays the claim, so the two are worked
-- in one chain under one lease.

-- name: PersonProviderLink :one
-- PersonProviderLink is a person's link under @provider with every column
-- the worker and the Account tab read. No row is a person with no link
-- under it.
SELECT id, external_id, state, managed, blocked_reason, last_error, attempts, next_attempt_at,
    last_synced_at, last_spec_hash, external_upn, external_email, announced_at, pass_issued_at,
    reported_codes, updated_at
FROM staff.identity_links
WHERE person_id = @person_id AND provider = @provider;

-- name: PersonProviderLinkKeys :many
-- PersonProviderLinkKeys is the providers a person has a link under, the
-- identity link aside: those not deleted first, then by age. A person whose
-- kind no longer names a provider can still have one (review M3), which the
-- worker deactivates for a leaver or a disable asked for.
SELECT provider FROM staff.identity_links
WHERE person_id = @person_id AND provider <> 'identity'
ORDER BY (state = 'deleted'), id;

-- name: ClearProviderLinkAnnouncement :exec
-- ClearProviderLinkAnnouncement forgets that the account a provider link
-- names was announced, once the announcement was withdrawn (Unexpect) on a
-- change of the person's kind — only when the link still records the one
-- withdrawn.
UPDATE staff.identity_links SET announced_at = NULL
WHERE id = @id AND announced_at = @announced_at::timestamptz;

-- name: InsertPreparingProviderLink :exec
-- InsertPreparingProviderLink writes the worker's preparing link under
-- @provider — no external account yet, managed — under the person's lock,
-- before the provider's Prepare is called. A link already there is left as
-- it is.
INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
VALUES (@person_id, @provider, NULL, 'preparing', true, @now::timestamptz, @now::timestamptz)
ON CONFLICT (person_id, provider) DO NOTHING;

-- name: WriteProviderLinkResult :execrows
-- WriteProviderLinkResult records what a claim did on a provider link, every
-- provider column replaced, guarded by the claim's lease — held on the
-- person's identity link, not on this row: no row is a link gone, a link
-- under the identity provider (never written here), or a claim lost — the
-- identity link's lease lapsed and taken by another cycle, or the identity
-- link gone. Neither the person's revision nor updated_at moves.
UPDATE staff.identity_links AS pl SET
    external_id = sqlc.narg(external_id)::text,
    state = @state,
    managed = true,
    blocked_reason = sqlc.narg(blocked_reason)::varchar,
    last_error = sqlc.narg(last_error)::varchar,
    attempts = @attempts,
    next_attempt_at = sqlc.narg(next_attempt_at)::timestamptz,
    last_synced_at = sqlc.narg(last_synced_at)::timestamptz,
    last_spec_hash = sqlc.narg(last_spec_hash)::bytea,
    external_upn = sqlc.narg(external_upn)::varchar,
    external_email = sqlc.narg(external_email)::varchar,
    announced_at = sqlc.narg(announced_at)::timestamptz,
    pass_issued_at = sqlc.narg(pass_issued_at)::timestamptz,
    reported_codes = @reported_codes::text[],
    updated_at = @now::timestamptz
WHERE pl.id = @id AND pl.provider <> 'identity'
  AND EXISTS (SELECT 1 FROM staff.identity_links il
              WHERE il.person_id = pl.person_id AND il.provider = 'identity' AND il.lease_id = @lease_id::uuid);

-- name: ProviderLinksOfPeople :many
-- ProviderLinksOfPeople is the external account of each of @person_ids'
-- people under @provider that exists in the directory — enabled or
-- disabled — by person: a manager's external id, which a report's spec
-- names (ExternalSpec.ManagerExternalID).
SELECT person_id, external_id::text AS external_id FROM staff.identity_links
WHERE person_id = ANY(@person_ids::bigint[]) AND provider = @provider
  AND state IN ('enabled', 'disabled') AND external_id IS NOT NULL;

-- name: ClearPassIssued :execrows
-- ClearPassIssued records that the pass the provider issued on the person's
-- link under @provider was shown (or is gone): pass_issued_at cleared, so
-- the Account tab no longer offers it and passUnrevealed is not raised.
-- Neither the person's revision nor the claim's lease moves.
UPDATE staff.identity_links SET pass_issued_at = NULL, updated_at = @now::timestamptz
WHERE person_id = @person_id AND provider = @provider AND pass_issued_at IS NOT NULL;
