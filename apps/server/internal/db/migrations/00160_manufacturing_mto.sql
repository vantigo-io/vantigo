-- +goose Up
-- Make-to-order and sub-assemblies (manufacturing design §9, delivery E,
-- M43–M48, M44a, M44b): the requests the manufacturing.mto handler queues
-- for the manufacturing.mto-worker, the one live order per sales order line,
-- and the flag a cancelled sales order leaves on the orders made for it.
--
-- A sales order and its lines are Orders': opaque ids, never a key across a
-- schema (module-boundaries rule 4).

-- A make-to-order request (M44a): one per sales order, inserted by the
-- handler on orders.sales_order.confirmed, on the delivery's own
-- transaction, and nothing else. The worker reads the order's lines through
-- contracts.SalesOrderLines, creates and confirms the orders, and marks it
-- done — some line made — or skipped — none made —, with each line it left
-- out and why in reasons ([{lineId, code, detail}]). A failure counts an
-- attempt and waits until next_attempt_at; the eighth leaves it failed.
CREATE TABLE manufacturing.mto_requests (
    id              bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    sales_order_id  bigint      NOT NULL,
    event_id        bigint      NOT NULL,
    status          varchar(10) NOT NULL DEFAULT 'pending',
    reasons         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    attempts        integer     NOT NULL DEFAULT 0,
    last_error      text,
    next_attempt_at timestamptz NOT NULL,
    created_at      timestamptz NOT NULL,
    done_at         timestamptz,
    CONSTRAINT ck_mto_requests_status CHECK (status IN ('pending', 'done', 'skipped', 'failed')),
    CONSTRAINT ck_mto_requests_attempts CHECK (attempts >= 0)
);
CREATE UNIQUE INDEX ux_mto_requests_sales_order ON manufacturing.mto_requests (sales_order_id);
CREATE INDEX ix_mto_requests_pending ON manufacturing.mto_requests (next_attempt_at, id) WHERE status = 'pending';

-- One live order per sales order line (M44, M44b): a backorder carries its
-- original's origin and is left out, as is a cancelled order, so a line
-- cancelled out can be made again. The worker and the new-order form both
-- refuse a second one through it.
CREATE UNIQUE INDEX ux_orders_origin_line ON manufacturing.orders (origin_line_id)
    WHERE origin_kind = 'sales_order' AND backorder_of_id IS NULL AND status <> 'cancelled';
CREATE INDEX ix_orders_origin_order ON manufacturing.orders (origin_order_id) WHERE origin_kind = 'sales_order';

-- origin_cancelled is set when the sales order an order was made for is
-- cancelled (M44): the order is left as it is — the goods may be half made —
-- and flagged for a person to decide.
ALTER TABLE manufacturing.orders ADD COLUMN origin_cancelled boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE manufacturing.orders DROP COLUMN origin_cancelled;
DROP INDEX manufacturing.ix_orders_origin_order;
DROP INDEX manufacturing.ux_orders_origin_line;
DROP TABLE manufacturing.mto_requests;
