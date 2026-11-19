-- +goose Up
-- The 2028 buyer rule and credit limits (invoices phase 6 design I37, I38;
-- plan docs/superpowers/plans/2026-11-18-invoices-phase6-e-plan.md, delivery
-- E-2, step E10).
--
-- business_register_registered is Enhetsregisteret's
-- registrertIForetaksregisteret. It is NULL until the record's next refresh:
-- there is no backfill, and NULL means "not read yet", never "no".
ALTER TABLE customers.customer_registry_records
    ADD COLUMN business_register_registered boolean;

-- bookkeeping_obligated_override is a person's decision about whether the
-- customer is bookkeeping-obligated. NULL means "as the registry says"; true
-- and false overrule it, with a reason. credit_limit is in NOK. All three are
-- business facts: anonymisation keeps the override and the limit and blanks
-- only the reason, which is free text.
ALTER TABLE customers.customers
    ADD COLUMN bookkeeping_obligated_override        boolean,
    ADD COLUMN bookkeeping_obligated_override_reason varchar(500) NOT NULL DEFAULT '',
    ADD COLUMN credit_limit                          numeric(14,2),
    ADD CONSTRAINT ck_customers_credit_limit CHECK (credit_limit IS NULL OR credit_limit >= 0),
    ADD CONSTRAINT ck_customers_bookkeeping_override_reason CHECK (
        bookkeeping_obligated_override IS NOT NULL OR bookkeeping_obligated_override_reason = '');

-- +goose Down
ALTER TABLE customers.customers
    DROP CONSTRAINT ck_customers_bookkeeping_override_reason,
    DROP CONSTRAINT ck_customers_credit_limit,
    DROP COLUMN credit_limit,
    DROP COLUMN bookkeeping_obligated_override_reason,
    DROP COLUMN bookkeeping_obligated_override;

ALTER TABLE customers.customer_registry_records
    DROP COLUMN business_register_registered;
