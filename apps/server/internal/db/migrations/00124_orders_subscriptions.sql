-- +goose Up
-- Orders' subscriptions (orders design O42–O44, delivery S): a subscription
-- (abonnement) is its own aggregate, services only in this phase, billed per
-- period through the same allocations an order's lines are (O36, O43).
--
-- A subscription has the header of a quote and an order (O13) without a
-- delivery date, a plan — the interval, billing in advance or in arrears, the
-- start, an optional end, the notice and the invoice day — and a number from
-- the subscription series. Its lines are orders.lines with owner_kind
-- subscription, dated: valid_from and valid_to (both inclusive, NULL open)
-- say which periods a line bills, judged on the period's first day, so a
-- change takes effect from a period start and never rewrites a billed one
-- (O44). Its periods are computed, never stored; what is stored is each
-- period's allocation (the truth, O43) and the periods a pause skipped.
--
-- final_period says how the period an end falls in is billed when the end is
-- not its last day (owner, 2026-11-06): full, the whole period, or prorated,
-- by its days to the end over the full period's. A new subscription takes it
-- from the settings' subscription_final_period_default; End may choose again
-- and stores the choice.
--
-- billed_through is derived — the last day of the unbroken run, from the
-- start, of periods invoiced or skipped — and stored for lists, recomputed by
-- the invoices holder and on every release. paused_from is the first period
-- start a pause stops billing from.
CREATE TABLE orders.subscriptions (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number             bigint        NOT NULL,
    status             varchar(10)   NOT NULL DEFAULT 'draft',
    -- The plan (O42).
    billing_interval   varchar(12)   NOT NULL,
    billing            varchar(10)   NOT NULL,
    start_date         date          NOT NULL,
    end_date           date,
    notice_months      smallint      NOT NULL DEFAULT 0,
    invoice_day        smallint,
    final_period       varchar(10)   NOT NULL DEFAULT 'full',
    paused_from        date,
    billed_through     date,
    version            integer       NOT NULL DEFAULT 1,
    activated_at       timestamptz,
    activated_by_user_id uuid,
    activated_by_display varchar(200),
    ended_at           timestamptz,
    cancelled_at       timestamptz,
    -- The customer and its snapshot (O13).
    customer_id                  integer       NOT NULL,
    customer_number              bigint,
    customer_name                varchar(255)  NOT NULL DEFAULT '',
    customer_type                varchar(20),
    customer_organisation_number varchar(9),
    customer_foreign_id          varchar(60),
    customer_country             char(2),
    invoice_address_line1        varchar(255),
    invoice_address_line2        varchar(255),
    invoice_postal_code          varchar(20),
    invoice_city                 varchar(100),
    invoice_country              char(2),
    delivery_address_id          integer,
    delivery_name                varchar(255),
    delivery_address_line1       varchar(255),
    delivery_address_line2       varchar(255),
    delivery_postal_code         varchar(20),
    delivery_city                varchar(100),
    delivery_country             char(2),
    contact_id                   integer,
    contact_name                 varchar(255),
    contact_email                varchar(254),
    contact_phone                varchar(50),
    customer_invoice_email       varchar(254),
    your_reference               varchar(100)  NOT NULL DEFAULT '',
    -- Our side (O13).
    salesperson_id      uuid,
    salesperson_display varchar(200),
    project_id          integer,
    -- Money and text (O13, O19, O22).
    currency            char(3)       NOT NULL,
    prices_include_vat  boolean       NOT NULL DEFAULT false,
    payment_terms_days  integer       NOT NULL,
    discount_visible    boolean       NOT NULL DEFAULT true,
    language            char(2)       NOT NULL DEFAULT 'nb',
    intro_text          varchar(4000) NOT NULL DEFAULT '',
    closing_text        varchar(4000) NOT NULL DEFAULT '',
    internal_note       varchar(4000) NOT NULL DEFAULT '',
    -- A full period's totals of the lines in force from the latest change on
    -- (valid_to NULL), kept for lists.
    net_total           numeric(14,2) NOT NULL DEFAULT 0,
    vat_total           numeric(14,2) NOT NULL DEFAULT 0,
    gross_total         numeric(14,2) NOT NULL DEFAULT 0,
    -- The origin (O27).
    channel             varchar(20)   NOT NULL DEFAULT 'manual',
    source_module       varchar(30),
    source_kind         varchar(30),
    source_id           varchar(100),
    external_id         varchar(100),
    external_number     varchar(50),
    client_request_id   uuid,
    payment_reference   varchar(100),
    prepaid_amount      numeric(14,2),
    created_by_user_id  uuid,
    created_by_display  varchar(200),
    created_at          timestamptz   NOT NULL,
    updated_at          timestamptz   NOT NULL,
    anonymised_at       timestamptz,
    CONSTRAINT ck_subscriptions_status CHECK (status IN ('draft', 'active', 'paused', 'ended', 'cancelled')),
    CONSTRAINT ck_subscriptions_interval CHECK (billing_interval IN ('monthly', 'quarterly', 'half_yearly', 'yearly')),
    CONSTRAINT ck_subscriptions_billing CHECK (billing IN ('in_advance', 'in_arrears')),
    CONSTRAINT ck_subscriptions_end CHECK (end_date IS NULL OR end_date >= start_date),
    CONSTRAINT ck_subscriptions_notice CHECK (notice_months BETWEEN 0 AND 12),
    CONSTRAINT ck_subscriptions_invoice_day CHECK (invoice_day IS NULL OR invoice_day BETWEEN 1 AND 28),
    CONSTRAINT ck_subscriptions_final_period CHECK (final_period IN ('full', 'prorated')),
    CONSTRAINT ck_subscriptions_paused CHECK ((status = 'paused') = (paused_from IS NOT NULL)),
    CONSTRAINT ck_subscriptions_version CHECK (version >= 1),
    CONSTRAINT ck_subscriptions_customer_type CHECK (customer_type IS NULL OR customer_type IN ('business', 'person')),
    CONSTRAINT ck_subscriptions_language CHECK (language IN ('nb', 'en')),
    CONSTRAINT ck_subscriptions_payment_terms CHECK (payment_terms_days BETWEEN 0 AND 365)
);
CREATE UNIQUE INDEX ux_subscriptions_number ON orders.subscriptions (number);
CREATE INDEX ix_subscriptions_status ON orders.subscriptions (status);
CREATE INDEX ix_subscriptions_customer ON orders.subscriptions (customer_id);
CREATE UNIQUE INDEX ux_subscriptions_external ON orders.subscriptions (channel, external_id) WHERE external_id IS NOT NULL;
CREATE UNIQUE INDEX ux_subscriptions_client_request ON orders.subscriptions (client_request_id);

