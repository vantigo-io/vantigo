-- +goose Up
-- Quality checks and deviations (manufacturing phase 2 M95–M99).
--
-- A check template of a BoM operation (M95): read and acknowledge an
-- instruction, pass or fail, a measure judged against its limits, or a photo;
-- recorded once per work order (each_work_order) or before any output passes
-- the work order (first_piece). A measure has a min, a max or both, and the
-- nominal lies between them when given. At most 20 an operation, judged by
-- the module.
CREATE TABLE manufacturing.bom_operation_checks (
    id                 bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    operation_id       bigint         NOT NULL REFERENCES manufacturing.bom_operations (id) ON DELETE CASCADE,
    sequence           integer        NOT NULL,
    kind               text           NOT NULL,
    title              varchar(200)   NOT NULL,
    instructions       text           NOT NULL DEFAULT '',
    nominal            numeric(14, 4),
    min_value          numeric(14, 4),
    max_value          numeric(14, 4),
    unit               varchar(20),
    frequency          text           NOT NULL,
    quarantine_on_fail boolean        NOT NULL DEFAULT false,
    CONSTRAINT ck_bom_operation_checks_kind CHECK (kind IN ('instruction', 'pass_fail', 'measure', 'photo')),
    CONSTRAINT ck_bom_operation_checks_frequency CHECK (frequency IN ('each_work_order', 'first_piece')),
    CONSTRAINT ck_bom_operation_checks_instructions CHECK (char_length(instructions) <= 5000),
    CONSTRAINT ck_bom_operation_checks_limits CHECK (kind <> 'measure' OR min_value IS NOT NULL OR max_value IS NOT NULL)
);
CREATE INDEX ix_bom_operation_checks_operation ON manufacturing.bom_operation_checks (operation_id, sequence, id);

-- The checks as the work order got them at confirm (M96): the template's
-- fields copied, as the operations are, so a later BoM edit never reaches an
-- open order. template_id names the template it came from, opaque once the
-- template is gone.
CREATE TABLE manufacturing.work_order_checks (
    id                 bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    work_order_id      bigint         NOT NULL REFERENCES manufacturing.work_orders (id) ON DELETE CASCADE,
    template_id        bigint,
    sequence           integer        NOT NULL,
    kind               text           NOT NULL,
    title              varchar(200)   NOT NULL,
    instructions       text           NOT NULL DEFAULT '',
    nominal            numeric(14, 4),
    min_value          numeric(14, 4),
    max_value          numeric(14, 4),
    unit               varchar(20),
    frequency          text           NOT NULL,
    quarantine_on_fail boolean        NOT NULL DEFAULT false,
    CONSTRAINT ck_work_order_checks_kind CHECK (kind IN ('instruction', 'pass_fail', 'measure', 'photo')),
    CONSTRAINT ck_work_order_checks_frequency CHECK (frequency IN ('each_work_order', 'first_piece'))
);
CREATE INDEX ix_work_order_checks_work_order ON manufacturing.work_order_checks (work_order_id, sequence, id);

