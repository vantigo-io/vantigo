-- name: ListManagedUsers :many
-- ListManagedUsers is owner user management's view of every user, or only
-- of the user id names (EA/AuthAccountEndpoints.cs:401-457): the Owners
-- first, then by display name ordinally, then by id. is_owner decides the
-- managed role (EA/AuthAccountState.cs:105-110); sso_enabled is whether the
-- user is linked to the workforce OIDC provider while one is configured.
SELECT u.id, u.email, u.display_name, u.is_disabled, u.lockout_end, u.totp_enabled,
       EXISTS (
           SELECT 1
           FROM identity.user_roles ur
           JOIN identity.roles r ON r.id = ur.role_id
           WHERE ur.user_id = u.id AND r.name = 'Owner'
       )::boolean AS is_owner,
       EXISTS (SELECT 1 FROM identity.profile_avatars a WHERE a.user_id = u.id)::boolean AS has_avatar,
       (@oidc_enabled::boolean AND EXISTS (SELECT 1 FROM identity.oidc_links l WHERE l.user_id = u.id))::boolean AS sso_enabled
FROM identity.users u
WHERE sqlc.narg(id)::uuid IS NULL OR u.id = sqlc.narg(id)::uuid
ORDER BY is_owner DESC, u.display_name COLLATE "C", u.id;

-- name: IsLastActiveOwner :one
-- IsLastActiveOwner is .NET's AuthAccountState.IsLastActiveOwner
-- (EA/AuthAccountState.cs:27-62): the user is an active Owner (neither
-- disabled nor locked out at now) and no other active Owner exists. Callers
-- hold the owner lock, so the answer holds until they commit.
WITH active_owners AS (
    SELECT u.id
    FROM identity.users u
    JOIN identity.user_roles ur ON ur.user_id = u.id
    JOIN identity.roles r ON r.id = ur.role_id
    WHERE r.name = 'Owner'
      AND NOT u.is_disabled
      AND (u.lockout_end IS NULL OR u.lockout_end <= @now::timestamptz)
)
SELECT (EXISTS (SELECT 1 FROM active_owners WHERE id = @user_id::uuid)
        AND (SELECT count(*) FROM active_owners) <= 1)::boolean AS last_active_owner;

-- name: GetUserRoleNames :many
SELECT r.name
FROM identity.user_roles ur
JOIN identity.roles r ON r.id = ur.role_id
WHERE ur.user_id = @user_id
ORDER BY r.name COLLATE "C";

-- name: RemoveUserRoles :exec
DELETE FROM identity.user_roles
WHERE user_id = @user_id AND role_id = ANY (@role_ids::uuid[]);

-- name: UpdateManagedUser :exec
-- UpdateManagedUser is an Owner's edit of another user's display name and
-- email (EA/AuthAccountEndpoints.cs:621-624). An email an Owner set counts
-- as confirmed. version rotates, as UserManager.UpdateAsync rotated the
-- concurrency stamp.
UPDATE identity.users
SET display_name = @display_name,
    email = @email,
    normalized_email = @normalized_email,
    email_confirmed = true,
    version = @version,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: SetUserDisabled :exec
UPDATE identity.users
SET is_disabled = @is_disabled, version = @version, updated_at = @now::timestamptz
WHERE id = @id;

-- name: DeleteUser :exec
-- DeleteUser removes the user and, by cascade, their roles, sessions,
-- credentials and avatar. A SCIM mapping refuses it (ON DELETE RESTRICT).
DELETE FROM identity.users WHERE id = @id;

-- name: HasScimMapping :one
SELECT (EXISTS (SELECT 1 FROM identity.scim_user_mappings WHERE user_id = @user_id))::boolean AS has_mapping;

-- name: GetUserAuthorizationSnapshot :one
-- GetUserAuthorizationSnapshot is what .NET's
-- AuthorizationAuditWriter.CaptureUserAsync recorded of a user
-- (AZ/AuthorizationAuditWriter.cs:47-73): their direct roles and the
-- distinct permission keys those roles carry, both in ordinal order, and
-- whether they are disabled. The caller replaces the keys with "*" for an
-- Owner.
SELECT u.is_disabled, r.role_names, p.permission_keys
FROM identity.users u
CROSS JOIN LATERAL (
    SELECT coalesce(array_agg(ro.name ORDER BY ro.name COLLATE "C"), '{}')::text[] AS role_names
    FROM identity.user_roles ur
    JOIN identity.roles ro ON ro.id = ur.role_id
    WHERE ur.user_id = u.id
) r
CROSS JOIN LATERAL (
    SELECT coalesce(array_agg(k.permission_key ORDER BY k.permission_key), '{}')::text[] AS permission_keys
    FROM (
        SELECT DISTINCT rp.permission_key COLLATE "C" AS permission_key
        FROM identity.user_roles ur
        JOIN identity.role_permissions rp ON rp.role_id = ur.role_id
        WHERE ur.user_id = u.id
    ) k
) p
WHERE u.id = @id;
