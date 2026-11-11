-- +goose Up
-- Work orders and time logs (manufacturing design §9, delivery C, M27–M30,
-- M27a, M28a, M29a): one work order per operation snapshotted at an order's
-- confirm, and the time logged against it on the shop floor — productive
-- time per operator, downtime per operator while a work center is blocked,
-- and the center's own downtime with no user. D adds the schedule's slots.
--
-- A user is identity's: an opaque id with its display name at the time, kept
-- as the order's timeline keeps its actor (erasure, rule 19). Times are UTC
-- instants (§11).

-- A work order (M27, M27a): an operation of the order's BoM as it was at
-- confirm — its sequence, name, work center, setup (the operation's, else the
-- work center's), cleanup, minutes per cycle and instructions — with the work
-- center's capacity, efficiency and both cost rates, for D's estimate.
-- operation_id is the BoM operation it was copied from: the component lines
-- consumed at it name the same id (M11). expected_minutes is M28a's figure,
-- computed at the snapshot and never recomputed. status: pending until the
-- work order before it is done, ready, in_progress while any productive log
-- is open, paused when started before with none open, done, cancelled.
CREATE TABLE manufacturing.work_orders (
    id                bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    order_id          bigint         NOT NULL REFERENCES manufacturing.orders (id) ON DELETE CASCADE,
    operation_id      bigint,
    sequence          integer        NOT NULL,
    name              varchar(200)   NOT NULL,
    work_center_id    bigint         NOT NULL REFERENCES manufacturing.work_centers (id) ON DELETE RESTRICT,
    setup_minutes     numeric(10, 2) NOT NULL DEFAULT 0,
    cleanup_minutes   numeric(10, 2) NOT NULL DEFAULT 0,
    minutes_per_cycle numeric(10, 2) NOT NULL,
    capacity          integer        NOT NULL,
    time_efficiency   integer        NOT NULL,
    machine_rate      numeric(12, 2),
    labour_rate       numeric(12, 2),
    instructions      text           NOT NULL DEFAULT '',
    expected_minutes  bigint         NOT NULL,
    planned_start     timestamptz,
    planned_finish    timestamptz,
    status            varchar(12)    NOT NULL DEFAULT 'pending',
    started_at        timestamptz,
    finished_at       timestamptz,
    revision          integer        NOT NULL DEFAULT 1,
    CONSTRAINT ck_work_orders_setup CHECK (setup_minutes >= 0),
    CONSTRAINT ck_work_orders_cleanup CHECK (cleanup_minutes >= 0),
    CONSTRAINT ck_work_orders_minutes_per_cycle CHECK (minutes_per_cycle > 0),
    CONSTRAINT ck_work_orders_capacity CHECK (capacity >= 1),
    CONSTRAINT ck_work_orders_time_efficiency CHECK (time_efficiency BETWEEN 1 AND 500),
    CONSTRAINT ck_work_orders_expected CHECK (expected_minutes >= 0),
    CONSTRAINT ck_work_orders_status CHECK (status IN ('pending', 'ready', 'in_progress', 'paused', 'done', 'cancelled')),
    CONSTRAINT ck_work_orders_planned CHECK (planned_finish IS NULL OR planned_start IS NULL OR planned_finish >= planned_start)
);
CREATE INDEX ix_work_orders_order ON manufacturing.work_orders (order_id, sequence, id);
CREATE INDEX ix_work_orders_work_center ON manufacturing.work_orders (work_center_id, status);

-- A time log (M29, M30): time at a work center from started_at to ended_at,
-- null while it runs. productive is an operator's work; downtime carries a
-- reason and is either an operator's on a work order or, with neither a user
-- nor a work order, the center's own while it is blocked. quantity is what
-- the operator registered at finish. forced marks a start out of sequence;
-- corrected_by and corrected_at a supervisor's correction or addition
-- (M29). note_by is whose words the note is, with their display name at the
-- time: the supervisor's for a correction or an addition, the blocker's for a
-- block's downtime logs — on the center's own log it is who blocked it,
-- noted or not. The erasure sweep blanks the notes of a note_by identity no
-- longer knows. One open log per user in the whole workspace: the partial unique
-- index below, which a race of two starts trips (log_conflict, M29a).
CREATE TABLE manufacturing.time_logs (
    id                   bigint         GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    work_order_id        bigint         REFERENCES manufacturing.work_orders (id) ON DELETE CASCADE,
    work_center_id       bigint         NOT NULL REFERENCES manufacturing.work_centers (id) ON DELETE RESTRICT,
    user_id              uuid,
    user_display         text           NOT NULL DEFAULT '',
    kind                 varchar(12)    NOT NULL,
    reason               varchar(24),
    note                 text           NOT NULL DEFAULT '',
    started_at           timestamptz    NOT NULL,
    ended_at             timestamptz,
    quantity             numeric(14, 3),
    forced               boolean        NOT NULL DEFAULT false,
    corrected_by         uuid,
    corrected_by_display text           NOT NULL DEFAULT '',
    corrected_at         timestamptz,
    note_by              uuid,
    note_by_display      text           NOT NULL DEFAULT '',
    CONSTRAINT ck_time_logs_kind CHECK (kind IN ('productive', 'downtime')),
    CONSTRAINT ck_time_logs_reason CHECK (
        (kind = 'productive' AND reason IS NULL) OR
        (kind = 'downtime' AND reason IN ('breakdown', 'material_missing', 'setup', 'waiting_for_operator', 'quality', 'other'))),
    CONSTRAINT ck_time_logs_center CHECK (user_id IS NOT NULL OR (work_order_id IS NULL AND kind = 'downtime')),
    CONSTRAINT ck_time_logs_note CHECK (char_length(note) <= 2000),
    CONSTRAINT ck_time_logs_range CHECK (ended_at IS NULL OR ended_at >= started_at),
    CONSTRAINT ck_time_logs_quantity CHECK (quantity IS NULL OR quantity >= 0)
);
CREATE UNIQUE INDEX ux_time_logs_open_user ON manufacturing.time_logs (user_id) WHERE ended_at IS NULL AND user_id IS NOT NULL;
CREATE INDEX ix_time_logs_work_order ON manufacturing.time_logs (work_order_id, started_at) WHERE work_order_id IS NOT NULL;
CREATE INDEX ix_time_logs_open_work_center ON manufacturing.time_logs (work_center_id) WHERE ended_at IS NULL;
CREATE INDEX ix_time_logs_user ON manufacturing.time_logs (user_id, started_at) WHERE user_id IS NOT NULL;
CREATE INDEX ix_time_logs_note_by ON manufacturing.time_logs (note_by) WHERE note <> '';

-- +goose Down
DROP TABLE manufacturing.time_logs;
DROP TABLE manufacturing.work_orders;
