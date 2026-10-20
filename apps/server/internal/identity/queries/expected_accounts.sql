-- The announced account (Entra design D4): a user Staff knows will arrive
-- from the identity provider for a person it registers, matched on arrival
-- by the employee number's key, the user principal name or the object id.
-- The caller normalises what it compares: the number by
-- contracts.NormalizeEmployeeNumber, the UPN as emails are.

-- name: UpsertExpectedAccount :exec
-- UpsertExpectedAccount records the announcement for the number, or replaces
-- the one it has, every column with it: an announcement is the latest
-- Prepare's answer, never a merge of two.
INSERT INTO identity.expected_accounts (
    employee_number_key, employee_number, display_name, preferred_language, issuer, object_id,
    user_principal_name, replace_user_id, created_at, created_by_user_id, created_by_system
) VALUES (
    @employee_number_key, @employee_number, @display_name, sqlc.narg(preferred_language)::varchar,
    sqlc.narg(issuer)::varchar, sqlc.narg(object_id)::varchar, sqlc.narg(user_principal_name)::varchar,
    sqlc.narg(replace_user_id)::uuid, @now::timestamptz, sqlc.narg(created_by_user_id)::uuid,
    sqlc.narg(created_by_system)::varchar
)
ON CONFLICT (employee_number_key) DO UPDATE SET
    employee_number = EXCLUDED.employee_number,
    display_name = EXCLUDED.display_name,
    preferred_language = EXCLUDED.preferred_language,
    issuer = EXCLUDED.issuer,
    object_id = EXCLUDED.object_id,
    user_principal_name = EXCLUDED.user_principal_name,
    replace_user_id = EXCLUDED.replace_user_id,
    created_at = EXCLUDED.created_at,
    created_by_user_id = EXCLUDED.created_by_user_id,
    created_by_system = EXCLUDED.created_by_system;

-- name: DeleteExpectedAccount :execrows
-- DeleteExpectedAccount withdraws or consumes the announcement for the
-- number's key; no row is none to withdraw.
DELETE FROM identity.expected_accounts WHERE employee_number_key = @employee_number_key;

-- name: GetExpectedAccountByNumber :one
-- GetExpectedAccountByNumber is the announcement for the number's key, the
-- first of the three matches. The key is the primary key, so there is at
-- most one; the order is the other two's, for the same shape.
SELECT * FROM identity.expected_accounts
WHERE employee_number_key = @employee_number_key
ORDER BY created_at DESC
LIMIT 1;

-- name: GetExpectedAccountByUPN :one
-- GetExpectedAccountByUPN is the newest announcement naming the user
-- principal name, compared as stored — normalised as emails are, by the
-- caller (plan reading 5: the column is not unique).
SELECT * FROM identity.expected_accounts
WHERE user_principal_name = @user_principal_name
ORDER BY created_at DESC
LIMIT 1;

-- name: GetExpectedAccountByObjectID :one
-- GetExpectedAccountByObjectID is the newest announcement naming the
-- provider's object id. The caller checks the issuer.
SELECT * FROM identity.expected_accounts
WHERE object_id = @object_id
ORDER BY created_at DESC
LIMIT 1;

-- name: ListExpectedAccountsOlderThan :many
-- ListExpectedAccountsOlderThan is every announcement made before @before,
-- the oldest first: the unfulfilled ones, for a test and for the erase.
SELECT * FROM identity.expected_accounts
WHERE created_at < @before::timestamptz
ORDER BY created_at, employee_number_key;
