-- name: LockLoginTicket :one
-- LockLoginTicket finds the login ticket with token_hash that is still
-- unexpired at now, and locks it: two redemptions of one ticket run one
-- after the other, and the second finds the ticket the first spent gone.
SELECT user_id
FROM identity.login_tickets
WHERE token_hash = @token_hash AND expires_at > @now::timestamptz
FOR UPDATE;

-- name: SpendLoginTicket :execrows
-- SpendLoginTicket spends a login ticket once its second step succeeded. It
-- affects one row only while the ticket is still there and unexpired at
-- now, so a ticket starts one session even without the row lock
-- LockLoginTicket takes.
DELETE FROM identity.login_tickets
WHERE token_hash = @token_hash AND expires_at > @now::timestamptz;

-- name: DeleteLoginTicket :exec
-- DeleteLoginTicket discards a login ticket: one a refused second step
-- ends, or one a sign-out finds.
DELETE FROM identity.login_tickets
WHERE token_hash = @token_hash;

-- name: GetTwoFactorCandidate :one
-- GetTwoFactorCandidate reads what the second sign-in step decides on for
-- the user a login ticket names: the TOTP state and the encrypted secret,
-- the lockout, the roles, and whether the account is effectively disabled
-- beyond is_disabled, as GetLoginCandidate reads it.
SELECT u.id, u.email, u.display_name, u.is_disabled, u.lockout_end, u.totp_enabled, u.totp_secret,
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
WHERE u.id = @id;

-- name: RecordTOTPStep :execrows
-- RecordTOTPStep spends a verified TOTP code's time step. It affects no row,
-- and the code counts as invalid, when the step is not later than the last
-- one spent, which rejects a replay: of two requests racing with one code,
-- the second waits on the row lock and then finds the step taken. It also
-- affects no row when the secret the code was verified against has since
-- been replaced.
UPDATE identity.users
SET totp_last_step = @step::bigint
WHERE id = @id
  AND totp_secret = @totp_secret
  AND (totp_last_step IS NULL OR totp_last_step < @step::bigint);

-- name: SetTOTPSecret :exec
-- SetTOTPSecret gives the user a new encrypted authenticator secret.
-- totp_enabled is left as it is: enrolment finishes with EnableTOTP. The
-- last spent step belongs to the old secret and goes with it.
UPDATE identity.users
SET totp_secret = @totp_secret, totp_last_step = NULL, version = @version, updated_at = @now::timestamptz
WHERE id = @id;

-- name: EnableTOTP :execrows
-- EnableTOTP turns TOTP on once a code verified against totp_secret, and
-- spends the code's step as RecordTOTPStep does. It affects no row when the
-- step was already spent or the secret has been replaced since it was read.
UPDATE identity.users
SET totp_enabled = true, totp_last_step = @step::bigint, version = @version, updated_at = @now::timestamptz
WHERE id = @id
  AND totp_secret = @totp_secret
  AND (totp_last_step IS NULL OR totp_last_step < @step::bigint);

-- name: DisableTOTP :exec
-- DisableTOTP turns TOTP off and forgets the secret.
UPDATE identity.users
SET totp_enabled = false, totp_secret = NULL, totp_last_step = NULL, version = @version, updated_at = @now::timestamptz
WHERE id = @id;

-- name: ResetTOTP :exec
-- ResetTOTP turns TOTP off and replaces the secret with a new one nobody
-- holds, as an Owner's reset of another Owner's MFA does: the target
-- enrols a new authenticator before TOTP is on again.
UPDATE identity.users
SET totp_enabled = false, totp_secret = @totp_secret, totp_last_step = NULL, version = @version, updated_at = @now::timestamptz
WHERE id = @id;

-- name: DeleteRecoveryCodes :exec
DELETE FROM identity.recovery_codes
WHERE user_id = @user_id;

-- name: InsertRecoveryCodes :exec
INSERT INTO identity.recovery_codes (user_id, code_hash)
SELECT @user_id, unnest(@code_hashes::bytea[]);

-- name: ConsumeRecoveryCode :execrows
-- ConsumeRecoveryCode spends a recovery code once: of two sign-ins racing
-- with one code, only one deletes the row.
DELETE FROM identity.recovery_codes
WHERE user_id = @user_id AND code_hash = @code_hash;

-- name: SetSessionMFAVerified :exec
-- SetSessionMFAVerified records that a session verified a second factor at
-- mfa_verified_at, or, given null, that it no longer counts as having done
-- so.
UPDATE identity.sessions
SET mfa_verified_at = @mfa_verified_at
WHERE id = @id;

-- name: IsOwner :one
SELECT EXISTS (
    SELECT 1 FROM identity.user_roles ur
    JOIN identity.roles ro ON ro.id = ur.role_id
    WHERE ur.user_id = @user_id AND ro.name = 'Owner'
)::boolean AS is_owner;
