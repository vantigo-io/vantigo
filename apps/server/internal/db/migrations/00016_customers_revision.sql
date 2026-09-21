-- +goose Up
-- The customer row's optimistic-concurrency token (customers foundation design
-- D5): every write adds one, and an update that names the revision it read is
-- refused when the row has moved on.
ALTER TABLE customers.customers ADD COLUMN revision integer NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE customers.customers DROP COLUMN revision;
