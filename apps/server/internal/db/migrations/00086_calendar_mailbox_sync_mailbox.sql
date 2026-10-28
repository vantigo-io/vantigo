-- +goose Up
-- The change notifications' marks (calendar Outlook sync design O11.6;
-- delivery D): each cycle the calendar-mailbox-sync worker makes due the
-- syncs of the mailboxes notified, by the mailbox compared
-- case-insensitively, among the rows that are on (MarkMailboxSyncNotified).
-- This index is what that statement reads, so a cycle of up to 5000 notices
-- is one statement through it, not a scan of every row.
CREATE INDEX ix_calendar_mailbox_sync_mailbox ON calendar.mailbox_sync (lower(mailbox)) WHERE enabled;

-- +goose Down
DROP INDEX calendar.ix_calendar_mailbox_sync_mailbox;
