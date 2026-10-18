-- The account flag (design S22, §3.1) and the link it drives (S27, §3.6),
-- delivery C. The flag is three columns of staff.people, 00042's; the link is
-- the identity provider's row in staff.identity_links, which the accounts
-- worker writes and HR's self-service link shares (plan reading 8).

-- name: GetPersonAccount :one
-- GetPersonAccount is a person's account flag with the two addresses it may
-- name and the flag's own revision, which a change of it is guarded by (00051:
-- never people.revision, which a manager reads).
SELECT id, provision_account, provision_from, account_email_kind, work_email, private_email, account_revision
FROM staff.people
WHERE id = @id;

-- name: UpdatePersonAccount :one
-- UpdatePersonAccount replaces the flag's three fields and moves the flag's
-- own revision on — not the person's, nor updated_at, which tell nothing of
-- the flag to anyone who reads the person without the links group.
UPDATE staff.people SET
    provision_account = @provision_account,
    provision_from = sqlc.narg(provision_from)::date,
    account_email_kind = sqlc.narg(account_email_kind)::varchar,
    account_revision = account_revision + 1
WHERE id = @id
RETURNING account_revision;

-- name: PersonAccountLink :one
-- PersonAccountLink is a person's link under the identity provider with the
-- worker's columns the account reads. No row is a person with no link.
SELECT external_id, state, managed, blocked_reason, attempts, next_attempt_at, last_synced_at, lease_until
FROM staff.identity_links
WHERE person_id = @person_id AND provider = 'identity';

-- name: LockPersonAccountLink :one
-- LockPersonAccountLink is PersonAccountLink held for the rest of the
-- transaction: taken under the person's lock, after it (design §4 Locking,
-- the link last), so the delete and the self-service link judge the row they
-- then write against the worker's claim, which locks the link alone.
SELECT external_id, state, managed, blocked_reason, attempts, next_attempt_at, last_synced_at, lease_until
FROM staff.identity_links
WHERE person_id = @person_id AND provider = 'identity'
FOR UPDATE;

-- The accounts worker (Task 4b). It holds no cycle-wide lock: a link is
-- claimed alone, in a statement of its own that commits at once, by a lease
-- (lease_id, lease_until) that every later write of the claim names; two
-- cycles — two workers, a run beside the ticker — share the work by the
-- claims. Every query takes the clock as @now (the clock rule).

-- name: AccountCandidates :many
-- AccountCandidates is every person whose account flag is on, who is not
-- erased, and who has no link under the identity provider: the people the
-- cycle may prepare a link for, once the decision table says it is due.
SELECT p.id FROM staff.people p
WHERE p.provision_account AND p.erased_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM staff.identity_links l WHERE l.person_id = p.id AND l.provider = 'identity')
ORDER BY p.id;

-- name: InsertPreparingLink :exec
-- InsertPreparingLink writes the worker's preparing link — no user yet,
-- managed — under the person's lock. A link already there (another cycle's,
-- or HR's) is left as it is.
INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
VALUES (@person_id, 'identity', NULL, 'preparing', true, @now::timestamptz, @now::timestamptz)
ON CONFLICT (person_id, provider) DO NOTHING;

-- name: ClaimAccountLink :one
-- ClaimAccountLink leases the next due link — its next attempt reached or
-- never set, no lease or a lapsed one — of a person whose flag is on and who
-- is not erased: the link row alone is locked (OF l), and one another cycle
-- is claiming is skipped. Blocked and failed links are claimed on their
-- schedule like any other: the decision table never reads blocked as an
-- input, so a link recovers when what blocked it is gone. LIMIT 1 with SKIP
-- LOCKED may find no row while due links remain — each one locked at that
-- instant by another transaction (another cycle's claim, HR's delete or
-- relink of the person) — and the cycle then ends early: those links are
-- claimed by the next poll, never lost.
UPDATE staff.identity_links SET lease_id = @lease_id::uuid, lease_until = @lease_until::timestamptz
WHERE id = (
    SELECT l.id FROM staff.identity_links l
    JOIN staff.people p ON p.id = l.person_id
    WHERE l.provider = 'identity' AND p.provision_account AND p.erased_at IS NULL
      AND (l.next_attempt_at IS NULL OR l.next_attempt_at <= @now::timestamptz)
      AND (l.lease_until IS NULL OR l.lease_until <= @now::timestamptz)
    ORDER BY l.next_attempt_at NULLS FIRST, l.id
    LIMIT 1
    FOR UPDATE OF l SKIP LOCKED
)
RETURNING id, person_id, external_id, state, managed, blocked_reason, last_error, attempts,
    last_synced_at, last_spec_hash, synced_employee_number;

-- name: WriteAccountResult :execrows
-- WriteAccountResult records what a claim did, on its lease alone, and
-- clears the lease: no row is a link gone (the person deleted), claimed
-- since by another cycle, whose lease it now is, or naming another user than
-- the claim knew (@claimed_external_id: HR relinked it) — a result about one
-- user is never written onto a link that names another. Neither the person's
-- revision nor updated_at moves: the link is the links group's.
UPDATE staff.identity_links SET
    external_id = sqlc.narg(external_id)::text,
    state = @state,
    managed = @managed,
    blocked_reason = sqlc.narg(blocked_reason)::varchar,
    last_error = sqlc.narg(last_error)::varchar,
    attempts = @attempts,
    next_attempt_at = @next_attempt_at::timestamptz,
    last_synced_at = sqlc.narg(last_synced_at)::timestamptz,
    last_spec_hash = sqlc.narg(last_spec_hash)::bytea,
    synced_employee_number = sqlc.narg(synced_employee_number)::varchar,
    lease_id = NULL,
    lease_until = NULL,
    updated_at = @now::timestamptz
WHERE id = @id AND lease_id = @lease_id::uuid
  AND external_id IS NOT DISTINCT FROM sqlc.narg(claimed_external_id)::text;

-- name: RenewAccountLease :execrows
-- RenewAccountLease moves a live claim's lease five minutes on, before each
-- identity call and read of a chain, so a chain longer than one lease keeps
-- its link. No row is a lease lapsed, or taken by another cycle, or a link
-- gone: the chain stops.
UPDATE staff.identity_links SET lease_until = @lease_until::timestamptz
WHERE id = @id AND lease_id = @lease_id::uuid AND lease_until > @now::timestamptz;

-- name: RecordPreparedUser :execrows
-- RecordPreparedUser writes the user a Prepare answered onto the claim's
-- link, which named none, before anything more is done with that user: a
-- user another link names already trips ux_identity_links_provider_external,
-- which the worker records as a mismatch and stops at. No row is the lease
-- lost, or a link that names a user by now.
UPDATE staff.identity_links SET
    external_id = @external_id::text,
    managed = true,
    synced_employee_number = @synced_employee_number::varchar,
    last_synced_at = @now::timestamptz,
    updated_at = @now::timestamptz
WHERE id = @id AND lease_id = @lease_id::uuid AND external_id IS NULL;

-- name: AccountLinkClaim :one
-- AccountLinkClaim is a link's lease and user, for the log line of a result
-- the link refused. No row is a link gone.
SELECT lease_id, external_id FROM staff.identity_links WHERE id = @id;
