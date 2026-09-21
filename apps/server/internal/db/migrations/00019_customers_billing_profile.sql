-- +goose Up
-- A customer's billing profile (invoice-ready design D1, D4): payment terms,
-- currency, document language, delivery methods and the identifiers used to
-- send it invoices. All nullable — NULL means "not decided here, whoever
-- invoices uses its own default" — never part of SafeCustomerResponse
-- (GetCustomer/ListCustomers never select these columns; only the dedicated
-- GET/PUT .../billing-profile sub-resource does).
ALTER TABLE customers.customers
    ADD COLUMN invoice_email       varchar(255),
    ADD COLUMN reminder_email      varchar(255),
    ADD COLUMN payment_terms_days  integer,
    ADD COLUMN currency            varchar(3),
    ADD COLUMN language            varchar(2),
    ADD COLUMN invoice_delivery    varchar(20),
    ADD COLUMN reminder_delivery   varchar(20),
    ADD COLUMN peppol_id           varchar(60),
    ADD COLUMN gln                 varchar(13),
    ADD COLUMN buyer_reference     varchar(100);

-- +goose Down
ALTER TABLE customers.customers
    DROP COLUMN buyer_reference,
    DROP COLUMN gln,
    DROP COLUMN peppol_id,
    DROP COLUMN reminder_delivery,
    DROP COLUMN invoice_delivery,
    DROP COLUMN language,
    DROP COLUMN currency,
    DROP COLUMN payment_terms_days,
    DROP COLUMN reminder_email,
    DROP COLUMN invoice_email;
