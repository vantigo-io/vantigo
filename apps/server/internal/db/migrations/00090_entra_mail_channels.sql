-- +goose Up
-- Mail channels: Communications sending through Exchange Online shared
-- mailboxes (Outlook connection and Microsoft 365 mail design M10 and M11,
-- docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md;
-- delivery C, docs/superpowers/plans/2026-10-29-communications-m365-channels.md).
--
-- mail_channels is the Owner's switch beside the Outlook connection: while it
-- is on, contracts.ChannelMailbox is available, its reach scoped to the
-- channel mailboxes by Exchange RBAC for Applications.
--
-- entra.channel_mailboxes is every mailbox ChannelMailbox.Check resolved, by
-- its object id (lower case), with its primary SMTP address (lower case) and
-- when it was last checked. Entra answers only for a mailbox recorded here;
-- Communications is the authority on which mailboxes are channels, and
-- ChannelMailbox.Forget deletes the row when a channel leaves the mailbox.
-- Nothing here is a person's: the rows are shared mailboxes, never
-- entra.accounts.
ALTER TABLE entra.connection ADD COLUMN mail_channels boolean NOT NULL DEFAULT false;
CREATE TABLE entra.channel_mailboxes (
    object_id  uuid         PRIMARY KEY,
    address    varchar(320) NOT NULL,   -- the primary SMTP address, lower case
    checked_at timestamptz  NOT NULL    -- the last Check that reached it
);

-- +goose Down
DROP TABLE entra.channel_mailboxes;
ALTER TABLE entra.connection DROP COLUMN mail_channels;
