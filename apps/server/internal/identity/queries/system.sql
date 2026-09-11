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
