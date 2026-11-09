-- +goose Up
-- Who an internal note mentions (virtual employees design V32): the ids of the
-- users, people or agents, the author picked with the @ picker. The note's text
-- carries their names for the readers; this table is what the module acts on.
-- user_id is identity's, an opaque id with no foreign key (module boundaries
-- rule 4). A note's mentions go with the note.
CREATE TABLE communications.conversation_message_mentions (
    message_id uuid NOT NULL REFERENCES communications.conversation_messages (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL,
    PRIMARY KEY (message_id, user_id)
);

-- +goose Down
DROP TABLE communications.conversation_message_mentions;
