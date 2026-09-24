-- name: RepointConversationsCustomer :execrows
-- RepointConversationsCustomer, RepointConversationsSuggestedCustomer and
-- RepointConversationCustomerCandidates are communications' half of a
-- customer merge (customers merge design D1, contracts.CustomerReferenceHolder),
-- all three inside the customers module's own transaction. A conversation
-- about the absorbed customer is about the survivor; how it came to be
-- associated (customer_association_source) is unchanged.
UPDATE communications.conversations
SET customer_id = @into_customer_id::integer
WHERE customer_id = @from_customer_id::integer;

-- name: RepointConversationsSuggestedCustomer :execrows
-- A suggestion naming the absorbed customer names the survivor: it was the
-- same entity all along.
UPDATE communications.conversations
SET suggested_customer_id = @into_customer_id::integer
WHERE suggested_customer_id = @from_customer_id::integer;

-- name: RepointConversationCustomerCandidates :one
-- The candidate list is the one place a re-point can collide: a conversation
-- may already list the survivor beside the absorbed customer, and
-- (conversation_id, customer_id) is the primary key. So the absorbed rows are
-- deleted and re-inserted for the survivor with ON CONFLICT DO NOTHING — one
-- statement, a conversation that listed both keeps the survivor once, and
-- each candidate keeps its created_at. moved is how many the absorbed customer
-- had; added how many of those were new to the survivor.
WITH gone AS (
    DELETE FROM communications.conversation_customer_candidates
    WHERE customer_id = @from_customer_id::integer
    RETURNING conversation_id, created_at
), kept AS (
    INSERT INTO communications.conversation_customer_candidates (conversation_id, customer_id, created_at)
    SELECT conversation_id, @into_customer_id::integer, created_at FROM gone
    ON CONFLICT (conversation_id, customer_id) DO NOTHING
    RETURNING conversation_id
)
SELECT (SELECT count(*) FROM gone)::bigint AS moved, (SELECT count(*) FROM kept)::bigint AS added;
