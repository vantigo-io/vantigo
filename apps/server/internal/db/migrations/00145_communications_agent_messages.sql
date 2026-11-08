-- +goose Up
-- The agent a message was sent as (virtual employees design V11): Communications
-- sets it from the principal of an agent grant, never from a field the caller
-- sends. It is an opaque id of the agents module, with no foreign key (module
-- boundaries rule 4), and null for a message a person wrote.
ALTER TABLE communications.conversation_messages ADD COLUMN agent_id uuid;

-- +goose Down
ALTER TABLE communications.conversation_messages DROP COLUMN agent_id;
