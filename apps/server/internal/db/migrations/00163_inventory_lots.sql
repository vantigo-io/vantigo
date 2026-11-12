-- +goose Up
-- Lots (manufacturing phase 2 M52): a numbered quantity of one variant, its
-- number unique per variant ignoring case. serial is history: the variant's
-- tracking was serial when the lot was made. status is derived from the lot's
-- active holds (M64) and rewritten under the lot's row lock with every hold
-- change; no write sets it directly. A lot is never deleted.
CREATE TABLE inventory.lots (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    variant_id         integer       NOT NULL,
    number             varchar(20)   NOT NULL CHECK (number ~ '^[A-Za-z0-9._/-]{1,20}$'),
    serial             boolean       NOT NULL,
    supplier_lot       varchar(40),
    production_date    date,
    best_before        date,
    expiry             date,
    received_at        timestamptz   NOT NULL,
    origin_module      text,
    origin_kind        text,
    origin_id          text,
    status             text          NOT NULL DEFAULT 'available' CHECK (status IN ('available', 'quarantine', 'blocked')),
    note               varchar(2000),
    sku                text          NOT NULL,
    product_name       text          NOT NULL,
    revision           integer       NOT NULL DEFAULT 1,
    created_by_user_id uuid,
    created_by_display varchar(200),
    created_at         timestamptz   NOT NULL,
    updated_at         timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_lots_variant_number ON inventory.lots (variant_id, lower(number));
CREATE INDEX ix_lots_fefo ON inventory.lots (variant_id, expiry NULLS LAST, received_at, id);
CREATE INDEX ix_lots_status ON inventory.lots (status) WHERE status <> 'available';
CREATE INDEX ix_lots_expiry ON inventory.lots (expiry) WHERE expiry IS NOT NULL;
CREATE INDEX ix_lots_number ON inventory.lots (lower(number));

-- A hold on a lot (M64), owned by the document that put it: a lot's status is
-- blocked while any active hold is a block, quarantine while any is a
-- quarantine, else available. A release ends only its own hold.
CREATE TABLE inventory.lot_holds (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    lot_id              bigint        NOT NULL REFERENCES inventory.lots (id),
    kind                text          NOT NULL CHECK (kind IN ('quarantine', 'block')),
    cause               text          NOT NULL CHECK (cause IN ('inspection', 'deviation', 'complaint', 'recall', 'other')),
    note                varchar(1000),
    source_module       text          NOT NULL,
    source_kind         text          NOT NULL,
    source_id           text          NOT NULL,
    created_by_user_id  uuid,
    created_by_display  varchar(200),
    created_at          timestamptz   NOT NULL,
    released_at         timestamptz,
    released_by_user_id uuid,
    released_by_display varchar(200),
    release_note        varchar(1000)
);
CREATE INDEX ix_lot_holds_active ON inventory.lot_holds (lot_id) WHERE released_at IS NULL;
CREATE INDEX ix_lot_holds_source ON inventory.lot_holds (source_module, source_kind, source_id) WHERE released_at IS NULL;

-- The balance key gains the lot (M55): (variant, location, lot), a missing lot
-- one key of its own (NULLS NOT DISTINCT), so two postings to a new
-- (variant, location, no lot) still serialise on one row. The table has no
-- primary key from here on: a primary key's columns are NOT NULL, and the lot
-- is NULL for untracked stock. The unique index is the key every writer uses;
-- nor can it be the replica identity, which also needs NOT NULL columns. Were
-- the table ever published for logical replication, it would need a surrogate
-- id as its primary key, or REPLICA IDENTITY FULL.
ALTER TABLE inventory.balances DROP CONSTRAINT balances_pkey;
ALTER TABLE inventory.balances ADD COLUMN lot_id bigint REFERENCES inventory.lots (id);
CREATE UNIQUE INDEX ux_balances_key ON inventory.balances (variant_id, location_id, lot_id) NULLS NOT DISTINCT;
CREATE INDEX ix_balances_lot ON inventory.balances (lot_id) WHERE lot_id IS NOT NULL;

-- The lot of a movement line, and whether an expired lot was taken on the
-- principal's override (M62).
ALTER TABLE inventory.movement_lines
    ADD COLUMN lot_id           bigint  REFERENCES inventory.lots (id),
    ADD COLUMN expired_override boolean NOT NULL DEFAULT false;
CREATE INDEX ix_movement_lines_lot ON inventory.movement_lines (lot_id, movement_id) WHERE lot_id IS NOT NULL;

-- The generated lot numbers' series (M57): L1001, L1002, …
INSERT INTO inventory.counters (counter_name, next_value) VALUES ('lot', 1001);

-- +goose Down
-- A down is for development: it fails while any balance or line has a lot.
DELETE FROM inventory.counters WHERE counter_name = 'lot';
ALTER TABLE inventory.movement_lines ADD CONSTRAINT ck_movement_lines_no_lot CHECK (lot_id IS NULL);
ALTER TABLE inventory.movement_lines DROP CONSTRAINT ck_movement_lines_no_lot;
DROP INDEX inventory.ix_movement_lines_lot;
ALTER TABLE inventory.movement_lines DROP COLUMN expired_override, DROP COLUMN lot_id;
ALTER TABLE inventory.balances ADD CONSTRAINT ck_balances_no_lot CHECK (lot_id IS NULL);
DROP INDEX inventory.ix_balances_lot;
DROP INDEX inventory.ux_balances_key;
ALTER TABLE inventory.balances DROP COLUMN lot_id;
ALTER TABLE inventory.balances ADD PRIMARY KEY (variant_id, location_id);
DROP TABLE inventory.lot_holds;
DROP TABLE inventory.lots;
