-- name: ListUserPasskeys :many
-- ListUserPasskeys is every passkey of the user, oldest first.
SELECT *
FROM identity.passkeys
WHERE user_id = @user_id
ORDER BY created_at, credential_id;

-- name: CountUserPasskeys :one
SELECT count(*)::integer AS passkeys
FROM identity.passkeys
WHERE user_id = @user_id;

-- name: InsertPasskey :execrows
-- InsertPasskey stores an enrolled passkey. A credential id is unique across
-- every account, so it affects no row when the credential is already
-- registered, to this user or another.
INSERT INTO identity.passkeys (
    credential_id, user_id, name, credential, user_verified, backup_eligible, backed_up, transports, created_at
) VALUES (
    @credential_id, @user_id, @name, @credential, @user_verified, @backup_eligible, @backed_up, @transports, @now::timestamptz
)
ON CONFLICT (credential_id) DO NOTHING;

-- name: RecordPasskeyUse :exec
-- RecordPasskeyUse stores a passkey as a sign-in left it: its new signature
-- counter and flags, inside credential, and when it was used.
UPDATE identity.passkeys
SET credential = @credential, user_verified = @user_verified, backed_up = @backed_up, last_used_at = @now::timestamptz
WHERE credential_id = @credential_id AND user_id = @user_id;

-- name: DeletePasskey :execrows
-- DeletePasskey removes one of the user's passkeys. It affects no row for a
-- credential id the user does not hold, another user's included.
DELETE FROM identity.passkeys
WHERE user_id = @user_id AND credential_id = @credential_id;

-- name: LockPasskeyCeremonies :exec
-- LockPasskeyCeremonies takes the transaction advisory lock of one ceremony
-- cap's scope (a user's enrolments, or one client address's sign-ins), so
-- that counting the live ceremonies and adding one happen as a unit and
-- concurrent begins cannot overshoot the cap.
SELECT pg_advisory_xact_lock(hashtextextended(@scope::text, 0));

-- name: DeleteSpentPasskeyCeremonies :exec
-- DeleteSpentPasskeyCeremonies purges the ceremonies no one can use any
-- more: expired at now, or already consumed.
DELETE FROM identity.passkey_ceremonies
WHERE expires_at <= @now::timestamptz OR consumed_at IS NOT NULL;

-- name: CountLiveEnrolmentCeremonies :one
SELECT count(*)::integer AS ceremonies
FROM identity.passkey_ceremonies
WHERE kind = 'enroll' AND user_id = @user_id
  AND consumed_at IS NULL AND expires_at > @now::timestamptz;

-- name: CountLiveLoginCeremonies :one
SELECT count(*)::integer AS ceremonies
FROM identity.passkey_ceremonies
WHERE kind = 'login' AND client_ip = @client_ip
  AND consumed_at IS NULL AND expires_at > @now::timestamptz;

-- name: InsertPasskeyCeremony :exec
INSERT INTO identity.passkey_ceremonies (id, user_id, kind, session_data, credential_name, client_ip, expires_at)
VALUES (@id, @user_id, @kind, @session_data, @credential_name, @client_ip, @expires_at::timestamptz);

-- name: ConsumeEnrolmentCeremony :one
-- ConsumeEnrolmentCeremony spends the user's enrolment ceremony id once: it
-- returns the row only while the ceremony is unconsumed and unexpired at
-- now, so of two completions racing for it only one gets it. Another
-- user's ceremony is not theirs to spend, and stays as it was.
UPDATE identity.passkey_ceremonies
SET consumed_at = @now::timestamptz
WHERE id = @id AND kind = 'enroll' AND user_id = @user_id
  AND consumed_at IS NULL AND expires_at > @now::timestamptz
RETURNING session_data, credential_name;

-- name: ConsumeLoginCeremony :one
-- ConsumeLoginCeremony spends a sign-in ceremony once, as
-- ConsumeEnrolmentCeremony does. user_id is the account the ceremony was
-- begun for, or null when begin found none that could sign in.
UPDATE identity.passkey_ceremonies
SET consumed_at = @now::timestamptz
WHERE id = @id AND kind = 'login'
  AND consumed_at IS NULL AND expires_at > @now::timestamptz
RETURNING user_id, session_data;

-- name: GetPasskeyLoginCandidate :one
-- GetPasskeyLoginCandidate is the account a passkey sign-in for
-- normalized_email may be bound to. It always answers one row, whose
-- user_id is null unless the account exists, is neither disabled nor locked
-- out at now, holds a passkey, and (while SCIM is configured) is not a
-- non-Owner the directory deactivated. Every email therefore takes the same
-- path, one query, whether or not it names such an account.
SELECT u.id AS user_id
FROM (SELECT 1) AS probe
LEFT JOIN identity.users u
  ON u.normalized_email = @normalized_email
 AND NOT u.is_disabled
 AND (u.lockout_end IS NULL OR u.lockout_end <= @now::timestamptz)
 AND EXISTS (SELECT 1 FROM identity.passkeys p WHERE p.user_id = u.id)
 AND NOT (
       @scim_enabled::boolean
   AND EXISTS (SELECT 1 FROM identity.scim_user_mappings m WHERE m.user_id = u.id AND m.upstream_active IS FALSE)
   AND NOT EXISTS (
       SELECT 1 FROM identity.user_roles ur
       JOIN identity.roles ro ON ro.id = ur.role_id
       WHERE ur.user_id = u.id AND ro.name = 'Owner'));

-- name: LockPasskeyLoginUser :one
-- LockPasskeyLoginUser reads what a passkey sign-in's completion decides on
-- for the account its ceremony was begun for, as GetTwoFactorCandidate
-- reads it, and locks the account's row: completions for one account take
-- turns, so each checks and advances the signature counter the previous
-- one stored, and a lockout written meanwhile is seen.
SELECT u.id, u.email, u.display_name, u.is_disabled, u.lockout_end, u.totp_enabled,
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
WHERE u.id = @id
FOR UPDATE OF u;
