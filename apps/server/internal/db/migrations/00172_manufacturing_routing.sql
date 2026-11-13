-- +goose Up
-- Routing upgrades (manufacturing phase 2 M112–M119): operations that form a
-- graph instead of one line, a send-ahead quantity that lets a successor
-- start before its predecessor is done, and work center groups an operation
-- may target instead of one center.

-- A group of work centers (M117): interchangeable active centers of one site.
-- An operation that targets the group is planned at whichever member finishes
-- it first (M119). Members are kept while an archived center stays in the
-- group: planning skips a member that is not active.
CREATE TABLE manufacturing.work_center_groups (
    id         bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    code       varchar(16)  NOT NULL,
    name       varchar(200) NOT NULL,
    site_id    bigint       NOT NULL,
    status     varchar(10)  NOT NULL DEFAULT 'active',
    revision   integer      NOT NULL DEFAULT 1,
    created_at timestamptz  NOT NULL,
    updated_at timestamptz  NOT NULL,
    CONSTRAINT ck_work_center_groups_code CHECK (code ~ '^[A-Z0-9]{1,16}$'),
    CONSTRAINT ck_work_center_groups_status CHECK (status IN ('active', 'archived'))
);
CREATE UNIQUE INDEX ux_work_center_groups_code ON manufacturing.work_center_groups (code);

CREATE TABLE manufacturing.work_center_group_members (
    group_id       bigint NOT NULL REFERENCES manufacturing.work_center_groups (id) ON DELETE CASCADE,
    work_center_id bigint NOT NULL REFERENCES manufacturing.work_centers (id) ON DELETE RESTRICT,
    PRIMARY KEY (group_id, work_center_id)
);
CREATE INDEX ix_work_center_group_members_center ON manufacturing.work_center_group_members (work_center_id);

-- An operation targets a center or a group (M118), and may name a send-ahead
-- quantity (M116): how many good units it passes on before its successors
-- may start.
ALTER TABLE manufacturing.bom_operations
    ALTER COLUMN work_center_id DROP NOT NULL,
    ADD COLUMN work_center_group_id bigint REFERENCES manufacturing.work_center_groups (id) ON DELETE RESTRICT,
    ADD COLUMN send_ahead_quantity numeric(14, 3),
    ADD CONSTRAINT ck_bom_operations_target CHECK (num_nonnulls(work_center_id, work_center_group_id) = 1),
    ADD CONSTRAINT ck_bom_operations_send_ahead CHECK (send_ahead_quantity IS NULL OR send_ahead_quantity > 0);
CREATE INDEX ix_bom_operations_group ON manufacturing.bom_operations (work_center_group_id) WHERE work_center_group_id IS NOT NULL;

-- The operations' graph (M112): operation_id runs after depends_on_id. An
-- operation with no row starts at once.
CREATE TABLE manufacturing.bom_operation_dependencies (
    operation_id  bigint NOT NULL REFERENCES manufacturing.bom_operations (id) ON DELETE CASCADE,
    depends_on_id bigint NOT NULL REFERENCES manufacturing.bom_operations (id) ON DELETE CASCADE,
    PRIMARY KEY (operation_id, depends_on_id),
    CONSTRAINT ck_bom_operation_dependencies_self CHECK (operation_id <> depends_on_id)
);
CREATE INDEX ix_bom_operation_dependencies_on ON manufacturing.bom_operation_dependencies (depends_on_id);

-- Every phase 1 BoM keeps its meaning: each operation after the one before it
-- by sequence.
INSERT INTO manufacturing.bom_operation_dependencies (operation_id, depends_on_id)
SELECT id, previous FROM (
    SELECT id, lag(id) OVER (PARTITION BY bom_id ORDER BY sequence, id) AS previous
    FROM manufacturing.bom_operations) x
WHERE previous IS NOT NULL;

