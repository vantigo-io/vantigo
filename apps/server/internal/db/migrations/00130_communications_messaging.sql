-- +goose Up
-- A messaging channel in Communications (Messenger design N1, N3, N4, N7;
-- docs/superpowers/specs/2026-11-05-communications-messenger-design.md,
-- delivery A): a message carries components — answer options, a request for
-- an e-mail address, cards — beside its text, an attachment is a file or a
-- card's image, each Messenger channel has the state of its own sync worker,
-- and a message sent as ordered parts keeps the progress of each part. Nothing
-- here is needed for a channel's type, which is free text already.

ALTER TABLE communications.conversation_messages ADD COLUMN components jsonb;

-- A card's image is an attachment of its message (role 'card'), never sent as a
-- part of its own and never a download in the thread's file list.
ALTER TABLE communications.message_attachments
    ADD COLUMN role varchar(10) NOT NULL DEFAULT 'file',
    ADD CONSTRAINT ck_message_attachments_role CHECK (role IN ('file', 'card'));

-- When a Messenger participant's profile — its name — was last read from the
-- platform: it is fetched when the participant is new and again when it is
-- older than 30 days (N3). A table of its own, so the participants' rows and
-- every query that returns them stay as they were; an e-mail participant has no
-- row, its name coming with its mail.
CREATE TABLE communications.participant_profiles (
    participant_id  uuid        PRIMARY KEY REFERENCES communications.participants (id) ON DELETE CASCADE,
    fetched_at      timestamptz NOT NULL
);

-- Each Messenger channel's sync state, read and written by the
-- communications-messenger-sync worker under its lease (N3). cursor is the
-- provider's own insertion id: the last event of the channel's inbox in
-- Communications. notified is set by the provider's Notified and cleared by a
-- finished pass. lease_id is the outbox's hex form (32 characters), as
-- channel_sync's.
CREATE TABLE communications.messenger_sync (
    channel_id      uuid        PRIMARY KEY REFERENCES communications.channels (id) ON DELETE CASCADE,
    cursor          bigint      NOT NULL DEFAULT 0,
    notified        boolean     NOT NULL DEFAULT false,
    next_sync_at    timestamptz NOT NULL,
    lease_id        varchar(32),
    lease_until     timestamptz,
    state           varchar(20) NOT NULL,
    last_error      varchar(40),
    attempts        integer     NOT NULL DEFAULT 0,
    last_synced_at  timestamptz,
    updated_at      timestamptz NOT NULL,
    CONSTRAINT ck_messenger_sync_state CHECK (state IN ('active', 'failed', 'stopped'))
);
CREATE INDEX ix_messenger_sync_due ON communications.messenger_sync (next_sync_at);

-- The progress of a message sent as ordered parts (N7): attempted_at is written
-- immediately before the part's Send, platform_message_id and sent_at
-- immediately after, each in a statement of its own outside any transaction.
-- A job claimed again skips the parts with a sent_at; one with attempted_at and
-- no sent_at is the per-part possible duplicate. platform_message_id is how an
-- echo of our own is told from one sent elsewhere (N12).
CREATE TABLE communications.messenger_parts (
    message_id           uuid         NOT NULL REFERENCES communications.conversation_messages (id) ON DELETE CASCADE,
    part_index           integer      NOT NULL,
    attempted_at         timestamptz,
    platform_message_id  varchar(400),
    sent_at              timestamptz,
    PRIMARY KEY (message_id, part_index)
);
CREATE INDEX ix_messenger_parts_platform_message
    ON communications.messenger_parts (platform_message_id)
    WHERE platform_message_id IS NOT NULL;

-- +goose Down
DROP TABLE communications.messenger_parts;
DROP TABLE communications.messenger_sync;
DROP TABLE communications.participant_profiles;
ALTER TABLE communications.message_attachments
    DROP CONSTRAINT ck_message_attachments_role,
    DROP COLUMN role;
ALTER TABLE communications.conversation_messages DROP COLUMN components;
