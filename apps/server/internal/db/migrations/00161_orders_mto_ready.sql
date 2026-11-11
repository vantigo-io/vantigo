-- +goose Up
-- Produced, ready to reserve (manufacturing design M48, M48a): the
-- orders.mto-reserve handler marks a sales order line when a manufacturing
-- order made for it closes, and the order page then offers Reserve now on
-- it. The mark is a row of its own keyed by the line rather than a column of
-- orders.lines, because a save replaces a document's lines whole (O15) and
-- would drop it. manufacturing_order_id and site_id are Manufacturing's ids,
-- opaque here (module-boundaries rule 4): the order closed and the site it
-- made the goods at, which a reservation needs to be the order's own.
CREATE TABLE orders.line_production_ready (
    line_id                bigint         PRIMARY KEY,
    order_id               bigint         NOT NULL,
    manufacturing_order_id bigint         NOT NULL,
    site_id                bigint         NOT NULL,
    produced               numeric(14, 3) NOT NULL,
    marked_at              timestamptz    NOT NULL
);
CREATE INDEX ix_line_production_ready_order ON orders.line_production_ready (order_id);

-- +goose Down
DROP TABLE orders.line_production_ready;
