-- Work orders and time logs (manufacturing design M27–M30, M29a): the work
-- orders snapshotted at confirm, their states, and the time logged on the
-- shop floor. Every write runs under the lock order M26a names: the orders,
-- then their work orders in id order (LockWorkOrders), then the work center
-- (GetWorkCenterForShare for a start, GetWorkCenterForUpdate for a block),
-- then the time logs in id order (LockTimeLogs).

-- name: InsertWorkOrder :one
INSERT INTO manufacturing.work_orders (
    order_id, operation_id, sequence, name, work_center_id, setup_minutes, cleanup_minutes, minutes_per_cycle,
    capacity, time_efficiency, machine_rate, labour_rate, instructions, expected_minutes, status)
VALUES (@order_id, @operation_id, @sequence, @name, @work_center_id, @setup_minutes, @cleanup_minutes, @minutes_per_cycle,
        @capacity, @time_efficiency, @machine_rate, @labour_rate, @instructions, @expected_minutes, @status)
RETURNING *;

-- name: GetWorkOrder :one
SELECT * FROM manufacturing.work_orders WHERE id = @id;

-- name: WorkOrdersOfOrder :many
SELECT * FROM manufacturing.work_orders WHERE order_id = @order_id ORDER BY sequence, id;

-- name: LockWorkOrders :many
-- LockWorkOrders is step 4 of the lock order (M26a): the work orders, in id
-- order, after their orders.
SELECT * FROM manufacturing.work_orders WHERE id = ANY(@ids::bigint[]) ORDER BY id FOR UPDATE;

-- name: WorkOrderIDsOfOrders :many
SELECT id FROM manufacturing.work_orders WHERE order_id = ANY(@order_ids::bigint[]) ORDER BY id;

-- name: WorkOrderRows :many
-- WorkOrderRows is work orders with their order's figures, by id, for the
-- answers that show a work order with its order (M31).
SELECT sqlc.embed(w), o.number AS order_number, o.suffix AS order_suffix, o.variant_id AS order_variant_id,
       o.quantity AS order_quantity, o.produced AS order_produced, o.due_date AS order_due_date,
       o.priority AS order_priority, o.status AS order_status, o.site_id AS order_site_id
FROM manufacturing.work_orders w JOIN manufacturing.orders o ON o.id = w.order_id
WHERE w.id = ANY(@ids::bigint[])
ORDER BY w.id;

-- name: WorkCenterQueue :many
-- WorkCenterQueue is the work center's queue (M31): its ready, in-progress
-- and paused work orders of open orders, by planned start, then the order's
-- priority, then its due date.
SELECT w.id FROM manufacturing.work_orders w JOIN manufacturing.orders o ON o.id = w.order_id
WHERE w.work_center_id = @work_center_id
  AND w.status IN ('ready', 'in_progress', 'paused')
  AND o.status IN ('confirmed', 'in_progress')
ORDER BY w.planned_start ASC NULLS LAST, (o.priority = 'urgent') DESC, o.due_date ASC NULLS LAST,
         o.number, o.suffix, w.sequence, w.id
LIMIT 500;

-- name: LastWorkOrderOfOrders :many
-- LastWorkOrderOfOrders is each order's last work order that is not
-- cancelled: the one whose finish offers to register the production (M29).
SELECT DISTINCT ON (order_id) order_id, id FROM manufacturing.work_orders
WHERE order_id = ANY(@order_ids::bigint[]) AND status <> 'cancelled'
ORDER BY order_id, sequence DESC, id DESC;

-- name: StartWorkOrder :one
-- StartWorkOrder marks a work order started and in progress.
UPDATE manufacturing.work_orders
SET status = 'in_progress', started_at = COALESCE(started_at, @now::timestamptz), revision = revision + 1
WHERE id = @id
RETURNING *;

-- name: SettleWorkOrder :one
-- SettleWorkOrder is a work order's state from its logs (M27, M29a): in
-- progress while a productive log is open, else paused once started; a
-- work order never started, done or cancelled keeps its state.
UPDATE manufacturing.work_orders w
SET status = CASE
        WHEN EXISTS (SELECT 1 FROM manufacturing.time_logs l
                     WHERE l.work_order_id = w.id AND l.ended_at IS NULL AND l.kind = 'productive') THEN 'in_progress'
        WHEN w.started_at IS NOT NULL THEN 'paused'
        ELSE w.status END,
    revision = w.revision + 1
WHERE w.id = @id AND w.status NOT IN ('done', 'cancelled')
RETURNING *;

-- name: FinishWorkOrder :one
UPDATE manufacturing.work_orders
SET status = 'done', started_at = COALESCE(started_at, @now::timestamptz), finished_at = @now::timestamptz,
    revision = revision + 1
WHERE id = @id
RETURNING *;

-- name: ReadyNextWorkOrder :many
-- ReadyNextWorkOrder readies the first pending work order after the one
-- that finished, by sequence (M27).
UPDATE manufacturing.work_orders SET status = 'ready', revision = revision + 1
WHERE id = (SELECT n.id FROM manufacturing.work_orders n
            WHERE n.order_id = @order_id AND n.status = 'pending' AND (n.sequence, n.id) > (@sequence::int, @id::bigint)
            ORDER BY n.sequence, n.id LIMIT 1)
