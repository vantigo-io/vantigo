-- name: AcquireScimLock :exec
-- AcquireScimLock takes the transaction advisory lock every SCIM write
-- serialises on. The installation has one SCIM connection, whose state .NET
-- changed in serializable transactions (SV/ScimProtocolService.cs:123-124);
-- here each write reads what it checks after this lock, at READ COMMITTED,
-- so a check and the write it guards never interleave with another's.
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: GetScimUser :one
-- GetScimUser is a SCIM User as ReadUserResourceAsync reads it
-- (SV/ScimProtocolService.cs:586-610): the mapping and its user.
SELECT m.resource_id, m.user_id, m.external_id, m.user_name, m.upstream_active, m.source_profile,
       m.etag, m.created_at, m.updated_at, u.email, u.display_name
FROM identity.scim_user_mappings m
JOIN identity.users u ON u.id = m.user_id
WHERE m.resource_id = @resource_id;

-- name: LockScimUser :one
-- LockScimUser is GetScimUser for a write: it holds the mapping's and the
-- user's rows until the transaction ends, so an Owner's edit or promotion of
-- the user waits for the SCIM write or the SCIM write waits for it.
SELECT m.resource_id, m.user_id, m.external_id, m.user_name, m.upstream_active, m.source_profile,
       m.etag, m.created_at, m.updated_at, u.email, u.display_name
FROM identity.scim_user_mappings m
JOIN identity.users u ON u.id = m.user_id
WHERE m.resource_id = @resource_id
FOR NO KEY UPDATE;

-- name: LockScimUserByExternalID :one
-- LockScimUserByExternalID is LockScimUser by the correlation key, the
-- lookup a create makes inside its transaction (SV/ScimProtocolService.cs:127-128).
SELECT m.resource_id, m.user_id, m.external_id, m.user_name, m.upstream_active, m.source_profile,
       m.etag, m.created_at, m.updated_at, u.email, u.display_name
FROM identity.scim_user_mappings m
JOIN identity.users u ON u.id = m.user_id
WHERE m.external_id = @external_id
FOR NO KEY UPDATE;

-- name: ScimUserNameTaken :one
-- ScimUserNameTaken reports whether a mapping other than @resource_id has
-- exactly the userName (SV/ScimProtocolService.cs:690-692); a create passes
-- the nil uuid.
SELECT EXISTS (
    SELECT 1 FROM identity.scim_user_mappings
    WHERE user_name = @user_name AND resource_id <> @resource_id
) AS taken;

-- name: ScimExternalIDTaken :one
-- ScimExternalIDTaken is ScimUserNameTaken for the externalId
-- (SV/ScimProtocolService.cs:693-695).
SELECT EXISTS (
    SELECT 1 FROM identity.scim_user_mappings
    WHERE external_id = @external_id AND resource_id <> @resource_id
) AS taken;

-- name: CountScimUsers :one
-- CountScimUsers counts the Users a list's filter admits. Each filter clause
-- is an equality every row must meet (SV/ScimProtocolService.cs:208-214); an
-- empty array admits every row.
SELECT count(*)
FROM identity.scim_user_mappings m
WHERE m.user_name = ALL (@user_names::text[])
  AND m.external_id = ALL (@external_ids::text[]);

-- name: ListScimUsers :many
-- ListScimUsers is one page of CountScimUsers' rows, by resource id
-- (SV/ScimProtocolService.cs:215-216).
SELECT m.resource_id, m.user_id, m.external_id, m.user_name, m.upstream_active, m.source_profile,
       m.etag, m.created_at, m.updated_at, u.email, u.display_name
FROM identity.scim_user_mappings m
JOIN identity.users u ON u.id = m.user_id
WHERE m.user_name = ALL (@user_names::text[])
  AND m.external_id = ALL (@external_ids::text[])
ORDER BY m.resource_id
OFFSET @skip::bigint LIMIT @take::bigint;

-- name: InsertScimMapping :exec
INSERT INTO identity.scim_user_mappings (
    resource_id, user_id, external_id, user_name, upstream_active, source_profile, version, etag, created_at, updated_at
) VALUES (
    @resource_id, @user_id, @external_id, @user_name, @upstream_active, @source_profile, 1, @etag, @now::timestamptz, @now::timestamptz
);

-- name: UpdateScimMapping :exec
-- UpdateScimMapping writes a changed mapping with a new ETag, as .NET's
-- Touch did (SV/ScimProtocolService.cs:965-971).
UPDATE identity.scim_user_mappings
SET external_id = @external_id, user_name = @user_name, upstream_active = @upstream_active,
    source_profile = @source_profile, version = version + 1, etag = @etag, updated_at = @now::timestamptz
WHERE resource_id = @resource_id;

-- name: UpdateScimUserAccount :exec
-- UpdateScimUserAccount writes the email and display name SCIM changed on
-- the user's account, with a new version.
UPDATE identity.users
SET email = @email, normalized_email = @normalized_email, display_name = @display_name,
    version = @version, updated_at = @now::timestamptz
WHERE id = @id;

-- name: MarkScimUserMembershipsAbsent :exec
-- MarkScimUserMembershipsAbsent records that upstream no longer lists the
-- user in any SCIM group (SV/ScimProtocolService.cs:346-352). Only the
-- SCIM-sourced rows are touched, and only is_upstream_present: a local
-- override stays as it is.
UPDATE identity.access_group_memberships AS m
SET is_upstream_present = false
FROM identity.access_groups g
WHERE g.id = m.group_id AND g.source = 'scim' AND m.user_id = @user_id AND m.source = 'scim';

