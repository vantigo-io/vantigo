-- +goose Up
-- A customer's default bill rate (customers bill-rate design D1): the
-- billing profile's eleventh column, and this module's first money column.
-- numeric(12,2) is the scale every rate in the chain already has — the
-- project default (00009) and time's bill and cost rates (00010) — so a rate
-- moves from one step of the chain to the next without rounding. It is quoted
-- in the profile's own currency column (00019): one currency per customer,
-- never a pair of its own the way a project carries one. NULL is "no rate
-- decided here", and the chain falls through to the person. No CHECK, the
-- 00019 shape: greater than zero and two decimals are validateDefaultBillRate's
-- (billing_values.go), and unlike a group's default payment term (00027) no
-- other row inherits this value, so a floor under the handler buys nothing.
ALTER TABLE customers.customers
    ADD COLUMN default_bill_rate numeric(12,2);

-- +goose Down
ALTER TABLE customers.customers
    DROP COLUMN default_bill_rate;
