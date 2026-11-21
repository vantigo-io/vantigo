-- +goose Up
-- OAuth connections (OAuth connections design C1, C7, C14, C16, A10, A11,
-- A18, A20, docs/superpowers/specs/2026-11-21-oauth-connections-design.md):
-- an MCP client or app a person authorized, by signing in, to act as one
-- integration, the codes and tokens it holds, and the client metadata
-- documents Vantigo fetched. Only identity writes them.

-- A connection (C1): one client authorized by one person for one
-- integration. client_name and client_host are what the consent page showed;
-- scopes is what the person approved ('read', and 'write' for Read and write).
-- activated_at is set when the client exchanges its code (C15); a connection
-- never activated is pruned after 10 minutes. expires_at is 180 days after
-- consent. The integration may not be deleted while a connection names it
-- (A20), as for its keys; the person who authorized it may, and the
-- connection's tokens are then refused (A17).
CREATE TABLE identity.oauth_connections (
    id             uuid PRIMARY KEY,
    integration_id uuid         NOT NULL REFERENCES identity.integrations (user_id) ON DELETE RESTRICT,
    client_id      varchar(512) NOT NULL,
    client_kind    text         NOT NULL CHECK (client_kind IN ('metadata', 'registered')),
    client_name    varchar(80)  NOT NULL,
    client_host    varchar(253) NOT NULL,
    redirect_uri   varchar(1024) NOT NULL,
    scopes         text[]       NOT NULL,
    authorized_by  uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    created_at     timestamptz  NOT NULL,
    activated_at   timestamptz,
    expires_at     timestamptz  NOT NULL,
    last_used_at   timestamptz,
    last_used_ip   text,
    revoked_at     timestamptz,
    revoked_by     uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    revoke_reason  varchar(40),
    CONSTRAINT ck_oauth_connections_scopes CHECK (scopes <@ ARRAY['read', 'write']::text[] AND cardinality(scopes) >= 1),
    CONSTRAINT ck_oauth_connections_revoked CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);
CREATE INDEX ix_oauth_connections_integration ON identity.oauth_connections (integration_id);
CREATE INDEX ix_oauth_connections_authorized_by ON identity.oauth_connections (authorized_by) WHERE authorized_by IS NOT NULL;
CREATE INDEX ix_oauth_connections_revoked_by ON identity.oauth_connections (revoked_by) WHERE revoked_by IS NOT NULL;
CREATE INDEX ix_oauth_connections_client_host ON identity.oauth_connections (client_host);
-- The worker prunes the unactivated, and the revoked or expired.
CREATE INDEX ix_oauth_connections_unactivated ON identity.oauth_connections (created_at) WHERE activated_at IS NULL;
CREATE INDEX ix_oauth_connections_expires ON identity.oauth_connections (expires_at);
CREATE INDEX ix_oauth_connections_revoked ON identity.oauth_connections (revoked_at) WHERE revoked_at IS NOT NULL;

-- An authorization code (C14): the SHA-256 of its 32 random bytes, the PKCE
-- challenge it was issued for, valid 60 seconds and consumed once. A
-- consumed code is kept an hour, so a replay still finds its connection.
CREATE TABLE identity.oauth_codes (
    code_hash      bytea       PRIMARY KEY,
    connection_id  uuid        NOT NULL REFERENCES identity.oauth_connections (id) ON DELETE CASCADE,
    code_challenge varchar(43) NOT NULL,
    created_at     timestamptz NOT NULL,
    expires_at     timestamptz NOT NULL,
    consumed_at    timestamptz
);
CREATE INDEX ix_oauth_codes_connection ON identity.oauth_codes (connection_id);
CREATE INDEX ix_oauth_codes_expires ON identity.oauth_codes (expires_at);