-- A check's photo (M97): the object in the manufacturing-quality storage
-- scope under checks/<uuid>, a key that names nothing, uploaded before the
-- result that points at it. A photo is a quality record and stays when its
-- result is superseded; one no result points at 24 hours after its upload is
-- deleted and its object queued on manufacturing.object_deletions.
CREATE TABLE manufacturing.check_photos (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    work_order_id       bigint        NOT NULL REFERENCES manufacturing.work_orders (id) ON DELETE CASCADE,
    object_key          varchar(200)  NOT NULL,
    content_type        varchar(100)  NOT NULL,
    size_bytes          bigint        NOT NULL,
    uploaded_by_user_id uuid,
    uploaded_by_display varchar(200),
    created_at          timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_check_photos_object_key ON manufacturing.check_photos (object_key);
CREATE INDEX ix_check_photos_created ON manufacturing.check_photos (created_at);

-- A check's result (M97): done for an instruction, pass or fail — a measure
-- judged by the server against its limits. Recording again supersedes the
-- result in force, which is kept and points at its successor: one result in
-- force per check. user_id is identity's user, kept with the display as it
-- was (rule 19).
CREATE TABLE manufacturing.check_results (
    id                  bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    work_order_check_id bigint         NOT NULL REFERENCES manufacturing.work_order_checks (id) ON DELETE CASCADE,
    result              text           NOT NULL,
    value               numeric(14, 4),
    note                varchar(1000),
    photo_id            bigint         REFERENCES manufacturing.check_photos (id),
    user_id             uuid,
    user_display        varchar(200)   NOT NULL DEFAULT '',
    recorded_at         timestamptz    NOT NULL,
    superseded_by       bigint         REFERENCES manufacturing.check_results (id),
    CONSTRAINT ck_check_results_result CHECK (result IN ('done', 'pass', 'fail'))
);
CREATE UNIQUE INDEX ux_check_results_current ON manufacturing.check_results (work_order_check_id) WHERE superseded_by IS NULL;
CREATE INDEX ix_check_results_photo ON manufacturing.check_results (photo_id) WHERE photo_id IS NOT NULL;

-- A deviation (M99; nb avvik), DEV-<n> from the counter 'deviation': opened
-- by a failed check in its result's transaction or by a person, linked to an
-- order, a work order, a result and a lot as it was opened from. quarantine
-- puts a quarantine hold from it, through Inventory's ledger, on the lots its
-- order made and makes while it is open, and on its lot. Closing it names a
-- disposition and a note; use_as_is and rework release its own holds.
CREATE TABLE manufacturing.deviations (
    id                bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number            bigint         NOT NULL,
    title             varchar(200)   NOT NULL,
    description       text           NOT NULL DEFAULT '',
    order_id          bigint         REFERENCES manufacturing.orders (id),
    work_order_id     bigint         REFERENCES manufacturing.work_orders (id),
    check_result_id   bigint         REFERENCES manufacturing.check_results (id),
    lot_id            bigint,
    lot_number        varchar(20),
    variant_id        integer,
    disposition       text,
    status            text           NOT NULL DEFAULT 'open',
    quarantine        boolean        NOT NULL DEFAULT false,
    holds_released    boolean        NOT NULL DEFAULT false,
    opened_by_user_id uuid,
    opened_by_display varchar(200)   NOT NULL DEFAULT '',
    opened_at         timestamptz    NOT NULL,
    closed_by_user_id uuid,
    closed_by_display varchar(200),
    closed_at         timestamptz,
    closing_note      varchar(2000),
    revision          integer        NOT NULL DEFAULT 1,
    CONSTRAINT ck_deviations_status CHECK (status IN ('open', 'closed')),
    CONSTRAINT ck_deviations_disposition CHECK (disposition IS NULL OR disposition IN ('use_as_is', 'rework', 'scrap', 'return')),
    CONSTRAINT ck_deviations_description CHECK (char_length(description) <= 5000),
    CONSTRAINT ck_deviations_closed CHECK (status = 'open' OR (disposition IS NOT NULL AND closed_at IS NOT NULL))
);
CREATE UNIQUE INDEX ux_deviations_number ON manufacturing.deviations (number);
CREATE INDEX ix_deviations_status ON manufacturing.deviations (status, opened_at DESC, id DESC);
CREATE INDEX ix_deviations_order ON manufacturing.deviations (order_id) WHERE order_id IS NOT NULL;
CREATE INDEX ix_deviations_lot ON manufacturing.deviations (lot_id) WHERE lot_id IS NOT NULL;
CREATE INDEX ix_deviations_check_result ON manufacturing.deviations (check_result_id) WHERE check_result_id IS NOT NULL;

-- The lots a deviation put a hold on (M99), with their numbers as they were:
-- the deviation's own record of its HoldLots calls, which its page lists and
-- its release names.
CREATE TABLE manufacturing.deviation_lots (
    deviation_id bigint      NOT NULL REFERENCES manufacturing.deviations (id) ON DELETE CASCADE,
    lot_id       bigint      NOT NULL,
    lot_number   varchar(20) NOT NULL,
    held_at      timestamptz NOT NULL,
    PRIMARY KEY (deviation_id, lot_id)
);
CREATE INDEX ix_deviation_lots_lot ON manufacturing.deviation_lots (lot_id);

-- The objects still to be removed from the store (M97): Customers'
-- customers.object_deletions (00044), column for column.
CREATE TABLE manufacturing.object_deletions (
    id              bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    object_key      varchar(200) NOT NULL,
    attempts        integer      NOT NULL DEFAULT 0,
    next_attempt_at timestamptz  NOT NULL,
    claimed_until   timestamptz,
    last_error      varchar(200),
    created_at      timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_manufacturing_object_deletions_object_key ON manufacturing.object_deletions (object_key);
CREATE INDEX ix_manufacturing_object_deletions_next_attempt ON manufacturing.object_deletions (next_attempt_at);

-- +goose Down
DROP TABLE manufacturing.object_deletions;
DROP TABLE manufacturing.deviation_lots;
DROP TABLE manufacturing.deviations;
DROP TABLE manufacturing.check_results;
DROP TABLE manufacturing.check_photos;
DROP TABLE manufacturing.work_order_checks;
DROP TABLE manufacturing.bom_operation_checks;
DELETE FROM manufacturing.counters WHERE counter_name = 'deviation';
