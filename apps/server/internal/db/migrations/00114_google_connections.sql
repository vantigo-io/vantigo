-- +goose Up
-- The person's own Google connection (Google Workspace design G25–G29,
-- docs/superpowers/specs/2026-11-03-google-workspace-design.md; delivery D,
-- docs/superpowers/plans/2026-11-03-google-connection.md): what Entra's
-- entra.outlook_connections (00089) is for Outlook.
--
-- One row per Google account by its Directory id (google_user_id, which the ID
-- token's sub carries, U1): user_id identity's user (no foreign key across
-- schemas), scopes as Google answered them (sorted), refresh_sealed the
-- refresh token sealed under its own purpose (google/connection-refresh) and
-- NULL once forgotten (a leaver, a deleted account), state 'active',
-- 'reconnect' (Google refused the token; error_code says why) or 'closing'
-- (disconnected, left, suspended, the switch turned off: kept while its
-- removals may need it, then deleted by google-sweep, which revokes its token).
-- account_checked_at is when google-sweep last read the account of a row in
-- reconnect; changed_at when the state last moved, read through its index by
-- the calendar's Connections (delivery E). Every write is guarded by revision,
-- so a refresh never overwrites a newer connect.
--
-- One live row per person: the partial unique index admits any number of
-- closing rows beside it.
--
-- google.connection gains the switch, Connection for people, when it was last
-- turned on, and the last failure of the web client itself an Owner must act
-- on (consent_missing, invalid_client, unauthorized_client).
CREATE TABLE google.connections (
    google_user_id     varchar(32)  PRIMARY KEY,   -- the account's sub = Directory id
    user_id            uuid         NOT NULL,      -- identity's user, no FK
    scopes             text[]       NOT NULL,      -- as granted, sorted
    refresh_sealed     bytea,                      -- google/connection-refresh; NULL once forgotten
    state              varchar(20)  NOT NULL,      -- active | reconnect | closing
    error_code         varchar(40),
    connected_at       timestamptz  NOT NULL,
    refreshed_at       timestamptz,
    used_at            timestamptz,
    account_checked_at timestamptz,
    changed_at         timestamptz  NOT NULL,
    revision           bigint       NOT NULL DEFAULT 1,
    created_at         timestamptz  NOT NULL,
    updated_at         timestamptz  NOT NULL,
    CONSTRAINT connections_state_check CHECK (state IN ('active', 'reconnect', 'closing'))
);
CREATE UNIQUE INDEX ux_google_connections_user ON google.connections (user_id) WHERE state <> 'closing';
CREATE INDEX ix_google_connections_changed ON google.connections (changed_at);

ALTER TABLE google.connection
    ADD COLUMN personal_connection    boolean     NOT NULL DEFAULT false,
    ADD COLUMN personal_connection_at timestamptz,
    ADD COLUMN personal_last_error    varchar(40),
    ADD COLUMN personal_last_error_at timestamptz;

-- +goose Down
-- The rows go with their sealed tokens, unrevoked: Google lets an unused
-- refresh token lapse, and a person revokes the grant in their Google
-- account's security page.
ALTER TABLE google.connection
    DROP COLUMN personal_last_error_at,
    DROP COLUMN personal_last_error,
    DROP COLUMN personal_connection_at,
    DROP COLUMN personal_connection;
DROP TABLE google.connections;
