-- +goose Up
-- A customer's own contact details (invoice-ready design D2): what reaches the
-- customer itself rather than one of its contacts. NULL is "none given".
ALTER TABLE customers.customers
    ADD COLUMN email   varchar(255),
    ADD COLUMN phone   varchar(30),
    ADD COLUMN website varchar(2048);

-- +goose Down
ALTER TABLE customers.customers DROP COLUMN website, DROP COLUMN phone, DROP COLUMN email;
