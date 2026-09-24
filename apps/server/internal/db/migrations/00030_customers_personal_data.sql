-- +goose Up
-- A private person's anonymisation (customers GDPR design D4): the day it is
-- scheduled for, and the moment the worker ran it. Both NULL for every customer
-- nobody scheduled — which is nearly all of them, and every business.
--
-- anonymise_on is a calendar day, not an instant: a person chooses a date, and
-- the worker takes every customer whose day has come in UTC, the day every
-- other date-only field of this module is counted in. It has no default, and no
-- column says why a date was chosen: Norwegian bookkeeping rules keep accounting
-- material for years after the fiscal year, this installation invoices nothing
-- yet, and the person scheduling is the one who knows what was invoiced — so the
-- schema encodes no retention period at all (docs/customers.md, Personal data
-- and anonymisation). anonymise_on stays set once the customer is anonymised,
-- the record of what was asked for.
--
-- anonymised_at set is what makes the customer read-only (the lock-time check
-- that answers customer_anonymised) and what the worker skips. The partial
-- index is the worker's one lookup — the scheduled and not yet anonymised —
-- and holds only them.
ALTER TABLE customers.customers
    ADD COLUMN anonymise_on date,
    ADD COLUMN anonymised_at timestamptz;
CREATE INDEX ix_customers_anonymise_on ON customers.customers (anonymise_on)
    WHERE anonymise_on IS NOT NULL AND anonymised_at IS NULL;

-- +goose Down
DROP INDEX customers.ix_customers_anonymise_on;
ALTER TABLE customers.customers DROP COLUMN anonymised_at, DROP COLUMN anonymise_on;
