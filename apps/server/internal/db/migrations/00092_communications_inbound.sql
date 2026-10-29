-- +goose Up
-- Receiving (Outlook connection and Microsoft 365 mail design M14,
-- docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md;
-- delivery D, docs/superpowers/plans/2026-10-29-communications-m365-inbound.md):
-- each Microsoft 365 channel's sync state, read and written by the
-- communications-mailbox-sync worker under its lease, and a receipt for every
-- message the worker saw, which makes an import idempotent. The inbound tables
-- the port dropped (00006's header) come back in this shape, not .NET's.
--
-- lease_id is the outbox's hex form (32 characters), not a uuid. notified is
-- read by the claim's ordering and set by nothing until mail change
-- notifications arrive (delivery E); a finished pass clears it. restart_from
-- is the instant a fresh read starts from once an expired delta link is
-- forgotten, kept until a pass finishes so a failed fresh read resumes there.
-- failing_item and failing_tries count the failed imports of the message a
-- page is held at: the third is given up on with a failed receipt.
CREATE TABLE communications.channel_sync (
    channel_id      uuid        PRIMARY KEY REFERENCES communications.channels (id) ON DELETE CASCADE,
    folder          varchar(40) NOT NULL DEFAULT 'inbox',
    delta_link      text,
    import_from     timestamptz NOT NULL,
    state           varchar(20) NOT NULL,
    error           varchar(40),
    next_sync_at    timestamptz NOT NULL,
    last_synced_at  timestamptz,
    attempts        integer     NOT NULL DEFAULT 0,
    lease_id        varchar(32),
    lease_until     timestamptz,
    notified        boolean     NOT NULL DEFAULT false,
    restart_from    timestamptz,
    failing_item    varchar(400),
    failing_tries   integer     NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL,
    CONSTRAINT ck_channel_sync_state CHECK (state IN ('pending', 'active', 'failed', 'stopped'))
);
CREATE INDEX ix_channel_sync_due ON communications.channel_sync (next_sync_at);

CREATE TABLE communications.inbound_receipts (
    id                  uuid          PRIMARY KEY,
    channel_id          uuid          NOT NULL REFERENCES communications.channels (id) ON DELETE CASCADE,
    provider_message_id varchar(400)  NOT NULL,
    rfc_message_id      varchar(998),
    message_id          uuid          REFERENCES communications.conversation_messages (id) ON DELETE SET NULL,
    outcome             varchar(20)   NOT NULL,
    reason              varchar(40),
    received_at         timestamptz   NOT NULL,
    created_at          timestamptz   NOT NULL,
    CONSTRAINT ck_inbound_receipts_outcome CHECK (outcome IN ('imported', 'duplicate', 'skipped', 'failed'))
);
CREATE UNIQUE INDEX ux_inbound_receipts_provider
    ON communications.inbound_receipts (channel_id, provider_message_id);
-- Only imported receipts: a duplicate carries its original's Message-Id.
CREATE UNIQUE INDEX ux_inbound_receipts_rfc
    ON communications.inbound_receipts (channel_id, rfc_message_id)
    WHERE rfc_message_id IS NOT NULL AND outcome = 'imported';
-- The newest receipt, a bound on an expired delta's restart point; the
-- receipts' sweep, by received_at.
CREATE INDEX ix_inbound_receipts_channel_received
    ON communications.inbound_receipts (channel_id, received_at DESC);
-- ON DELETE SET NULL from conversation_messages: retention deletes in batches.
CREATE INDEX ix_inbound_receipts_message_id ON communications.inbound_receipts (message_id);
-- Threading's fallback by Graph's conversationId (M15).
CREATE INDEX ix_conversation_messages_graph_conversation
    ON communications.conversation_messages ((channel_metadata_json->>'graphConversationId'))
    WHERE direction = 'inbound';

-- A channel made under delivery C receives from the day D ships, never its
-- past. An inactive one too: the claim skips it until it is reactivated.
INSERT INTO communications.channel_sync (channel_id, folder, import_from, state, next_sync_at, updated_at)
SELECT id, 'inbox', now(), 'pending', now(), now()
FROM communications.channels WHERE provider = 'entra';

-- +goose Down
DROP INDEX communications.ix_conversation_messages_graph_conversation;
DROP TABLE communications.inbound_receipts;
DROP TABLE communications.channel_sync;
