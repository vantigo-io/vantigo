-- +goose Up
-- Anthropic is a provider (the Assistant design S18). A Claude model's thinking
-- blocks are valid only in the conversation that produced them, so the
-- Assistant keeps every conversation append-only: it stores each assistant
-- turn's provider blocks to replay them unchanged, freezes the system prompt at
-- the conversation's first run, and shortens a long conversation by a summary
-- instead of editing its turns.

-- An assistant turn as the provider returned it: for Anthropic the content
-- blocks, thinking blocks with their signatures included. Opaque, never sent to
-- the interface, and null for every other provider. It is json, not jsonb: it
-- must come back byte for byte, and jsonb would reorder its keys. The cache counts are the
-- prompt tokens read from and written to the provider's prompt cache.
ALTER TABLE assistant.messages ADD COLUMN provider json;
ALTER TABLE assistant.messages ADD COLUMN cache_read_tokens integer;
ALTER TABLE assistant.messages ADD COLUMN cache_write_tokens integer;

-- What a conversation freezes: the system prompt it ran under from its first
-- run, with the prompt's version, and the compaction marker once it was
-- summarised. The summary stands for every message up to summary_through_seq;
-- the messages themselves stay, only the next request leaves them out. One row
-- a conversation, kept apart from it so a list of conversations does not carry
-- a prompt each, and deleted with it.
CREATE TABLE assistant.conversation_state (
    conversation_id      uuid        PRIMARY KEY REFERENCES assistant.conversations (id) ON DELETE CASCADE,
    system_prompt        text        NOT NULL,
    prompt_version       varchar(40) NOT NULL,
    summary              text,
    summary_through_seq  bigint,
    summarized_at        timestamptz,
    CHECK ((summary IS NULL) = (summary_through_seq IS NULL))
);

-- +goose Down
DROP TABLE assistant.conversation_state;
ALTER TABLE assistant.messages DROP COLUMN cache_write_tokens;
ALTER TABLE assistant.messages DROP COLUMN cache_read_tokens;
ALTER TABLE assistant.messages DROP COLUMN provider;
