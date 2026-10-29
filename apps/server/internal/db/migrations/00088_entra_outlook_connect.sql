-- +goose Up
-- The Owner's switch becomes the Outlook connection's (Outlook connection
-- and Microsoft 365 mail design M2,
-- docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md):
-- calendar_sync, which let the calendar sync on the application permission,
-- is renamed outlook_connect, which lets people connect their own Outlook.
-- The rename breaks nothing: the calendar never reads the column, only
-- contracts.MailboxStatus, whose states do not change, and nothing outside
-- Entra reads it at all.
--
-- outlook_connect_at is when the switch was last turned on (a connection
-- made since then makes the status ready rather than untested);
-- outlook_last_error and outlook_last_error_at the last validated connect's
-- failure (consent_missing), cleared by the next connect that works. The
-- UPDATE gives an installation that turned the unreleased switch on a start.
--
-- By house style no CHECK holds a value to its set: the module's validation
-- does.
ALTER TABLE entra.connection RENAME COLUMN calendar_sync TO outlook_connect;
ALTER TABLE entra.connection
    ADD COLUMN outlook_connect_at    timestamptz,   -- when the switch was last turned on
    ADD COLUMN outlook_last_error    varchar(40),   -- the last validated connect's failure (consent_missing)
    ADD COLUMN outlook_last_error_at timestamptz;
UPDATE entra.connection SET outlook_connect_at = now() WHERE outlook_connect;

-- +goose Down
ALTER TABLE entra.connection
    DROP COLUMN outlook_last_error_at,
    DROP COLUMN outlook_last_error,
    DROP COLUMN outlook_connect_at;
ALTER TABLE entra.connection RENAME COLUMN outlook_connect TO calendar_sync;
