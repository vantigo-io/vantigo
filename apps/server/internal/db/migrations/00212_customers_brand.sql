-- +goose Up
-- A default brand per customer and per customer group (brands design B27, B28,
-- delivery E1). Both columns are NULL for "decides nothing": a customer with no
-- brand of its own takes its group's, and a customer with neither takes the
-- workspace's default brand. The order is resolved at read, never stored.
--
-- No foreign key. A brand is identity's row, and this module may not reference
-- another module's schema (internal/db/schema_test.go bars it, depguard bars the
-- import) — the same reason customers.customers.owner_user_id has none. The
-- value is checked at write through contracts.Brands, and what happens when a
-- brand is archived afterwards is decided at read: the customer's default falls
-- back to the next one in the order, with a notice, rather than blocking the
-- brand's archive or leaving a customer without one.
--
-- Not personal data, so anonymising a customer leaves brand_id as it is, like
-- the owner and the billing terms: it says how the customer is invoiced, not who
-- it is. A merge leaves the absorbed customer's row as it was.
ALTER TABLE customers.customers ADD COLUMN brand_id uuid;
ALTER TABLE customers.customer_groups ADD COLUMN brand_id uuid;

-- +goose Down
ALTER TABLE customers.customer_groups DROP COLUMN brand_id;
ALTER TABLE customers.customers DROP COLUMN brand_id;
