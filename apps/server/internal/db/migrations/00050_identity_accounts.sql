-- +goose Up
-- Identity's accounts for the Staff register (staff foundation design
-- S24–S26, delivery C; docs/superpowers/plans/2026-10-17-staff-accounts.md).
--
-- identity.users gains the Staff-managed columns: managed_by ('staff' or
-- null) marks a user Staff's provisioning may act on, which an Owner sets by
-- adopting a user or Staff by creating one; employee_number correlates the
-- user with the person in Staff's register, as it was given (trimmed and
-- NFC-normalised), and employee_number_key is the same number as identity's
-- one Go normaliser (contracts.NormalizeEmployeeNumber) compares it — unique
-- by that key, as normalized_email is for addresses, so no database
-- collation's upper() decides which two numbers are one;
-- pending_since marks a user Staff created who has not yet chosen a
-- credential; invitations_since starts their current invitation spell — the
-- invitations named for them since count toward the three Staff's
-- provisioning delivers before it stops, and a fresh invitation, or a
-- pending user's new address, starts another (null reads as pending_since);
-- disabled_by records who disabled the user, so Staff never re-enables what
-- an Owner disabled.
ALTER TABLE identity.users
    ADD COLUMN managed_by      varchar(20) CHECK (managed_by IN ('staff')),
    ADD COLUMN employee_number varchar(16),
    ADD COLUMN employee_number_key text,
    ADD COLUMN pending_since   timestamptz,
    ADD COLUMN invitations_since timestamptz,
    ADD COLUMN disabled_by     varchar(10) CHECK (disabled_by IN ('owner', 'staff')),
    ADD CONSTRAINT users_employee_number_key_check CHECK ((employee_number IS NULL) = (employee_number_key IS NULL));
CREATE UNIQUE INDEX ux_users_employee_number ON identity.users (employee_number_key) WHERE employee_number_key IS NOT NULL;

-- Before this migration only an Owner disabled a user (SCIM's upstream state
-- is scim_user_mappings.upstream_active, a separate thing).
UPDATE identity.users SET disabled_by = 'owner' WHERE is_disabled;

-- An invitation may name the pending user it completes (user_id, opaque:
-- an invitation outlives nothing, and a user's delete leaves it to expire),
-- and records why it was revoked.
ALTER TABLE identity.invitations
    ADD COLUMN user_id        uuid,
    ADD COLUMN revoked_reason varchar(20) CHECK (revoked_reason IN ('owner', 'replaced', 'delivery_failed', 'email_changed', 'deactivated'));
CREATE INDEX ix_invitations_user ON identity.invitations (user_id) WHERE user_id IS NOT NULL;

-- Before this migration a revoked invitation was revoked as an Owner's
-- (plan reading 14).
UPDATE identity.invitations SET revoked_reason = 'owner' WHERE revoked_at IS NOT NULL;

-- The authority mode the API process derives from its configuration and
-- records, so a worker-only process cannot disagree with it silently.
ALTER TABLE identity.system_settings
    ADD COLUMN authority_mode    varchar(10) CHECK (authority_mode IN ('local', 'federated')),
    ADD COLUMN authority_mode_at timestamptz;

-- The roles Staff may give the users it provisions: custom roles an Owner
-- names, gone with their role.
CREATE TABLE identity.provisioning_roles (
    role_id    uuid PRIMARY KEY REFERENCES identity.roles (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE identity.provisioning_roles;
ALTER TABLE identity.system_settings
    DROP COLUMN authority_mode_at,
    DROP COLUMN authority_mode;
DROP INDEX identity.ix_invitations_user;
ALTER TABLE identity.invitations
    DROP COLUMN revoked_reason,
    DROP COLUMN user_id;
DROP INDEX identity.ux_users_employee_number;
ALTER TABLE identity.users
    DROP COLUMN disabled_by,
    DROP CONSTRAINT users_employee_number_key_check,
    DROP COLUMN invitations_since,
    DROP COLUMN pending_since,
    DROP COLUMN employee_number_key,
    DROP COLUMN employee_number,
    DROP COLUMN managed_by;