-- A token (C16, A10, A11): an access token (vta_) or a refresh token (vtr_),
-- stored as the SHA-256 of its 32 random bytes only. kind is part of every
-- lookup, because the hash covers the bytes alone. parent_hash is the refresh
-- token a pair was issued from; used_at is when a refresh token was spent.
CREATE TABLE identity.oauth_tokens (
    token_hash    bytea       PRIMARY KEY,
    connection_id uuid        NOT NULL REFERENCES identity.oauth_connections (id) ON DELETE CASCADE,
    kind          text        NOT NULL CHECK (kind IN ('access', 'refresh')),
    parent_hash   bytea,
    created_at    timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL,
    used_at       timestamptz
);
CREATE INDEX ix_oauth_tokens_connection ON identity.oauth_tokens (connection_id, kind);
CREATE INDEX ix_oauth_tokens_parent ON identity.oauth_tokens (parent_hash) WHERE parent_hash IS NOT NULL;
CREATE INDEX ix_oauth_tokens_expires ON identity.oauth_tokens (expires_at);

-- A client metadata document Vantigo fetched and accepted (C7): kept for its
-- Cache-Control max-age, clamped to 5 minutes to 24 hours. A failure is never
-- stored. The worker deletes a document a week after it expired.
CREATE TABLE identity.oauth_client_documents (
    client_id  varchar(512) PRIMARY KEY,
    document   jsonb        NOT NULL,
    fetched_at timestamptz  NOT NULL,
    expires_at timestamptz  NOT NULL
);
CREATE INDEX ix_oauth_client_documents_expires ON identity.oauth_client_documents (expires_at);

-- oauth_authorizer_active is the rule a connection's authorizer is held to on
-- every request (A17): a person, not disabled, not pending, holding Owner
-- now, and, while SCIM is on, not deactivated by the directory (no Owner
-- exemption here). A password lockout is ignored, or anyone could end an
-- Owner's connections by failing their password. One definition, so the
-- token check and the connections list cannot drift.
-- +goose StatementBegin
CREATE FUNCTION identity.oauth_authorizer_active(authorizer uuid, scim_enabled boolean)
RETURNS boolean LANGUAGE sql STABLE AS $function$
    SELECT coalesce((
        SELECT u.kind = 'person' AND NOT u.is_disabled AND u.pending_since IS NULL
           AND EXISTS (
               SELECT 1
               FROM identity.user_roles ur
               JOIN identity.roles r ON r.id = ur.role_id
               WHERE ur.user_id = u.id AND r.name = 'Owner'
           )
           AND (NOT scim_enabled OR NOT EXISTS (
               SELECT 1 FROM identity.scim_user_mappings m
               WHERE m.user_id = u.id AND m.upstream_active IS FALSE
           ))
        FROM identity.users u
        WHERE u.id = authorizer
    ), false)
$function$;
-- +goose StatementEnd

-- The request log names the connection a request came through, or the key
-- (A18). No CHECK that exactly one is set: it would scan 30 days of
-- partitions under an exclusive lock; the writer sets exactly one.
ALTER TABLE identity.api_requests ALTER COLUMN key_id DROP NOT NULL;
ALTER TABLE identity.api_requests ADD COLUMN connection_id uuid;

COMMENT ON COLUMN identity.api_requests.reason IS
    'Why the request was refused (revoked, disabled, archived, expired, network, never, read_only, forbidden, rate_limited, insufficient_scope, token_invalid), or idempotent_replay for an answer replayed for an Idempotency-Key.';

-- +goose Down
COMMENT ON COLUMN identity.api_requests.reason IS
    'Why the request was refused (revoked, disabled, archived, expired, network, never, read_only, forbidden, rate_limited), or idempotent_replay for an answer replayed for an Idempotency-Key.';
ALTER TABLE identity.api_requests DROP COLUMN connection_id;
DELETE FROM identity.api_requests WHERE key_id IS NULL;
ALTER TABLE identity.api_requests ALTER COLUMN key_id SET NOT NULL;
DROP FUNCTION identity.oauth_authorizer_active(uuid, boolean);
DROP TABLE identity.oauth_client_documents;
DROP TABLE identity.oauth_tokens;
DROP TABLE identity.oauth_codes;
DROP TABLE identity.oauth_connections;
