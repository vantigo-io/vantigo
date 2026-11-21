-- +goose Up
-- OAuth connections, delivery C (OAuth connections design C8, C11, A18,
-- docs/superpowers/specs/2026-11-21-oauth-connections-design.md): clients an
-- Owner registers by hand, for the clients that publish no metadata
-- document; the list of client hosts that may connect; and the request
-- log's index for its filter by connection. Only identity writes them.

-- A registered client (C8): a public client with PKCE and no secret, named by
-- a vtc_ client_id Vantigo mints. name is what the consent page and Connected
-- apps show; redirect_uris are the only addresses a code is sent to, each
-- held to the rules a metadata document's are (C9). version moves on every
-- change and is the If-Match of an edit. An archived client admits no new
-- sign-in, and archiving it revoked its connections (client_archived).
CREATE TABLE identity.oauth_clients (
    id            uuid PRIMARY KEY,
    client_id     varchar(28)  NOT NULL,
    name          varchar(80)  NOT NULL,
    redirect_uris text[]       NOT NULL,
    created_by    uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    created_at    timestamptz  NOT NULL,
    updated_at    timestamptz  NOT NULL,
    version       uuid         NOT NULL,
    archived_at   timestamptz,
    archived_by   uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    CONSTRAINT ck_oauth_clients_client_id CHECK (client_id ~ '^vtc_[0-9A-Za-z]{24}$'),
    CONSTRAINT ck_oauth_clients_redirect_uris CHECK (cardinality(redirect_uris) BETWEEN 1 AND 20)
);
CREATE UNIQUE INDEX ux_oauth_clients_client_id ON identity.oauth_clients (client_id);
CREATE INDEX ix_oauth_clients_created_by ON identity.oauth_clients (created_by) WHERE created_by IS NOT NULL;
CREATE INDEX ix_oauth_clients_archived_by ON identity.oauth_clients (archived_by) WHERE archived_by IS NOT NULL;

-- A connection's client_id is looked up when its client is archived.
CREATE INDEX ix_oauth_connections_client_id ON identity.oauth_connections (client_id);

-- The client hosts that may connect by their metadata document (C11): empty,
-- the default, admits any; otherwise a client_id's host must equal an entry
-- or be a subdomain of one. Registered clients are always admitted.
ALTER TABLE identity.system_settings ADD COLUMN oauth_client_hosts text[] NOT NULL DEFAULT '{}';

-- The request log's filter by connection (A18). Partial: a key's request
-- leaves connection_id NULL, and only a connection's rows are indexed.
CREATE INDEX ix_api_requests_connection_at ON identity.api_requests (connection_id, at DESC) WHERE connection_id IS NOT NULL;

-- +goose Down
DROP INDEX identity.ix_api_requests_connection_at;
ALTER TABLE identity.system_settings DROP COLUMN oauth_client_hosts;
DROP INDEX identity.ix_oauth_connections_client_id;
DROP TABLE identity.oauth_clients;
