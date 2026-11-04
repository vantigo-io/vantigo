-- The accounts (Google Workspace design G17): the users the module found or
-- created in the organisation, by their Directory id, one per employee
-- number (case-folded). Written in short transactions after each call to
-- Google, never around one.

-- name: GetAccount :one
SELECT * FROM google.accounts WHERE user_id = @user_id;

-- name: GetAccountByNumber :one
SELECT * FROM google.accounts WHERE upper(employee_number) = upper(@employee_number::text);

-- name: UpsertAccount :one
-- UpsertAccount records the user Prepare found or created. A row already
-- there keeps created_by_us, its confirmation, its licences and its
-- password: who made the user does not change.
INSERT INTO google.accounts (
    user_id, employee_number, primary_email, state, created_by_us, reported, last_error,
    last_synced_at, created_at, updated_at
) VALUES (
    @user_id, @employee_number, @primary_email, @state, @created_by_us, @reported, @last_error,
    @now::timestamptz, @now::timestamptz, @now::timestamptz
)
ON CONFLICT (user_id) DO UPDATE SET
    employee_number = EXCLUDED.employee_number,
    primary_email = EXCLUDED.primary_email,
    state = EXCLUDED.state,
    reported = EXCLUDED.reported,
    last_error = EXCLUDED.last_error,
    last_synced_at = EXCLUDED.last_synced_at,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: SetAccountState :one
-- SetAccountState records what a lifecycle call left the user as: its state,
-- the codes it reported and its last refusal code (null when none).
UPDATE google.accounts SET
    state = @state,
    reported = @reported,
    last_error = @last_error,
    last_synced_at = @now::timestamptz,
    updated_at = @now::timestamptz
WHERE user_id = @user_id
RETURNING *;

-- name: SetAccountLicences :exec
-- SetAccountLicences records the licences (productId/skuId) the module
-- itself assigned the user: the only ones it removes.
UPDATE google.accounts SET licences_assigned = @licences_assigned::text[], updated_at = @now::timestamptz
WHERE user_id = @user_id;

-- name: SetAccountPass :exec
-- SetAccountPass keeps a newly set password sealed under google/pass with
-- its expiry; it replaces any not yet revealed, as Google keeps one
-- password per user.
UPDATE google.accounts SET
    pass_sealed = @pass_sealed,
    pass_expires_at = @pass_expires_at,
    pass_revealed_at = NULL,
    updated_at = @now::timestamptz
WHERE user_id = @user_id;

-- name: ClearAccountPass :exec
-- ClearAccountPass forgets a password not yet revealed: the leaver's, a
-- deleted user's, or one found expired.
UPDATE google.accounts SET pass_sealed = NULL, updated_at = @now::timestamptz
WHERE user_id = @user_id AND pass_sealed IS NOT NULL;

-- name: ClearExpiredPasses :exec
-- ClearExpiredPasses forgets every password whose lifetime has ended at
-- @now: after it, HR's Run now sets a new one.
UPDATE google.accounts SET pass_sealed = NULL, updated_at = @now::timestamptz
WHERE pass_sealed IS NOT NULL AND pass_expires_at <= @now::timestamptz;

-- name: TakeAccountPass :one
-- TakeAccountPass is the password's one reveal, atomic: it answers the
-- sealed password and its expiry and clears it in one statement. The row's
-- lock is taken in the CTE, so of two concurrent reveals the second waits,
-- re-reads the row, finds pass_sealed null and answers no row (the password
-- gone). The caller judges the expiry.
WITH taken AS (
    SELECT g.user_id, g.pass_sealed, g.pass_expires_at FROM google.accounts AS g
    WHERE g.user_id = @user_id AND g.pass_sealed IS NOT NULL
    FOR UPDATE
)
UPDATE google.accounts AS a SET
    pass_sealed = NULL,
    pass_revealed_at = @now::timestamptz,
    updated_at = @now::timestamptz
FROM taken
WHERE a.user_id = taken.user_id
RETURNING taken.pass_sealed AS taken_sealed, taken.pass_expires_at AS taken_expires_at;

-- name: ConfirmAccountAdoption :one
-- ConfirmAccountAdoption records that a holder of google:manage confirmed an
-- adopted account, and takes adoption_unconfirmed out of its codes. No row
-- is answered for an account the module created or one confirmed already:
-- the handler tells the two from an unknown id.
UPDATE google.accounts SET
    adoption_confirmed_at = @now::timestamptz,
    adoption_confirmed_by_user_id = @confirmed_by_user_id,
    reported = array_remove(reported, 'adoption_unconfirmed'),
    updated_at = @now::timestamptz
WHERE user_id = @user_id AND NOT created_by_us AND adoption_confirmed_at IS NULL
RETURNING *;

-- name: ListAccounts :many
SELECT * FROM google.accounts ORDER BY primary_email, user_id;

-- name: DeleteAccount :exec
-- DeleteAccount forgets the row of a user the organisation no longer holds,
-- so the employee number can name the new user a rehire after a delete is
-- given. The events keep the old id.
DELETE FROM google.accounts WHERE user_id = @user_id;
