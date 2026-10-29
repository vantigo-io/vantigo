-- The accounts (Entra design D11, D13): the users the module found or
-- created in the tenant, by their immutable object id, one per employee
-- number (case-folded). Written in short transactions after each Graph call,
-- never around one.

-- name: GetAccount :one
SELECT * FROM entra.accounts WHERE object_id = @object_id;

-- name: LockAccountShare :one
-- LockAccountShare is GetAccount under the row's share lock, for an Outlook
-- connect's complete: a leaver's Deactivate, which writes the row before it
-- closes the connection, is judged either before the connect — which then
-- sees the account disabled — or after it, when it closes what the connect
-- stored.
SELECT * FROM entra.accounts WHERE object_id = @object_id FOR SHARE;

-- name: GetAccountByNumber :one
SELECT * FROM entra.accounts WHERE upper(employee_number) = upper(@employee_number::text);

-- name: UpsertAccount :one
-- UpsertAccount records the user Prepare found or created. A row already
-- there keeps created_by_us and its pass: who made the user does not change,
-- and a found user's pass is the lifecycle's to issue or clear.
INSERT INTO entra.accounts (
    object_id, employee_number, user_principal_name, state, created_by_us, reported, last_error,
    last_synced_at, created_at, updated_at
) VALUES (
    @object_id, @employee_number, @user_principal_name, @state, @created_by_us, @reported, @last_error,
    @now::timestamptz, @now::timestamptz, @now::timestamptz
)
ON CONFLICT (object_id) DO UPDATE SET
    employee_number = EXCLUDED.employee_number,
    user_principal_name = EXCLUDED.user_principal_name,
    state = EXCLUDED.state,
    reported = EXCLUDED.reported,
    last_error = EXCLUDED.last_error,
    last_synced_at = EXCLUDED.last_synced_at,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: SetAccountState :one
-- SetAccountState records what a lifecycle call left the user as: its state,
-- the codes it reported and its last refusal code (null when none).
UPDATE entra.accounts SET
    state = @state,
    reported = @reported,
    last_error = @last_error,
    last_synced_at = @now::timestamptz,
    updated_at = @now::timestamptz
WHERE object_id = @object_id
RETURNING *;

-- name: SetAccountLicences :exec
-- SetAccountLicences records the SKU ids (lower-case) of the direct licences
-- the module itself assigned the user: the only ones an Update removes
-- (design reading 9). The leaver's strip empties it.
UPDATE entra.accounts SET licences_assigned = @licences_assigned::text[], updated_at = @now::timestamptz
WHERE object_id = @object_id;

-- name: SetAccountPass :one
-- SetAccountPass keeps a newly issued pass sealed under entra/pass with its
-- expiry; it replaces any pass not yet revealed, as the tenant's one pass per
-- user replaces the old.
UPDATE entra.accounts SET
    pass_sealed = @pass_sealed,
    pass_expires_at = @pass_expires_at,
    pass_revealed_at = NULL,
    updated_at = @now::timestamptz
WHERE object_id = @object_id
RETURNING *;

-- name: ClearAccountPass :exec
-- ClearAccountPass forgets a pass not yet revealed: the leaver's, or one
-- found expired.
UPDATE entra.accounts SET pass_sealed = NULL, updated_at = @now::timestamptz
WHERE object_id = @object_id AND pass_sealed IS NOT NULL;

-- name: TakeAccountPass :one
-- TakeAccountPass is the pass's one reveal, atomic: it answers the sealed
-- pass and its expiry and clears it in one statement. The row's lock is
-- taken in the CTE, so of two concurrent reveals the second waits, then
-- re-reads the row, finds pass_sealed null and answers no row (pgx.ErrNoRows,
-- the pass gone). The caller judges the expiry: a pass taken after it is
-- gone too, and has been cleared all the same.
WITH taken AS (
    SELECT e.object_id, e.pass_sealed, e.pass_expires_at FROM entra.accounts AS e
    WHERE e.object_id = @object_id AND e.pass_sealed IS NOT NULL
    FOR UPDATE
)
UPDATE entra.accounts AS a SET
    pass_sealed = NULL,
    pass_revealed_at = @now::timestamptz,
    updated_at = @now::timestamptz
FROM taken
WHERE a.object_id = taken.object_id
RETURNING taken.pass_sealed AS taken_sealed, taken.pass_expires_at AS taken_expires_at;

-- name: ClearAllPasses :exec
-- ClearAllPasses forgets every pass not yet revealed, in the transaction of a
-- confirmed change of tenant: a pass of the old tenant's users is no longer
-- the module's to reveal.
UPDATE entra.accounts SET pass_sealed = NULL, pass_expires_at = NULL, updated_at = @now::timestamptz
WHERE pass_sealed IS NOT NULL OR pass_expires_at IS NOT NULL;

-- name: ConfirmAdoption :one
-- ConfirmAdoption records that a holder of entra:manage confirmed an adopted
-- account (plan reading 8), and takes adoption_unconfirmed out of its
-- codes. No row is answered for an account the module created or one
-- confirmed already: the handler tells the two from an unknown id.
UPDATE entra.accounts SET
    adoption_confirmed_at = @now::timestamptz,
    adoption_confirmed_by_user_id = @confirmed_by_user_id,
    reported = array_remove(reported, 'adoption_unconfirmed'),
    updated_at = @now::timestamptz
WHERE object_id = @object_id AND NOT created_by_us AND adoption_confirmed_at IS NULL
RETURNING *;

-- name: CountAccounts :one
SELECT
    count(*)::integer AS total,
    (count(*) FILTER (WHERE state = 'enabled'))::integer AS enabled,
    (count(*) FILTER (WHERE state = 'disabled'))::integer AS disabled,
    (count(*) FILTER (WHERE state = 'deleted'))::integer AS deleted
FROM entra.accounts;

-- name: ListAccounts :many
SELECT * FROM entra.accounts ORDER BY user_principal_name, object_id;

-- name: DeleteAccount :exec
-- DeleteAccount forgets the row of a user the tenant no longer holds, so the
-- employee number can name the new user a rehire after a delete is given
-- (D13: a new account). The events keep the old object id and its UPN.
DELETE FROM entra.accounts WHERE object_id = @object_id;
