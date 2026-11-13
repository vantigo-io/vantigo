-- Work orders and time logs (manufacturing design M27–M30, M29a): the work
-- orders snapshotted at confirm, their states, and the time logged on the
-- shop floor. Every write runs under the lock order M26a names: the orders,
-- then their work orders in id order (LockWorkOrders), then the work center
-- (GetWorkCenterForShare for a start, GetWorkCenterForUpdate for a block),
-- then the time logs in id order (LockTimeLogs).

-- name: InsertWorkOrder :one
INSERT INTO manufacturing.work_orders (
    order_id, operation_id, sequence, name, work_center_id, work_center_group_id, setup_minutes, cleanup_minutes, minutes_per_cycle,
    capacity, time_efficiency, machine_rate, labour_rate, instructions, expected_minutes, status,
    send_ahead_quantity, bom_output_quantity, own_setup)
VALUES (@order_id, @operation_id, @sequence, @name, @work_center_id, sqlc.narg(work_center_group_id), @setup_minutes, @cleanup_minutes, @minutes_per_cycle,
        @capacity, @time_efficiency, @machine_rate, @labour_rate, @instructions, @expected_minutes, @status,
        sqlc.narg(send_ahead_quantity), @bom_output_quantity, @own_setup)
RETURNING *;

-- name: InsertWorkOrderDependency :exec
-- InsertWorkOrderDependency is one edge of the order's graph, copied from
-- its operations' at confirm (manufacturing phase 2 M113).
INSERT INTO manufacturing.work_order_dependencies (work_order_id, depends_on_id) VALUES (@work_order_id, @depends_on_id);

-- name: DependenciesOfOrders :many
-- DependenciesOfOrders is the work orders' graph of the orders (M113): each
-- edge, the work order and the one it depends on.
SELECT d.work_order_id, d.depends_on_id FROM manufacturing.work_order_dependencies d
JOIN manufacturing.work_orders w ON w.id = d.work_order_id
WHERE w.order_id = ANY(@order_ids::bigint[])
ORDER BY d.work_order_id, d.depends_on_id;

-- name: SetWorkOrderMember :one
-- SetWorkOrderMember moves a group work order to another member of its group
-- (M118, M119): the member's figures are taken again and the expected
-- minutes recomputed with them — never once a time log exists, which the
-- caller has judged.
UPDATE manufacturing.work_orders
SET work_center_id = @work_center_id, setup_minutes = @setup_minutes, cleanup_minutes = @cleanup_minutes,
    capacity = @capacity, time_efficiency = @time_efficiency, machine_rate = sqlc.narg(machine_rate),
    labour_rate = sqlc.narg(labour_rate), expected_minutes = @expected_minutes, revision = revision + 1
WHERE id = @id
RETURNING *;

-- name: WorkOrdersWithLogs :many
-- WorkOrdersWithLogs is which of the work orders have a time log: a group
-- work order's member is fixed once one does (M119).
SELECT DISTINCT work_order_id::bigint AS id FROM manufacturing.time_logs
WHERE work_order_id = ANY(@ids::bigint[])
ORDER BY id;

-- name: GroupMembers :many
-- GroupMembers is the members of the groups, by group, with the center.
SELECT m.group_id, sqlc.embed(c) FROM manufacturing.work_center_group_members m
JOIN manufacturing.work_centers c ON c.id = m.work_center_id
WHERE m.group_id = ANY(@group_ids::bigint[])
ORDER BY m.group_id, c.code;

-- name: QuantityRegistration :one
-- QuantityRegistration is the registration a clientRequestId made, for its
-- replay.
SELECT * FROM manufacturing.work_order_quantities WHERE client_request_id = @client_request_id;

-- name: InsertQuantityRegistration :one
INSERT INTO manufacturing.work_order_quantities (work_order_id, client_request_id, quantity, user_id, user_display, registered_at)
VALUES (@work_order_id, @client_request_id, @quantity, @user_id, @user_display, @now::timestamptz)
RETURNING *;

-- name: AddQuantityDone :one
-- AddQuantityDone raises the good units the work order has passed on (M116).
UPDATE manufacturing.work_orders SET quantity_done = quantity_done + @quantity, revision = revision + 1
WHERE id = @id
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
-- priority, then its due date. A group work order whose member is not fixed
-- yet — unplanned with no log, of a group that is active (manufacturing
-- phase 2 M119) — is not here but under Group work (GroupWorkQueue); one of
-- an archived group keeps its member and is here.
SELECT w.id FROM manufacturing.work_orders w JOIN manufacturing.orders o ON o.id = w.order_id
WHERE w.work_center_id = @work_center_id
  AND w.status IN ('ready', 'in_progress', 'paused')
  AND o.status IN ('confirmed', 'in_progress')
  AND (w.work_center_group_id IS NULL OR w.planned_start IS NOT NULL
       OR EXISTS (SELECT 1 FROM manufacturing.time_logs l WHERE l.work_order_id = w.id)
       OR EXISTS (SELECT 1 FROM manufacturing.work_center_groups g WHERE g.id = w.work_center_group_id AND g.status <> 'active'))
