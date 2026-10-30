-- +goose Up
-- Personal sending (Outlook connection and Microsoft 365 mail design M20,
-- docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md;
-- delivery F, docs/superpowers/plans/2026-10-30-communications-personal-sending.md):
-- an outbound message may be sent either from the conversation's channel
-- mailbox ('channel', the default) or from the author's connected Microsoft 365
-- address ('personal') with Reply-To set to the channel's address.
ALTER TABLE communications.conversation_messages
    ADD COLUMN send_as           varchar(20)  NOT NULL DEFAULT 'channel',
    ADD COLUMN from_address      varchar(320),
    ADD COLUMN from_display_name varchar(200),
    ADD CONSTRAINT ck_conversation_messages_send_as
        CHECK (send_as IN ('channel', 'personal'));

-- +goose Down
ALTER TABLE communications.conversation_messages
    DROP CONSTRAINT ck_conversation_messages_send_as,
    DROP COLUMN from_display_name,
    DROP COLUMN from_address,
    DROP COLUMN send_as;
