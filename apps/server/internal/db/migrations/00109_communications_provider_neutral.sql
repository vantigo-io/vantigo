-- +goose Up
-- Communications without a named provider (Google Workspace design G3, G4,
-- G5, G11; docs/superpowers/specs/2026-11-03-google-workspace-design.md): a
-- mailbox channel's provider is the Key of a filled ChannelMailbox, so one
-- mailbox per channel holds per provider; the thread key a provider answers
-- is stored under a neutral name and found in either direction; a personal
-- message records the provider it is sent through; and a Message-ID a
-- provider replaced on send is kept beside the message's own, so a reply to
-- either threads.

-- One channel per mailbox, per provider (G4). The same mailbox id under two
-- providers is two mailboxes.
DROP INDEX communications.ux_channels_entra_mailbox;
CREATE UNIQUE INDEX ux_channels_provider_mailbox
    ON communications.channels (provider, (settings->>'mailbox'))
    WHERE settings ? 'mailbox';

-- The provider's thread id under its neutral key (G5), in every row that
-- has one.
UPDATE communications.conversation_messages
SET channel_metadata_json = (channel_metadata_json - 'graphConversationId')
        || jsonb_build_object('providerThreadId', channel_metadata_json->'graphConversationId')
WHERE channel_metadata_json IS NOT NULL
  AND channel_metadata_json ? 'graphConversationId';

-- Threading's third step by the provider's thread, a message of either
-- direction (G5): an outbound message records the thread its provider
-- answered.
DROP INDEX communications.ix_conversation_messages_graph_conversation;
CREATE INDEX ix_conversation_messages_provider_thread
    ON communications.conversation_messages ((channel_metadata_json->>'providerThreadId'));

-- The personal provider a message is sent through (G3): written with
-- from_address when send_as = 'personal'. Every existing one went through
-- Entra, the only provider there was.
ALTER TABLE communications.conversation_messages
    ADD COLUMN send_provider   varchar(20),
    ADD COLUMN sent_message_id varchar(998);
UPDATE communications.conversation_messages
SET send_provider = 'entra'
WHERE send_as = 'personal';

-- Threading's first two steps match In-Reply-To and References against
-- rfc_message_id or sent_message_id (G5).
CREATE INDEX ix_conversation_messages_sent_message_id
    ON communications.conversation_messages (sent_message_id)
    WHERE sent_message_id IS NOT NULL;

-- +goose Down
DROP INDEX communications.ix_conversation_messages_sent_message_id;
ALTER TABLE communications.conversation_messages
    DROP COLUMN sent_message_id,
    DROP COLUMN send_provider;
DROP INDEX communications.ix_conversation_messages_provider_thread;
UPDATE communications.conversation_messages
SET channel_metadata_json = (channel_metadata_json - 'providerThreadId')
        || jsonb_build_object('graphConversationId', channel_metadata_json->'providerThreadId')
WHERE channel_metadata_json IS NOT NULL
  AND channel_metadata_json ? 'providerThreadId';
CREATE INDEX ix_conversation_messages_graph_conversation
    ON communications.conversation_messages ((channel_metadata_json->>'graphConversationId'))
    WHERE direction = 'inbound';
DROP INDEX communications.ux_channels_provider_mailbox;
CREATE UNIQUE INDEX ux_channels_entra_mailbox
    ON communications.channels ((settings->>'mailbox')) WHERE provider = 'entra';
