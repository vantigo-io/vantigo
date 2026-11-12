-- +goose Up
-- Lot capture in Manufacturing (manufacturing phase 2 M80–M82).
--
-- A BoM names the pattern of the lot its productions make (M80): {MO} by
-- default, so every production of one order adds to one lot; the order keeps
-- the pattern its BoM had when it was confirmed. The {N} token counts per
-- rendered prefix in manufacturing.counters ('lot:<prefix>'), whose name grows
-- to hold it.
ALTER TABLE manufacturing.boms ADD COLUMN lot_pattern varchar(40) NOT NULL DEFAULT '{MO}';
ALTER TABLE manufacturing.orders ADD COLUMN lot_pattern varchar(40) NOT NULL DEFAULT '{MO}';
ALTER TABLE manufacturing.counters ALTER COLUMN counter_name TYPE varchar(60);

-- The lots a production consumed, per component line (M82): the split of a
-- tracked component's production line over the lots and locations it took
-- from. production_lines stays the per-component total; the rows of one
-- component sum to its line's consumed and scrapped. The lot and the location
-- are Inventory's, named by id; the lot's number and expiry are kept as they
-- were, for the order's Lots tab.
CREATE TABLE manufacturing.production_line_lots (
    production_id bigint         NOT NULL,
    component_id  bigint         NOT NULL,
    lot_id        bigint         NOT NULL,
    location_id   bigint         NOT NULL,
    lot_number    varchar(20)    NOT NULL,
    expiry        date,
    consumed      numeric(14, 3) NOT NULL DEFAULT 0,
    scrapped      numeric(14, 3) NOT NULL DEFAULT 0,
    allow_expired boolean        NOT NULL DEFAULT false,
    PRIMARY KEY (production_id, component_id, lot_id, location_id),
    FOREIGN KEY (production_id, component_id) REFERENCES manufacturing.production_lines (production_id, component_id) ON DELETE CASCADE,
    CONSTRAINT ck_production_line_lots_consumed CHECK (consumed >= 0),
    CONSTRAINT ck_production_line_lots_scrapped CHECK (scrapped >= 0),
    CONSTRAINT ck_production_line_lots_some CHECK (consumed + scrapped > 0)
);
CREATE INDEX ix_production_line_lots_lot ON manufacturing.production_line_lots (lot_id);

-- The lots a production made (M80, M81): one row per lot its good output went
-- into — the order's lot, or one serial per unit — with its number as made.
CREATE TABLE manufacturing.production_outputs (
    production_id bigint         NOT NULL REFERENCES manufacturing.productions (id) ON DELETE CASCADE,
    lot_id        bigint         NOT NULL,
    lot_number    varchar(20)    NOT NULL,
    serial        boolean        NOT NULL DEFAULT false,
    quantity      numeric(14, 3) NOT NULL,
    PRIMARY KEY (production_id, lot_id),
    CONSTRAINT ck_production_outputs_quantity CHECK (quantity > 0)
);
CREATE INDEX ix_production_outputs_lot ON manufacturing.production_outputs (lot_id);

-- +goose Down
DROP TABLE manufacturing.production_outputs;
DROP TABLE manufacturing.production_line_lots;
ALTER TABLE manufacturing.counters ALTER COLUMN counter_name TYPE varchar(20);
ALTER TABLE manufacturing.orders DROP COLUMN lot_pattern;
ALTER TABLE manufacturing.boms DROP COLUMN lot_pattern;
