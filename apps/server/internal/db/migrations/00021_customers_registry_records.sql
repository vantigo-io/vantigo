-- +goose Up
-- The registry's own view of a Norwegian business customer (Brreg-in-full
-- design D1): a fact about the world with a timestamp, kept beside the
-- customer and never edited by hand. Its own table so a fetch never touches
-- the customer row or its revision.
CREATE TABLE customers.customer_registry_records (
    customer_id                  integer      PRIMARY KEY REFERENCES customers.customers (id) ON DELETE CASCADE,
    organisation_number          varchar(9)   NOT NULL,
    name                         varchar(255) NOT NULL,
    organisation_form_code       varchar(10),
    organisation_form            varchar(100),
    industry_code                varchar(10),
    industry                     varchar(255),
    employees                    integer,
    vat_registered               boolean      NOT NULL,
    bankrupt                     boolean      NOT NULL,
    under_liquidation            boolean      NOT NULL,
    under_forced_liquidation     boolean      NOT NULL,
    deleted_on                   date,
    founded_on                   date,
    website                      varchar(2048),
    email                        varchar(255),
    phone                        varchar(30),
    mobile                       varchar(30),
    parent_organisation_number   varchar(9),
    business_address             jsonb,
    postal_address               jsonb,
    fetched_at                   timestamptz  NOT NULL,
    registry_updated_hint        timestamptz
);

-- +goose Down
DROP TABLE customers.customer_registry_records;
