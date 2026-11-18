-- +goose Up
-- A person's date of birth (invoices phase 6 design I30, delivery D): an
-- optional fact on a private customer, read through the billing profile to
-- find the person for eFaktura, never printed, and blanked by anonymisation.
-- A business never has one: a change of type to business clears it in the
-- same update (SetCustomerType).
ALTER TABLE customers.customers
    ADD COLUMN date_of_birth date,
    ADD CONSTRAINT ck_customers_date_of_birth CHECK (date_of_birth IS NULL OR type = 'person');

-- +goose Down
ALTER TABLE customers.customers
    DROP CONSTRAINT ck_customers_date_of_birth,
    DROP COLUMN date_of_birth;
