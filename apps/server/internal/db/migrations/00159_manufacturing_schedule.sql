-- +goose Up
-- The production schedule and cost (manufacturing design §9, delivery D,
-- M34–M41, M35a): the pieces of working time each planned work order holds
-- on its work center's calendar, the order's requested start, and the
-- order's estimate (fixed at confirm) and actual (fixed at close).
--
-- Times are UTC instants (§11); a date is a civil date in the business time
-- zone and never converted.

-- A slot (M35, M35a): one piece of a planned work order, from starts_at to
-- ends_at, inside one open interval of its work center's calendar. A work
-- order cut around a lunch break, a night or other bookings has several.
-- Free time at a work center is its open intervals less the slots of its
-- work orders that are not done or cancelled — never the work orders'
-- planned_start … planned_finish. work_center_id is the work order's own,
-- kept here so the board and the planner read a center's slots by its index.
-- Every write of a center's slots holds the schedule's advisory lock on the
-- center first (queries/schedule.sql, LockScheduleCenter).
CREATE TABLE manufacturing.work_order_slots (
    id             bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    work_order_id  bigint      NOT NULL REFERENCES manufacturing.work_orders (id) ON DELETE CASCADE,
    work_center_id bigint      NOT NULL REFERENCES manufacturing.work_centers (id) ON DELETE RESTRICT,
    starts_at      timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    CONSTRAINT ck_work_order_slots_range CHECK (ends_at > starts_at)
);
CREATE INDEX ix_work_order_slots_center ON manufacturing.work_order_slots (work_center_id, starts_at);
CREATE INDEX ix_work_order_slots_work_order ON manufacturing.work_order_slots (work_order_id, starts_at);

-- requested_start is the day planning may start the order from (M35a), set
-- by hand; null takes the default the planner works out from the due date.
-- estimate is the cost fixed at confirm (M39) and actual the cost fixed at
-- close (M41): each {components, machine, labour, total, incomplete}, the
-- amounts exact decimal text rounded half-up to two decimals once, at that
-- moment; actual also carries costPerUnit. Null until then.
ALTER TABLE manufacturing.orders
    ADD COLUMN requested_start date,
    ADD COLUMN estimate        jsonb,
    ADD COLUMN actual          jsonb;

-- +goose Down
ALTER TABLE manufacturing.orders
    DROP COLUMN actual,
    DROP COLUMN estimate,
    DROP COLUMN requested_start;
DROP TABLE manufacturing.work_order_slots;
