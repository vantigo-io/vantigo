-- +goose Up
-- Integrations and their API keys (API access design K8-K15,
-- docs/superpowers/specs/2026-11-17-api-access-design.md): an external
-- application's account, a user of kind integration (00194), and the keys
-- that authenticate as it. Only identity's integrations service writes them.

-- An integration (K8). Its user carries the display name (the integration's
-- name) and is_disabled; disabling refuses every key at once. Archiving
-- revokes every key, keeps the row for attribution and frees the name. Every
-- person it names is ON DELETE SET NULL, so deleting an Owner who made one is
-- never refused; the user itself may not be deleted while the row exists.
CREATE TABLE identity.integrations (
    user_id               uuid PRIMARY KEY REFERENCES identity.users (id) ON DELETE RESTRICT,
    name                  varchar(100)  NOT NULL,
    description           varchar(1000),
    contact_user_id       uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    rate_limit_per_minute integer       NOT NULL DEFAULT 600,
    created_by            uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    created_at            timestamptz   NOT NULL,
    archived_at           timestamptz,
    archived_by           uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    version               uuid          NOT NULL,
    CONSTRAINT ck_integrations_name CHECK (btrim(name) <> ''),
    CONSTRAINT ck_integrations_rate_limit CHECK (rate_limit_per_minute BETWEEN 60 AND 6000)
);
CREATE UNIQUE INDEX ux_integrations_name ON identity.integrations (lower(name)) WHERE archived_at IS NULL;
CREATE INDEX ix_integrations_contact ON identity.integrations (contact_user_id) WHERE contact_user_id IS NOT NULL;
CREATE INDEX ix_integrations_created_by ON identity.integrations (created_by) WHERE created_by IS NOT NULL;
CREATE INDEX ix_integrations_archived_by ON identity.integrations (archived_by) WHERE archived_by IS NOT NULL;

-- The contact gets the integration's notices (K25), so only a person may be
-- one: 00141's guard, as 00194 widened it.
CREATE TRIGGER integrations_contact_refuse_agent BEFORE INSERT OR UPDATE OF contact_user_id ON identity.integrations
    FOR EACH ROW EXECUTE FUNCTION identity.refuse_agent_user('contact_user_id');

-- An API key (K11-K13). Only the SHA-256 of its 32 random bytes is stored;
-- display is vtg_, the first four and the last four characters of the
-- random part. permissions is the narrowing: NULL for none, else the only
-- keys the key may use of those its integration's roles grant. Every key
-- expires. A roll links the new key to the old through rolled_from, unique
-- so a key is rolled once. expired_recorded_at is when the expiry worker
-- (K15) audited api_key.expired for it, so it is audited once; the worker
-- deletes the row 90 days after expires_at.
CREATE TABLE identity.api_keys (
    id                  uuid PRIMARY KEY,
    integration_id      uuid         NOT NULL REFERENCES identity.integrations (user_id) ON DELETE RESTRICT,
    name                varchar(100) NOT NULL,
    token_hash          bytea        NOT NULL,
    display             text         NOT NULL,
    permissions         text[],
    read_only           boolean      NOT NULL DEFAULT false,
    allowed_networks    cidr[]       NOT NULL DEFAULT '{}',
    expires_at          timestamptz  NOT NULL,
    created_by          uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    created_at          timestamptz  NOT NULL,
    revoked_at          timestamptz,
    revoked_by          uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    rolled_from         uuid REFERENCES identity.api_keys (id) ON DELETE SET NULL,
    last_used_at        timestamptz,
    last_used_ip        text,
    expired_recorded_at timestamptz,
    CONSTRAINT ck_api_keys_name CHECK (btrim(name) <> ''),
    CONSTRAINT ck_api_keys_networks CHECK (cardinality(allowed_networks) <= 20),
    CONSTRAINT ck_api_keys_permissions CHECK (permissions IS NULL OR cardinality(permissions) <= 1000)
);
CREATE UNIQUE INDEX ux_api_keys_token_hash ON identity.api_keys (token_hash);
CREATE UNIQUE INDEX ux_api_keys_rolled_from ON identity.api_keys (rolled_from) WHERE rolled_from IS NOT NULL;
CREATE INDEX ix_api_keys_integration ON identity.api_keys (integration_id);
CREATE INDEX ix_api_keys_expires ON identity.api_keys (expires_at) WHERE revoked_at IS NULL;
CREATE INDEX ix_api_keys_created_by ON identity.api_keys (created_by) WHERE created_by IS NOT NULL;
CREATE INDEX ix_api_keys_revoked_by ON identity.api_keys (revoked_by) WHERE revoked_by IS NOT NULL;

-- +goose Down
DROP TABLE identity.api_keys;
DROP TABLE identity.integrations;
