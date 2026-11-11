-- +goose Up
-- Manufacturing orders (manufacturing design §9, delivery B): the order with
-- its number and suffix (M17, M17b), its component lines — the BoM's lines
-- snapshotted at confirm, with Manufacturing's copy of what each holds
-- reserved and the standard cost at confirm (M18, M20a, M23a) — the
-- productions posted against it with their lines (M22, M22a), and its
-- append-only timeline (M26). Work orders and time logs (C), the schedule's
-- slots (D) and the make-to-order index (E) add their own.
--
-- A variant is Products', a site and a location Inventory's, a user
-- identity's: opaque ids, never a key across a schema (module-boundaries
-- rule 4). Quantities are numeric(14,3), as the BoM's are.

-- A manufacturing order (M17). number is the root's MO-<n>, from the
-- counters row 'orders'; suffix is 0 for a root and 2, 3, … for the
-- backorders of its chain, the next suffix of the root (M17b), so (number,
-- suffix) is unique. produced counts good output only; output_scrap the
-- finished units discarded (M22a). origin_* carry where the order came from
-- (E fills sales_order and parent). due_date is the day the goods must be
-- ready; planned_start and planned_finish are set by hand or by planning (D).
CREATE TABLE manufacturing.orders (
    id                    bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number                bigint         NOT NULL,
    suffix                integer        NOT NULL DEFAULT 0,
    variant_id            integer        NOT NULL,
    bom_id                bigint         NOT NULL REFERENCES manufacturing.boms (id) ON DELETE RESTRICT,
    quantity              numeric(14, 3) NOT NULL,
    produced              numeric(14, 3) NOT NULL DEFAULT 0,
    output_scrap          numeric(14, 3) NOT NULL DEFAULT 0,
    site_id               bigint         NOT NULL,
    component_location_id bigint,
    output_location_id    bigint         NOT NULL,
    due_date              date,
    planned_start         timestamptz,
    planned_finish        timestamptz,
    priority              varchar(10)    NOT NULL DEFAULT 'normal',
    responsible_user_id   uuid,
    note                  text           NOT NULL DEFAULT '',
    origin_kind           varchar(12)    NOT NULL DEFAULT 'manual',
    origin_order_id       bigint,
    origin_line_id        bigint,
    origin_reference      varchar(64),
    parent_order_id       bigint         REFERENCES manufacturing.orders (id) ON DELETE RESTRICT,
    backorder_of_id       bigint         REFERENCES manufacturing.orders (id) ON DELETE RESTRICT,
    status                varchar(12)    NOT NULL DEFAULT 'draft',
    confirmed_at          timestamptz,
    closed_at             timestamptz,
    revision              integer        NOT NULL DEFAULT 1,
    created_at            timestamptz    NOT NULL,
    updated_at            timestamptz    NOT NULL,
    CONSTRAINT ck_orders_number CHECK (number >= 1),
    CONSTRAINT ck_orders_suffix CHECK (suffix = 0 OR suffix >= 2),
    CONSTRAINT ck_orders_quantity CHECK (quantity > 0),
    CONSTRAINT ck_orders_produced CHECK (produced >= 0),
    CONSTRAINT ck_orders_output_scrap CHECK (output_scrap >= 0),
    CONSTRAINT ck_orders_priority CHECK (priority IN ('normal', 'urgent')),
    CONSTRAINT ck_orders_note CHECK (char_length(note) <= 2000),
    CONSTRAINT ck_orders_origin_kind CHECK (origin_kind IN ('manual', 'sales_order', 'parent')),
    CONSTRAINT ck_orders_status CHECK (status IN ('draft', 'confirmed', 'in_progress', 'done', 'cancelled')),
    CONSTRAINT ck_orders_planned CHECK (planned_finish IS NULL OR planned_start IS NULL OR planned_finish >= planned_start)
);
CREATE UNIQUE INDEX ux_orders_number ON manufacturing.orders (number, suffix);
CREATE INDEX ix_orders_status_due ON manufacturing.orders (status, due_date);
CREATE INDEX ix_orders_variant ON manufacturing.orders (variant_id);
CREATE INDEX ix_orders_bom ON manufacturing.orders (bom_id);
CREATE INDEX ix_orders_backorder_of ON manufacturing.orders (backorder_of_id) WHERE backorder_of_id IS NOT NULL;
CREATE INDEX ix_orders_parent ON manufacturing.orders (parent_order_id) WHERE parent_order_id IS NOT NULL;

