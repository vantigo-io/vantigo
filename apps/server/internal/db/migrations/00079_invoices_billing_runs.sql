-- +goose Up
-- Invoices phase 5, delivery E (energy consumption billing design D10; plan
-- docs/superpowers/plans/2026-10-27-invoices-billing-runs.md, reading 1): a
-- billing run settles, drafts and — when it issues — issues one invoice per
-- customer with consumption due, carried item by item by the
-- invoices-billing worker under a row lease. This migration names no other
-- schema: a customer is an opaque id here, as everywhere in this module.

-- A run. customers is the number of items it was made with; the counts by
-- outcome are read from the items, never stored, so they cannot drift. At
-- most one run is queued or running at a time (ux_billing_runs_active): two
-- creates racing end in one, the other refused.
CREATE TABLE invoices.billing_runs (
    id                 bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    through            date        NOT NULL,
    issue              boolean     NOT NULL,
    status             varchar(10) NOT NULL DEFAULT 'queued',
    customers          integer     NOT NULL,
    created_by_user_id uuid        NOT NULL,
    created_at         timestamptz NOT NULL,
    started_at         timestamptz,
    finished_at        timestamptz,
    CONSTRAINT ck_billing_runs_status CHECK (status IN ('queued', 'running', 'done', 'cancelled')),
    CONSTRAINT ck_billing_runs_customers CHECK (customers >= 0),
    CONSTRAINT ck_billing_runs_finished CHECK ((finished_at IS NOT NULL) = (status IN ('done', 'cancelled')))
);

CREATE UNIQUE INDEX ux_billing_runs_active ON invoices.billing_runs ((true))
    WHERE status IN ('queued', 'running');

-- An item: one customer of a run. invoice_ids are the documents it made, in
-- order — more than one when a customer's settlements did not fit on one
-- document and were split by metering point. They carry no foreign key: a
-- draft the run made may be deleted afterwards, and the reads answer only the
-- documents that still exist. code and detail say why an item was skipped or
-- failed. attempts, next_attempt_at, lease_id and lease_until are the
-- worker's: a claim leases a queued item due by next_attempt_at, and an error
-- that is not a refusal puts it back, due later.
CREATE TABLE invoices.billing_run_items (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id          bigint      NOT NULL REFERENCES invoices.billing_runs (id) ON DELETE CASCADE,
    customer_id     integer     NOT NULL,
    status          varchar(10) NOT NULL DEFAULT 'queued',
    invoice_ids     bigint[]    NOT NULL DEFAULT '{}',
    code            varchar(60),
    detail          text,
    attempts        integer     NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    lease_id        varchar(100),
    lease_until     timestamptz,
    finished_at     timestamptz,
    CONSTRAINT ux_billing_run_items_customer UNIQUE (run_id, customer_id),
    CONSTRAINT ck_billing_run_items_status CHECK (status IN ('queued', 'drafted', 'issued', 'skipped', 'failed', 'cancelled')),
    CONSTRAINT ck_billing_run_items_code CHECK ((code IS NOT NULL) = (status IN ('skipped', 'failed'))),
    CONSTRAINT ck_billing_run_items_finished CHECK ((finished_at IS NOT NULL) = (status <> 'queued')),
    CONSTRAINT ck_billing_run_items_attempts CHECK (attempts >= 0)
);

CREATE INDEX ix_billing_run_items_due ON invoices.billing_run_items (next_attempt_at, id)
    WHERE status = 'queued';
CREATE INDEX ix_billing_run_items_run ON invoices.billing_run_items (run_id, id);

-- +goose Down
DROP TABLE invoices.billing_run_items;
DROP TABLE invoices.billing_runs;
