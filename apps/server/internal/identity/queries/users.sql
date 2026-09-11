-- name: GetUserByNormalizedEmail :one
SELECT id, email, normalized_email, email_confirmed, display_name, preferred_language,
       password_hash, is_disabled, lockout_end, failed_login_count, totp_secret,
       totp_enabled, totp_last_step, version, created_at, updated_at
FROM identity.users
WHERE normalized_email = $1;

-- name: InsertUser :exec
INSERT INTO identity.users (
    id, email, normalized_email, email_confirmed, display_name, preferred_language,
    password_hash, is_disabled, version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
);

-- name: GetUserByID :one
SELECT id, email, normalized_email, email_confirmed, display_name, preferred_language,
       password_hash, is_disabled, lockout_end, failed_login_count, totp_secret,
       totp_enabled, totp_last_step, version, created_at, updated_at
FROM identity.users
WHERE id = $1;

-- name: GetLoginCandidate :one
-- GetLoginCandidate reads what password sign-in decides on for the account
-- with normalized_email: its credential, TOTP and lockout state, its roles,
-- and whether it is "effectively disabled" beyond is_disabled: while SCIM
-- is configured, a non-Owner the directory deactivated
-- (SV/ScimLifecycleService.cs:21-44).
SELECT u.id, u.email, u.display_name, u.password_hash, u.is_disabled, u.lockout_end, u.totp_enabled,
       r.role_names,
       (@scim_enabled::boolean AND m.upstream_active IS FALSE AND NOT r.is_owner)::boolean AS scim_inactive
FROM identity.users u
CROSS JOIN LATERAL (
    SELECT coalesce(array_agg(ro.name ORDER BY ro.name), '{}')::text[] AS role_names,
           coalesce(bool_or(ro.name = 'Owner'), false)::boolean AS is_owner
    FROM identity.user_roles ur
    JOIN identity.roles ro ON ro.id = ur.role_id
    WHERE ur.user_id = u.id
) r
LEFT JOIN identity.scim_user_mappings m ON m.user_id = u.id
WHERE u.normalized_email = @normalized_email;

-- name: RecordLoginFailure :one
-- RecordLoginFailure counts one wrong password, as ASP.NET Identity's
-- AccessFailedAsync does: the failure that reaches max_failures locks the
-- account until lockout_until and starts the count again from zero. It
-- returns the lockout end as it stands afterwards.
UPDATE identity.users
SET failed_login_count = CASE WHEN failed_login_count + 1 >= @max_failures::integer THEN 0 ELSE failed_login_count + 1 END,
    lockout_end = CASE WHEN failed_login_count + 1 >= @max_failures::integer THEN @lockout_until::timestamptz ELSE lockout_end END
WHERE id = @id
RETURNING lockout_end;

-- name: ResetLoginFailures :execrows
-- ResetLoginFailures clears the failure count after a right password, but
-- only while no lockout is in force at now. When parallel wrong guesses
-- lock the account between the password check and this statement, it
-- affects no row and the sign-in is refused: the row lock makes it wait for
-- a concurrent RecordLoginFailure and re-check the lockout it wrote.
UPDATE identity.users
SET failed_login_count = 0
WHERE id = @id
  AND (lockout_end IS NULL OR lockout_end <= @now::timestamptz);

-- name: AssignUserRole :execrows
-- AssignUserRole gives the user a direct role. It affects no row when the
-- user already holds it.
INSERT INTO identity.user_roles (user_id, role_id)
VALUES (@user_id, @role_id)
ON CONFLICT DO NOTHING;

-- name: RotateUserVersion :exec
UPDATE identity.users
SET version = @version, updated_at = @now::timestamptz
WHERE id = @id;

-- name: AcquireOwnerMutationLock :exec
-- AcquireOwnerMutationLock takes the transaction advisory lock every change
-- to who holds Owner serialises on (EA/AuthAccountState.cs:9, :20-25).
SELECT pg_advisory_xact_lock(@lock_key::bigint);
