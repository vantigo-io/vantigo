-- +goose Up
-- Orders' deliveries and stock (orders design O45–O48, delivery C).
--
-- An order names the site it is fulfilled from (O46): site_id is Inventory's
-- site, opaque here as every foreign id is (module-boundaries rule 4), and a
-- new order takes it from the settings' default_site_id. Whether an order line
-- holds stock is not stored here: Inventory's reservations are the truth
-- (contracts.StockLedger), read on the order's transaction.
ALTER TABLE orders.orders ADD COLUMN site_id bigint;
ALTER TABLE orders.settings ADD COLUMN default_site_id bigint;

-- A delivery (leveranse, O47): a number from the delivery series, the order
-- and the site it ships from, and its lines. A draft is edited, picked and
-- posted or cancelled; a posted delivery is immutable. Posting issues the
-- stock through contracts.StockLedger, whose movement id is kept, and stores
-- the packing slip's PDF (orders.documents, owner kind delivery) after the
-- commit. version is the draft's optimistic-concurrency token.
CREATE TABLE orders.deliveries (
    id                       bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number                   bigint        NOT NULL,
    order_id                 bigint        NOT NULL REFERENCES orders.orders (id) ON DELETE RESTRICT,
    site_id                  bigint        NOT NULL,
    status                   varchar(10)   NOT NULL DEFAULT 'draft',
    movement_id              bigint,
    posted_at                timestamptz,
    posted_by_user_id        uuid,
    posted_by_display        varchar(200),
    cancelled_at             timestamptz,
    packing_slip_document_id bigint        REFERENCES orders.documents (id) ON DELETE SET NULL,
    version                  integer       NOT NULL DEFAULT 1,
    created_by_user_id       uuid,
    created_by_display       varchar(200),
    created_at               timestamptz   NOT NULL,
    updated_at               timestamptz   NOT NULL,
    CONSTRAINT ck_deliveries_status CHECK (status IN ('draft', 'posted', 'cancelled')),
    CONSTRAINT ck_deliveries_posted CHECK ((status = 'posted') = (posted_at IS NOT NULL AND movement_id IS NOT NULL)),
    CONSTRAINT ck_deliveries_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX ux_deliveries_number ON orders.deliveries (number);
CREATE INDEX ix_deliveries_order ON orders.deliveries (order_id);
CREATE INDEX ix_deliveries_status ON orders.deliveries (status, id);

-- A delivery's line: one order line, the variant, the location it is picked
-- from (null until picked; its full code kept for the pick list and the
-- packing slip) and the quantity. The order line's reference is deferred, as
-- an allocation's is: a save writes an order's lines again under their ids.
CREATE TABLE orders.delivery_lines (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    delivery_id        bigint        NOT NULL REFERENCES orders.deliveries (id) ON DELETE CASCADE,
    position           integer       NOT NULL,
    order_line_id      bigint        NOT NULL REFERENCES orders.lines (id) DEFERRABLE INITIALLY DEFERRED,
    variant_id         integer       NOT NULL,
    from_location_id   bigint,
    from_location_code varchar(100),
    quantity           numeric(12,3) NOT NULL,
    CONSTRAINT ck_delivery_lines_quantity CHECK (quantity > 0),
    CONSTRAINT ck_delivery_lines_position CHECK (position >= 1)
);
CREATE UNIQUE INDEX ux_delivery_lines_order_line ON orders.delivery_lines (delivery_id, order_line_id);
CREATE INDEX ix_delivery_lines_order_line ON orders.delivery_lines (order_line_id);
CREATE INDEX ix_delivery_lines_variant ON orders.delivery_lines (variant_id);

-- A delivery's packing slip is a stored PDF of its own (O47).
ALTER TABLE orders.documents DROP CONSTRAINT ck_documents_owner_kind;
ALTER TABLE orders.documents ADD CONSTRAINT ck_documents_owner_kind CHECK (owner_kind IN ('quote', 'order', 'delivery'));
ALTER TABLE orders.documents DROP CONSTRAINT ck_documents_kind;
ALTER TABLE orders.documents ADD CONSTRAINT ck_documents_kind CHECK (kind IN ('quote', 'confirmation', 'packing_slip'));

-- +goose Down
DELETE FROM orders.documents WHERE owner_kind = 'delivery';
ALTER TABLE orders.documents DROP CONSTRAINT ck_documents_kind;
ALTER TABLE orders.documents ADD CONSTRAINT ck_documents_kind CHECK (kind IN ('quote', 'confirmation'));
ALTER TABLE orders.documents DROP CONSTRAINT ck_documents_owner_kind;
ALTER TABLE orders.documents ADD CONSTRAINT ck_documents_owner_kind CHECK (owner_kind IN ('quote', 'order'));
DROP TABLE orders.delivery_lines;
DROP TABLE orders.deliveries;
ALTER TABLE orders.settings DROP COLUMN default_site_id;
ALTER TABLE orders.orders DROP COLUMN site_id;
