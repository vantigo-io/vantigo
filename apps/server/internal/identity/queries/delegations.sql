-- name: ActiveDelegationIDs :many
-- ActiveDelegationIDs is the authorization delegations a user holds that
-- are active at @now: not revoked, and without an expiry or expiring after
-- now (EA/AuthAuthorization.cs:127-129). Whether each is valid is
-- DelegationScopes' and ScopeRoles' to decide.
SELECT id
FROM identity.authorization_delegations
WHERE grantee_user_id = @grantee_user_id
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > @now::timestamptz)
ORDER BY id;

-- name: LockActiveDelegations :many
-- LockActiveDelegations is ActiveDelegationIDs holding each row FOR SHARE
-- until the transaction ends: a mutation made under a delegation commits
-- before any revoke or update of it, and one queued behind an uncommitted
-- revoke or update reads the delegation as that left it.
SELECT id
FROM identity.authorization_delegations
WHERE grantee_user_id = @grantee_user_id
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > @now::timestamptz)
ORDER BY id
FOR SHARE;

-- name: DelegationScopes :many
-- DelegationScopes reads the delegations among @ids still active at @now:
-- whether each may create roles, its permission keys (ordinal) and its
-- stewarded role ids.
SELECT d.id, d.can_create_roles,
       coalesce((
           SELECT array_agg(p.permission_key ORDER BY p.permission_key COLLATE "C")
           FROM identity.authorization_delegation_permissions p
           WHERE p.delegation_id = d.id
       ), '{}')::text[] AS permission_keys,
       coalesce((
           SELECT array_agg(r.role_id ORDER BY r.role_id)
           FROM identity.authorization_delegation_roles r
           WHERE r.delegation_id = d.id
       ), '{}')::uuid[] AS role_ids
FROM identity.authorization_delegations d
WHERE d.id = ANY(@ids::uuid[])
  AND d.revoked_at IS NULL
  AND (d.expires_at IS NULL OR d.expires_at > @now::timestamptz)
ORDER BY d.id;

-- name: ScopeRoles :many
-- ScopeRoles reads the roles among @ids that exist: whether each is
-- protected, and its current permission keys (ordinal). An id with no row
-- is a role that no longer exists.
SELECT r.id, r.is_system, r.is_built_in,
       coalesce((
           SELECT array_agg(rp.permission_key ORDER BY rp.permission_key COLLATE "C")
           FROM identity.role_permissions rp
           WHERE rp.role_id = r.id
       ), '{}')::text[] AS permission_keys
FROM identity.roles r
WHERE r.id = ANY(@ids::uuid[]);

-- name: ActiveDelegationGrantees :many
-- ActiveDelegationGrantees is every user who holds a delegation active at
-- @now, valid or not: the users a delegate's directory leaves out
-- (EA/AuthorizationManagementEndpoints.cs:494-503).
SELECT DISTINCT grantee_user_id
FROM identity.authorization_delegations
WHERE revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > @now::timestamptz);

-- name: ListDelegations :many
-- ListDelegations is GET /access/delegations
-- (EA/AuthorizationManagementEndpoints.cs:556-571): every delegation, by id
-- descending, with its permission keys (ordinal) and stewarded role ids.
SELECT d.id, d.grantee_user_id, d.expires_at, d.revoked_at, d.version, d.can_create_roles,
       coalesce((
           SELECT array_agg(p.permission_key ORDER BY p.permission_key COLLATE "C")
           FROM identity.authorization_delegation_permissions p
           WHERE p.delegation_id = d.id
       ), '{}')::text[] AS permission_keys,
       coalesce((
           SELECT array_agg(r.role_id ORDER BY r.role_id)
           FROM identity.authorization_delegation_roles r
           WHERE r.delegation_id = d.id
       ), '{}')::uuid[] AS stewarded_role_ids
FROM identity.authorization_delegations d
ORDER BY d.id DESC;

-- name: LockDelegation :one
-- LockDelegation reads a delegation an Owner is about to update or revoke
-- and holds its row FOR UPDATE until the transaction ends.
SELECT id, grantee_user_id, created_by_user_id, can_create_roles, expires_at, revoked_at,
       version, created_at, updated_at
FROM identity.authorization_delegations
WHERE id = @id
FOR UPDATE;

-- name: DelegationChildren :one
-- DelegationChildren is one delegation's permission keys (ordinal) and
-- stewarded role ids, as AuthorizationAuditWriter.CaptureDelegationAsync
-- read them.
SELECT coalesce((
           SELECT array_agg(p.permission_key ORDER BY p.permission_key COLLATE "C")
           FROM identity.authorization_delegation_permissions p
           WHERE p.delegation_id = @id
       ), '{}')::text[] AS permission_keys,
       coalesce((
           SELECT array_agg(r.role_id ORDER BY r.role_id)
           FROM identity.authorization_delegation_roles r
           WHERE r.delegation_id = @id
       ), '{}')::uuid[] AS stewarded_role_ids;

-- name: InsertDelegation :exec
INSERT INTO identity.authorization_delegations (
    id, grantee_user_id, created_by_user_id, can_create_roles, expires_at, revoked_at,
    version, created_at, updated_at
) VALUES (
    @id, @grantee_user_id, @created_by_user_id, @can_create_roles, sqlc.narg(expires_at)::timestamptz, NULL,
    @version, @now::timestamptz, @now::timestamptz
);

-- name: UpdateDelegation :execrows
-- UpdateDelegation writes a delegation's grantee, flag, expiry and new
-- version, only while it still has the version the caller read: zero rows
-- is a concurrent change. A revoked delegation stays revoked.
UPDATE identity.authorization_delegations
SET grantee_user_id = @grantee_user_id, can_create_roles = @can_create_roles,
    expires_at = sqlc.narg(expires_at)::timestamptz, version = @version, updated_at = @now::timestamptz
WHERE id = @id AND version = @expected_version;

-- name: RevokeDelegation :execrows
-- RevokeDelegation marks a delegation revoked at @now with a new version,
-- only while it still has the version the caller read.
UPDATE identity.authorization_delegations
SET revoked_at = @now::timestamptz, version = @version, updated_at = @now::timestamptz
WHERE id = @id AND version = @expected_version;

-- name: DeleteDelegationPermissions :exec
DELETE FROM identity.authorization_delegation_permissions WHERE delegation_id = @delegation_id;

-- name: DeleteDelegationRoles :exec
DELETE FROM identity.authorization_delegation_roles WHERE delegation_id = @delegation_id;

-- name: InsertDelegationPermissions :exec
-- InsertDelegationPermissions gives the delegation each key once.
INSERT INTO identity.authorization_delegation_permissions (delegation_id, permission_key)
SELECT @delegation_id, k FROM unnest(@keys::text[]) AS k
ON CONFLICT DO NOTHING;

-- name: InsertDelegationRoles :exec
-- InsertDelegationRoles makes the delegation steward each role once.
INSERT INTO identity.authorization_delegation_roles (delegation_id, role_id)
SELECT @delegation_id, r FROM unnest(@role_ids::uuid[]) AS r
ON CONFLICT DO NOTHING;
