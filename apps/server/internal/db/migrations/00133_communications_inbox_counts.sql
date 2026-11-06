-- +goose Up
-- The inbox counts (messenger design N27, delivery E): how many open
-- conversations a person has not read, and how many of those are theirs. The
-- Conversations button asks every 30 seconds, so the count has to be cheap.
-- Open conversations are found through the (status, last_activity_at) index and
-- the read state through its primary key; what had no index was the assignee,
-- and only open conversations are counted, so the index is partial.
CREATE INDEX ix_conversations_assigned_user_open
    ON communications.conversations (assigned_user_id)
    WHERE status = 'open';

-- +goose Down
DROP INDEX communications.ix_conversations_assigned_user_open;
