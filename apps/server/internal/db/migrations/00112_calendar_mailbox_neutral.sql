-- +goose Up
-- The calendar's mailbox sync on any provider, in neutral words (Google
-- Workspace design G3, G10; docs/superpowers/specs/2026-11-03-google-workspace-design.md).
--
-- calendar.mailbox_sync gains provider, the key of the MailboxCalendar
-- provider whose mailbox the worker last confirmed (Workforce.DirectoryAccount's
-- Provider): set with mailbox whenever a sync confirms the account, and the
-- provider the clean-up runs on. A sync whose account's provider differs from
-- the row's is a changed mailbox (calendar Outlook sync design O8): the
-- clean-up runs on the row's provider, then the row takes the new one. Every
-- mailbox written before this migration was Entra's.
--
-- The change notifications' and connections' marks name a provider's
-- mailboxes, so 00086's index on lower(mailbox) becomes one on
-- (provider, lower(mailbox)) over the rows that are on.
--
-- calendar.mailbox_busy.show_as takes the calendar's own words: 'oof' is
-- 'out_of_office' and 'workingElsewhere' 'working_elsewhere' (no CHECK holds
-- the column to its set; the module's validation does).
ALTER TABLE calendar.mailbox_sync
    ADD COLUMN provider varchar(20);  -- the provider of mailbox; null while none is confirmed

UPDATE calendar.mailbox_sync SET provider = 'entra' WHERE mailbox IS NOT NULL;

DROP INDEX calendar.ix_calendar_mailbox_sync_mailbox;
CREATE INDEX ix_calendar_mailbox_sync_provider_mailbox
    ON calendar.mailbox_sync (provider, lower(mailbox)) WHERE enabled;

UPDATE calendar.mailbox_busy SET show_as = 'out_of_office' WHERE show_as = 'oof';
UPDATE calendar.mailbox_busy SET show_as = 'working_elsewhere' WHERE show_as = 'workingElsewhere';

-- +goose Down
UPDATE calendar.mailbox_busy SET show_as = 'workingElsewhere' WHERE show_as = 'working_elsewhere';
UPDATE calendar.mailbox_busy SET show_as = 'oof' WHERE show_as = 'out_of_office';

DROP INDEX calendar.ix_calendar_mailbox_sync_provider_mailbox;
CREATE INDEX ix_calendar_mailbox_sync_mailbox ON calendar.mailbox_sync (lower(mailbox)) WHERE enabled;

ALTER TABLE calendar.mailbox_sync DROP COLUMN provider;
