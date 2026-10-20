-- +goose Up
-- The entra module (Entra design D11, D13,
-- docs/superpowers/specs/2026-10-19-entra-design.md; delivery C,
-- docs/superpowers/plans/2026-10-20-entra-module.md): Microsoft Entra ID as
-- the first provider of contracts.IdentityProvisioner. The module owns this
-- schema alone and no table here references another schema.
--
-- entra.connection is the installation's one connection to a tenant, a single
-- row seeded below: the tenant and the application registered in it, the
-- account rules (the UPN's domain and pattern, the usage location, the
-- leaver's steps, the hire date, the Temporary Access Pass), and what the last
-- connection test found — the status and its reason code, the token's roles,
-- the verified domains, the organisation's name. sensitive_denied_streak
-- counts the sensitive writes refused in a row with none succeeding since
-- (plan reading 4): three or more reads as role_missing, the User
-- Administrator role not assigned. version is the If-Match of both of the
-- connection's PUTs.
--
-- entra.credentials are the application's client secrets, in the plural, each
-- sealed under the purpose entra/client-secret and never answered. The newest
-- unrevoked one whose expires_at is ahead is the one used; a revoked one is
-- kept, never deleted.
--
-- entra.accounts are the users the module found or created in the tenant, by
-- their immutable object id, one per employee number (case-folded, as Staff's
-- numbers are). pass_sealed is a Temporary Access Pass sealed under the
-- purpose entra/pass until it is revealed once or expires; last_error is the
-- account's last refusal code. A deleted account is kept, marked deleted.
--
-- entra.events is the module's own append-only trail of its writes: who (a
-- user or a system, exactly one, as the audit names it), what, which user by
-- object id and the UPN the module made, and the codes reported — never an
-- address beyond the UPN, a secret or a pass.
CREATE SCHEMA entra;

CREATE TABLE entra.connection (
    id                      smallint     PRIMARY KEY CHECK (id = 1),
    tenant_id               uuid,
    client_id               uuid,
    upn_domain              varchar(253),
    upn_pattern             varchar(40)  NOT NULL DEFAULT 'given.family',
    default_usage_location  char(2)      NOT NULL DEFAULT 'NO',
    leaver_strip_licences   boolean      NOT NULL DEFAULT true,
    leaver_remove_groups    varchar(10)  NOT NULL DEFAULT 'all',
    leaver_revoke_sessions  boolean      NOT NULL DEFAULT true,
    leaver_write_leave_date boolean      NOT NULL DEFAULT true,
    write_hire_date         boolean      NOT NULL DEFAULT true,
    pass_lifetime_minutes   integer      NOT NULL DEFAULT 480,
    pass_usable_once        boolean      NOT NULL DEFAULT true,
    pass_delivery           varchar(10)  NOT NULL DEFAULT 'hr',
    status                  varchar(12)  NOT NULL DEFAULT 'unconfigured',
    status_reason           varchar(60),
    permissions_present     text[]       NOT NULL DEFAULT '{}',
    verified_domains        text[]       NOT NULL DEFAULT '{}',
    organisation_name       varchar(200),
    last_checked_at         timestamptz,
    sensitive_denied_streak integer      NOT NULL DEFAULT 0,
    version                 integer      NOT NULL DEFAULT 1,
    updated_at              timestamptz  NOT NULL,
    updated_by_user_id      uuid,
    CONSTRAINT connection_upn_pattern_check CHECK (upn_pattern IN ('given.family', 'g.family', 'given')),
    CONSTRAINT connection_leaver_remove_groups_check CHECK (leaver_remove_groups IN ('all', 'managed', 'none')),
    CONSTRAINT connection_pass_lifetime_minutes_check CHECK (pass_lifetime_minutes BETWEEN 10 AND 43200),
    CONSTRAINT connection_pass_delivery_check CHECK (pass_delivery IN ('hr', 'manager', 'both')),
    CONSTRAINT connection_status_check CHECK (status IN ('unconfigured', 'connected', 'failing')),
    CONSTRAINT connection_sensitive_denied_streak_check CHECK (sensitive_denied_streak >= 0)
);

CREATE TABLE entra.credentials (
    id                 bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind               varchar(20)  NOT NULL,
    label              varchar(100),
    sealed             bytea        NOT NULL,
    expires_at         timestamptz  NOT NULL,
    created_at         timestamptz  NOT NULL,
    created_by_user_id uuid,
    last_used_at       timestamptz,
    last_failed_at     timestamptz,
    revoked_at         timestamptz,
    CONSTRAINT credentials_kind_check CHECK (kind IN ('client_secret'))
);

CREATE TABLE entra.accounts (
    object_id           uuid         PRIMARY KEY,
    employee_number     varchar(16)  NOT NULL,
    user_principal_name varchar(254) NOT NULL,
    state               varchar(10)  NOT NULL,
    pass_sealed         bytea,
    pass_expires_at     timestamptz,
    pass_revealed_at    timestamptz,
    created_by_us       boolean      NOT NULL,
    reported            text[]       NOT NULL DEFAULT '{}',
    last_error          varchar(60),
    last_synced_at      timestamptz,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL,
    CONSTRAINT accounts_state_check CHECK (state IN ('disabled', 'enabled', 'deleted')),
    CONSTRAINT accounts_pass_check CHECK (pass_sealed IS NULL OR pass_expires_at IS NOT NULL)
);
CREATE UNIQUE INDEX ux_entra_accounts_number ON entra.accounts (upper(employee_number));

CREATE TABLE entra.events (
    id            bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at            timestamptz  NOT NULL,
    actor_user_id uuid,
    actor_system  varchar(40),
    action        varchar(40)  NOT NULL,
    object_id     uuid,
    upn           varchar(254),
    codes         text[]       NOT NULL DEFAULT '{}',
    CONSTRAINT events_actor_check CHECK ((actor_user_id IS NULL) <> (actor_system IS NULL))
);
CREATE INDEX ix_entra_events_at ON entra.events (at);

INSERT INTO entra.connection (id, updated_at) VALUES (1, now());

-- +goose Down
-- The schema goes whole: the connection, the sealed secrets, the accounts'
-- rows with any pass not yet revealed, and the trail. Nothing in the tenant
-- is touched; the users the module made stay there, and a later Up finds
-- them again by employee number.
DROP SCHEMA entra CASCADE;
