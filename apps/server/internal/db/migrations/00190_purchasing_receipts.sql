-- +goose Up
-- Purchasing's receipts (purchasing design §4, delivery B): goods received
-- against a sent order (P24–P29). One receipt is one Inventory movement of
-- kind receipt, posted through contracts.StockLedger.Receive on the receipt's
-- own transaction, with the receipt as the movement's document; an undo is
-- one movement of kind receipt_reversal through ReverseReceipt. The receipt's
-- number is a series of its own in purchasing.counters ('receipt'), which
-- needs no row here: the first receipt makes it.
--
-- A site, a location, a variant, a lot and a movement are Inventory's and
-- Products' ids, opaque here (module-boundaries rule 4).

-- A receipt (P28): its number, the order, the day, who, the supplier's
-- delivery note and a note, the movement it posted, and the idempotency key
-- and hash a replay is judged by (A11). An undo keeps the receipt, marked
-- with when, who, its note and the reversal's movement (P29).
CREATE TABLE purchasing.receipts (
    id                   bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number               bigint        NOT NULL,
    order_id             bigint        NOT NULL REFERENCES purchasing.orders (id) ON DELETE RESTRICT,
    site_id              bigint        NOT NULL,
    received_on          date          NOT NULL,
    delivery_note        varchar(100)  NOT NULL DEFAULT '',
    note                 varchar(1000) NOT NULL DEFAULT '',
    movement_id          bigint,
    received_by_user_id  uuid,
    received_by_display  varchar(200),
    client_request_id    uuid          NOT NULL,
    request_hash         char(64)      NOT NULL,
    created_at           timestamptz   NOT NULL,
    undone_at            timestamptz,
    undone_by_user_id    uuid,
    undone_by_display    varchar(200),
    undo_note            varchar(1000),
    reversal_movement_id bigint,
    CONSTRAINT ck_receipts_number CHECK (number >= 1),
    CONSTRAINT ck_receipts_undone CHECK ((undone_at IS NULL) = (reversal_movement_id IS NULL))
);
CREATE UNIQUE INDEX ux_receipts_number ON purchasing.receipts (number);
CREATE UNIQUE INDEX ux_receipts_client_request ON purchasing.receipts (client_request_id);
CREATE INDEX ix_receipts_order ON purchasing.receipts (order_id, id);

-- A receipt's line: the order line, the variant, where it was put, the
-- quantity in the variant's unit and as entered, the lot or serials it was
-- received as, and the order line's price then (P28) — the figure a later
-- valuation starts from, read by nothing in this phase and answered by no
-- endpoint (A21).
CREATE TABLE purchasing.receipt_lines (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    receipt_id       bigint        NOT NULL REFERENCES purchasing.receipts (id) ON DELETE CASCADE,
    position         integer       NOT NULL,
    order_line_id    bigint        NOT NULL REFERENCES purchasing.order_lines (id) ON DELETE RESTRICT,
    variant_id       integer       NOT NULL,
    location_id      bigint        NOT NULL,
    quantity         numeric(12,3) NOT NULL,
    entered_quantity numeric(12,3) NOT NULL,
    in_packaging     boolean       NOT NULL DEFAULT false,
    packaging_name   varchar(100),
    lot_id           bigint,
    lot_number       varchar(20),
    supplier_lot     varchar(40),
    production_date  date,
    best_before      date,
    expiry           date,
    serials          text[]        NOT NULL DEFAULT '{}',
    unit_price       numeric(18,4) NOT NULL,
    currency         char(3)       NOT NULL,
    CONSTRAINT ck_receipt_lines_position CHECK (position >= 1),
    CONSTRAINT ck_receipt_lines_quantity CHECK (quantity > 0 AND entered_quantity > 0),
    CONSTRAINT ck_receipt_lines_unit_price CHECK (unit_price >= 0)
);
CREATE UNIQUE INDEX ux_receipt_lines_position ON purchasing.receipt_lines (receipt_id, position);
CREATE INDEX ix_receipt_lines_order_line ON purchasing.receipt_lines (order_line_id);

-- +goose Down
DROP TABLE purchasing.receipt_lines;
DROP TABLE purchasing.receipts;