-- A component line of an order (M18): a snapshot of a BoM line at confirm,
-- or a line added later. required is rounded up to three decimals; consumed
-- and scrapped are the productions' running totals; reserved is
-- Manufacturing's copy of what the line holds reserved in Inventory, updated
-- under the order's lock whenever it reserves, consumes or releases (M20a).
-- standard_cost is the catalog's at confirm, for delivery D's cost (M23a).
CREATE TABLE manufacturing.order_components (
    id            bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    order_id      bigint         NOT NULL REFERENCES manufacturing.orders (id) ON DELETE CASCADE,
    variant_id    integer        NOT NULL,
    sku           text           NOT NULL,
    product_name  text           NOT NULL,
    unit          text           NOT NULL,
    required      numeric(14, 3) NOT NULL,
    reserved      numeric(14, 3) NOT NULL DEFAULT 0,
    consumed      numeric(14, 3) NOT NULL DEFAULT 0,
    scrapped      numeric(14, 3) NOT NULL DEFAULT 0,
    operation_id  bigint,
    standard_cost numeric(12, 2),
    sort_order    integer        NOT NULL DEFAULT 0,
    added         boolean        NOT NULL DEFAULT false,
    note          text           NOT NULL DEFAULT '',
    CONSTRAINT ck_order_components_required CHECK (required >= 0),
    CONSTRAINT ck_order_components_reserved CHECK (reserved >= 0),
    CONSTRAINT ck_order_components_consumed CHECK (consumed >= 0),
    CONSTRAINT ck_order_components_scrapped CHECK (scrapped >= 0),
    CONSTRAINT ck_order_components_note CHECK (char_length(note) <= 2000)
);
CREATE INDEX ix_order_components_order ON manufacturing.order_components (order_id, sort_order, id);
CREATE INDEX ix_order_components_variant ON manufacturing.order_components (variant_id);

-- A production (M22): output registered against an order, posted to
-- Inventory as one production movement (movement_id; null when nothing went
-- into or out of stock). client_request_id makes a post idempotent; the
-- request_hash tells a replay from a reuse with another body (M22a).
CREATE TABLE manufacturing.productions (
    id                bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    order_id          bigint         NOT NULL REFERENCES manufacturing.orders (id) ON DELETE RESTRICT,
    client_request_id varchar(100)   NOT NULL,
    request_hash      varchar(64)    NOT NULL,
    quantity          numeric(14, 3) NOT NULL,
    output_scrap      numeric(14, 3) NOT NULL DEFAULT 0,
    to_location_id    bigint         NOT NULL,
    movement_id       bigint,
    posted_by_user_id uuid,
    posted_by_display text           NOT NULL DEFAULT '',
    posted_at         timestamptz    NOT NULL,
    CONSTRAINT ck_productions_quantity CHECK (quantity >= 0),
    CONSTRAINT ck_productions_output_scrap CHECK (output_scrap >= 0)
);
CREATE UNIQUE INDEX ux_productions_client_request ON manufacturing.productions (client_request_id);
CREATE INDEX ix_productions_order ON manufacturing.productions (order_id, id);

-- A production's line: what it consumed and scrapped of one component line,
-- and the location the request named, if any (M22a).
CREATE TABLE manufacturing.production_lines (
    production_id    bigint         NOT NULL REFERENCES manufacturing.productions (id) ON DELETE CASCADE,
    component_id     bigint         NOT NULL REFERENCES manufacturing.order_components (id) ON DELETE RESTRICT,
    consumed         numeric(14, 3) NOT NULL DEFAULT 0,
    scrapped         numeric(14, 3) NOT NULL DEFAULT 0,
    from_location_id bigint,
    PRIMARY KEY (production_id, component_id),
    CONSTRAINT ck_production_lines_consumed CHECK (consumed >= 0),
    CONSTRAINT ck_production_lines_scrapped CHECK (scrapped >= 0)
);

-- An order's timeline (M26): append-only, who and when, with the event's
-- figures in payload. actor_display is the poster's name at the time, kept
-- with the id as Inventory's ledger keeps its poster (erasure, rule 19).
CREATE TABLE manufacturing.order_events (
    id            bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    order_id      bigint      NOT NULL REFERENCES manufacturing.orders (id) ON DELETE CASCADE,
    kind          varchar(30) NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}',
    actor_user_id uuid,
    actor_display text        NOT NULL DEFAULT '',
    at            timestamptz NOT NULL
);
CREATE INDEX ix_order_events_order ON manufacturing.order_events (order_id, id);

-- +goose Down
DROP TABLE manufacturing.order_events;
DROP TABLE manufacturing.production_lines;
DROP TABLE manufacturing.productions;
DROP TABLE manufacturing.order_components;
DROP TABLE manufacturing.orders;
