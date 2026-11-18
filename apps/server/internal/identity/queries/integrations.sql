-- name: InsertIntegrationUser :exec
-- InsertIntegrationUser makes the user of an integration, API access design
-- K1: kind integration, a confirmed address that never routes, no
-- credential and no role, and pending_since left null.
INSERT INTO identity.users (
    id, email, normalized_email, email_confirmed, display_name, kind, version, created_at, updated_at
) VALUES (
    @id, @email, @normalized_email, true, @display_name, 'integration', @version, @now::timestamptz, @now::timestamptz
);

-- name: InsertIntegration :exec
INSERT INTO identity.integrations (
    user_id, name, description, contact_user_id, created_by, created_at, version
) VALUES (
    sqlc.arg(user_id), sqlc.arg(name), sqlc.narg(description), sqlc.narg(contact_user_id), sqlc.narg(created_by), sqlc.arg(now)::timestamptz, sqlc.arg(version)
);

-- name: ListIntegrations :many
-- ListIntegrations is every integration, archived ones last, each with its
-- user's state, its roles, the number of keys in force at now, the nearest
-- expiry among them and the last time any of its keys was used.
SELECT i.user_id, i.name, i.description, i.contact_user_id, c.display_name AS contact_display_name,
       i.rate_limit_per_minute, i.created_by, i.created_at, i.archived_at, i.archived_by, i.version,
       u.is_disabled, r.role_ids, r.role_names, r.role_display_names,
       k.live_keys, k.nearest_expiry, k.last_used_at
FROM identity.integrations i
JOIN identity.users u ON u.id = i.user_id
LEFT JOIN identity.users c ON c.id = i.contact_user_id
CROSS JOIN LATERAL (
    SELECT coalesce(array_agg(ro.id ORDER BY ro.name), '{}')::uuid[] AS role_ids,
           coalesce(array_agg(ro.name ORDER BY ro.name), '{}')::text[] AS role_names,
           coalesce(array_agg(ro.display_name ORDER BY ro.name), '{}')::text[] AS role_display_names
    FROM identity.user_roles ur
    JOIN identity.roles ro ON ro.id = ur.role_id
    WHERE ur.user_id = i.user_id
) r
LEFT JOIN LATERAL (
    SELECT count(*) FILTER (WHERE ak.revoked_at IS NULL AND ak.expires_at > sqlc.arg(now)::timestamptz)::int AS live_keys,
           min(ak.expires_at) FILTER (WHERE ak.revoked_at IS NULL AND ak.expires_at > sqlc.arg(now)::timestamptz) AS nearest_expiry,
           max(ak.last_used_at) AS last_used_at
    FROM identity.api_keys ak
    WHERE ak.integration_id = i.user_id
) k ON true
WHERE sqlc.narg(id)::uuid IS NULL OR i.user_id = sqlc.narg(id)::uuid
ORDER BY i.archived_at IS NOT NULL, lower(i.name), i.user_id;

-- name: LockIntegration :one
-- LockIntegration holds an integration's row, and its user's, for a write.
SELECT i.user_id, i.name, i.description, i.contact_user_id, i.rate_limit_per_minute,
       i.archived_at, i.version, u.is_disabled
FROM identity.integrations i
JOIN identity.users u ON u.id = i.user_id
WHERE i.user_id = @user_id
FOR UPDATE OF i, u;

-- name: UpdateIntegration :exec
UPDATE identity.integrations
SET name = sqlc.arg(name),
    description = sqlc.narg(description),
    contact_user_id = sqlc.narg(contact_user_id),
    rate_limit_per_minute = sqlc.arg(rate_limit_per_minute),
    version = sqlc.arg(version)
WHERE user_id = sqlc.arg(user_id);

-- name: TouchIntegrationVersion :exec
UPDATE identity.integrations SET version = @version WHERE user_id = @user_id;

-- name: SetIntegrationUser :exec
-- SetIntegrationUser keeps the integration's user in step: its display name
-- is the integration's name, and disabling it refuses every key.
UPDATE identity.users
SET display_name = @display_name, is_disabled = @is_disabled, version = @version, updated_at = @now::timestamptz
WHERE id = @id AND kind = 'integration';

-- name: ArchiveIntegration :exec
UPDATE identity.integrations
SET archived_at = sqlc.arg(now)::timestamptz, archived_by = sqlc.narg(archived_by), version = sqlc.arg(version)
WHERE user_id = sqlc.arg(user_id);

-- name: IntegrationNameTaken :one
-- IntegrationNameTaken reports whether another integration that is not
-- archived has the name, compared without regard to case.
SELECT EXISTS (
    SELECT 1 FROM identity.integrations
    WHERE lower(name) = lower(@name) AND archived_at IS NULL AND user_id <> @user_id
) AS taken;

-- name: SetIntegrationRolesLicence :exec
-- SetIntegrationRolesLicence is the integrations service's licence to write
-- this one integration's roles, for the rest of the transaction only
-- (00194's refuse_integration_role_write, API access design K3, K55).
SELECT set_config('vantigo.integration_roles', @user_id::text, true);

-- name: RoleHeldByIntegration :one
-- RoleHeldByIntegration reports whether any integration holds the role
-- directly, which makes it no delegate's to widen (API access design K56).
SELECT EXISTS (
    SELECT 1
    FROM identity.user_roles ur
    JOIN identity.users u ON u.id = ur.user_id AND u.kind = 'integration'
    WHERE ur.role_id = @role_id
) AS held;

-- name: EffectivePermissionsForUser :many
-- EffectivePermissionsForUser is every permission key the user holds through
-- an effective role, in ordinal order: the direct roles and the roles mapped
-- to every active access group the user is effectively a member of, as
-- EffectivePermissionForUser decides one key. It reads no user state; the
-- caller has.
SELECT k.permission_key
FROM (
    SELECT DISTINCT rp.permission_key
    FROM identity.role_permissions rp
    WHERE rp.role_id IN (
        SELECT ur.role_id
        FROM identity.user_roles ur
        WHERE ur.user_id = @user_id
        UNION
        SELECT gm_map.role_id
        FROM identity.access_group_memberships gm
        JOIN identity.access_groups g ON g.id = gm.group_id AND g.is_active
        JOIN identity.access_group_role_mappings gm_map ON gm_map.group_id = g.id AND gm_map.source = g.source
        WHERE gm.user_id = @user_id
          AND (gm.membership_override = 'force_member'
               OR (gm.membership_override IS NULL AND gm.is_upstream_present))
    )
) k
ORDER BY k.permission_key COLLATE "C";
