-- +goose Up
-- BoM depth (manufacturing phase 2 M120–M128): by-products a BoM makes beside
-- its product, with the share of the order's cost each carries; unbuilds that
-- take finished goods apart; and the split and merge of manufacturing orders.

-- A by-product of a BoM (M120): another goods variant its operations make, per
-- the BoM's output quantity, carrying cost_share percent of the order's cost.
-- At most 20 per BoM, never its own variant nor one of its components, the
-- shares summing to at most 100: the module judges each before a CHECK could
-- fail.
CREATE TABLE manufacturing.bom_outputs (
    id         bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    bom_id     bigint         NOT NULL REFERENCES manufacturing.boms (id) ON DELETE CASCADE,
    variant_id integer        NOT NULL,
    quantity   numeric(14, 3) NOT NULL,
    cost_share numeric(5, 2)  NOT NULL DEFAULT 0,
    sort_order integer        NOT NULL DEFAULT 0,
    CONSTRAINT ck_bom_outputs_quantity CHECK (quantity > 0),
    CONSTRAINT ck_bom_outputs_cost_share CHECK (cost_share >= 0 AND cost_share <= 100)
);
CREATE UNIQUE INDEX ux_bom_outputs_variant ON manufacturing.bom_outputs (bom_id, variant_id);
CREATE INDEX ix_bom_outputs_variant ON manufacturing.bom_outputs (variant_id);

-- An order's by-products (M121), snapshotted from its BoM at confirm: the
-- quantity expected for the order's quantity, half-up to three decimals, and
-- what its productions made. The ids are this table's own, so one may equal a
-- component line's or the order's: a production's produce lines are keyed by
-- (variant, line id).
CREATE TABLE manufacturing.order_outputs (
    id           bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    order_id     bigint         NOT NULL REFERENCES manufacturing.orders (id) ON DELETE CASCADE,
    variant_id   integer        NOT NULL,
    sku          text           NOT NULL,
    product_name text           NOT NULL,
    unit         text           NOT NULL,
    quantity     numeric(14, 3) NOT NULL,
    cost_share   numeric(5, 2)  NOT NULL DEFAULT 0,
    expected     numeric(14, 3) NOT NULL,
    produced     numeric(14, 3) NOT NULL DEFAULT 0,
    sort_order   integer        NOT NULL DEFAULT 0,
    CONSTRAINT ck_order_outputs_quantity CHECK (quantity > 0),
    CONSTRAINT ck_order_outputs_cost_share CHECK (cost_share >= 0 AND cost_share <= 100),
    CONSTRAINT ck_order_outputs_expected CHECK (expected >= 0),
    CONSTRAINT ck_order_outputs_produced CHECK (produced >= 0)
);
CREATE INDEX ix_order_outputs_order ON manufacturing.order_outputs (order_id, sort_order, id);
CREATE INDEX ix_order_outputs_variant ON manufacturing.order_outputs (variant_id);

-- What a production made of each by-product (M121): one row per lot it went
-- into, or one row without a lot for an untracked by-product.
CREATE TABLE manufacturing.production_byproducts (
    id            bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    production_id bigint         NOT NULL REFERENCES manufacturing.productions (id) ON DELETE CASCADE,
    output_id     bigint         NOT NULL REFERENCES manufacturing.order_outputs (id) ON DELETE RESTRICT,
    lot_id        bigint,
    lot_number    varchar(20),
    quantity      numeric(14, 3) NOT NULL,
    CONSTRAINT ck_production_byproducts_quantity CHECK (quantity > 0)
);
CREATE INDEX ix_production_byproducts_production ON manufacturing.production_byproducts (production_id);

