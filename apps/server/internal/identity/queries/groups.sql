-- name: ListAccessGroups :many
-- ListAccessGroups is GET /access/groups
-- (EA/IdentityControlPlaneEndpoints.cs:40-45): every group, SCIM groups
-- included, by display name (ordinal), then id, each with its effective
-- members and its mapped roles, all in this one query, so listing any
-- number of groups costs the same round-trips
-- (SV/AccessGroupManagementService.cs:249-280). A member is effective when
-- forced in, or when upstream-present with no override (:260-262).
SELECT g.id, g.display_name, g.source, g.is_active, g.version, g.created_at, g.updated_at,
       coalesce((
           SELECT array_agg(m.user_id ORDER BY m.user_id)
           FROM identity.access_group_memberships m
           WHERE m.group_id = g.id
             AND (m.membership_override = 'force_member'
                  OR (m.membership_override IS NULL AND m.is_upstream_present))
       ), '{}')::uuid[] AS member_user_ids,
       coalesce((
           SELECT array_agg(rm.role_id ORDER BY rm.role_id)
           FROM identity.access_group_role_mappings rm
           WHERE rm.group_id = g.id
       ), '{}')::uuid[] AS role_ids
FROM identity.access_groups g
ORDER BY g.display_name COLLATE "C", g.id;

-- name: GetAccessGroup :one
-- GetAccessGroup is one group as ListAccessGroups reads each
-- (SV/AccessGroupManagementService.cs:282-297), in one query.
SELECT g.id, g.display_name, g.source, g.is_active, g.version, g.created_at, g.updated_at,
       coalesce((
           SELECT array_agg(m.user_id ORDER BY m.user_id)
           FROM identity.access_group_memberships m
           WHERE m.group_id = g.id
             AND (m.membership_override = 'force_member'
                  OR (m.membership_override IS NULL AND m.is_upstream_present))
       ), '{}')::uuid[] AS member_user_ids,
       coalesce((
           SELECT array_agg(rm.role_id ORDER BY rm.role_id)
           FROM identity.access_group_role_mappings rm
           WHERE rm.group_id = g.id
       ), '{}')::uuid[] AS role_ids
FROM identity.access_groups g
WHERE g.id = @id;

-- name: LockAccessGroup :one
-- LockAccessGroup reads a group a mutation is about to change and holds its
-- row FOR UPDATE until the transaction ends, so mutations of one group take
-- turns and each compares the version the one before it left.
SELECT id, display_name, source, external_id, is_active, version, created_at, updated_at
FROM identity.access_groups
WHERE id = @id
FOR UPDATE;

-- name: AccessGroupNameTaken :one
-- AccessGroupNameTaken reports whether a group other than @id has exactly
-- the display name (EA/IdentityControlPlaneEndpoints.cs:59, :78); a create
-- passes the nil uuid.
SELECT EXISTS (
    SELECT 1 FROM identity.access_groups
    WHERE display_name = @display_name AND id <> @id
) AS taken;

-- name: InsertAccessGroup :exec
-- InsertAccessGroup creates a local group (SV/AccessGroupManagementService.cs:28-50).
INSERT INTO identity.access_groups (id, display_name, source, is_active, version, created_at, updated_at)
VALUES (@id, @display_name, 'local', @is_active, @version, @now::timestamptz, @now::timestamptz);

-- name: UpdateAccessGroup :execrows
-- UpdateAccessGroup writes a group's name, state and new version, only while
-- it still has the version the caller read: zero rows is a concurrent change.
UPDATE identity.access_groups
SET display_name = @display_name, is_active = @is_active, version = @version, updated_at = @now::timestamptz
WHERE id = @id AND version = @expected_version;

-- name: TouchAccessGroup :execrows
-- TouchAccessGroup gives a group whose members or role mappings changed a
-- new version (SV/AccessGroupManagementService.cs:126-127, :150-151,
-- :212-213, :241-242), only while it still has the version the caller read.
UPDATE identity.access_groups
SET version = @version, updated_at = @now::timestamptz
WHERE id = @id AND version = @expected_version;

-- name: DeleteAccessGroup :execrows
-- DeleteAccessGroup deletes a group, its memberships and its role mappings
-- (they cascade), only while it still has the version the caller read.
DELETE FROM identity.access_groups WHERE id = @id AND version = @expected_version;

-- name: KeyShareMember :one
-- KeyShareMember reads the user a membership is about to name and holds a
-- key share on the row until the transaction ends, so the user cannot be
-- deleted before the membership commits.
SELECT id FROM identity.users WHERE id = @id FOR KEY SHARE;

-- name: UpsertForcedMembership :exec
-- UpsertForcedMembership makes the user a local forced member of the group,
-- whatever the row said before (SV/AccessGroupManagementService.cs:110-124).
INSERT INTO identity.access_group_memberships (group_id, user_id, source, is_upstream_present, membership_override)
VALUES (@group_id, @user_id, 'local', false, 'force_member')
ON CONFLICT (group_id, user_id) DO UPDATE
SET source = 'local', is_upstream_present = false, membership_override = 'force_member';

-- name: DeleteMembership :exec
DELETE FROM identity.access_group_memberships WHERE group_id = @group_id AND user_id = @user_id;

-- name: GetMappingRole :one
-- GetMappingRole is what mapping a role to a group checks
-- (SV/AccessGroupManagementService.cs:174-197): the role's name, its
-- protection, and its permission keys. The caller holds the role's lock, so
-- no edit or deletion of the role is in flight.
SELECT r.id, r.name, r.is_system, r.is_built_in,
       coalesce((
           SELECT array_agg(rp.permission_key ORDER BY rp.permission_key COLLATE "C")
           FROM identity.role_permissions rp
           WHERE rp.role_id = r.id
       ), '{}')::text[] AS permission_keys
FROM identity.roles r
WHERE r.id = @id;

-- name: InsertRoleMapping :exec
-- InsertRoleMapping maps the role to the group with the group's source, and
-- leaves a mapping that already exists as it is
-- (SV/AccessGroupManagementService.cs:199-210).
INSERT INTO identity.access_group_role_mappings (group_id, role_id, source)
VALUES (@group_id, @role_id, @source)
ON CONFLICT (group_id, role_id) DO NOTHING;

-- name: DeleteRoleMapping :exec
DELETE FROM identity.access_group_role_mappings WHERE group_id = @group_id AND role_id = @role_id;
