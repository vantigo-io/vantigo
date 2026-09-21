-- +goose Up
CREATE TABLE customers.customer_addresses (
    id          integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id integer      NOT NULL REFERENCES customers.customers (id) ON DELETE CASCADE,
    type        varchar(20)  NOT NULL,
    label       varchar(100),
    line1       varchar(255) NOT NULL,
    line2       varchar(255),
    postal_code varchar(20),
    city        varchar(100),
    region      varchar(100),
    country     varchar(2)   NOT NULL,
    is_primary  boolean      NOT NULL,
    created_at  timestamptz  NOT NULL,
    updated_at  timestamptz  NOT NULL
);
CREATE INDEX ix_customer_addresses_customer ON customers.customer_addresses (customer_id, type);
-- One primary per customer and type (invoice-ready design D3): the invariant
-- the handlers keep under the customer row's lock, and the database's own
-- last word should they ever not.
CREATE UNIQUE INDEX ux_customer_addresses_primary
    ON customers.customer_addresses (customer_id, type) WHERE is_primary;

-- +goose Down
DROP TABLE customers.customer_addresses;
