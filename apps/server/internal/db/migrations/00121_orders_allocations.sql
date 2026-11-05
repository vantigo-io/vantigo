-- +goose Up
-- Orders' invoice allocations (orders design O36, delivery B): what part of an
-- order line an invoice bills. An invoice line holds one live source per
-- source, and an order line may be invoiced in several parts, so the source an
-- invoice line names is an allocation (contracts.WorkSourceOrderAllocation),
-- never the line itself — Energy's settlement pattern.
--
-- Each Allocate writes new draft rows under the lines' lock; it never rewrites
-- one. A draft is stamped invoiced by the invoice's issue (the holder, O38),
-- with the invoice's id, number and date, or released: by the invoicing
-- module after its draft is deleted or the line removed, by the daily sweep
-- after 30 days untouched, by the order page's Release draft allocations, or
-- by the credit note that returns its invoice line in full. A line's to
-- invoice is ordered − cancelled − invoiced − draft-allocated (on order), or
-- delivered − invoiced − draft-allocated (on delivery, C).
--
-- kind is order_line in B; subscription_period (S) carries its period, and S
-- adds the unique live period index. amount is docmath's line net for
-- quantity, unit_price and discount_percent (O37), so the invoice line built
-- from the same three numbers always agrees with it.
CREATE TABLE orders.invoice_allocations (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    -- Deferred: a save replaces an order's lines whole, writing each line
    -- again under its id (InsertLineKeepingID), so the reference is judged
    -- at commit, when the line is back.
    line_id             bigint        NOT NULL REFERENCES orders.lines (id) DEFERRABLE INITIALLY DEFERRED,
    order_id            bigint        NOT NULL REFERENCES orders.orders (id) ON DELETE RESTRICT,
    kind                varchar(20)   NOT NULL DEFAULT 'order_line',
    period_start        date,
    period_end          date,
    quantity            numeric(12,3) NOT NULL,
    unit_price          numeric(18,8) NOT NULL,
    discount_percent    numeric(5,2)  NOT NULL DEFAULT 0,
    amount              numeric(14,2) NOT NULL,
    vat_code_id         integer       NOT NULL,
    state               varchar(10)   NOT NULL DEFAULT 'draft',
    revision            integer       NOT NULL DEFAULT 1,
    -- The invoice's stamp (O38), set by the issue's holder call.
    invoice_id          bigint,
    invoice_number      bigint,
    issue_date          date,
    invoiced_at         timestamptz,
    invoiced_by         uuid,
    invoiced_by_display varchar(200),
    released_at         timestamptz,
    created_at          timestamptz   NOT NULL,
    updated_at          timestamptz   NOT NULL,
    CONSTRAINT ck_invoice_allocations_kind CHECK (kind IN ('order_line', 'subscription_period')),
    CONSTRAINT ck_invoice_allocations_period CHECK (
        (kind = 'order_line' AND period_start IS NULL AND period_end IS NULL)
        OR (kind = 'subscription_period' AND period_start IS NOT NULL AND period_end IS NOT NULL
            AND period_end >= period_start)),
    CONSTRAINT ck_invoice_allocations_quantity CHECK (quantity > 0),
    CONSTRAINT ck_invoice_allocations_unit_price CHECK (unit_price >= 0),
    CONSTRAINT ck_invoice_allocations_discount CHECK (discount_percent BETWEEN 0 AND 100),
    CONSTRAINT ck_invoice_allocations_state CHECK (state IN ('draft', 'invoiced', 'released')),
    CONSTRAINT ck_invoice_allocations_revision CHECK (revision >= 1),
    CONSTRAINT ck_invoice_allocations_stamp CHECK (
        (state = 'draft' AND invoice_id IS NULL AND invoice_number IS NULL AND issue_date IS NULL
            AND invoiced_at IS NULL)
        OR (state = 'invoiced' AND invoice_id IS NOT NULL AND invoice_number IS NOT NULL
            AND issue_date IS NOT NULL AND invoiced_at IS NOT NULL)
        OR state = 'released'),
    CONSTRAINT ck_invoice_allocations_released CHECK ((state = 'released') = (released_at IS NOT NULL))
);
CREATE INDEX ix_invoice_allocations_line ON orders.invoice_allocations (line_id, state);
CREATE INDEX ix_invoice_allocations_order ON orders.invoice_allocations (order_id);
CREATE INDEX ix_invoice_allocations_sweep ON orders.invoice_allocations (state, created_at);

-- +goose Down
DROP TABLE orders.invoice_allocations;
