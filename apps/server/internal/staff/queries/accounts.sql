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

-- name: EarliestOpenEmploymentStart :one
-- EarliestOpenEmploymentStart is the first day of the person's earliest
-- employment that has not ended by @today — the end day itself is still
-- worked — the account's effective date when HR set none (spec reading 14).
-- No row is a person with no such employment.
SELECT start_date FROM staff.employments
WHERE person_id = @person_id AND (end_date IS NULL OR end_date >= @today::date)
ORDER BY start_date, id
LIMIT 1;

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
