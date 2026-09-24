-- +goose Up
-- The merge marker (customers merge design D3): which customer this one was
-- merged into, NULL for every customer that was not. A merged-away customer is
-- archived, never deleted — history stays readable and its page links to the
-- survivor through this column — so it is a pointer inside the customers
-- table, not a tombstone table of its own.
--
-- The foreign key is this module's own table, the group_id precedent (00027):
-- no module boundary is crossed. ON DELETE RESTRICT because nothing may take
-- a survivor away from under the customers merged into it; customers are not
-- deleted today, and if that ever changes this is the line that says the
-- marker has to be dealt with first. Partial, like ix_customers_group: the
-- merged-away are a handful among many, and the one lookup keyed by this
-- column (who was merged into X — the RESTRICT check itself) wants only them.
ALTER TABLE customers.customers
    ADD COLUMN merged_into_customer_id integer REFERENCES customers.customers (id) ON DELETE RESTRICT;
CREATE INDEX ix_customers_merged_into ON customers.customers (merged_into_customer_id)
    WHERE merged_into_customer_id IS NOT NULL;

-- +goose Down
ALTER TABLE customers.customers DROP COLUMN merged_into_customer_id;
