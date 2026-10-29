-- The connection (Entra design D11): the installation's one row, id 1, seeded
-- by the baseline. The registration (step 1) and the settings (step 4) are
-- the two writes under If-Match: each runs under the row's lock
-- (LockConnection) and moves version on. What a connection test finds, and
-- what the lifecycle's refusals say about the connection, are written with
-- SetConnectionStatus and the streak's two queries, which leave version
-- alone so a background write never turns a person's open form into a 412.

-- name: GetConnection :one
SELECT * FROM entra.connection WHERE id = 1;

-- name: LockConnection :one
-- LockConnection takes the row's lock for a write of the registration or the
-- settings, so the If-Match version is judged against the row the write then
-- changes; the credentials' revoke takes it too, so the last valid secret's
-- check and the revoke are one step.
SELECT * FROM entra.connection WHERE id = 1 FOR UPDATE;

-- name: LockConnectionShare :one
-- LockConnectionShare takes the row's share lock in the transaction that
-- records a user Prepare created, so a change of the tenant or the revoke of
-- the last secret (LockConnection, FOR UPDATE) is judged either before the
-- row exists — and the transaction then sees what changed — or after it,
-- when CountAccounts counts it.
SELECT * FROM entra.connection WHERE id = 1 FOR SHARE;

-- name: UpdateConnectionRegistration :one
-- UpdateConnectionRegistration sets the tenant and the client (step 1) and
-- forgets everything the last test found about the previous pair: the status
-- is unconfigured until the next test.
UPDATE entra.connection SET
    tenant_id = @tenant_id,
    client_id = @client_id,
    status = 'unconfigured',
    status_reason = NULL,
    permissions_present = '{}',
    verified_domains = '{}',
    organisation_name = NULL,
    last_checked_at = NULL,
    sensitive_denied_streak = 0,
    version = version + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = @updated_by_user_id
WHERE id = 1
RETURNING *;

-- name: UpdateConnectionSettings :one
-- UpdateConnectionSettings writes every account rule (step 4), the Mail
-- channels switch (M10) and the Outlook connection switch (Outlook
-- connection design M2), with when it was turned on: moved only when this
-- write turns it from off to on; the handler
-- has merged the request over the row, so a field the request left out is
-- written back as it was.
UPDATE entra.connection SET
    upn_domain = @upn_domain,
    upn_pattern = @upn_pattern,
    default_usage_location = @default_usage_location,
    leaver_strip_licences = @leaver_strip_licences,
    leaver_remove_groups = @leaver_remove_groups,
    leaver_revoke_sessions = @leaver_revoke_sessions,
    leaver_write_leave_date = @leaver_write_leave_date,
    write_hire_date = @write_hire_date,
    pass_lifetime_minutes = @pass_lifetime_minutes,
    pass_usable_once = @pass_usable_once,
    pass_delivery = @pass_delivery,
    mail_channels = @mail_channels,
    outlook_connect = @outlook_connect,
    outlook_connect_at = CASE WHEN @outlook_connect::boolean AND NOT outlook_connect
        THEN @now::timestamptz ELSE outlook_connect_at END,
    version = version + 1,
    updated_at = @now::timestamptz,
    updated_by_user_id = @updated_by_user_id
WHERE id = 1
RETURNING *;

-- name: SetConnectionStatus :one
-- SetConnectionStatus records a status and its reason code. The connection
-- test passes what it read beside them; a lifecycle refusal
-- (credential_invalid) passes null for each, which keeps them.
UPDATE entra.connection SET
    status = @status,
    status_reason = sqlc.narg(status_reason),
    permissions_present = COALESCE(sqlc.narg(permissions_present)::text[], permissions_present),
    verified_domains = COALESCE(sqlc.narg(verified_domains)::text[], verified_domains),
    organisation_name = COALESCE(sqlc.narg(organisation_name)::varchar, organisation_name),
    last_checked_at = COALESCE(sqlc.narg(last_checked_at)::timestamptz, last_checked_at),
    updated_at = @now::timestamptz