-- An unbuild (M123), numbered UB-<n> from the counters row 'unbuild': finished
-- goods taken apart, from a done order's productions (order_id, and for a
-- tracked product the finished lot chosen) or free-standing by a BoM. It is
-- posted to Inventory as one movement of kind unbuild.
CREATE TABLE manufacturing.unbuilds (
    id                bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number            bigint         NOT NULL,
    order_id          bigint         REFERENCES manufacturing.orders (id) ON DELETE RESTRICT,
    bom_id            bigint         NOT NULL REFERENCES manufacturing.boms (id) ON DELETE RESTRICT,
    variant_id        integer        NOT NULL,
    quantity          numeric(14, 3) NOT NULL,
    lot_id            bigint,
    lot_number        varchar(20),
    site_id           bigint         NOT NULL,
    from_location_id  bigint         NOT NULL,
    to_location_id    bigint         NOT NULL,
    movement_id       bigint,
    note              text           NOT NULL DEFAULT '',
    posted_by_user_id uuid,
    posted_by_display text           NOT NULL DEFAULT '',
    posted_at         timestamptz    NOT NULL,
    client_request_id varchar(100)   NOT NULL,
    request_hash      varchar(64)    NOT NULL,
    CONSTRAINT ck_unbuilds_quantity CHECK (quantity > 0),
    CONSTRAINT ck_unbuilds_note CHECK (char_length(note) <= 2000)
);
CREATE UNIQUE INDEX ux_unbuilds_number ON manufacturing.unbuilds (number);
CREATE UNIQUE INDEX ux_unbuilds_client_request ON manufacturing.unbuilds (client_request_id);
CREATE INDEX ix_unbuilds_order ON manufacturing.unbuilds (order_id, lot_id) WHERE order_id IS NOT NULL;

-- What an unbuild restored (M123): each component back into a lot it was
-- consumed from, or into a lot named, or without a lot when untracked.
CREATE TABLE manufacturing.unbuild_lines (
    unbuild_id   bigint         NOT NULL REFERENCES manufacturing.unbuilds (id) ON DELETE CASCADE,
    line_no      integer        NOT NULL,
    variant_id   integer        NOT NULL,
    sku          text           NOT NULL,
    product_name text           NOT NULL,
    unit         text           NOT NULL,
    quantity     numeric(14, 3) NOT NULL,
    lot_id       bigint,
    lot_number   varchar(20),
    PRIMARY KEY (unbuild_id, line_no),
    CONSTRAINT ck_unbuild_lines_quantity CHECK (quantity > 0)
);

-- Split and merge (M124, M125): a part of a split names the order it was split
-- from; an order merged into another is cancelled naming the survivor.
ALTER TABLE manufacturing.orders
    ADD COLUMN split_from_id  bigint REFERENCES manufacturing.orders (id) ON DELETE RESTRICT,
    ADD COLUMN merged_into_id bigint REFERENCES manufacturing.orders (id) ON DELETE RESTRICT;
CREATE INDEX ix_orders_split_from ON manufacturing.orders (split_from_id) WHERE split_from_id IS NOT NULL;

-- A sales order line has one live order of its own (manufacturing design
-- M44b); its backorders and now the parts of a split are that order's chain,
-- not another (M124).
DROP INDEX manufacturing.ux_orders_origin_line;
CREATE UNIQUE INDEX ux_orders_origin_line ON manufacturing.orders (origin_line_id)
    WHERE origin_kind = 'sales_order' AND backorder_of_id IS NULL AND split_from_id IS NULL AND status <> 'cancelled';

-- +goose Down
DROP INDEX manufacturing.ux_orders_origin_line;
CREATE UNIQUE INDEX ux_orders_origin_line ON manufacturing.orders (origin_line_id)
    WHERE origin_kind = 'sales_order' AND backorder_of_id IS NULL AND status <> 'cancelled';
DROP INDEX manufacturing.ix_orders_split_from;
ALTER TABLE manufacturing.orders DROP COLUMN merged_into_id, DROP COLUMN split_from_id;
DROP TABLE manufacturing.unbuild_lines;
DROP TABLE manufacturing.unbuilds;
DROP TABLE manufacturing.production_byproducts;
DROP TABLE manufacturing.order_outputs;
DROP TABLE manufacturing.bom_outputs;
DELETE FROM manufacturing.counters WHERE counter_name = 'unbuild';
