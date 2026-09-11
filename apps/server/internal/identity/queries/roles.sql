-- name: UserHasRoleByName :one
-- UserHasRoleByName reports whether the user directly holds the named role
-- (user_roles only; access-group roles never grant a built-in role).
SELECT EXISTS (
    SELECT 1
    FROM identity.user_roles ur
    JOIN identity.roles r ON r.id = ur.role_id
    WHERE ur.user_id = @user_id AND r.name = @name
) AS has_role;

-- name: EffectivePermissionForUser :one
-- EffectivePermissionForUser is the permission: check's table lookup
-- (AZ/PermissionAuthorization.cs:56-99). It is false for a disabled user
-- and for one locked out at the now parameter (:56-61). Otherwise the
-- effective roles are the user's direct roles plus the roles mapped to every
-- active access group the user is effectively a member of (a forced member,
-- or one with no override whose upstream membership is present) where the
-- mapping's source equals the group's (:80-96); the user has the permission
-- when any of those roles carries its key (:97-99). The Owner short-circuit
-- (:70-74) is the caller's, since it must follow the lockout check but skip
-- this lookup.
SELECT EXISTS (
    SELECT 1
    FROM identity.users u
    WHERE u.id = @user_id
      AND NOT u.is_disabled
      AND (u.lockout_end IS NULL OR u.lockout_end <= @now::timestamptz)
      AND EXISTS (
          SELECT 1
          FROM identity.role_permissions rp
          WHERE rp.permission_key = @permission_key
            AND rp.role_id IN (
                SELECT ur.role_id
                FROM identity.user_roles ur
                WHERE ur.user_id = u.id
                UNION
                SELECT gm_map.role_id
                FROM identity.access_group_memberships gm
                JOIN identity.access_groups g ON g.id = gm.group_id AND g.is_active
                JOIN identity.access_group_role_mappings gm_map ON gm_map.group_id = g.id AND gm_map.source = g.source
                WHERE gm.user_id = u.id
                  AND (gm.membership_override = 'force_member'
                       OR (gm.membership_override IS NULL AND gm.is_upstream_present))
            )
      )
) AS allowed;