WHERE id = 1
RETURNING *;

-- name: AddSensitiveDenied :one
-- AddSensitiveDenied counts one more sensitive write refused in a row (plan
-- reading 4) and answers the streak; three or more reads as role_missing.
UPDATE entra.connection SET sensitive_denied_streak = sensitive_denied_streak + 1
WHERE id = 1
RETURNING sensitive_denied_streak;

-- name: ResetSensitiveDenied :exec
-- ResetSensitiveDenied ends the streak on a sensitive write that succeeded.
UPDATE entra.connection SET sensitive_denied_streak = 0
WHERE id = 1 AND sensitive_denied_streak <> 0;

-- name: SetServicePrincipal :exec
-- SetServicePrincipal records the application's service principal in the
-- tenant, read from its token's oid claim (plan reading 2), which every
-- group the module makes names as an owner. Like the status, it leaves
-- version alone.
UPDATE entra.connection SET service_principal_id = @service_principal_id::uuid
WHERE id = 1 AND service_principal_id IS DISTINCT FROM @service_principal_id::uuid;

-- name: SetSkus :exec
-- SetSkus caches the tenant's licences as the connection test or the
-- licences read found them — at most 200 of { skuId, skuPartNumber, enabled,
-- consumed }, which the caller bounds — so GroupTraits declares the licences
-- trait with no network (plan reading 1). It leaves version alone.
UPDATE entra.connection SET skus = @skus::jsonb WHERE id = 1;

-- name: SetNotificationsValidating :exec
-- SetNotificationsValidating opens the window in which the webhook answers
-- Graph's handshake, until validating_until, before each create of a
-- subscription, and (null) closes it after (calendar Outlook sync design
-- O11.5). It leaves version alone.
UPDATE entra.connection SET notifications_validating_until = sqlc.narg(validating_until)::timestamptz WHERE id = 1;

-- name: SetNotificationsUnreachable :exec
-- SetNotificationsUnreachable records when a create's handshake failed, as
-- Microsoft could not reach the webhook, or (null) clears it after a
-- create that worked (O11.7). It leaves version alone.
UPDATE entra.connection SET notifications_unreachable_at = sqlc.narg(unreachable_at)::timestamptz WHERE id = 1;

-- name: TouchLastNotification :exec
-- TouchLastNotification records that a notification came at now, at most
-- once a minute, so a flood writes the row no more often (O11.5). It leaves
-- version alone.
UPDATE entra.connection SET last_notification_at = @now::timestamptz
WHERE id = 1
  AND (last_notification_at IS NULL OR last_notification_at + interval '1 minute' < @now::timestamptz);

-- name: CloseNotificationsValidating :exec
-- CloseNotificationsValidating closes the handshake's window after a
-- create, only while it is still the one that create opened
-- (validating_until): a create started since keeps the window its own
-- POST needs (O11.5). It leaves version alone.
UPDATE entra.connection SET notifications_validating_until = NULL
WHERE id = 1 AND notifications_validating_until = @validating_until::timestamptz;

-- name: SetOutlookLastError :exec
-- SetOutlookLastError records a validated connect's failure the Owner must
-- act on (consent_missing: the tenant has not consented to a feature's
-- delegated permission), which outlookConnect's status shows until a
-- connect works. It leaves version alone.
UPDATE entra.connection SET
    outlook_last_error = @code,
    outlook_last_error_at = @now::timestamptz
WHERE id = 1;

-- name: ClearOutlookLastError :exec
-- ClearOutlookLastError forgets the last connect's failure once a connect
-- works. It leaves version alone.
UPDATE entra.connection SET outlook_last_error = NULL, outlook_last_error_at = NULL
WHERE id = 1 AND outlook_last_error IS NOT NULL;
