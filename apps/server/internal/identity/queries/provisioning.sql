-- name: ListProvisioningRoles :many
-- ListProvisioningRoles is the set of roles Staff may give the users it
-- provisions, by name ordinally, then by id, each with the permission keys
-- it carries in ordinal order.
SELECT r.id, r.name, r.display_name,
       array(SELECT rp.permission_key FROM identity.role_permissions rp
             WHERE rp.role_id = r.id ORDER BY rp.permission_key COLLATE "C")::text[] AS permission_keys
FROM identity.provisioning_roles p
JOIN identity.roles r ON r.id = p.role_id
ORDER BY r.name COLLATE "C", r.id;

-- name: LockAssignableRoles :many
-- LockAssignableRoles is which of @ids name a custom role (neither system nor
-- built in), each taken FOR KEY SHARE so it cannot be deleted before the
-- set that names it commits.
SELECT id FROM identity.roles
WHERE id = ANY (@ids::uuid[]) AND NOT is_system AND NOT is_built_in
FOR KEY SHARE;

-- name: FirstDeniedRolePermission :one
-- FirstDeniedRolePermission is the first of @role_ids, by name, carrying a
-- key of @keys, and that key: the roles Staff may never give.
SELECT r.name, rp.permission_key
FROM identity.role_permissions rp
JOIN identity.roles r ON r.id = rp.role_id
WHERE rp.role_id = ANY (@role_ids::uuid[]) AND rp.permission_key = ANY (@keys::text[])
ORDER BY r.name COLLATE "C", rp.permission_key COLLATE "C"
LIMIT 1;

-- name: DeleteProvisioningRolesExcept :exec
-- DeleteProvisioningRolesExcept removes every provisioning role not in
-- @role_ids, half of replacing the set.
DELETE FROM identity.provisioning_roles WHERE role_id <> ALL (@role_ids::uuid[]);

-- name: InsertProvisioningRoles :exec
-- InsertProvisioningRoles adds the roles of @role_ids not in the set yet,
-- the other half; a role kept keeps when it was named.
INSERT INTO identity.provisioning_roles (role_id, created_at)
SELECT unnest(@role_ids::uuid[]), @now::timestamptz
ON CONFLICT (role_id) DO NOTHING;
