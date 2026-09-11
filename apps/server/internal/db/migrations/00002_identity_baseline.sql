-- +goose Up
-- Identity's schema, designed fresh for the Go port (no .NET data is
-- migrated): users and credentials, sessions, RBAC, access groups,
-- delegations, the audit trail, invitations, passkeys, OIDC links, SCIM
-- mappings and system settings. See docs/superpowers/specs/2026-09-11-identity-design.md.
CREATE SCHEMA identity;

CREATE TABLE identity.users (
    id                 uuid PRIMARY KEY,
    email              text NOT NULL,
    normalized_email   text NOT NULL,
    email_confirmed    boolean NOT NULL DEFAULT false,
    display_name       text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 200),
    preferred_language text CHECK (preferred_language IN ('en', 'nb')),
    password_hash      text,
    is_disabled        boolean NOT NULL DEFAULT false,
    lockout_end        timestamptz,
    failed_login_count integer NOT NULL DEFAULT 0,
    totp_secret        bytea,
    totp_enabled       boolean NOT NULL DEFAULT false,
    totp_last_step     bigint,
    version            uuid NOT NULL,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_users_normalized_email ON identity.users (normalized_email);

CREATE TABLE identity.roles (
    id              uuid PRIMARY KEY,
    name            text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 256),
    normalized_name text NOT NULL,
    display_name    text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 200),
    description     text NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    is_system       boolean NOT NULL DEFAULT false,
    is_built_in     boolean NOT NULL DEFAULT false,
    steward_user_id uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    version         uuid NOT NULL,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_roles_normalized_name ON identity.roles (normalized_name);

INSERT INTO identity.roles (id, name, normalized_name, display_name, description, is_system, is_built_in, version, created_at, updated_at) VALUES
    ('00000000-0000-4000-8000-000000000001', 'SystemAdmin', 'SYSTEMADMIN', 'System administrator', 'Global tenant control-plane administration.', true, true, gen_random_uuid(), now(), now()),
    ('00000000-0000-4000-8000-000000000002', 'Owner', 'OWNER', 'Owner', 'Full installation access.', true, true, gen_random_uuid(), now(), now()),
    ('00000000-0000-4000-8000-000000000003', 'User', 'USER', 'User', 'Standard user role with no permissions by default.', true, true, gen_random_uuid(), now(), now());

