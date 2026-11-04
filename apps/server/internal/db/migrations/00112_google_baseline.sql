-- +goose Up
-- The google module (Google Workspace design G12–G16,
-- docs/superpowers/specs/2026-11-03-google-workspace-design.md; delivery A,
-- docs/superpowers/plans/2026-11-03-google-foundation.md): the connection to a
-- Google Workspace organisation through a service account's domain-wide
-- delegation. The module owns this schema alone and no table here references
-- another schema.
--
-- google.connection is the installation's one connection, a single row seeded
-- below: how tokens are obtained (credential_kind; only service_account_key is
-- implemented, vantigo_hosted is reserved), the admin the Admin SDK calls
-- impersonate, the Internal web client's id, and what the last connection test
-- found — the customer, its primary and verified domains, its name, the
-- scopes granted, missing and unexpected, the status and its reason. version
-- is the If-Match of PUT /connection.
--
-- google.credentials are the service account's keys and the web client's
-- secret, each sealed (google/service-account-key, google/oauth-client-secret)
-- and never answered. Of a key file only the four clear fields are kept:
-- client_email, client_id (the numeric id delegation is granted to),
-- private_key_id and project_id. The newest live key is used; a revoked one is
-- kept, never deleted.
--
-- google.events is the module's own append-only trail: who acted (a user or a
-- system, exactly one), what, the Google id it concerned — never an address —
-- and codes.
CREATE SCHEMA google;

CREATE TABLE google.connection (
    id                    smallint     PRIMARY KEY CHECK (id = 1),
    credential_kind       varchar(30)  NOT NULL DEFAULT 'service_account_key',
    customer_id           varchar(40),
    primary_domain        varchar(253),
    domains               text[]       NOT NULL DEFAULT '{}',
    organisation_name     varchar(256),
    admin_subject         varchar(254),
    oauth_client_id       varchar(200),
    scopes_granted        text[]       NOT NULL DEFAULT '{}',
    scopes_missing        text[]       NOT NULL DEFAULT '{}',
    scopes_unexpected     text[]       NOT NULL DEFAULT '{}',
    status                varchar(20)  NOT NULL DEFAULT 'unconfigured',
    status_reason         varchar(60),
    last_checked_at       timestamptz,
    version               integer      NOT NULL DEFAULT 1,
    updated_at            timestamptz  NOT NULL,
    updated_by_user_id    uuid,
    CONSTRAINT connection_credential_kind_check CHECK (credential_kind IN ('service_account_key', 'vantigo_hosted')),
    CONSTRAINT connection_status_check CHECK (status IN ('unconfigured', 'pending_delegation', 'connected', 'failing'))
);

CREATE TABLE google.credentials (
    id                 bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind               varchar(30)  NOT NULL,
    client_email       varchar(254),
    client_id          varchar(40),
    private_key_id     varchar(64),
    project_id         varchar(64),
    sealed             bytea        NOT NULL,
    created_at         timestamptz  NOT NULL,
    created_by_user_id uuid,
    last_used_at       timestamptz,
    last_failed_at     timestamptz,
    revoked_at         timestamptz,
    CONSTRAINT credentials_kind_check CHECK (kind IN ('service_account_key', 'oauth_client_secret'))
);

CREATE TABLE google.events (
    id            bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at            timestamptz  NOT NULL,
    actor_user_id uuid,
    actor_system  varchar(40),
    action        varchar(40)  NOT NULL,
    subject_id    varchar(64),
    codes         text[]       NOT NULL DEFAULT '{}',
    CONSTRAINT events_actor_check CHECK ((actor_user_id IS NULL) <> (actor_system IS NULL))
);
CREATE INDEX ix_google_events_at ON google.events (at);

INSERT INTO google.connection (id, updated_at) VALUES (1, now());

-- +goose Down
-- The schema goes whole: the connection, the sealed keys and secret, and the
-- trail. Nothing at Google is touched; the service account, its keys and the
-- delegation stay until an administrator removes them.
DROP SCHEMA google CASCADE;
