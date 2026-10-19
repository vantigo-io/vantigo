-- name: DirectoryUser :one
SELECT id, display_name, is_disabled, coalesce(preferred_language, '')::text AS preferred_language
FROM identity.users WHERE id = $1;

-- name: DirectoryUsers :many
SELECT id, display_name, is_disabled, coalesce(preferred_language, '')::text AS preferred_language
FROM identity.users WHERE id = ANY($1::uuid[]);

-- name: DirectorySearchUsers :many
SELECT id, display_name, is_disabled, coalesce(preferred_language, '')::text AS preferred_language
FROM identity.users
WHERE NOT is_disabled AND display_name ILIKE '%' || sqlc.arg(pattern)::text || '%' ESCAPE '\'
ORDER BY display_name, id
LIMIT sqlc.arg(max_rows);

-- name: DirectoryPermissionHolders :many
-- DirectoryPermissionHolders is contracts.UserDirectory.PermissionHolders:
-- the active users holding permission_key. Active is what a session needs
-- (sessions.sql's GetSessionByTokenHash): not disabled, not pending, and,
-- while SCIM is configured, not deactivated by the directory unless an
-- Owner; a lockout is ignored. A user holds the key as an Owner (every key,
-- access.go's permitted), or through a role among their direct roles and the
-- roles mapped to every active access group they are effectively a member of
-- — exactly EffectivePermissionForUser's rule (roles.sql). A delegation
-- grants no key.
SELECT u.id, u.display_name, u.email, coalesce(u.preferred_language, '')::text AS preferred_language
FROM identity.users u
CROSS JOIN LATERAL (
    SELECT EXISTS (
        SELECT 1
        FROM identity.user_roles ur
        JOIN identity.roles r ON r.id = ur.role_id
        WHERE ur.user_id = u.id AND r.name = 'Owner'
    ) AS is_owner
) o
LEFT JOIN identity.scim_user_mappings m ON m.user_id = u.id
WHERE NOT u.is_disabled
  AND u.pending_since IS NULL
  AND (NOT @scim_enabled::boolean OR m.upstream_active IS NOT FALSE OR o.is_owner)
  AND (o.is_owner OR EXISTS (
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
  ))
ORDER BY u.display_name, u.id;

-- name: DirectoryUserForNotice :one
-- DirectoryUserForNotice is contracts.UserDirectory.Notify's one read: the
-- address and language of the user, when active as DirectoryPermissionHolders
-- means it — not disabled, not pending, and, while SCIM is configured, not
-- deactivated by the directory unless an Owner. No row for a user who is not.
SELECT u.email, coalesce(u.preferred_language, '')::text AS preferred_language
FROM identity.users u
LEFT JOIN identity.scim_user_mappings m ON m.user_id = u.id
WHERE u.id = @id
  AND NOT u.is_disabled
  AND u.pending_since IS NULL
  AND (NOT @scim_enabled::boolean OR m.upstream_active IS NOT FALSE OR EXISTS (
      SELECT 1
      FROM identity.user_roles ur
      JOIN identity.roles r ON r.id = ur.role_id
      WHERE ur.user_id = u.id AND r.name = 'Owner'
  ));
