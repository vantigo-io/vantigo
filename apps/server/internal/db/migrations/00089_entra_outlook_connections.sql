-- +goose Up
-- The person's delegated Outlook connection (Outlook connection and
-- Microsoft 365 mail design M3,
-- docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md;
-- delivery A, docs/superpowers/plans/2026-10-29-entra-outlook-connection.md).
--
-- One row per Entra account by its object id: user_id identity's user (no
-- foreign key across schemas), tenant_id the tenant that issued it, scopes
-- as granted (lower-cased, sorted), refresh_sealed the refresh token sealed
-- under its own purpose (entra/outlook-refresh) and NULL once forgotten (a
-- leaver, a deleted account), state 'active', 'reconnect' (Microsoft
-- refused the token; error_code says why) or 'closing' (disconnected, left,
-- the switch turned off: kept while its removals may need it, then deleted
-- by entra-outlook-sweep). account_checked_at is when the account's
-- accountEnabled was last read for a row in reconnect; changed_at when the
-- state last moved, read through its index. Every write is guarded by
-- revision, so a refresh never overwrites a newer connect.
--
-- One live row per person: the partial unique index admits any number of
-- closing rows beside it.
--
-- By house style no CHECK holds a value to its set: the module's validation
-- does.
CREATE TABLE entra.outlook_connections (
    object_id          uuid         PRIMARY KEY,
    user_id            uuid         NOT NULL,      -- identity's user, no FK
    tenant_id          uuid         NOT NULL,
    scopes             text[]       NOT NULL,      -- as granted, lower-cased, sorted
    refresh_sealed     bytea,                      -- entra/outlook-refresh; NULL once forgotten
    state              varchar(20)  NOT NULL,      -- 'active' | 'reconnect' | 'closing'
    error_code         varchar(40),
    connected_at       timestamptz  NOT NULL,
    refreshed_at       timestamptz,
    used_at            timestamptz,
    account_checked_at timestamptz,                -- the daily accountEnabled read of a row in reconnect
    changed_at         timestamptz  NOT NULL,
    revision           bigint       NOT NULL DEFAULT 1,
    created_at         timestamptz  NOT NULL,
    updated_at         timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_entra_outlook_connections_user ON entra.outlook_connections (user_id)
    WHERE state <> 'closing';
CREATE INDEX ix_entra_outlook_connections_changed ON entra.outlook_connections (changed_at);

-- +goose Down
DROP TABLE entra.outlook_connections;
