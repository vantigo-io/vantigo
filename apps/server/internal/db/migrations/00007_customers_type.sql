-- +goose Up
-- Every customer is either a business or a private person, whether or not
-- it carries legal-identity columns: a foreign company never looked up in
-- Brreg is still a business, and a private customer needs no identifier at
-- all. Until now the distinction lived only in legal_type, which is null
-- for every customer with no legal_* values. The Go value object restricts
-- the column to 'business' and 'person' (validateCustomerType), the same
-- convention as status — no CHECK constraint, like the baselines.
ALTER TABLE customers.customers
    ADD COLUMN type varchar(20) NOT NULL DEFAULT 'business';

-- Backfill from legal_type where it already says person; every other
-- customer keeps the default.
UPDATE customers.customers SET type = 'person' WHERE legal_type = 'person';

-- +goose Down
ALTER TABLE customers.customers DROP COLUMN type;
