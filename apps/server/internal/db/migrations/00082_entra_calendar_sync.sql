-- +goose Up
-- The admin switch for the calendar's Outlook sync (calendar Outlook sync
-- design O2, docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md):
-- an Owner turns it on in the Entra module's settings, beside the account
-- rules, to let people sync their Vantigo calendar to their mailbox. It is
-- not a required permission of the connection: provisioning works with it
-- off, and Calendars.ReadWrite is asked for only while it is on.
ALTER TABLE entra.connection ADD COLUMN calendar_sync boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE entra.connection DROP COLUMN calendar_sync;