RETURNING *;

-- name: CancelOpenWorkOrders :many
-- CancelOpenWorkOrders cancels every work order of the order not done
-- (M19, M23).
UPDATE manufacturing.work_orders SET status = 'cancelled', revision = revision + 1
WHERE order_id = @order_id AND status NOT IN ('done', 'cancelled')
RETURNING *;

-- name: SetOrderInProgress :exec
-- SetOrderInProgress moves a confirmed order to in progress: its first
-- start (M19).
UPDATE manufacturing.orders SET status = 'in_progress', revision = revision + 1, updated_at = @now
WHERE id = @id AND status = 'confirmed';

-- name: CountOpenWorkOrdersAtWorkCenter :one
-- CountOpenWorkOrdersAtWorkCenter is how many work orders not done or
-- cancelled name the work center (work_center_has_open_work, M8).
SELECT count(*)::int FROM manufacturing.work_orders
WHERE work_center_id = @work_center_id AND status IN ('pending', 'ready', 'in_progress', 'paused');

-- name: GetWorkCenterForShare :one
-- GetWorkCenterForShare is step 5 of the lock order for a start (M26a): a
-- block, which takes the row FOR UPDATE, waits for the start or the start
-- for the block.
SELECT * FROM manufacturing.work_centers WHERE id = @id FOR SHARE;

-- name: SetWorkingState :one
UPDATE manufacturing.work_centers SET working_state = @working_state, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: InsertTimeLog :one
INSERT INTO manufacturing.time_logs (
    work_order_id, work_center_id, user_id, user_display, kind, reason, note, started_at, ended_at, quantity, forced,
    corrected_by, corrected_by_display, corrected_at)
VALUES (@work_order_id, @work_center_id, @user_id, @user_display, @kind, @reason, @note, @started_at, @ended_at, @quantity, @forced,
        @corrected_by, @corrected_by_display, @corrected_at)
RETURNING *;

-- name: GetTimeLog :one
SELECT * FROM manufacturing.time_logs WHERE id = @id;

-- name: LockTimeLogs :many
-- LockTimeLogs is step 6 of the lock order (M26a): the time logs, in id
-- order, after the work orders and the work center.
SELECT * FROM manufacturing.time_logs WHERE id = ANY(@ids::bigint[]) ORDER BY id FOR UPDATE;

-- name: OpenLogOfUser :one
-- OpenLogOfUser is the user's one open log, wherever it is (M29).
SELECT * FROM manufacturing.time_logs WHERE user_id = @user_id AND ended_at IS NULL;

-- name: OpenLogsOfWorkOrders :many
SELECT * FROM manufacturing.time_logs
WHERE work_order_id = ANY(@work_order_ids::bigint[]) AND ended_at IS NULL
ORDER BY id;

-- name: OpenLogsAtWorkCenter :many
SELECT * FROM manufacturing.time_logs WHERE work_center_id = @work_center_id AND ended_at IS NULL ORDER BY id;

-- name: OpenLogsOfOrder :many
SELECT l.* FROM manufacturing.time_logs l JOIN manufacturing.work_orders w ON w.id = l.work_order_id
WHERE w.order_id = @order_id AND l.ended_at IS NULL
ORDER BY l.id;

-- name: EndTimeLog :exec
UPDATE manufacturing.time_logs SET ended_at = @now::timestamptz, quantity = COALESCE(sqlc.narg(quantity), quantity)
WHERE id = @id AND ended_at IS NULL;

-- name: LogsOfWorkOrders :many
SELECT * FROM manufacturing.time_logs WHERE work_order_id = ANY(@work_order_ids::bigint[]) ORDER BY started_at, id;

-- name: CorrectTimeLog :one
-- CorrectTimeLog is a supervisor's correction (M29): the times, the
-- quantity and the note, with who and when.
UPDATE manufacturing.time_logs
SET started_at = @started_at, ended_at = @ended_at, quantity = @quantity, note = @note,
    corrected_by = @corrected_by, corrected_by_display = @corrected_by_display, corrected_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: UserLogOverlaps :one
-- UserLogOverlaps reports whether the user has another log, open or closed,
-- overlapping started_at … ended_at (log_overlaps, M29a).
SELECT EXISTS (
    SELECT 1 FROM manufacturing.time_logs
    WHERE user_id = @user_id AND id <> @id
      AND started_at < @ended_at::timestamptz
      AND COALESCE(ended_at, 'infinity'::timestamptz) > @started_at::timestamptz);

-- name: WorkOrdersTouchedBy :many
-- WorkOrdersTouchedBy is the work orders the user logged time on between
-- from and to, or still runs: "my work" (M31), the latest first.
SELECT work_order_id::bigint AS work_order_id, bool_or(ended_at IS NULL)::boolean AS running,
       max(COALESCE(ended_at, started_at))::timestamptz AS last_at
FROM manufacturing.time_logs
WHERE user_id = @user_id AND work_order_id IS NOT NULL
  AND started_at < @to_at::timestamptz
  AND (ended_at IS NULL OR ended_at >= @from_at::timestamptz)
GROUP BY work_order_id
ORDER BY running DESC, last_at DESC, work_order_id DESC
LIMIT 100;
