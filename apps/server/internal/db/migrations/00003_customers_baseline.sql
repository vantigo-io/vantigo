-- +goose Up
-- Customers' schema, single-tenant (every tenant_id from the .NET EF
-- configuration is dropped, along with the composite keys/indexes it was
-- part of; what survives is the rest of each key): customers, their
-- contacts, the customer-contact association, the customer timeline (manual
-- notes and generated events, each edit preserved as a revision), and the
-- counters table that replaces .NET's shared tenant_counters. See
-- docs/superpowers/specs/2026-09-12-customers-inventory.md §3.
CREATE SCHEMA customers;

CREATE TABLE customers.customers (
    id              integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    customer_number bigint       NOT NULL,
    name            varchar(255) NOT NULL,
    status          varchar(20)  NOT NULL DEFAULT 'active',
    legal_country   varchar(2),
    legal_id        varchar(50),
    legal_name      varchar(255),
    legal_source    varchar(50),
    legal_type      varchar(50),
    created_at      timestamptz  NOT NULL,
    updated_at      timestamptz  NOT NULL
);
-- .NET's unique key was (tenant_id, customer_number); with tenant_id gone,
-- customer_number is unique on its own.
CREATE UNIQUE INDEX ux_customers_customer_number ON customers.customers (customer_number);

CREATE TABLE customers.contacts (
    id          integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    first_name  varchar(100) NOT NULL,
    last_name   varchar(100) NOT NULL,
    middle_name varchar(100),
    prefix      varchar(20),
    suffix      varchar(20),
    phone       varchar(30),
    email       varchar(255),
    created_at  timestamptz  NOT NULL
);

CREATE TABLE customers.customers_contacts (
    customer_id integer      NOT NULL REFERENCES customers.customers (id) ON DELETE CASCADE,
    contact_id  integer      NOT NULL REFERENCES customers.contacts (id) ON DELETE CASCADE,
    role        varchar(255) NOT NULL,
    phone       varchar(30),
    email       varchar(255),
    PRIMARY KEY (customer_id, contact_id)
);
-- .NET's index was (tenant_id, contact_id); with tenant_id gone, contact_id
-- alone still supports the "which customers is this contact attached to"
-- lookup the association endpoints need.
CREATE INDEX ix_customers_contacts_contact_id ON customers.customers_contacts (contact_id);

CREATE TABLE customers.customers_timeline_entries (
    id                integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    customer_id       integer      NOT NULL REFERENCES customers.customers (id) ON DELETE CASCADE,
    provenance        varchar(20)  NOT NULL,
    producer          varchar(100) NOT NULL,
    event_type        varchar(100) NOT NULL,
    occurred_on       date         NOT NULL,
    occurred_at       timestamptz,
    summary           varchar(500) NOT NULL,
    note              varchar(10000),
    source_url        varchar(2048),
    payload_json      jsonb,
    payload_version   integer      NOT NULL,
    -- current_revision is the optimistic-concurrency token (inventory §4):
    -- an update or delete carries the revision it read in its WHERE clause,
    -- and a mismatch means a concurrent writer got there first.
    current_revision  integer      NOT NULL,
    state             varchar(20)  NOT NULL,
    actor_kind        varchar(30)  NOT NULL,
    actor_display     varchar(255) NOT NULL,
    created_at        timestamptz  NOT NULL,
    updated_at        timestamptz  NOT NULL,
    deleted_at        timestamptz
);
-- .NET's index was (tenant_id, customer_id); with tenant_id gone, customer_id
-- alone still supports "this customer's timeline" listing.
CREATE INDEX ix_customers_timeline_entries_customer_id ON customers.customers_timeline_entries (customer_id);

-- Mirrors customers_timeline_entries column-for-column (a point-in-time copy
-- of the entry, taken on every edit) plus the two columns that identify which
-- entry and which revision it is.
CREATE TABLE customers.customers_timeline_entries_revisions (
    id                         integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    customer_timeline_entry_id integer      NOT NULL REFERENCES customers.customers_timeline_entries (id) ON DELETE CASCADE,
    revision_number            integer      NOT NULL,
    customer_id                integer      NOT NULL,
    provenance                 varchar(20)  NOT NULL,
    producer                   varchar(100) NOT NULL,
    event_type                 varchar(100) NOT NULL,
    occurred_on                date         NOT NULL,
    occurred_at                timestamptz,
    summary                    varchar(500) NOT NULL,
    note                       varchar(10000),
    source_url                 varchar(2048),
    payload_json               jsonb,
    payload_version            integer      NOT NULL,
    current_revision           integer      NOT NULL,
    state                      varchar(20)  NOT NULL,
    actor_kind                 varchar(30)  NOT NULL,
    actor_display              varchar(255) NOT NULL,
    created_at                 timestamptz  NOT NULL,
    updated_at                 timestamptz  NOT NULL,
    deleted_at                 timestamptz
);
-- .NET's unique key was (tenant_id, customer_timeline_entry_id,
-- revision_number); with tenant_id gone, the pair alone is the second,
-- belt-and-suspenders concurrency guard (inventory §4): a revision insert
-- racing another for the same next revision_number hits this constraint.
CREATE UNIQUE INDEX ux_customers_timeline_entries_revisions_entry_revision
    ON customers.customers_timeline_entries_revisions (customer_timeline_entry_id, revision_number);

-- Replaces .NET's shared tenant_counters (tenant_id, counter_name) PK: one
-- row per counter, e.g. "customer-number". Allocation is an upsert
-- (NextCounterValue), not a Postgres SEQUENCE, matching .NET's
-- gapless-under-rollback semantics (inventory §4).
CREATE TABLE customers.counters (
    counter_name text   PRIMARY KEY,
    next_value   bigint NOT NULL
);

-- +goose Down
DROP SCHEMA customers CASCADE;
