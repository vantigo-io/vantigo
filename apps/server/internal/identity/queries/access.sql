-- name: AcquireRoleMutationLock :exec
-- AcquireRoleMutationLock takes one role's transaction advisory lock
-- (AZ/AuthorizationMutationService.cs:20-25); the key is
-- roleMutationLockKey's.
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: ListManageableRoles :many
-- ListManageableRoles is GET /access/roles (EA/AuthorizationManagementEndpoints.cs:61-104):
-- every role but SystemAdmin, by name (ordinal), with its permission keys
-- in ordinal order.
SELECT r.id, r.name, r.normalized_name, r.display_name, r.description, r.is_system, r.is_built_in,
       r.steward_user_id, r.version,
       coalesce((
           SELECT array_agg(rp.permission_key ORDER BY rp.permission_key COLLATE "C")
           FROM identity.role_permissions rp
           WHERE rp.role_id = r.id
       ), '{}')::text[] AS permissions
FROM identity.roles r
WHERE r.name <> 'SystemAdmin'
ORDER BY r.name COLLATE "C", r.id;

-- name: LockRole :one
-- LockRole reads a role and holds its row until the transaction ends, so
-- no assignment can take it (AssignUserRoleLocks) while it is deleted.
SELECT id, name, normalized_name, display_name, description, is_system, is_built_in,
       steward_user_id, version, created_at, updated_at
FROM identity.roles
WHERE id = @id
FOR UPDATE;

-- name: RoleNameTaken :one
-- RoleNameTaken reports whether a role other than @id has the normalized
-- name; a create passes the nil uuid.
SELECT EXISTS (
    SELECT 1 FROM identity.roles
    WHERE normalized_name = @normalized_name AND id <> @id
) AS taken;

-- name: InsertRole :exec
INSERT INTO identity.roles (
    id, name, normalized_name, display_name, description, is_system, is_built_in,
    steward_user_id, version, created_at, updated_at
) VALUES (
    @id, @name, @normalized_name, @display_name, @description, false, false,
    @steward_user_id, @version, @now::timestamptz, @now::timestamptz
);

-- name: UpdateRole :execrows
-- UpdateRole writes a role's metadata and its new version, only while it
-- still has the version the caller read: zero rows is a concurrent change.
UPDATE identity.roles
SET name = @name, normalized_name = @normalized_name, display_name = @display_name,
    description = @description, version = @version, updated_at = @now::timestamptz
WHERE id = @id AND version = @expected_version;

-- name: GetRolePermissionKeys :many
SELECT permission_key
FROM identity.role_permissions
WHERE role_id = @role_id
ORDER BY permission_key COLLATE "C";

-- name: DeleteRolePermissions :exec
DELETE FROM identity.role_permissions WHERE role_id = @role_id;

-- name: InsertRolePermissions :exec
-- InsertRolePermissions gives the role each key once.
INSERT INTO identity.role_permissions (role_id, permission_key)
SELECT @role_id, k FROM unnest(@keys::text[]) AS k
ON CONFLICT DO NOTHING;

-- name: RoleIsMapped :one
-- RoleIsMapped reports whether any access group maps the role.
SELECT EXISTS (
    SELECT 1 FROM identity.access_group_role_mappings WHERE role_id = @role_id
) AS mapped;

-- name: RoleIsAssigned :one
-- RoleIsAssigned reports whether any user holds the role directly.
SELECT EXISTS (
    SELECT 1 FROM identity.user_roles WHERE role_id = @role_id
) AS assigned;

-- name: DeleteRole :exec
DELETE FROM identity.roles WHERE id = @id;

-- name: LockUserVersion :one
-- LockUserVersion reads a user's version and holds the users row until the
-- transaction ends, so concurrent role replacements for one user take
-- turns and each compares the version the one before it left.
SELECT version FROM identity.users WHERE id = @id FOR UPDATE;

-- name: AssignUserRoleLocks :many
-- AssignUserRoleLocks reads the roles an assignment names and holds a key
-- share on each until the transaction ends: a role deleted first is missing
-- here, and one read here cannot be deleted before the assignment commits.
SELECT id, is_system, is_built_in
FROM identity.roles
WHERE id = ANY(@ids::uuid[])
FOR KEY SHARE;

-- name: ListUserRoleAssignments :many
-- ListUserRoleAssignments is a user's direct roles, by name (ordinal).
SELECT r.id, r.name, r.is_system, r.is_built_in
FROM identity.user_roles ur
JOIN identity.roles r ON r.id = ur.role_id
WHERE ur.user_id = @user_id
ORDER BY r.name COLLATE "C", r.id;

-- name: ListUserDirectPermissionKeys :many
-- ListUserDirectPermissionKeys is the distinct permission keys of a user's
-- direct roles only, in ordinal order: /access/me's projection
-- (EA/AuthorizationManagementEndpoints.cs:529-543), which leaves out the
-- roles access groups grant.
SELECT k.permission_key
FROM (
    SELECT DISTINCT rp.permission_key
    FROM identity.user_roles ur
    JOIN identity.role_permissions rp ON rp.role_id = ur.role_id
    WHERE ur.user_id = @user_id
) k
ORDER BY k.permission_key COLLATE "C";

-- name: ListAccessUsers :many
-- ListAccessUsers is the authorization user directory
-- (EA/AuthorizationManagementEndpoints.cs:475-487): every user, by display
-- name (ordinal), then id.
SELECT id, display_name, email, is_disabled, lockout_end, version
FROM identity.users
ORDER BY display_name COLLATE "C", id;

-- name: ListAllUserRoleAssignments :many
-- ListAllUserRoleAssignments is every direct role assignment, each user's
-- by role name (ordinal).
SELECT ur.user_id, r.id AS role_id, r.name
FROM identity.user_roles ur
JOIN identity.roles r ON r.id = ur.role_id
ORDER BY ur.user_id, r.name COLLATE "C", r.id;

-- name: ListAuditEvents :many
-- ListAuditEvents is GET /access/audit (EA/AuthorizationManagementEndpoints.cs:552-554):
-- the latest 500 rows, newest first; id breaks ties between rows written at
-- the same instant, the later write first.
SELECT id, actor_user_id, target_user_id, target_role_id, action, details,
       coalesce(before_json, '') AS before_json, coalesce(after_json, '') AS after_json,
       coalesce(correlation_id, '') AS correlation_id, mfa_authenticated, occurred_at
FROM identity.authorization_audit_events
ORDER BY occurred_at DESC, id DESC
LIMIT 500;
