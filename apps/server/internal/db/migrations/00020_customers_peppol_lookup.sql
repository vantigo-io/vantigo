-- +goose Up
-- The last answer the Peppol network gave about a customer (peppol lookup
-- design D3). Its own table, not columns on customers.customers: recording an
-- answer must never bump the row's revision and conflict somebody's open form.
CREATE TABLE customers.customer_peppol_lookups (
    customer_id             integer      PRIMARY KEY REFERENCES customers.customers (id) ON DELETE CASCADE,
    participant_id          varchar(60)  NOT NULL,
    status                  varchar(20)  NOT NULL,
    can_receive_invoice     boolean      NOT NULL,
    can_receive_credit_note boolean      NOT NULL,
    smp_host                varchar(255),
    checked_at              timestamptz  NOT NULL
);

-- +goose Down
DROP TABLE customers.customer_peppol_lookups;