-- The final period a new subscription takes (owner, 2026-11-06).
ALTER TABLE orders.settings
    ADD COLUMN subscription_final_period_default varchar(10) NOT NULL DEFAULT 'full',
    ADD CONSTRAINT ck_settings_subscription_final_period CHECK (subscription_final_period_default IN ('full', 'prorated'));

-- The periods a pause skipped (O44): written by resume for every period start
-- from the pause to the period it resumes from, never billed, and counted as
-- covered when billed_through is derived.
CREATE TABLE orders.subscription_skips (
    subscription_id bigint NOT NULL REFERENCES orders.subscriptions (id) ON DELETE CASCADE,
    period_start    date   NOT NULL,
    PRIMARY KEY (subscription_id, period_start)
);

-- A subscription owns lines, dated (O44). Its lines share positions across
-- their dated versions — a changed line keeps its place — so the owner's
-- unique position holds for the other owners only.
ALTER TABLE orders.lines DROP CONSTRAINT ck_lines_owner_kind;
ALTER TABLE orders.lines ADD CONSTRAINT ck_lines_owner_kind CHECK (owner_kind IN ('quote', 'order', 'template', 'subscription'));
-- chain_id links a subscription line's dated versions: NULL on the first,
-- the first's id on every later one, so a change finds every version of the
-- line it changes — a scheduled one too — and at any date exactly one
-- version of a line is in force.
ALTER TABLE orders.lines
    ADD COLUMN valid_from date,
    ADD COLUMN valid_to   date,
    ADD COLUMN chain_id   bigint;
-- valid_to one day before valid_from is a line that never took effect: a
-- change made again from the same period start, whose line an invoice once
-- stamped and so cannot be deleted.
ALTER TABLE orders.lines ADD CONSTRAINT ck_lines_validity CHECK (
    (valid_from IS NULL AND valid_to IS NULL)
    OR (owner_kind = 'subscription' AND (valid_from IS NULL OR valid_to IS NULL OR valid_to >= valid_from - 1)));
