-- +goose Up
-- The customer row's optimistic-concurrency token (customers foundation design
-- D5): every write adds one, and an update that names the revision it read is
-- refused when the row has moved on.
ALTER TABLE customers.customers ADD COLUMN revision integer NOT NULL DEFAULT 1;

-- The index behind the duplicate-legal-identity check (customers foundation
-- design D6): CustomersByLegalIdentity reads (legal_country, legal_id) on every
-- write that sets an identity — POST /customers, PUT /customers/{id} and
-- PUT /customers/{id}/legal-identity — and had nothing but a sequential scan to
-- do it with.
--
-- Deliberately NOT unique. D6's whole shape is that the conflict is the
-- caller's to overrule with allowDuplicateIdentity: true (two departments of
-- one company kept as separate customers is legitimate), so the same pair
-- legitimately sits on more than one row and a unique index would refuse the
-- very writes the design allows. The uniqueness this module wants is a default
-- with an escape hatch, which is an application rule, not a constraint.
CREATE INDEX ix_customers_legal_identity ON customers.customers (legal_country, legal_id);

-- +goose Down
DROP INDEX customers.ix_customers_legal_identity;
ALTER TABLE customers.customers DROP COLUMN revision;
