-- +goose Up
-- The inbox counts (messenger design N27, delivery E): how many open
-- conversations a person has not read, and how many of those are theirs. The
-- Conversations button asks every 30 seconds. The unread count walks the open
-- conversations through the (status, last_activity_at) index and probes each
-- one's read state by primary key, so it is O(open conversations) and this
-- index does not help it. The assigned count starts from the person's open
-- conversations, which had no index on the assignee; this partial one serves
-- exactly that query (status = 'open' AND assigned_user_id = $1).
CREATE INDEX ix_conversations_assigned_user_open
    ON communications.conversations (assigned_user_id)
    WHERE status = 'open';

-- +goose Down
DROP INDEX communications.ix_conversations_assigned_user_open;
