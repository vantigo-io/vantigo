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
-- built in), each taken FOR SHARE so it is neither deleted nor edited before
-- the set that names it commits. FOR SHARE, not FOR KEY SHARE: a role edit
-- bumps the role's version, and a serializable caller whose snapshot predates
-- an edit committed meanwhile then fails to serialize here and retries with
-- a fresh snapshot, instead of judging the role's keys as they were.
SELECT id FROM identity.roles
WHERE id = ANY (@ids::uuid[]) AND NOT is_system AND NOT is_built_in
FOR SHARE;

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

-- name: IsProvisioningRole :one
-- IsProvisioningRole reports whether the role is in the set Staff may give.
SELECT EXISTS (SELECT 1 FROM identity.provisioning_roles WHERE role_id = @role_id)::boolean;

-- name: GetProvisioningUser :one
-- GetProvisioningUser is a user as the account provisioning provider judges
-- and reports them (contracts.AccountRef): whether they hold a password, a
-- passkey or an OIDC link (a credential), Owner, SystemAdmin, and whether the
-- directory deactivated them (scim_inactive: a mapping whose upstream is
-- inactive; the caller counts it only while SCIM is configured and the user
-- is no Owner).
SELECT u.id, u.email, u.normalized_email, u.display_name, u.preferred_language, u.is_disabled,
       u.managed_by, u.employee_number, u.pending_since, u.disabled_by, u.invitations_since,
       (u.password_hash IS NOT NULL)::boolean AS has_password,
       EXISTS (SELECT 1 FROM identity.passkeys p WHERE p.user_id = u.id)::boolean AS has_passkey,
       EXISTS (SELECT 1 FROM identity.oidc_links l WHERE l.user_id = u.id)::boolean AS has_oidc_link,
       EXISTS (SELECT 1 FROM identity.user_roles ur JOIN identity.roles ro ON ro.id = ur.role_id
               WHERE ur.user_id = u.id AND ro.name = 'Owner')::boolean AS is_owner,
       EXISTS (SELECT 1 FROM identity.user_roles ur JOIN identity.roles ro ON ro.id = ur.role_id
               WHERE ur.user_id = u.id AND ro.name IN ('Owner', 'SystemAdmin'))::boolean AS privileged,
       EXISTS (SELECT 1 FROM identity.scim_user_mappings m WHERE m.user_id = u.id AND m.upstream_active IS FALSE)::boolean AS scim_inactive
FROM identity.users u
WHERE u.id = @id;

-- name: LockProvisioningUser :one
-- LockProvisioningUser is GetProvisioningUser holding the user's row FOR
-- UPDATE, taken after the owner lock and the user's invitations' locks
-- (LockUserInvitations), as acceptance orders them. Its columns are
-- GetProvisioningUser's, in the same order.
SELECT u.id, u.email, u.normalized_email, u.display_name, u.preferred_language, u.is_disabled,
       u.managed_by, u.employee_number, u.pending_since, u.disabled_by, u.invitations_since,
       (u.password_hash IS NOT NULL)::boolean AS has_password,
       EXISTS (SELECT 1 FROM identity.passkeys p WHERE p.user_id = u.id)::boolean AS has_passkey,
       EXISTS (SELECT 1 FROM identity.oidc_links l WHERE l.user_id = u.id)::boolean AS has_oidc_link,
       EXISTS (SELECT 1 FROM identity.user_roles ur JOIN identity.roles ro ON ro.id = ur.role_id
               WHERE ur.user_id = u.id AND ro.name = 'Owner')::boolean AS is_owner,
       EXISTS (SELECT 1 FROM identity.user_roles ur JOIN identity.roles ro ON ro.id = ur.role_id
               WHERE ur.user_id = u.id AND ro.name IN ('Owner', 'SystemAdmin'))::boolean AS privileged,
       EXISTS (SELECT 1 FROM identity.scim_user_mappings m WHERE m.user_id = u.id AND m.upstream_active IS FALSE)::boolean AS scim_inactive
