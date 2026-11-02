-- name: FreezeConversationPrompt :one
-- FreezeConversationPrompt stores the system prompt a conversation runs under,
-- at its first run, and answers the stored row: the winner's when two runs
-- race, so every run of one conversation sends the same prompt.
INSERT INTO assistant.conversation_state (conversation_id, system_prompt, prompt_version)
VALUES (@conversation_id, @system_prompt, @prompt_version)
ON CONFLICT (conversation_id) DO UPDATE SET conversation_id = EXCLUDED.conversation_id
RETURNING *;

-- name: GetConversationState :one
SELECT * FROM assistant.conversation_state WHERE conversation_id = @conversation_id;

-- name: SetConversationSummary :exec
-- SetConversationSummary is the compaction marker: the summary stands for every
-- message up to and including through_seq.
UPDATE assistant.conversation_state
SET summary = @summary, summary_through_seq = @through_seq, summarized_at = @now::timestamptz
WHERE conversation_id = @conversation_id;
