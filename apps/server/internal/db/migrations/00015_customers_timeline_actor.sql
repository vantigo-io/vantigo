-- +goose Up
-- Who wrote a timeline entry, and who made each revision of it (customers
-- foundation design D1). NULL for everything written before this migration and
-- for a write made with no user principal: nothing can be said about those.
ALTER TABLE customers.customers_timeline_entries ADD COLUMN actor_user_id uuid;
ALTER TABLE customers.customers_timeline_entries_revisions ADD COLUMN actor_user_id uuid;

-- +goose Down
ALTER TABLE customers.customers_timeline_entries_revisions DROP COLUMN actor_user_id;
ALTER TABLE customers.customers_timeline_entries DROP COLUMN actor_user_id;