FROM identity.users u
WHERE u.id = @id
FOR UPDATE OF u;

-- name: LockUserInvitations :exec
-- LockUserInvitations takes the row lock of every invitation naming the
-- user, before the user's own (LockProvisioningUser): acceptance locks the
-- invitation, then the user, and a provider's write takes them in the same
-- order.
SELECT id FROM identity.invitations WHERE user_id = @user_id ORDER BY id FOR UPDATE;

-- name: ListUserInvitationsSince :many
-- ListUserInvitationsSince is every invitation naming the user made at or
-- after @since — the user's current invitation spell — the latest first:
-- one still open (neither revoked nor accepted; at most one is, by
-- ux_invitations_active_email) before any other, then newest first, so two
-- made at one instant are never ordered by chance.
SELECT * FROM identity.invitations
WHERE user_id = @user_id AND created_at >= @since::timestamptz
ORDER BY (revoked_at IS NULL AND accepted_at IS NULL) DESC, created_at DESC, id DESC;

-- name: RevokeUserInvitations :exec
-- RevokeUserInvitations revokes every invitation naming the user that could
-- still be accepted, recording why (deactivated, email_changed).
UPDATE identity.invitations
SET revoked_at = @now::timestamptz, revoked_reason = @revoked_reason::text
WHERE user_id = @user_id AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: InsertProvisionedUser :exec
-- InsertProvisionedUser creates the user Staff's provisioning prepares: no
-- credential, the email unconfirmed, disabled by Staff and pending since
-- @now, which also starts their first invitation spell. The employee number
-- and the management are setStaffManaged's to write.
INSERT INTO identity.users (
    id, email, normalized_email, email_confirmed, display_name, preferred_language,
    is_disabled, disabled_by, pending_since, invitations_since, version, created_at, updated_at
) VALUES (
    @id, @email, @normalized_email, false, @display_name, sqlc.narg(preferred_language)::text,
    true, 'staff', @now::timestamptz, @now::timestamptz, @version, @now::timestamptz, @now::timestamptz
);

-- name: UpdateProvisionedUser :exec
-- UpdateProvisionedUser writes what Staff's Update may change on a managed
-- user: the display name, the language, and the email with its spell (only
-- while pending; the caller passes the stored ones otherwise).
UPDATE identity.users
SET display_name = @display_name, preferred_language = sqlc.narg(preferred_language)::text,
    email = @email, normalized_email = @normalized_email, invitations_since = sqlc.narg(invitations_since)::timestamptz,
    version = @version, updated_at = @now::timestamptz
WHERE id = @id;

-- name: SetInvitationsSince :exec
-- SetInvitationsSince starts a new invitation spell for the user at @now
-- (ActivateOpts.FreshInvitation).
UPDATE identity.users SET invitations_since = @now::timestamptz WHERE id = @id;

-- name: LockProvisioningRoleIDs :many
-- LockProvisioningRoleIDs is the set of roles Staff may give, each role
-- taken FOR SHARE as PUT /owner/provisioning-roles takes them
-- (LockAssignableRoles): no edit or delete of a role commits while an
-- Activate assigns it, and an edit committed meanwhile makes the
-- serializable caller retry and judge the role as edited.
SELECT r.id FROM identity.provisioning_roles p
JOIN identity.roles r ON r.id = p.role_id
ORDER BY r.id
FOR SHARE OF r;

-- name: ListOpenInvitationsForEmail :many
-- ListOpenInvitationsForEmail is every invitation to the address neither
-- accepted nor revoked — what RevokeActiveInvitationsForEmail would revoke —
-- by id, with its role and expiry.
SELECT id, role, expires_at FROM identity.invitations
WHERE normalized_email = @normalized_email AND accepted_at IS NULL AND revoked_at IS NULL
ORDER BY id;