-- A work order snapshots its operation's group, send-ahead quantity and the
-- BoM's output quantity, and whether the setup is the operation's own: when
-- planning or the floor chooses another member of the group, its figures are
-- taken again from that member and the expected minutes recomputed (M118).
-- quantity_done is what Register quantity has passed on (M116).
ALTER TABLE manufacturing.work_orders
    ADD COLUMN work_center_group_id bigint REFERENCES manufacturing.work_center_groups (id) ON DELETE RESTRICT,
    ADD COLUMN send_ahead_quantity  numeric(14, 3),
    ADD COLUMN quantity_done        numeric(14, 3) NOT NULL DEFAULT 0,
    ADD COLUMN bom_output_quantity  numeric(14, 3),
    ADD COLUMN own_setup            boolean        NOT NULL DEFAULT false,
    ADD CONSTRAINT ck_work_orders_send_ahead CHECK (send_ahead_quantity IS NULL OR send_ahead_quantity > 0),
    ADD CONSTRAINT ck_work_orders_quantity_done CHECK (quantity_done >= 0);

UPDATE manufacturing.work_orders w SET bom_output_quantity = b.output_quantity
FROM manufacturing.orders o JOIN manufacturing.boms b ON b.id = o.bom_id
WHERE o.id = w.order_id;

-- The work orders' graph (M113), copied from the operations' at confirm.
CREATE TABLE manufacturing.work_order_dependencies (
    work_order_id bigint NOT NULL REFERENCES manufacturing.work_orders (id) ON DELETE CASCADE,
    depends_on_id bigint NOT NULL REFERENCES manufacturing.work_orders (id) ON DELETE CASCADE,
    PRIMARY KEY (work_order_id, depends_on_id),
    CONSTRAINT ck_work_order_dependencies_self CHECK (work_order_id <> depends_on_id)
);
CREATE INDEX ix_work_order_dependencies_on ON manufacturing.work_order_dependencies (depends_on_id);

INSERT INTO manufacturing.work_order_dependencies (work_order_id, depends_on_id)
SELECT id, previous FROM (
    SELECT id, lag(id) OVER (PARTITION BY order_id ORDER BY sequence, id) AS previous
    FROM manufacturing.work_orders) x
WHERE previous IS NOT NULL;

-- A registration of good units passed on (M116), kept so a request sent again
-- with its clientRequestId registers once.
CREATE TABLE manufacturing.work_order_quantities (
    id                bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    work_order_id     bigint         NOT NULL REFERENCES manufacturing.work_orders (id) ON DELETE CASCADE,
    client_request_id uuid           NOT NULL,
    quantity          numeric(14, 3) NOT NULL,
    user_id           uuid,
    user_display      text           NOT NULL DEFAULT '',
    registered_at     timestamptz    NOT NULL,
    CONSTRAINT ck_work_order_quantities_quantity CHECK (quantity > 0)
);
CREATE UNIQUE INDEX ux_work_order_quantities_request ON manufacturing.work_order_quantities (client_request_id);
CREATE INDEX ix_work_order_quantities_work_order ON manufacturing.work_order_quantities (work_order_id, registered_at);

-- +goose Down
DROP TABLE manufacturing.work_order_quantities;
DROP TABLE manufacturing.work_order_dependencies;
ALTER TABLE manufacturing.work_orders
    DROP CONSTRAINT ck_work_orders_quantity_done,
    DROP CONSTRAINT ck_work_orders_send_ahead,
    DROP COLUMN own_setup,
    DROP COLUMN bom_output_quantity,
    DROP COLUMN quantity_done,
    DROP COLUMN send_ahead_quantity,
    DROP COLUMN work_center_group_id;
DROP TABLE manufacturing.bom_operation_dependencies;
DROP INDEX manufacturing.ix_bom_operations_group;
ALTER TABLE manufacturing.bom_operations
    DROP CONSTRAINT ck_bom_operations_send_ahead,
    DROP CONSTRAINT ck_bom_operations_target,
    DROP COLUMN send_ahead_quantity,
    DROP COLUMN work_center_group_id,
    ALTER COLUMN work_center_id SET NOT NULL;
DROP TABLE manufacturing.work_center_group_members;
DROP TABLE manufacturing.work_center_groups;