ORDER BY w.planned_start ASC NULLS LAST, (o.priority = 'urgent') DESC, o.due_date ASC NULLS LAST,
         o.number, o.suffix, w.sequence, w.id
LIMIT 500;

-- name: GroupWorkQueue :many
-- GroupWorkQueue is a work center's Group work (M119): the ready group work
-- orders of open orders whose member is not fixed yet — unplanned, with no
-- log — of every active group the center is an active member of, by the
-- order's priority, then its due date.
SELECT w.id FROM manufacturing.work_orders w JOIN manufacturing.orders o ON o.id = w.order_id
JOIN manufacturing.work_center_group_members m ON m.group_id = w.work_center_group_id AND m.work_center_id = @work_center_id
JOIN manufacturing.work_centers c ON c.id = m.work_center_id AND c.status = 'active'
JOIN manufacturing.work_center_groups g ON g.id = w.work_center_group_id AND g.status = 'active'
WHERE w.status = 'ready'
  AND o.status IN ('confirmed', 'in_progress')
  AND w.planned_start IS NULL
  AND NOT EXISTS (SELECT 1 FROM manufacturing.time_logs l WHERE l.work_order_id = w.id)
ORDER BY (o.priority = 'urgent') DESC, o.due_date ASC NULLS LAST, o.number, o.suffix, w.sequence, w.id
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

-- name: ReadyWorkOrders :many
-- ReadyWorkOrders readies every pending work order of the order whose
-- predecessors allow it (manufacturing phase 2 M114): each predecessor is
-- done or cancelled, or has passed on its send-ahead quantity — one at or
-- above the order's quantity counts as none (M116). Run after every finish
-- and every quantity registered. One started out of turn is left as it is,
-- and the ones after it wait for it.
UPDATE manufacturing.work_orders w SET status = 'ready', revision = w.revision + 1
FROM manufacturing.orders o
WHERE o.id = w.order_id AND w.order_id = @order_id AND w.status = 'pending'
  AND NOT EXISTS (
      SELECT 1 FROM manufacturing.work_order_dependencies d
      JOIN manufacturing.work_orders p ON p.id = d.depends_on_id
      WHERE d.work_order_id = w.id AND p.status NOT IN ('done', 'cancelled')
        AND NOT (p.send_ahead_quantity IS NOT NULL AND p.send_ahead_quantity < o.quantity
                 AND p.quantity_done >= p.send_ahead_quantity))
RETURNING w.*;

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

-- name: LockWorkCentersForShare :many
-- LockWorkCentersForShare is step 5 of the lock order for a confirm's
-- snapshot (M26a): its work centers FOR SHARE in id order, after the work
-- orders are written, so an archive — which takes the row FOR UPDATE and
-- counts the open work orders — waits for the confirm and sees them.
SELECT id FROM manufacturing.work_centers WHERE id = ANY(@ids::bigint[]) ORDER BY id FOR SHARE;

-- name: SetWorkingState :one
UPDATE manufacturing.work_centers SET working_state = @working_state, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: InsertTimeLog :one
INSERT INTO manufacturing.time_logs (
    work_order_id, work_center_id, user_id, user_display, kind, reason, note, note_by, note_by_display, started_at, ended_at,
    quantity, forced, corrected_by, corrected_by_display, corrected_at)
VALUES (@work_order_id, @work_center_id, @user_id, @user_display, @kind, @reason, @note, @note_by, @note_by_display, @started_at, @ended_at,
        @quantity, @forced, @corrected_by, @corrected_by_display, @corrected_at)
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
-- quantity and the note, with who and when; the note is now the
-- supervisor's words (note_by).
UPDATE manufacturing.time_logs
SET started_at = @started_at, ended_at = @ended_at, quantity = @quantity, note = @note,
    note_by = @corrected_by, note_by_display = @corrected_by_display,
    corrected_by = @corrected_by, corrected_by_display = @corrected_by_display, corrected_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: LockUserLogs :exec
-- LockUserLogs serialises the corrections and additions of one user's time
-- logs (M29a): a transaction-scoped advisory lock of class 19784 (the class
-- after the schedule's 19783) and the hash of the user's id, taken last,
-- after the time logs, before UserLogOverlaps — so two supervisors adding
-- overlapping logs for the same person at once on different orders do not
-- both pass the check.
SELECT pg_advisory_xact_lock(19784, hashtext(sqlc.arg(user_id)::uuid::text));

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
