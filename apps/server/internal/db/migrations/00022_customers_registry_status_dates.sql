-- +goose Up
-- When a company went bankrupt or into liquidation, not when we noticed
-- (Brreg in full design D4, fix round 2 I3): the registry sends konkursdato
-- and underAvviklingDato beside the flags, and the dashboard's attention item
-- is sorted and printed by the day the thing happened — a bankruptcy from
-- 2019 must not re-float to the top of the list on every refresh. Both are
-- nullable: the flags are the load-bearing facts and the registry does not
-- always carry a date for either, in which case the item falls back to
-- fetched_at.
--
-- Stored and overwritten like every other column of the record, but never
-- diffed: the flag beside each date is what a refresh reports, and reporting
-- "bankruptOn changed" in the same breath as "bankrupt changed" would say the
-- same thing twice.
ALTER TABLE customers.customer_registry_records
    ADD COLUMN bankrupt_on    date,
    ADD COLUMN liquidation_on date;

-- +goose Down
ALTER TABLE customers.customer_registry_records
    DROP COLUMN bankrupt_on,
    DROP COLUMN liquidation_on;
