-- +goose Up
-- Inventory, the stock of the catalog's goods in warehouses and stores
-- (inventory design §3, delivery A): the sites, each the root of a tree of
-- locations; the ledger of posted movements and their lines; the balance per
-- variant and location the ledger keeps; and the record of every structural
-- write. Delivery B adds the counts and a line's entered packagings in a
-- migration of its own.
--
-- Every foreign identifier is opaque: a variant is Products', read and locked
-- through contracts.ProductCatalog, and a user is identity's, read through
-- contracts.UserDirectory (docs/src/content/docs/en/contributing/module-boundaries.md
-- rule 4), so no key crosses a schema. A movement line snapshots the
-- variant's SKU, product name and unit, so history reads after the variant is
-- gone. There are no CHECK constraints — the house style of 00042 and 00072 —
-- because the module's own validation owns the rules and reports them per
-- line; the unique indexes below state facts about the tables instead.
CREATE SCHEMA inventory;

-- A site (I1): a warehouse or a store, the root of its location tree. The
-- code is upper-case [A-Z0-9]{1,16}, unique in the deployment, and the first
-- segment of every full code below it.
CREATE TABLE inventory.sites (
    id            bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    code          varchar(16)  NOT NULL,
    name          varchar(200) NOT NULL,
    kind          varchar(16)  NOT NULL,
    address_line1 varchar(200),
    address_line2 varchar(200),
    postal_code   varchar(20),
    city          varchar(100),
    country       char(2),
    status        varchar(10)  NOT NULL,
    revision      integer      NOT NULL DEFAULT 1,
    created_at    timestamptz  NOT NULL,
    updated_at    timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_sites_code ON inventory.sites (code);

-- A location (I2–I4, I21): a node of its site's tree. The root has no parent
-- and its kind is site; one root per site is the partial unique index below,
-- with no key from the site to its root. ancestors holds the ids from the
-- root down to the parent, so X's subtree is id = X OR ancestors @> {X};
-- full_code joins the codes from the root down with '-' and is unique in the
-- deployment. Both are rewritten for a whole subtree in the transaction that
-- changes a code or a parent.
CREATE TABLE inventory.locations (
    id         bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    site_id    bigint       NOT NULL REFERENCES inventory.sites (id),
    parent_id  bigint       REFERENCES inventory.locations (id),
    kind       varchar(16)  NOT NULL,
    code       varchar(16)  NOT NULL,
    name       varchar(200),
    sort_order integer      NOT NULL DEFAULT 0,
    status     varchar(10)  NOT NULL,
    full_code  text         NOT NULL,
    ancestors  bigint[]     NOT NULL DEFAULT '{}',
    depth      smallint     NOT NULL,
    revision   integer      NOT NULL DEFAULT 1,
    created_at timestamptz  NOT NULL,
    updated_at timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_locations_root ON inventory.locations (site_id) WHERE parent_id IS NULL;
CREATE UNIQUE INDEX ux_locations_parent_code ON inventory.locations (parent_id, code) NULLS NOT DISTINCT;
CREATE UNIQUE INDEX ux_locations_full_code ON inventory.locations (full_code);
CREATE INDEX ix_locations_site ON inventory.locations (site_id);
CREATE INDEX ix_locations_ancestors ON inventory.locations USING gin (ancestors);

-- A movement (I8, I14): posted once and never edited or deleted. kind is an
-- open set (adjustment, move and receipt in A; count in B; issue, production
-- and transfer later — design §12), kept as text. client_request_id makes a
-- posting idempotent, and request_hash tells a replay from a reuse. The
-- source reference is null in this phase and indexed for the modules that
-- will set it. The poster is snapshotted: the movement is a record of the
-- ledger and keeps who posted it.
CREATE TABLE inventory.movements (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    client_request_id  uuid          NOT NULL,
    request_hash       bytea         NOT NULL,
    site_id            bigint        NOT NULL REFERENCES inventory.sites (id),
    kind               varchar(16)   NOT NULL,
    reason             varchar(20),
    note               varchar(1000),
    supplier_name      varchar(200),
    supplier_reference varchar(100),
    source_module      text,
    source_kind        text,
    source_id          text,
    source_line_id     text,
    posted_by_user_id  uuid,
    posted_by_display  varchar(200),
    posted_at          timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_movements_client_request ON inventory.movements (client_request_id);
CREATE INDEX ix_movements_site_posted ON inventory.movements (site_id, posted_at DESC, id DESC);
CREATE INDEX ix_movements_kind_posted ON inventory.movements (kind, posted_at DESC, id DESC);
CREATE INDEX ix_movements_source ON inventory.movements (source_module, source_kind, source_id) WHERE source_id IS NOT NULL;

-- A movement line (I9): a quantity of a variant, in the variant's own unit,
-- from a location, to one, or both. The SKU, the product name and the unit
-- are as they were at posting.
CREATE TABLE inventory.movement_lines (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    movement_id      bigint        NOT NULL REFERENCES inventory.movements (id),
    line_no          integer       NOT NULL,
    variant_id       integer       NOT NULL,
    sku              text          NOT NULL,
    product_name     text          NOT NULL,
    unit             text          NOT NULL,
    from_location_id bigint        REFERENCES inventory.locations (id),
    to_location_id   bigint        REFERENCES inventory.locations (id),
    quantity         numeric(14,3) NOT NULL
);
CREATE UNIQUE INDEX ux_movement_lines_movement_line ON inventory.movement_lines (movement_id, line_no);
CREATE INDEX ix_movement_lines_variant ON inventory.movement_lines (variant_id, movement_id);
CREATE INDEX ix_movement_lines_from ON inventory.movement_lines (from_location_id) WHERE from_location_id IS NOT NULL;
CREATE INDEX ix_movement_lines_to ON inventory.movement_lines (to_location_id) WHERE to_location_id IS NOT NULL;

-- The balance (I10): what lies on one location of one variant, kept by the
-- posting transaction. A row reaching zero is deleted, so the table lists
-- only where stock lies. The key gains a lot when Manufacturing comes (design
-- §12); nothing treats (variant, location) as the whole key.
CREATE TABLE inventory.balances (
    variant_id  integer       NOT NULL,
    location_id bigint        NOT NULL REFERENCES inventory.locations (id),
    on_hand     numeric(14,3) NOT NULL,
    updated_at  timestamptz   NOT NULL,
    PRIMARY KEY (variant_id, location_id)
);
CREATE INDEX ix_balances_location ON inventory.balances (location_id);

-- The record (I31): append-only, one row per structural write — a site or a
-- location created, changed, re-parented, archived, reactivated or deleted;
-- B's counts — with the actor snapshotted as planning.events keeps it.
-- Movements are their own record and are not repeated here. The row of a
-- deleted location stays: the record outlives what it records.
CREATE TABLE inventory.events (
    id            bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    entity_kind   varchar(12) NOT NULL,
    entity_id     bigint      NOT NULL,
    site_id       bigint,
    kind          varchar(32) NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    actor_kind    varchar(20) NOT NULL,
    actor_user_id uuid,
    actor_display varchar(200),
    occurred_at   timestamptz NOT NULL
);
CREATE INDEX ix_events_site_occurred ON inventory.events (site_id, occurred_at);
CREATE INDEX ix_events_entity ON inventory.events (entity_kind, entity_id);

-- +goose Down
DROP SCHEMA inventory CASCADE;
