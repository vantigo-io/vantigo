-- +goose Up
-- Manufacturing, making the catalog's goods from other goods (manufacturing
-- design §9, delivery A): the number counters; the work centers where an
-- operation is done, with their weekly pattern and their closures (M8, M9);
-- and the bills of materials with their operations and lines (M10–M13).
-- Manufacturing orders and their productions (B), work orders and time logs
-- (C) and the schedule's slots (D) add their tables in migrations of their
-- own.
--
-- Every foreign identifier is opaque: a variant is Products', read and locked
-- through contracts.ProductCatalog, and a site is Inventory's, read through
-- contracts.Stock (docs/src/content/docs/en/contributing/module-boundaries.md
-- rule 4), so no key crosses a schema. The enumerations are CHECKed; the
-- module's own validation reports every rule per field and per line before a
-- CHECK could fail, so a CHECK is the last word, never the first.
CREATE SCHEMA manufacturing;

-- The display-number series, one row per series: manufacturing orders (B)
-- first. A number is allocated by upsert under the row's lock; next_value is
-- the next number the series hands out.
CREATE TABLE manufacturing.counters (
    counter_name varchar(20) PRIMARY KEY,
    next_value   bigint      NOT NULL
);

-- A work center (M8): a machine, a cell or a team at one Inventory site. The
-- code is upper-case [A-Z0-9]{1,16}, unique in the deployment. pattern is the
-- weekly working pattern (M9): per weekday at most two HH:MM intervals on a
-- 15-minute grid, judged by the module. The cost rates are NOK per hour, both
-- optional. working_state moves only through block and unblock (M30), never
-- through an edit.
CREATE TABLE manufacturing.work_centers (
    id                    bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    code                  varchar(16)    NOT NULL,
    name                  varchar(200)   NOT NULL,
    site_id               bigint         NOT NULL,
    capacity              integer        NOT NULL DEFAULT 1,
    time_efficiency       integer        NOT NULL DEFAULT 100,
    setup_minutes         numeric(10, 2) NOT NULL DEFAULT 0,
    cleanup_minutes       numeric(10, 2) NOT NULL DEFAULT 0,
    machine_cost_per_hour numeric(12, 2),
    labour_cost_per_hour  numeric(12, 2),
    pattern               jsonb          NOT NULL,
    working_state         varchar(10)    NOT NULL DEFAULT 'normal',
    status                varchar(10)    NOT NULL DEFAULT 'active',
    revision              integer        NOT NULL DEFAULT 1,
    created_at            timestamptz    NOT NULL,
    updated_at            timestamptz    NOT NULL,
    CONSTRAINT ck_work_centers_code CHECK (code ~ '^[A-Z0-9]{1,16}$'),
    CONSTRAINT ck_work_centers_capacity CHECK (capacity >= 1),
    CONSTRAINT ck_work_centers_time_efficiency CHECK (time_efficiency BETWEEN 1 AND 500),
    CONSTRAINT ck_work_centers_setup CHECK (setup_minutes >= 0),
    CONSTRAINT ck_work_centers_cleanup CHECK (cleanup_minutes >= 0),
    CONSTRAINT ck_work_centers_machine_cost CHECK (machine_cost_per_hour >= 0),
    CONSTRAINT ck_work_centers_labour_cost CHECK (labour_cost_per_hour >= 0),
    CONSTRAINT ck_work_centers_working_state CHECK (working_state IN ('normal', 'blocked')),
    CONSTRAINT ck_work_centers_status CHECK (status IN ('active', 'archived'))
);
CREATE UNIQUE INDEX ux_work_centers_code ON manufacturing.work_centers (code);
CREATE INDEX ix_work_centers_site ON manufacturing.work_centers (site_id);

-- A closure (M9): a range of days a work center is closed, such as an
-- industrial holiday or a breakdown, both days included. At most 100 per work
-- center, which the module counts.
CREATE TABLE manufacturing.work_center_closures (
    id             bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    work_center_id bigint      NOT NULL REFERENCES manufacturing.work_centers (id) ON DELETE CASCADE,
    from_date      date        NOT NULL,
    to_date        date        NOT NULL,
    note           text        NOT NULL DEFAULT '',
    CONSTRAINT ck_work_center_closures_range CHECK (to_date >= from_date)
);
CREATE INDEX ix_work_center_closures_work_center ON manufacturing.work_center_closures (work_center_id, from_date);

