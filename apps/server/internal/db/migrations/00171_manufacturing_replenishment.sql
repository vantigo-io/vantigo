-- +goose Up
-- Replenishment suggestions (manufacturing phase 2 M107–M111).
--
-- A run of the replenishment job (M109): nightly at 02:00 in the business
-- zone (kind nightly, one per day, nightly_for unique) or asked for by a
-- person (kind manual, for one site or every active site). The worker claims
-- a pending run, or a running one whose lease has run out, by moving its
-- lease on; it holds no session lock. Runs are kept 90 days.
CREATE TABLE manufacturing.replenishment_runs (
    id                   bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    site_id              bigint,
    kind                 varchar(12)   NOT NULL,
    nightly_for          date,
    status               varchar(12)   NOT NULL DEFAULT 'pending',
    requested_at         timestamptz   NOT NULL,
    requested_by_user_id uuid,
    requested_by_display varchar(200),
    started_at           timestamptz,
    finished_at          timestamptz,
    lease_until          timestamptz,
    attempts             integer       NOT NULL DEFAULT 0,
    suggestions          integer       NOT NULL DEFAULT 0,
    orders_created       integer       NOT NULL DEFAULT 0,
    sales_counted        boolean       NOT NULL DEFAULT true,
    error                text,
    CONSTRAINT ck_replenishment_runs_kind CHECK (kind IN ('nightly', 'manual')),
    CONSTRAINT ck_replenishment_runs_status CHECK (status IN ('pending', 'running', 'done', 'failed')),
    CONSTRAINT ck_replenishment_runs_nightly CHECK ((kind = 'nightly') = (nightly_for IS NOT NULL))
);
CREATE UNIQUE INDEX ux_replenishment_runs_nightly ON manufacturing.replenishment_runs (nightly_for) WHERE nightly_for IS NOT NULL;
CREATE INDEX ix_replenishment_runs_due ON manufacturing.replenishment_runs (status, requested_at) WHERE status IN ('pending', 'running');
CREATE INDEX ix_replenishment_runs_requested ON manufacturing.replenishment_runs (requested_at DESC);

-- A suggestion (M110): to make (the variant has a default active BoM) or to
-- buy, at a site, for the quantity that brings the projected stock to the
-- placement's target, rounded up to its multiple, needed by need_by. reason
-- keeps the figures the sentence is made of. A run replaces the site's open
-- suggestions (superseded); one acted on — done with the order made, or
-- dismissed — is kept. One open suggestion per (site, variant). acted_by is
-- identity's user, opaque here, with the display as it was; when a run
-- created the order itself the user is null and the display "System".
CREATE TABLE manufacturing.suggestions (
    id                bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    run_id            bigint         NOT NULL REFERENCES manufacturing.replenishment_runs (id) ON DELETE CASCADE,
    site_id           bigint         NOT NULL,
    variant_id        integer        NOT NULL,
    sku               text           NOT NULL,
    product_name      text           NOT NULL,
    kind              varchar(8)     NOT NULL,
    quantity          numeric(14,3)  NOT NULL,
    need_by           date           NOT NULL,
    reason            jsonb          NOT NULL,
    bom_id            bigint         REFERENCES manufacturing.boms (id) ON DELETE SET NULL,
    status            varchar(12)    NOT NULL DEFAULT 'open',
    reorder_open      boolean        NOT NULL DEFAULT false,
    order_id          bigint         REFERENCES manufacturing.orders (id) ON DELETE SET NULL,
    acted_by_user_id  uuid,
    acted_by_display  varchar(200),
    acted_at          timestamptz,
    created_at        timestamptz    NOT NULL,
    CONSTRAINT ck_suggestions_kind CHECK (kind IN ('make', 'buy')),
    CONSTRAINT ck_suggestions_quantity CHECK (quantity > 0),
    CONSTRAINT ck_suggestions_status CHECK (status IN ('open', 'done', 'dismissed', 'superseded'))
);
CREATE UNIQUE INDEX ux_suggestions_open ON manufacturing.suggestions (site_id, variant_id) WHERE status = 'open';
CREATE INDEX ix_suggestions_run ON manufacturing.suggestions (run_id);
CREATE INDEX ix_suggestions_order ON manufacturing.suggestions (order_id) WHERE order_id IS NOT NULL;

-- Automatic creation per (variant, site) (M111, R reading 7), set on the BoM
-- page: off by default. With auto_create the nightly run creates the order
-- itself, as the system, and confirms it too with auto_confirm.
CREATE TABLE manufacturing.replenishment_rules (
    variant_id          integer       NOT NULL,
    site_id             bigint        NOT NULL,
    auto_create         boolean       NOT NULL DEFAULT false,
    auto_confirm        boolean       NOT NULL DEFAULT false,
    updated_by_user_id  uuid,
    updated_by_display  varchar(200),
    updated_at          timestamptz   NOT NULL,
    PRIMARY KEY (variant_id, site_id),
    CONSTRAINT ck_replenishment_rules_confirm CHECK (auto_create OR NOT auto_confirm)
);

-- An order made from a suggestion names the origin reorder.
ALTER TABLE manufacturing.orders DROP CONSTRAINT ck_orders_origin_kind;
ALTER TABLE manufacturing.orders
    ADD CONSTRAINT ck_orders_origin_kind CHECK (origin_kind IN ('manual', 'sales_order', 'parent', 'reorder'));
-- The projection reads the open and reorder-draft orders of a site.
CREATE INDEX ix_orders_site_status ON manufacturing.orders (site_id, status);

-- +goose Down
DROP INDEX manufacturing.ix_orders_site_status;
UPDATE manufacturing.orders SET origin_kind = 'manual' WHERE origin_kind = 'reorder';
ALTER TABLE manufacturing.orders DROP CONSTRAINT ck_orders_origin_kind;
ALTER TABLE manufacturing.orders
    ADD CONSTRAINT ck_orders_origin_kind CHECK (origin_kind IN ('manual', 'sales_order', 'parent'));
DROP TABLE manufacturing.replenishment_rules;
DROP TABLE manufacturing.suggestions;
DROP TABLE manufacturing.replenishment_runs;
