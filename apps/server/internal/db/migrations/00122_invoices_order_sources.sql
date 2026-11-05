-- +goose Up
-- Orders delivery B, Invoices' lane (orders design O36–O40): an order line's
-- invoice allocation as a line's source, and the billing run's orders
-- source. This migration names no other schema: a source's kind is opaque
-- here, held to a shape, and orders.allocation is named only as the kind
-- whose project is optional and whose snapshot is its period.

-- The line sources (O38). An allocation (kind orders.allocation) carries its
-- order's project when the order has one, and none otherwise; its subkind is
-- order_line or subscription_period; it keeps no metering point, and
-- period_from is a subscription period's first day (source_date its last),
-- NULL for an order line's part.
ALTER TABLE invoices.line_sources
    DROP CONSTRAINT ck_line_sources_project,
    ADD CONSTRAINT ck_line_sources_project CHECK (
        source_kind = 'orders.allocation'
        OR (project_id IS NOT NULL) = (source_kind IN ('time.entry', 'expenses.entry', 'projects.milestone'))),
    DROP CONSTRAINT ck_line_sources_subkind,
    ADD CONSTRAINT ck_line_sources_subkind CHECK (
        CASE WHEN source_kind = 'orders.allocation'
            THEN source_subkind IS NULL OR source_subkind IN ('order_line', 'subscription_period')
            ELSE (source_kind = 'expenses.entry' OR project_id IS NULL) = (source_subkind IS NOT NULL)
        END),
    DROP CONSTRAINT ck_line_sources_point,
    ADD CONSTRAINT ck_line_sources_point CHECK (
        (source_kind = 'orders.allocation' AND point_gsrn IS NULL AND point_address IS NULL AND price_area IS NULL
            AND contract_name IS NULL AND estimated IS NULL)
        OR (source_kind <> 'orders.allocation' AND project_id IS NULL AND point_gsrn IS NOT NULL AND point_address IS NOT NULL
            AND price_area IS NOT NULL AND contract_name IS NOT NULL AND period_from IS NOT NULL AND estimated IS NOT NULL)
        OR (source_kind <> 'orders.allocation' AND project_id IS NOT NULL AND point_gsrn IS NULL AND point_address IS NULL
            AND price_area IS NULL AND contract_name IS NULL AND period_from IS NULL AND estimated IS NULL));

-- The billing run's source (O40): consumption, as before, or orders — every
-- customer with order lines due through the run's date. combine is the
-- orders source's grouping: one invoice per order (false) or the orders of a
-- customer combined where they agree (samlefaktura). Invoices cannot read
-- Orders' settings, so the run carries it.
ALTER TABLE invoices.billing_runs
    ADD COLUMN source  varchar(12) NOT NULL DEFAULT 'consumption',
    ADD COLUMN combine boolean     NOT NULL DEFAULT false,
    ADD CONSTRAINT ck_billing_runs_source CHECK (source IN ('consumption', 'orders')),
    ADD CONSTRAINT ck_billing_runs_combine CHECK (source = 'orders' OR NOT combine);

-- +goose Down
-- Restoring 00078's CHECKs fails while an allocation is a source: a Down
-- never deletes bookkeeping. A run of the orders source is refused too.
-- +goose StatementBegin
DO $down$
BEGIN
    IF EXISTS (SELECT 1 FROM invoices.line_sources WHERE source_kind = 'orders.allocation') THEN
        RAISE EXCEPTION 'invoices: an order allocation is a line''s source' USING ERRCODE = 'P0001';
    END IF;
    IF EXISTS (SELECT 1 FROM invoices.billing_runs WHERE source <> 'consumption') THEN
        RAISE EXCEPTION 'invoices: a billing run invoices orders' USING ERRCODE = 'P0001';
    END IF;
END;
$down$;
-- +goose StatementEnd

ALTER TABLE invoices.billing_runs
    DROP CONSTRAINT ck_billing_runs_combine,
    DROP CONSTRAINT ck_billing_runs_source,
    DROP COLUMN combine,
    DROP COLUMN source;

ALTER TABLE invoices.line_sources
    DROP CONSTRAINT ck_line_sources_point,
    ADD CONSTRAINT ck_line_sources_point CHECK (
        (project_id IS NULL AND point_gsrn IS NOT NULL AND point_address IS NOT NULL AND price_area IS NOT NULL
            AND contract_name IS NOT NULL AND period_from IS NOT NULL AND estimated IS NOT NULL)
        OR (project_id IS NOT NULL AND point_gsrn IS NULL AND point_address IS NULL AND price_area IS NULL
            AND contract_name IS NULL AND period_from IS NULL AND estimated IS NULL)),
    DROP CONSTRAINT ck_line_sources_subkind,
    ADD CONSTRAINT ck_line_sources_subkind
        CHECK ((source_kind = 'expenses.entry' OR project_id IS NULL) = (source_subkind IS NOT NULL)),
    DROP CONSTRAINT ck_line_sources_project,
    ADD CONSTRAINT ck_line_sources_project
        CHECK ((project_id IS NOT NULL) = (source_kind IN ('time.entry', 'expenses.entry', 'projects.milestone')));
