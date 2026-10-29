-- +goose Up
-- A channel's settings that are not a secret's (Outlook connection and
-- Microsoft 365 mail design M12,
-- docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md;
-- delivery C, docs/superpowers/plans/2026-10-29-communications-m365-channels.md).
--
-- An SMTP channel keeps its settings and its sealed password on its
-- channel_credentials row, as before, and settings stays '{}'. A Microsoft
-- 365 channel (provider 'entra') has no secret and no credential row: it
-- holds {"mailbox": "<object id>"} here, the mailbox contracts.ChannelMailbox
-- resolved.
--
-- One channel per mailbox: two aliases of one shared mailbox would otherwise
-- make two channels on one entra.channel_mailboxes row, and Forget of one
-- would orphan the other.
ALTER TABLE communications.channels ADD COLUMN settings jsonb NOT NULL DEFAULT '{}';
CREATE UNIQUE INDEX ux_channels_entra_mailbox
    ON communications.channels ((settings->>'mailbox')) WHERE provider = 'entra';

-- +goose Down
DROP INDEX communications.ux_channels_entra_mailbox;
ALTER TABLE communications.channels DROP COLUMN settings;
