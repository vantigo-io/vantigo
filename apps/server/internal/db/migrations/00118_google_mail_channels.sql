-- +goose Up
-- Gmail mail channels (Google Workspace design G33, G34,
-- docs/superpowers/specs/2026-11-03-google-workspace-design.md; delivery F,
-- docs/superpowers/plans/2026-11-03-google-mail-channels.md): what Entra's
-- mail channels (00090) are for Exchange Online.
--
-- mail_channels is the Owner's switch on the Calendar and Gmail tab: while it
-- is on, Vantigo asks the admin to delegate gmail.modify and gmail.send to the
-- service account, and contracts.ChannelMailbox is available once the test
-- found them delegated. Domain-wide delegation cannot be limited to some
-- mailboxes: the scopes reach every mailbox in the domain, and Vantigo uses
-- them only for the mailboxes recorded here.
--
-- google.channel_mailboxes is every mailbox ChannelMailbox.Check resolved, by
-- the user's Directory id, with the primary address the Gmail calls
-- impersonate (lower case) and when it was last checked. The module answers
-- only for a mailbox recorded here; Communications is the authority on which
-- mailboxes are channels, and ChannelMailbox.Forget deletes the row when a
-- channel leaves the mailbox. Never an account Staff manages for a person
-- (google.accounts): a person's own mailbox is never a channel (P2).
ALTER TABLE google.connection ADD COLUMN mail_channels boolean NOT NULL DEFAULT false;
CREATE TABLE google.channel_mailboxes (
    google_user_id varchar(32)  PRIMARY KEY,   -- the user's Directory id
    address        varchar(320) NOT NULL,      -- the primary address, lower case
    checked_at     timestamptz  NOT NULL       -- the last Check that reached it
);

-- +goose Down
DROP TABLE google.channel_mailboxes;
ALTER TABLE google.connection DROP COLUMN mail_channels;