-- A bill of materials (M10): how to make output_quantity of one goods
-- variant. code is optional and unique when given. Exactly one active BoM
-- per variant is the default — the partial unique index below, which a race
-- on the default trips and the module answers as revision_conflict (M14a).
CREATE TABLE manufacturing.boms (
    id              bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    variant_id      integer        NOT NULL,
    code            varchar(32),
    output_quantity numeric(14, 3) NOT NULL,
    is_default      boolean        NOT NULL DEFAULT false,
    make_to_order   boolean        NOT NULL DEFAULT false,
    lead_time_days  integer        NOT NULL DEFAULT 0,
    note            text           NOT NULL DEFAULT '',
    status          varchar(10)    NOT NULL DEFAULT 'active',
    revision        integer        NOT NULL DEFAULT 1,
    created_at      timestamptz    NOT NULL,
    updated_at      timestamptz    NOT NULL,
    CONSTRAINT ck_boms_code CHECK (code IS NULL OR char_length(code) BETWEEN 1 AND 32),
    CONSTRAINT ck_boms_output_quantity CHECK (output_quantity > 0),
    CONSTRAINT ck_boms_lead_time CHECK (lead_time_days >= 0),
    CONSTRAINT ck_boms_note CHECK (char_length(note) <= 2000),
    CONSTRAINT ck_boms_status CHECK (status IN ('active', 'archived'))
);
CREATE UNIQUE INDEX ux_boms_code ON manufacturing.boms (code) WHERE code IS NOT NULL;
CREATE UNIQUE INDEX ux_boms_default ON manufacturing.boms (variant_id) WHERE is_default AND status = 'active';
CREATE INDEX ix_boms_variant ON manufacturing.boms (variant_id);

-- An operation of a BoM (M12): run in sequence order at an active work
-- center. setup_minutes null means the work center's setup applies. A work
-- center an operation names is never deleted (work_center_in_use): archived
-- instead.
CREATE TABLE manufacturing.bom_operations (
    id                bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    bom_id            bigint         NOT NULL REFERENCES manufacturing.boms (id) ON DELETE CASCADE,
    sequence          integer        NOT NULL,
    name              varchar(200)   NOT NULL,
    work_center_id    bigint         NOT NULL REFERENCES manufacturing.work_centers (id) ON DELETE RESTRICT,
    setup_minutes     numeric(10, 2),
    minutes_per_cycle numeric(10, 2) NOT NULL,
    instructions      text           NOT NULL DEFAULT '',
    CONSTRAINT ck_bom_operations_setup CHECK (setup_minutes IS NULL OR setup_minutes >= 0),
    CONSTRAINT ck_bom_operations_minutes_per_cycle CHECK (minutes_per_cycle > 0),
    CONSTRAINT ck_bom_operations_instructions CHECK (char_length(instructions) <= 20000)
);
CREATE INDEX ix_bom_operations_bom ON manufacturing.bom_operations (bom_id, sequence);
CREATE INDEX ix_bom_operations_work_center ON manufacturing.bom_operations (work_center_id);

-- A line of a BoM (M11): a goods component per the BoM's output_quantity,
-- with its expected scrap. The operation that consumes it is optional, and
-- removing the operation clears it.
CREATE TABLE manufacturing.bom_lines (
    id            bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    bom_id        bigint         NOT NULL REFERENCES manufacturing.boms (id) ON DELETE CASCADE,
    variant_id    integer        NOT NULL,
    quantity      numeric(14, 3) NOT NULL,
    scrap_percent numeric(5, 2)  NOT NULL DEFAULT 0,
    operation_id  bigint         REFERENCES manufacturing.bom_operations (id) ON DELETE SET NULL,
    sort_order    integer        NOT NULL DEFAULT 0,
    note          text           NOT NULL DEFAULT '',
    CONSTRAINT ck_bom_lines_quantity CHECK (quantity > 0),
    CONSTRAINT ck_bom_lines_scrap CHECK (scrap_percent >= 0 AND scrap_percent <= 99.99),
    CONSTRAINT ck_bom_lines_note CHECK (char_length(note) <= 2000)
);
CREATE INDEX ix_bom_lines_bom ON manufacturing.bom_lines (bom_id, sort_order);
CREATE INDEX ix_bom_lines_variant ON manufacturing.bom_lines (variant_id);
CREATE INDEX ix_bom_lines_operation ON manufacturing.bom_lines (operation_id);

-- +goose Down
DROP SCHEMA manufacturing CASCADE;