-- name: ScimUserGroupIDs :many
-- ScimUserGroupIDs is the SCIM groups upstream lists the user in, a user
-- event's audit facts (SV/ScimProtocolService.cs:987-997).
SELECT g.id
FROM identity.access_group_memberships m
JOIN identity.access_groups g ON g.id = m.group_id
WHERE m.user_id = @user_id AND m.source = 'scim' AND m.is_upstream_present AND g.source = 'scim'
ORDER BY g.id;

-- name: GetScimGroup :one
-- GetScimGroup is a SCIM-sourced group (SV/ScimProtocolService.cs:640-642).
SELECT id, display_name, source, external_id, is_active, version, created_at, updated_at
FROM identity.access_groups
WHERE id = @id AND source = 'scim';

-- name: ScimGroupConflicts :one
-- ScimGroupConflicts is a new SCIM group's duplicate check
-- (SV/ScimProtocolService.cs:368-371): any group with the display name, or
-- a SCIM group with the externalId.
SELECT EXISTS (
    SELECT 1 FROM identity.access_groups
    WHERE display_name = @display_name OR (source = 'scim' AND external_id = @external_id)
) AS conflicts;

-- name: InsertScimGroup :exec
INSERT INTO identity.access_groups (id, display_name, source, external_id, is_active, version, created_at, updated_at)
VALUES (@id, @display_name, 'scim', @external_id, @is_active, @version, @now::timestamptz, @now::timestamptz);

-- name: UpdateScimGroup :exec
UPDATE identity.access_groups
SET display_name = @display_name, is_active = @is_active, version = @version, updated_at = @now::timestamptz
WHERE id = @id;

-- name: CountScimGroups :one
-- CountScimGroups counts the SCIM groups a list's filter admits
-- (SV/ScimProtocolService.cs:426-434), as CountScimUsers does.
SELECT count(*)
FROM identity.access_groups g
WHERE g.source = 'scim'
  AND g.display_name = ALL (@display_names::text[])
  AND g.external_id = ALL (@external_ids::text[]);

-- name: ListScimGroups :many
-- ListScimGroups is one page of CountScimGroups' rows, by id
-- (SV/ScimProtocolService.cs:435-436).
SELECT g.id, g.display_name, g.source, g.external_id, g.is_active, g.version, g.created_at, g.updated_at
FROM identity.access_groups g
WHERE g.source = 'scim'
  AND g.display_name = ALL (@display_names::text[])
  AND g.external_id = ALL (@external_ids::text[])
ORDER BY g.id
OFFSET @skip::bigint LIMIT @take::bigint;

-- name: ScimGroupMembers :many
-- ScimGroupMembers is the members a SCIM Group resource lists
-- (SV/ScimProtocolService.cs:627-635): SCIM-sourced rows forced in, or
-- upstream-present with no override, by the member's resource id, for
-- every group asked for in one query.
SELECT m.group_id, sm.resource_id
FROM identity.access_group_memberships m
JOIN identity.scim_user_mappings sm ON sm.user_id = m.user_id
WHERE m.group_id = ANY (@group_ids::uuid[])
  AND m.source = 'scim'
  AND (m.membership_override = 'force_member' OR (m.membership_override IS NULL AND m.is_upstream_present))
ORDER BY m.group_id, sm.resource_id;

-- name: ScimGroupUpstreamMembers :many
-- ScimGroupUpstreamMembers is the Users upstream lists in the group, a
-- group event's audit facts (SV/ScimProtocolService.cs:999-1008).
SELECT sm.resource_id
FROM identity.access_group_memberships m
JOIN identity.scim_user_mappings sm ON sm.user_id = m.user_id
WHERE m.group_id = @group_id AND m.source = 'scim' AND m.is_upstream_present
ORDER BY sm.resource_id;

-- name: ScimUsersByResourceID :many
-- ScimUsersByResourceID resolves group members' resource ids to their
-- users (SV/ScimProtocolService.cs:649-652), in user id order, the order a
-- membership change writes its rows in.
SELECT resource_id, user_id
FROM identity.scim_user_mappings
WHERE resource_id = ANY (@resource_ids::uuid[])
ORDER BY user_id;

-- name: UpsertScimMembership :exec
-- UpsertScimMembership records what upstream says of one member
-- (SV/ScimProtocolService.cs:653-673): the row becomes SCIM-sourced with
-- is_upstream_present as given, and its override, if any, is kept. A local
-- row carrying an override is left entirely as it is: SCIM never turns a
-- local include or exclude into a SCIM row.
INSERT INTO identity.access_group_memberships AS m (group_id, user_id, source, is_upstream_present)
VALUES (@group_id, @user_id, 'scim', @is_upstream_present)
ON CONFLICT (group_id, user_id) DO UPDATE
SET source = 'scim', is_upstream_present = EXCLUDED.is_upstream_present
WHERE NOT (m.source = 'local' AND m.membership_override IS NOT NULL);

-- name: MarkScimGroupMembershipsAbsent :exec
-- MarkScimGroupMembershipsAbsent records that upstream no longer lists the
-- group's SCIM-sourced members, except those whose resource id is in @keep
-- (SV/ScimProtocolService.cs:471-486, :528-530). Only is_upstream_present
-- changes.
UPDATE identity.access_group_memberships AS m
SET is_upstream_present = false
WHERE m.group_id = @group_id AND m.source = 'scim'
  AND NOT EXISTS (
      SELECT 1 FROM identity.scim_user_mappings sm
      WHERE sm.user_id = m.user_id AND sm.resource_id = ANY (@keep::uuid[])
  );
