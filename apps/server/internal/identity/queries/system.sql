-- name: GetSystemSettings :one
-- GetSystemSettings is the maintenance singleton row GetStatus's cache
-- reads through (EA/SystemMaintenanceEndpoints.cs:38-44). No row means
-- maintenance was never set; the caller defaults to off, as ToStatus did
-- for a null setting (:84-87).
SELECT maintenance_enabled, maintenance_message
FROM identity.system_settings
WHERE id = 1;

-- name: UpsertSystemSettings :exec
-- UpsertSystemSettings is PutMaintenance's upsert (:66-78): the singleton
-- row is created on its first PUT, else replaced.
INSERT INTO identity.system_settings (id, maintenance_enabled, maintenance_message, updated_at, updated_by_user_id)
VALUES (1, @maintenance_enabled, @maintenance_message, @now::timestamptz, @updated_by_user_id)
ON CONFLICT (id) DO UPDATE SET
    maintenance_enabled = EXCLUDED.maintenance_enabled,
    maintenance_message = EXCLUDED.maintenance_message,
    updated_at = EXCLUDED.updated_at,
    updated_by_user_id = EXCLUDED.updated_by_user_id;

-- name: RecordOperationalEvent :exec
-- RecordOperationalEvent is OperationalEventService.RecordAsync
-- (SV/OperationalEventService.cs:18-29): one row per kind, upserted with
-- its latest occurred_at. Callers run it on the pool, outside their own
-- transaction, and swallow its error (EA/WorkforceOidcEndpoints.cs:344-353,
-- SV/ScimProtocolService.cs:574-581).
INSERT INTO identity.operational_events (kind, occurred_at)
VALUES (@kind, @now::timestamptz)
ON CONFLICT (kind) DO UPDATE SET occurred_at = EXCLUDED.occurred_at;

-- name: GetLastOperationalEvents :one
-- GetLastOperationalEvents is OperationalEventService.LastAsync for both
-- kinds SystemStatus reports, in one round trip (EA/AuthEndpoints.cs:151-152).
-- The LEFT JOINs, rather than two scalar subqueries, are so sqlc infers
-- both columns nullable: neither kind may have a row yet.
SELECT oidc.occurred_at AS last_oidc_sign_in_at, scim.occurred_at AS last_scim_request_at
FROM (VALUES (1)) AS one (x)
LEFT JOIN identity.operational_events oidc ON oidc.kind = @oidc_kind
LEFT JOIN identity.operational_events scim ON scim.kind = @scim_kind;

-- name: GetOwnerSystemStatusCounts :one
-- GetOwnerSystemStatusCounts is AuthEndpoints.SystemStatus's total, disabled
-- and active counts (:119-139). active excludes a disabled user and one
-- locked out at @now, and, only while @scim_enabled (scim.Enabled), a
-- SCIM-inactive user unless they hold Owner: a SCIM-inactive Owner remains
-- available as the break-glass account (:116-118), the same exemption
-- GetSessionByTokenHash applies to a live session.
SELECT
    count(*)::int AS total,
    count(*) FILTER (WHERE u.is_disabled)::int AS disabled,
    count(*) FILTER (
        WHERE NOT u.is_disabled
          AND (u.lockout_end IS NULL OR u.lockout_end <= @now::timestamptz)
          AND (
              NOT @scim_enabled::boolean
              OR m.upstream_active IS NOT FALSE
              OR EXISTS (
                  SELECT 1 FROM identity.user_roles ur
                  JOIN identity.roles r ON r.id = ur.role_id
                  WHERE ur.user_id = u.id AND r.name = 'Owner'
              )
          )
    )::int AS active
FROM identity.users u
LEFT JOIN identity.scim_user_mappings m ON m.user_id = u.id;

-- name: CountPendingInvitations :one
-- CountPendingInvitations is SystemStatus's pendingInvitations (:140-142):
-- not accepted, not revoked, not yet expired at @now.
SELECT count(*)::int
FROM identity.invitations
WHERE accepted_at IS NULL AND revoked_at IS NULL AND expires_at > @now::timestamptz;

-- name: GetAuthorityMode :one
-- GetAuthorityMode is the authority mode identity's API process recorded,
-- null when none has been (no row yet, or a row from before 00050).
SELECT s.authority_mode
FROM (VALUES (1)) AS one (x)
LEFT JOIN identity.system_settings s ON s.id = 1;

-- name: EnsureSystemSettings :exec
-- EnsureSystemSettings creates the singleton row, maintenance off, when
-- there is none, so the authority mode has a row to be recorded on; an
-- existing row is left as it is.
INSERT INTO identity.system_settings (id, maintenance_enabled, updated_at)
VALUES (1, false, @now::timestamptz)
ON CONFLICT (id) DO NOTHING;

-- name: LockAuthorityMode :one
-- LockAuthorityMode reads the recorded mode holding the singleton row, so
-- two API processes starting at once record and audit one change each.
SELECT authority_mode FROM identity.system_settings WHERE id = 1 FOR UPDATE;

-- name: SetAuthorityMode :exec
UPDATE identity.system_settings
SET authority_mode = @authority_mode::text, authority_mode_at = @now::timestamptz
WHERE id = 1;

-- name: GetBusinessTimeZone :one
-- GetBusinessTimeZone is the workspace's business time zone (time zones
-- design D2), read by contracts.BusinessCalendar on every call. The
-- singleton row may not exist yet — maintenance was never set and no API
-- process has recorded an authority mode — so the LEFT JOIN answers NULL
-- then, and the caller reads NULL as the column's default, Europe/Oslo.
SELECT s.business_time_zone
FROM (VALUES (1)) AS one (x)
LEFT JOIN identity.system_settings s ON s.id = 1;

-- name: LockBusinessTimeZone :one
-- LockBusinessTimeZone reads the zone holding the singleton row, which the
-- caller has just ensured exists (EnsureSystemSettings), so two PUTs at once
-- queue and each audits the change it actually made.
SELECT business_time_zone FROM identity.system_settings WHERE id = 1 FOR UPDATE;

-- name: SetBusinessTimeZone :exec
UPDATE identity.system_settings
SET business_time_zone = @business_time_zone::text, updated_at = @now::timestamptz, updated_by_user_id = @updated_by_user_id
WHERE id = 1;

-- name: ResolveTimeZone :one
-- ResolveTimeZone asks Postgres what a zone name means, at a winter instant
-- and a summer one, for the caller to compare with Go's reading of the same
-- name: the two carry their own tzdata, and Postgres resolves
-- pg_timezone_abbrevs before pg_timezone_names, so 'CET' is a fixed +01:00 to
-- it and a zone with summer time to Go. It was Expenses' and Staff's own
-- question while each held a zone of its own; the workspace's zone is
-- checked once, here. A name Postgres does not know raises 22023.
WITH probe AS (
    SELECT @name::text AS zone, @winter::timestamptz AS winter, @summer::timestamptz AS summer
)
SELECT
    EXTRACT(EPOCH FROM ((winter AT TIME ZONE zone) - (winter AT TIME ZONE 'UTC')))::bigint AS winter_offset,
    EXTRACT(EPOCH FROM ((summer AT TIME ZONE zone) - (summer AT TIME ZONE 'UTC')))::bigint AS summer_offset
FROM probe;
