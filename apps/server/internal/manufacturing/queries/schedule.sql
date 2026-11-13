-- The production schedule, cost and reports (manufacturing design M34–M42,
-- M35a): the slots planned work orders hold on their work centers'
-- calendars, the board's reads, the cost snapshots and the reports' reads.
-- Every write of slots holds the schedule's advisory lock of each center it
-- touches first (LockScheduleCenter), in id order, before any row lock.

-- name: LockScheduleCenter :exec
-- LockScheduleCenter is the schedule's lock on one work center (M35a): the
-- transaction-scoped advisory lock of class 19783 (0x4D47, the class after
-- the BoM graph's 19782, kept for the schedule) and the work center's id as
-- the key. Plan, plan all, reschedule and unplan take one per center they
-- touch, in id order, before any row lock — before LockVariants too — so
-- two plans never book the same free time.
SELECT pg_advisory_xact_lock(19783, @work_center_id::int);

-- name: WorkCenterIDsOfOrders :many
-- WorkCenterIDsOfOrders is every work center the orders' work orders name,
-- and every member of the groups they target (manufacturing phase 2 M115,
-- M119): a plan or a drag may move a group work order to any of them.
SELECT work_center_id::bigint AS id FROM manufacturing.work_orders WHERE order_id = ANY(@order_ids::bigint[])
UNION
SELECT m.work_center_id FROM manufacturing.work_center_group_members m
JOIN manufacturing.work_orders w ON w.work_center_group_id = m.group_id
WHERE w.order_id = ANY(@order_ids::bigint[])
ORDER BY id;

-- name: WorkCenterIDsAtSite :many
-- WorkCenterIDsAtSite is every work center at the site, of any status: plan
-- all locks them all (M35a).
SELECT id FROM manufacturing.work_centers WHERE site_id = @site_id ORDER BY id;

-- name: UnplannedOrderIDsAtSite :many
-- UnplannedOrderIDsAtSite is the open orders at the site with no plan:
-- what plan all plans (M35). An order is planned when one of its open work
-- orders holds a slot; one with no open work order, when it has a window.
SELECT o.id FROM manufacturing.orders o
WHERE o.site_id = @site_id AND o.status IN ('confirmed', 'in_progress')
  AND NOT EXISTS (SELECT 1 FROM manufacturing.work_order_slots s
                  JOIN manufacturing.work_orders w ON w.id = s.work_order_id
                  WHERE w.order_id = o.id AND w.status NOT IN ('done', 'cancelled'))
  AND (o.planned_start IS NULL OR EXISTS (SELECT 1 FROM manufacturing.work_orders w
                                          WHERE w.order_id = o.id AND w.status NOT IN ('done', 'cancelled')))
ORDER BY o.id
LIMIT 500;

-- name: BookedSlots :many
-- BookedSlots is the time already booked at the work centers from from on
-- (M35a): the slots of work orders that are not done or cancelled, but for
-- the work orders being planned.
SELECT s.* FROM manufacturing.work_order_slots s
JOIN manufacturing.work_orders w ON w.id = s.work_order_id
WHERE s.work_center_id = ANY(@work_center_ids::bigint[])
  AND s.ends_at > @from_at::timestamptz
  AND w.status NOT IN ('done', 'cancelled')
  AND NOT (s.work_order_id = ANY(@except_ids::bigint[]))
ORDER BY s.work_center_id, s.starts_at;

-- name: SlotsOfWorkOrders :many
SELECT * FROM manufacturing.work_order_slots
WHERE work_order_id = ANY(@ids::bigint[])
ORDER BY work_order_id, starts_at;

-- name: DeleteSlotsOfWorkOrders :exec
DELETE FROM manufacturing.work_order_slots WHERE work_order_id = ANY(@ids::bigint[]);

-- name: InsertSlot :exec
INSERT INTO manufacturing.work_order_slots (work_order_id, work_center_id, starts_at, ends_at)
VALUES (@work_order_id, @work_center_id, @starts_at, @ends_at);

-- name: SetWorkOrderPlan :one
-- SetWorkOrderPlan sets or clears a work order's planned window and moves
-- its revision on.
UPDATE manufacturing.work_orders
SET planned_start = sqlc.narg(planned_start), planned_finish = sqlc.narg(planned_finish), revision = revision + 1
WHERE id = @id
RETURNING *;

-- name: SetOrderPlan :one
-- SetOrderPlan sets or clears an order's planned window (M34) and moves its
-- revision on.
UPDATE manufacturing.orders
SET planned_start = sqlc.narg(planned_start), planned_finish = sqlc.narg(planned_finish),
    revision = revision + 1, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: SetOrderEstimate :exec
-- SetOrderEstimate fixes the estimate at confirm (M39).
UPDATE manufacturing.orders SET estimate = @estimate WHERE id = @id;

-- name: SetOrderActual :exec
-- SetOrderActual fixes the actual cost at close (M41).
UPDATE manufacturing.orders SET actual = @actual WHERE id = @id;

-- name: ScheduleSlots :many
-- ScheduleSlots is the board's bars (M36): the slots at the work centers
-- overlapping [from_at, to_at), of work orders and orders not cancelled,
-- with their work order's and order's figures. A done work order's slots
-- end where it finished: those after are dropped, the one it finished in is
-- cut there.
SELECT s.id, s.work_order_id, s.work_center_id, s.starts_at,
       (CASE WHEN w.status = 'done' AND w.finished_at IS NOT NULL THEN LEAST(s.ends_at, w.finished_at) ELSE s.ends_at END)::timestamptz AS ends_at,
       w.sequence, w.name, w.status, w.revision, w.planned_start, w.planned_finish, w.work_center_group_id,
       EXISTS (SELECT 1 FROM manufacturing.time_logs l WHERE l.work_order_id = w.id)::boolean AS has_logs,
       o.id AS order_id, o.number AS order_number, o.suffix AS order_suffix, o.variant_id AS order_variant_id,
       o.priority AS order_priority, o.due_date AS order_due_date, o.status AS order_status
FROM manufacturing.work_order_slots s
JOIN manufacturing.work_orders w ON w.id = s.work_order_id
JOIN manufacturing.orders o ON o.id = w.order_id
WHERE s.work_center_id = ANY(@work_center_ids::bigint[])
  AND s.starts_at < @to_at::timestamptz AND s.ends_at > @from_at::timestamptz
  AND w.status <> 'cancelled' AND o.status <> 'cancelled'
  AND (w.status <> 'done' OR w.finished_at IS NULL OR s.starts_at < w.finished_at)
  AND (w.status <> 'done' OR w.finished_at IS NULL OR w.finished_at > @from_at::timestamptz)
ORDER BY s.work_center_id, s.starts_at, s.id;

-- name: ClosuresOfWorkCenters :many
-- ClosuresOfWorkCenters is the closures of the work centers touching the
-- days from_date … to_date.
SELECT * FROM manufacturing.work_center_closures
WHERE work_center_id = ANY(@work_center_ids::bigint[])
  AND to_date >= @from_date::date AND from_date <= @to_date::date
ORDER BY work_center_id, from_date, id;

-- name: UnscheduledOrdersAtSite :many
-- UnscheduledOrdersAtSite is the board's Unscheduled list (M36): the open
-- orders at the site with no plan — as UnplannedOrderIDsAtSite judges it —
-- urgent first, then by due date, with the expected minutes of their work
-- orders not done or cancelled.
SELECT sqlc.embed(o),
       (SELECT COALESCE(sum(w.expected_minutes), 0) FROM manufacturing.work_orders w
        WHERE w.order_id = o.id AND w.status NOT IN ('done', 'cancelled'))::bigint AS expected_minutes
FROM manufacturing.orders o
WHERE o.site_id = @site_id AND o.status IN ('confirmed', 'in_progress')
  AND NOT EXISTS (SELECT 1 FROM manufacturing.work_order_slots s
                  JOIN manufacturing.work_orders w ON w.id = s.work_order_id
                  WHERE w.order_id = o.id AND w.status NOT IN ('done', 'cancelled'))
  AND (o.planned_start IS NULL OR EXISTS (SELECT 1 FROM manufacturing.work_orders w
                                          WHERE w.order_id = o.id AND w.status NOT IN ('done', 'cancelled')))
ORDER BY (o.priority = 'urgent') DESC, o.due_date ASC NULLS LAST, o.number, o.suffix
LIMIT 500;

-- name: DoneOrdersClosedBetween :many
-- DoneOrdersClosedBetween is the production cost report's orders (M42): the
-- done orders closed in [from_at, to_at), at one site when site_id is set,
-- by product and close.
SELECT * FROM manufacturing.orders
WHERE status = 'done' AND closed_at >= @from_at::timestamptz AND closed_at < @to_at::timestamptz
  AND (sqlc.narg(site_id)::bigint IS NULL OR site_id = sqlc.narg(site_id)::bigint)
ORDER BY variant_id, closed_at, id
LIMIT 5000;

-- name: SlotsAtWorkCentersBetween :many
-- SlotsAtWorkCentersBetween is the planned time at the work centers
-- overlapping [from_at, to_at), of work orders not cancelled: the
-- utilisation report's planned time (M42). A done work order's slots end
-- where it finished, as on the board.
SELECT s.id, s.work_order_id, s.work_center_id, s.starts_at,
       (CASE WHEN w.status = 'done' AND w.finished_at IS NOT NULL THEN LEAST(s.ends_at, w.finished_at) ELSE s.ends_at END)::timestamptz AS ends_at
FROM manufacturing.work_order_slots s
JOIN manufacturing.work_orders w ON w.id = s.work_order_id
WHERE s.work_center_id = ANY(@work_center_ids::bigint[])
  AND s.starts_at < @to_at::timestamptz AND s.ends_at > @from_at::timestamptz
  AND w.status <> 'cancelled'
  AND (w.status <> 'done' OR w.finished_at IS NULL OR s.starts_at < w.finished_at)
  AND (w.status <> 'done' OR w.finished_at IS NULL OR w.finished_at > @from_at::timestamptz)
ORDER BY s.work_center_id, s.starts_at;

-- name: LogsAtWorkCentersBetween :many
-- LogsAtWorkCentersBetween is every time log at the work centers
-- overlapping [from_at, to_at), open ones included: the utilisation
-- report's productive time and downtime (M42).
SELECT * FROM manufacturing.time_logs
WHERE work_center_id = ANY(@work_center_ids::bigint[])
  AND started_at < @to_at::timestamptz AND (ended_at IS NULL OR ended_at > @from_at::timestamptz)
ORDER BY work_center_id, started_at, id;

-- name: RecentDoneWorkOrders :many
-- RecentDoneWorkOrders is the time deviation report's work orders (M42):
-- per BoM operation — the order's BoM and the operation's sequence — the
-- last 10 done work orders finished in [from_at, to_at), at one site when
-- site_id is set.
SELECT x.id, x.bom_id, x.sequence, x.name, x.work_center_id, x.expected_minutes, x.finished_at
FROM (SELECT w.id, o.bom_id, w.sequence, w.name, w.work_center_id, w.expected_minutes, w.finished_at,
             row_number() OVER (PARTITION BY o.bom_id, w.sequence ORDER BY w.finished_at DESC, w.id DESC) AS rank
      FROM manufacturing.work_orders w
      JOIN manufacturing.orders o ON o.id = w.order_id
      WHERE w.status = 'done' AND w.finished_at >= @from_at::timestamptz AND w.finished_at < @to_at::timestamptz
        AND (sqlc.narg(site_id)::bigint IS NULL OR o.site_id = sqlc.narg(site_id)::bigint)) x
WHERE x.rank <= 10
ORDER BY x.bom_id, x.sequence, x.finished_at DESC, x.id DESC
LIMIT 10000;

-- name: BomsByIDs :many
SELECT * FROM manufacturing.boms WHERE id = ANY(@ids::bigint[]) ORDER BY id;
