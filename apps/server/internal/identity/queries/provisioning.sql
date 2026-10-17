-- name: ListProvisioningRoles :many
-- ListProvisioningRoles is the set of roles Staff may give the users it
-- provisions, by name ordinally, then by id.
SELECT r.id, r.name, r.display_name
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
