-- +goose Up
-- The review queue, calibration, activity, retention and erasure of the agents
-- module (virtual employees design, delivery A3: V20, V21, V25, V26).

-- The channel a held item's conversation belongs to, so that unbinding a channel
-- withdraws the items that wait on it (design §10).
ALTER TABLE agents.review_items ADD COLUMN channel_id uuid;

-- What the pipeline decided for a run's reply: the mode, the threshold, the
-- score before and after the sources were checked, the valid sources, the rules
-- that held or stopped it and whether it was sent. The activity screen shows it,
-- also for a reply that went out by itself and so has no review item.
ALTER TABLE agents.runs ADD COLUMN verdict jsonb;

-- The customer and contacts a conversation was linked to when this module last
-- read it. Communications is not this module's to ask when a customer or a
-- contact is erased (V26), so what it saw is kept: the erasure of a customer
-- reaches every conversation the module saw linked to them.
CREATE TABLE agents.conversation_links (
    conversation_id uuid        PRIMARY KEY,
    customer_id     integer,
    contact_ids     integer[]   NOT NULL DEFAULT '{}',
    seen_at         timestamptz NOT NULL
);
CREATE INDEX ix_conversation_links_customer ON agents.conversation_links (customer_id) WHERE customer_id IS NOT NULL;
CREATE INDEX ix_conversation_links_contacts ON agents.conversation_links USING gin (contact_ids);

-- Retention (V26) deletes by age; the worker reads the oldest first.
CREATE INDEX ix_runs_started ON agents.runs (started_at);

-- +goose Down
DROP INDEX agents.ix_runs_started;
DROP TABLE agents.conversation_links;
ALTER TABLE agents.runs DROP COLUMN verdict;
ALTER TABLE agents.review_items DROP COLUMN channel_id;
