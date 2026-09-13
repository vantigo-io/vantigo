-- Communications' AI feature (SV/CommunicationsAiService.cs, inventory
-- §17): the interaction audit row, the inbound-message context window, the
-- latest-message pointer that row carries, and the suggestion write-back.

-- name: InsertAiInteraction :exec
-- NewInteraction plus its save (:214-226, inventory §17.5). Every non-503
-- call writes exactly one row — success, both guard rejections, the
-- validation rejection, and the exception path alike, not only success.
-- No prompt, no context, no model output and no customer text is ever
-- stored: only the digest, short summary labels, timings and token counts.
-- That is the module's privacy posture for AI and the reason this insert
-- has no body column to write one into.
INSERT INTO communications.ai_interactions (
    id, conversation_id, message_id, operation, requester_user_id, provider, model,
    context_digest, context_version, result_summary, validation_summary, error_summary,
    duration_ms, input_token_count, output_token_count, created_at
) VALUES (
    @id, @conversation_id, @message_id, @operation, @requester_user_id, @provider, @model,
    @context_digest, @context_version, @result_summary, @validation_summary, @error_summary,
    @duration_ms, @input_token_count, @output_token_count, @created_at
);

-- name: ListInboundMessagesForAiContext :many
-- BuildInboundContext's window (:209-210, inventory §17.2 step 3): the
-- NEWEST 20 inbound messages, selected occurred_at DESCENDING and limited
-- here; the caller reverses the slice so the prompt renders them
-- oldest-first. Selecting the oldest 20 ascending instead is a genuinely
-- different set for any conversation with more than 20 messages, and is the
-- mistranslation this ordering exists to avoid — pinned by
-- TestAiDraft_ContextTakesTheNewestTwentyInboundMessagesOldestFirst.
--
-- html_body is never read, and outbound and internal_note messages are
-- never included: the AI context is inbound text only.
SELECT m.text_body, m.occurred_at
FROM communications.conversation_messages m
WHERE m.conversation_id = @conversation_id AND m.direction = 'inbound'
ORDER BY m.occurred_at DESC
LIMIT 20;

-- name: GetLatestMessageIDForAiInteraction :one
-- LatestMessageId (:212): the newest message by occurred_at in ANY
-- direction. Deliberately a wider set than the inbound-only context above —
-- a conversation whose newest message is outbound still points its audit row
-- at that message. No rows means a conversation with no messages at all,
-- where ai_interactions.message_id stays null.
SELECT m.id
FROM communications.conversation_messages m
WHERE m.conversation_id = @conversation_id
ORDER BY m.occurred_at DESC
LIMIT 1;

-- name: UpdateConversationSuggestedCustomer :exec
-- SuggestCustomerAsync's accepted-suggestion write (:154-160): the only
-- path in the module that touches these three columns. A suggestion below
-- the 0.70 confidence threshold writes nothing to the conversation at all
-- (inventory §17.4 item 8), so this statement is never reached for one.
UPDATE communications.conversations
SET suggested_customer_id = @suggested_customer_id,
    suggested_customer_confidence = @suggested_customer_confidence,
    suggested_customer_reasoning = @suggested_customer_reasoning
WHERE id = @id;
