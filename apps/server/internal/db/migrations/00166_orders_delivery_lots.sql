-- +goose Up
-- A delivery's picks per lot (manufacturing phase 2 M75): a tracked
-- variant's delivery line holds its quantity in these rows, one per lot and
-- location, and its own from_location_id stays null; an untracked line has
-- none. The lot number, expiry and location code are snapshots for the pick
-- list and the packing slip. chosen is set once a person edits the line's
-- picks: a line whose picks are all proposed may be proposed again at the
-- post (M65a). The lot and the location are Inventory's, named by id only.
CREATE TABLE orders.delivery_line_lots (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    delivery_line_id bigint        NOT NULL REFERENCES orders.delivery_lines (id) ON DELETE CASCADE,
    lot_id           bigint        NOT NULL,
    lot_number       varchar(20)   NOT NULL,
    expiry           date,
    location_id      bigint        NOT NULL,
    location_code    varchar(100)  NOT NULL,
    quantity         numeric(12,3) NOT NULL,
    allow_expired    boolean       NOT NULL DEFAULT false,
    chosen           boolean       NOT NULL DEFAULT false,
    CONSTRAINT ck_delivery_line_lots_quantity CHECK (quantity > 0)
);
CREATE UNIQUE INDEX ux_delivery_line_lots_key ON orders.delivery_line_lots (delivery_line_id, lot_id, location_id);
CREATE INDEX ix_delivery_line_lots_lot ON orders.delivery_line_lots (lot_id);

-- +goose Down
DROP TABLE orders.delivery_line_lots;