CREATE TABLE identity.user_roles (
    user_id uuid NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES identity.roles (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX ix_user_roles_role ON identity.user_roles (role_id);

CREATE TABLE identity.role_permissions (
    role_id        uuid NOT NULL REFERENCES identity.roles (id) ON DELETE CASCADE,
    permission_key text NOT NULL CHECK (char_length(permission_key) <= 200),
    PRIMARY KEY (role_id, permission_key)
);
CREATE INDEX ix_role_permissions_key ON identity.role_permissions (permission_key);

CREATE TABLE identity.access_groups (
    id           uuid PRIMARY KEY,
    display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 200),
    source       text NOT NULL CHECK (source IN ('local', 'scim')),
    external_id  text CHECK (char_length(external_id) <= 256),
    is_active    boolean NOT NULL DEFAULT true,
    version      uuid NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_access_groups_display_name ON identity.access_groups (display_name);
CREATE UNIQUE INDEX ux_access_groups_external_id ON identity.access_groups (external_id) WHERE external_id IS NOT NULL;

CREATE TABLE identity.access_group_memberships (
    group_id            uuid NOT NULL REFERENCES identity.access_groups (id) ON DELETE CASCADE,
    user_id             uuid NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    source              text NOT NULL CHECK (source IN ('local', 'scim')),
    is_upstream_present boolean NOT NULL DEFAULT false,
    membership_override text CHECK (membership_override IN ('force_member', 'force_non_member')),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX ix_access_group_memberships_user ON identity.access_group_memberships (user_id);

CREATE TABLE identity.access_group_role_mappings (
    group_id uuid NOT NULL REFERENCES identity.access_groups (id) ON DELETE CASCADE,
    role_id  uuid NOT NULL REFERENCES identity.roles (id) ON DELETE CASCADE,
    source   text NOT NULL CHECK (source IN ('local', 'scim')),
    PRIMARY KEY (group_id, role_id)
);

CREATE TABLE identity.authorization_delegations (
    id                 uuid PRIMARY KEY,
    grantee_user_id    uuid NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    created_by_user_id uuid NOT NULL REFERENCES identity.users (id) ON DELETE RESTRICT,
    can_create_roles   boolean NOT NULL DEFAULT false,
    expires_at         timestamptz,
    revoked_at         timestamptz,
    version            uuid NOT NULL,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL
);
CREATE INDEX ix_authorization_delegations_grantee ON identity.authorization_delegations (grantee_user_id);

CREATE TABLE identity.authorization_delegation_permissions (
    delegation_id  uuid NOT NULL REFERENCES identity.authorization_delegations (id) ON DELETE CASCADE,
    permission_key text NOT NULL CHECK (char_length(permission_key) <= 200),
    PRIMARY KEY (delegation_id, permission_key)
);

-- role_id deliberately has no foreign key. Deleting a role through the
-- endpoint removes its rows here first (DeleteRoleStewardships), as .NET's
-- cascading foreign key did, so the delegation stays valid; a reference left
-- dangling any other way (a role deleted around the endpoint, or an id that
-- names no role) fails closed at evaluation, dropping the delegation.
CREATE TABLE identity.authorization_delegation_roles (
    delegation_id uuid NOT NULL REFERENCES identity.authorization_delegations (id) ON DELETE CASCADE,
    role_id       uuid NOT NULL,
    PRIMARY KEY (delegation_id, role_id)
);

CREATE TABLE identity.authorization_audit_events (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id     uuid,
    target_user_id    uuid,
    target_role_id    uuid,
    action            text NOT NULL CHECK (char_length(action) <= 100),
    details           text NOT NULL CHECK (char_length(details) <= 4000),
    before_json       text CHECK (char_length(before_json) <= 10000),
    after_json        text CHECK (char_length(after_json) <= 10000),
    correlation_id    text CHECK (char_length(correlation_id) <= 200),
    mfa_authenticated boolean NOT NULL,
    occurred_at       timestamptz NOT NULL
);
CREATE INDEX ix_authorization_audit_events_occurred_at ON identity.authorization_audit_events (occurred_at);

CREATE TABLE identity.sessions (
    id              uuid PRIMARY KEY,
    user_id         uuid NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    token_hash      bytea NOT NULL,
    persistent      boolean NOT NULL,
    mfa_verified_at timestamptz,
    created_at      timestamptz NOT NULL,
    last_seen_at    timestamptz NOT NULL,
    ip              text,
    user_agent      text CHECK (char_length(user_agent) <= 512),
    revoked_at      timestamptz
);
CREATE UNIQUE INDEX ux_sessions_token_hash ON identity.sessions (token_hash);
CREATE INDEX ix_sessions_user_id ON identity.sessions (user_id);

CREATE TABLE identity.login_tickets (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);

CREATE TABLE identity.recovery_codes (
    user_id   uuid NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    code_hash bytea NOT NULL,
    PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE identity.password_reset_tokens (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);

CREATE TABLE identity.invitations (
    id                 uuid PRIMARY KEY,
    email              text NOT NULL,
    normalized_email   text NOT NULL,
    role               text NOT NULL CHECK (role IN ('User', 'Owner')),
    display_name       text CHECK (char_length(display_name) <= 200),
    token_hash         bytea NOT NULL,
    invited_by_user_id uuid,
    created_at         timestamptz NOT NULL,
    expires_at         timestamptz NOT NULL,
    revoked_at         timestamptz,
    accepted_at        timestamptz
);
CREATE UNIQUE INDEX ux_invitations_token_hash ON identity.invitations (token_hash);
CREATE UNIQUE INDEX ux_invitations_active_email ON identity.invitations (normalized_email) WHERE accepted_at IS NULL AND revoked_at IS NULL;

CREATE TABLE identity.passkeys (
    credential_id    bytea PRIMARY KEY,
    user_id          uuid NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    name             text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    credential       jsonb NOT NULL,
    user_verified    boolean NOT NULL,
    backup_eligible  boolean NOT NULL,
    backed_up        boolean NOT NULL,
    transports       text[] NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL,
    last_used_at     timestamptz
);
CREATE INDEX ix_passkeys_user ON identity.passkeys (user_id);

CREATE TABLE identity.passkey_ceremonies (
    id              uuid PRIMARY KEY,
    user_id         uuid REFERENCES identity.users (id) ON DELETE CASCADE,
    kind            text NOT NULL CHECK (kind IN ('enroll', 'login')),
    session_data    jsonb NOT NULL,
    credential_name text CHECK (char_length(credential_name) <= 100),
    client_ip       text,
    expires_at      timestamptz NOT NULL,
    consumed_at     timestamptz
);
CREATE INDEX ix_passkey_ceremonies_expires ON identity.passkey_ceremonies (expires_at) WHERE consumed_at IS NULL;

CREATE TABLE identity.profile_avatars (
    user_id      uuid PRIMARY KEY REFERENCES identity.users (id) ON DELETE CASCADE,
    data         bytea NOT NULL,
    content_type text NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg')),
    version      integer NOT NULL,
    updated_at   timestamptz NOT NULL
);

CREATE TABLE identity.oidc_links (
    issuer     text NOT NULL,
    subject    text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 512),
    user_id    uuid NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX ix_oidc_links_user ON identity.oidc_links (user_id);

-- Deleting a SCIM-provisioned user is refused (RESTRICT → 409
-- provenance_conflict), as in the .NET module. external_id holds the 512
-- characters .NET's column did (ScimUserMappingEntityTypeConfiguration.cs:16),
-- the bound its SCIM validation allows.
CREATE TABLE identity.scim_user_mappings (
    resource_id     uuid PRIMARY KEY,
    user_id         uuid NOT NULL REFERENCES identity.users (id) ON DELETE RESTRICT,
    external_id     text NOT NULL CHECK (char_length(external_id) BETWEEN 1 AND 512),
    user_name       text NOT NULL,
    upstream_active boolean NOT NULL DEFAULT true,
    source_profile  jsonb,
    version         integer NOT NULL,
    etag            text NOT NULL,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_scim_user_mappings_user ON identity.scim_user_mappings (user_id);
CREATE UNIQUE INDEX ux_scim_user_mappings_external_id ON identity.scim_user_mappings (external_id);

CREATE TABLE identity.system_settings (
    id                  smallint PRIMARY KEY CHECK (id = 1),
    maintenance_enabled boolean NOT NULL,
    maintenance_message text CHECK (char_length(maintenance_message) <= 500),
    updated_at          timestamptz NOT NULL,
    updated_by_user_id  uuid
);

CREATE TABLE identity.bootstrap_state (
    id           smallint PRIMARY KEY CHECK (id = 1),
    completed_at timestamptz NOT NULL
);

CREATE TABLE identity.operational_events (
    kind        text PRIMARY KEY CHECK (char_length(kind) <= 64),
    occurred_at timestamptz NOT NULL
);

-- +goose Down
DROP SCHEMA identity CASCADE;