DROP INDEX orders.ux_lines_owner_position;
CREATE UNIQUE INDEX ux_lines_owner_position ON orders.lines (owner_kind, owner_id, position) WHERE owner_kind <> 'subscription';
CREATE INDEX ix_lines_subscription ON orders.lines (owner_id, position) WHERE owner_kind = 'subscription';
ALTER TABLE orders.lines ADD CONSTRAINT ck_lines_chain CHECK (chain_id IS NULL OR owner_kind = 'subscription');

-- A subscription's change log shares the orders' (O44).
ALTER TABLE orders.order_changes DROP CONSTRAINT ck_order_changes_owner_kind;
ALTER TABLE orders.order_changes ADD CONSTRAINT ck_order_changes_owner_kind CHECK (owner_kind IN ('order', 'subscription'));

-- A subscription period's allocation (O43): the subscription's line in
-- line_id — a subscription's lines are orders.lines, so the line's reference
-- holds — and the subscription in subscription_id instead of an order. The
-- unique live index makes billing one line's period twice impossible: a
-- period is due only while none of its lines has a draft or an invoiced
-- allocation, and a full credit, which releases it, makes it due again.
ALTER TABLE orders.invoice_allocations
    ALTER COLUMN order_id DROP NOT NULL,
    ADD COLUMN subscription_id bigint REFERENCES orders.subscriptions (id) ON DELETE RESTRICT;
ALTER TABLE orders.invoice_allocations ADD CONSTRAINT ck_invoice_allocations_owner CHECK (
    (kind = 'order_line' AND order_id IS NOT NULL AND subscription_id IS NULL)
    OR (kind = 'subscription_period' AND order_id IS NULL AND subscription_id IS NOT NULL));
CREATE UNIQUE INDEX ux_invoice_allocations_live_period ON orders.invoice_allocations (line_id, period_start)
    WHERE kind = 'subscription_period' AND state IN ('draft', 'invoiced');
CREATE INDEX ix_invoice_allocations_subscription ON orders.invoice_allocations (subscription_id, period_start)
    WHERE subscription_id IS NOT NULL;

-- The documents and the mailings may later belong to a subscription (its
-- printed agreement); nothing writes one in this phase.

-- +goose Down
DROP INDEX orders.ix_invoice_allocations_subscription;
DROP INDEX orders.ux_invoice_allocations_live_period;
ALTER TABLE orders.invoice_allocations DROP CONSTRAINT ck_invoice_allocations_owner;
DELETE FROM orders.invoice_allocations WHERE kind = 'subscription_period';
ALTER TABLE orders.invoice_allocations DROP COLUMN subscription_id;
ALTER TABLE orders.invoice_allocations ALTER COLUMN order_id SET NOT NULL;
DELETE FROM orders.order_changes WHERE owner_kind = 'subscription';
ALTER TABLE orders.order_changes DROP CONSTRAINT ck_order_changes_owner_kind;
ALTER TABLE orders.order_changes ADD CONSTRAINT ck_order_changes_owner_kind CHECK (owner_kind IN ('order'));
DELETE FROM orders.lines WHERE owner_kind = 'subscription';
DROP INDEX orders.ix_lines_subscription;
DROP INDEX orders.ux_lines_owner_position;
CREATE UNIQUE INDEX ux_lines_owner_position ON orders.lines (owner_kind, owner_id, position);
ALTER TABLE orders.lines DROP CONSTRAINT ck_lines_chain;
ALTER TABLE orders.lines DROP CONSTRAINT ck_lines_validity;
ALTER TABLE orders.lines DROP COLUMN chain_id, DROP COLUMN valid_to, DROP COLUMN valid_from;
ALTER TABLE orders.lines DROP CONSTRAINT ck_lines_owner_kind;
ALTER TABLE orders.lines ADD CONSTRAINT ck_lines_owner_kind CHECK (owner_kind IN ('quote', 'order', 'template'));
DROP TABLE orders.subscription_skips;
ALTER TABLE orders.settings DROP CONSTRAINT ck_settings_subscription_final_period;
ALTER TABLE orders.settings DROP COLUMN subscription_final_period_default;
DROP TABLE orders.subscriptions;
