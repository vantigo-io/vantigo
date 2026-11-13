-- Manufacturing orders (manufacturing design M17–M26): the order, its
-- component lines, its productions and its timeline.

-- name: NextOrderNumber :one
-- NextOrderNumber allocates the next MO-<n> under the counter row's lock
-- (M17).
INSERT INTO manufacturing.counters (counter_name, next_value) VALUES ('orders', 2)
ON CONFLICT (counter_name) DO UPDATE SET next_value = manufacturing.counters.next_value + 1
RETURNING (next_value - 1)::bigint AS number;

-- name: NextBackorderSuffix :one
-- NextBackorderSuffix is the next suffix of the root numbered number: 2 for
-- its first backorder, then 3 (M17b).
SELECT (GREATEST(COALESCE(max(suffix), 0), 1) + 1)::int AS suffix
FROM manufacturing.orders WHERE number = @number;

-- name: InsertOrder :one
INSERT INTO manufacturing.orders (
    number, suffix, variant_id, bom_id, quantity, site_id, component_location_id, output_location_id,
    due_date, planned_start, planned_finish, requested_start, priority, responsible_user_id, note,
    origin_kind, origin_order_id, origin_line_id, origin_reference, origin_cancelled, parent_order_id, backorder_of_id,
    split_from_id, created_at, updated_at)
VALUES (
    @number, @suffix, @variant_id, @bom_id, @quantity, @site_id, @component_location_id, @output_location_id,
    @due_date, @planned_start, @planned_finish, @requested_start, @priority, @responsible_user_id, @note,
    @origin_kind, @origin_order_id, @origin_line_id, @origin_reference, @origin_cancelled, @parent_order_id, @backorder_of_id,
    sqlc.narg(split_from_id), @now, @now)
RETURNING *;

-- name: GetOrder :one
SELECT * FROM manufacturing.orders WHERE id = @id;

-- name: GetOrderForUpdate :one
-- GetOrderForUpdate is step 2 of the lock order (M26a), after LockVariants.
SELECT * FROM manufacturing.orders WHERE id = @id FOR UPDATE;

-- name: OrdersByIDs :many
SELECT * FROM manufacturing.orders WHERE id = ANY(@ids::bigint[]) ORDER BY id;

-- name: UpdateOrder :one
-- UpdateOrder replaces an order's fields. Its planned window is never one of
-- them: only planning sets it (M34).
UPDATE manufacturing.orders SET
    bom_id = @bom_id, quantity = @quantity, site_id = @site_id,
    component_location_id = @component_location_id, output_location_id = @output_location_id,
    due_date = @due_date,
    priority = @priority, responsible_user_id = @responsible_user_id, note = @note,
    requested_start = @requested_start,
    revision = revision + 1, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: SetOrderStatus :one
-- SetOrderStatus moves the order to status, stamping confirmed_at or
-- closed_at when given.
UPDATE manufacturing.orders SET
    status = @status,
    confirmed_at = COALESCE(sqlc.narg(confirmed_at), confirmed_at),
    closed_at = COALESCE(sqlc.narg(closed_at), closed_at),
    revision = revision + 1, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: TouchOrder :one
-- TouchOrder bumps the revision of an order whose lines changed.
UPDATE manufacturing.orders SET revision = revision + 1, updated_at = @now WHERE id = @id RETURNING *;

-- name: AddOrderProduction :one
-- AddOrderProduction adds a production's good output and output scrap and
-- moves a confirmed order to in progress (M22, M22a).
UPDATE manufacturing.orders SET
    produced = produced + @produced, output_scrap = output_scrap + @output_scrap,
    status = CASE WHEN status = 'confirmed' THEN 'in_progress' ELSE status END,
    revision = revision + 1, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: ListOrderComponents :many
SELECT * FROM manufacturing.order_components WHERE order_id = @order_id ORDER BY sort_order, id;

-- name: ComponentsOfOrders :many
SELECT * FROM manufacturing.order_components WHERE order_id = ANY(@order_ids::bigint[]) ORDER BY order_id, sort_order, id;

-- name: ComponentVariantsOfOrder :many
-- ComponentVariantsOfOrder is the order's component variants, for the lock
-- order's re-read (M26a step 3).
SELECT DISTINCT variant_id FROM manufacturing.order_components WHERE order_id = @order_id ORDER BY variant_id;

-- name: InsertOrderComponent :one
INSERT INTO manufacturing.order_components (
    order_id, variant_id, sku, product_name, unit, required, operation_id, standard_cost, sort_order, added, note)
VALUES (@order_id, @variant_id, @sku, @product_name, @unit, @required, @operation_id, @standard_cost, @sort_order, @added, @note)
RETURNING *;

-- name: NextComponentSortOrder :one
SELECT (COALESCE(max(sort_order), -1) + 1)::int FROM manufacturing.order_components WHERE order_id = @order_id;

-- name: SetComponentRequired :exec
UPDATE manufacturing.order_components SET required = @required, note = @note WHERE id = @id;

-- name: SetComponentReserved :exec
-- SetComponentReserved is Manufacturing's copy of the line's reservation
-- (M20a), written under the order's lock.
UPDATE manufacturing.order_components SET reserved = @reserved WHERE id = @id;

-- name: ClearOrderReserved :exec
UPDATE manufacturing.order_components SET reserved = 0 WHERE order_id = @order_id;

-- name: AddComponentUse :exec
UPDATE manufacturing.order_components SET consumed = consumed + @consumed, scrapped = scrapped + @scrapped WHERE id = @id;

-- name: InsertProduction :one
INSERT INTO manufacturing.productions (
    order_id, client_request_id, request_hash, quantity, output_scrap, to_location_id,
    posted_by_user_id, posted_by_display, posted_at)
VALUES (@order_id, @client_request_id, @request_hash, @quantity, @output_scrap, @to_location_id,
        @posted_by_user_id, @posted_by_display, @now)
RETURNING *;

-- name: SetProductionMovement :exec
UPDATE manufacturing.productions SET movement_id = @movement_id WHERE id = @id;

-- name: InsertProductionLine :exec
INSERT INTO manufacturing.production_lines (production_id, component_id, consumed, scrapped, from_location_id)
VALUES (@production_id, @component_id, @consumed, @scrapped, @from_location_id);

-- name: GetProduction :one
SELECT p.*, o.number AS order_number, o.suffix AS order_suffix
FROM manufacturing.productions p JOIN manufacturing.orders o ON o.id = p.order_id
WHERE p.id = @id;

-- name: ProductionByClientRequest :one
SELECT * FROM manufacturing.productions WHERE client_request_id = @client_request_id;

-- name: ListProductionLines :many
SELECT l.*, c.variant_id, c.sku, c.product_name, c.unit
FROM manufacturing.production_lines l JOIN manufacturing.order_components c ON c.id = l.component_id
WHERE l.production_id = @production_id
ORDER BY c.sort_order, c.id;

-- name: SetOrderLotPattern :exec
-- SetOrderLotPattern keeps the BoM's lot pattern on the order at confirm
-- (manufacturing phase 2 M80).
UPDATE manufacturing.orders SET lot_pattern = @lot_pattern WHERE id = @id;

-- name: NextLotCounter :one
-- NextLotCounter allocates the next {N} of a lot pattern's rendered prefix
-- under the counter row's lock (manufacturing phase 2 M80), from 1.
INSERT INTO manufacturing.counters (counter_name, next_value) VALUES (@counter_name, 2)
ON CONFLICT (counter_name) DO UPDATE SET next_value = manufacturing.counters.next_value + 1
RETURNING (next_value - 1)::bigint AS number;

-- name: PeekLotCounter :one
-- PeekLotCounter is the {N} a prefix would hand out next, for the
-- production draft's preview; 1 when none was handed out.
SELECT COALESCE((SELECT next_value FROM manufacturing.counters WHERE counter_name = @counter_name), 1)::bigint AS number;

-- name: InsertProductionLineLot :exec
INSERT INTO manufacturing.production_line_lots (production_id, component_id, lot_id, location_id, lot_number, expiry, consumed, scrapped, allow_expired)
VALUES (@production_id, @component_id, @lot_id, @location_id, @lot_number, @expiry, @consumed, @scrapped, @allow_expired)
ON CONFLICT (production_id, component_id, lot_id, location_id) DO UPDATE
SET consumed = manufacturing.production_line_lots.consumed + EXCLUDED.consumed,
    scrapped = manufacturing.production_line_lots.scrapped + EXCLUDED.scrapped,
    allow_expired = manufacturing.production_line_lots.allow_expired OR EXCLUDED.allow_expired;

-- name: InsertProductionOutput :exec
INSERT INTO manufacturing.production_outputs (production_id, lot_id, lot_number, serial, quantity)
VALUES (@production_id, @lot_id, @lot_number, @serial, @quantity)
ON CONFLICT (production_id, lot_id) DO UPDATE SET quantity = manufacturing.production_outputs.quantity + EXCLUDED.quantity;

-- name: ProductionLineLots :many
-- ProductionLineLots is the lots productions consumed, by component line's
-- order then the lot's number.
SELECT l.*
FROM manufacturing.production_line_lots l JOIN manufacturing.order_components c ON c.id = l.component_id
WHERE l.production_id = ANY(@production_ids::bigint[])
ORDER BY l.production_id, c.sort_order, c.id, l.lot_number, l.location_id;

-- name: ProductionOutputs :many
-- ProductionOutputs is the lots productions made, by production then number.
SELECT * FROM manufacturing.production_outputs
WHERE production_id = ANY(@production_ids::bigint[])
ORDER BY production_id, lot_number;

-- name: ProductionIDsOfOrder :many
SELECT id FROM manufacturing.productions WHERE order_id = @order_id ORDER BY id;

-- name: SerialOutputNumbersOfOrder :many
-- SerialOutputNumbersOfOrder is the numbers of the serials the order's
-- productions made so far: the default serials of the next continue after
-- the highest of them (M81).
SELECT po.lot_number FROM manufacturing.production_outputs po
JOIN manufacturing.productions p ON p.id = po.production_id
WHERE p.order_id = @order_id AND po.serial;

-- name: CountProductionsOfOrder :one
SELECT count(*)::int FROM manufacturing.productions WHERE order_id = @order_id;

-- name: InsertOrderEvent :exec
INSERT INTO manufacturing.order_events (order_id, kind, payload, actor_user_id, actor_display, at)
VALUES (@order_id, @kind, @payload, @actor_user_id, @actor_display, @now);

-- name: ListOrderEvents :many
SELECT * FROM manufacturing.order_events WHERE order_id = @order_id ORDER BY id;

-- name: OrderLinks :many
-- OrderLinks is the orders made from id: its backorders (backorder_of_id),
-- its children (parent_order_id), the parts split from it (split_from_id,
-- manufacturing phase 2 M124) and the orders merged into it (merged_into_id,
-- M125), by id.
SELECT * FROM manufacturing.orders
WHERE backorder_of_id = @id OR parent_order_id = @id OR split_from_id = @id OR merged_into_id = @id
ORDER BY id;

-- name: ListOrders :many
-- ListOrders is the list (M24, M21a), one page, with the filtered total and
-- each row's reservation status from the stored copies ('' unless the
-- order is confirmed or in progress).
WITH filtered AS (
    SELECT o.*,
           CASE WHEN o.status NOT IN ('confirmed', 'in_progress') THEN ''
                WHEN NOT EXISTS (SELECT 1 FROM manufacturing.order_components c
                                 WHERE c.order_id = o.id AND c.required - c.consumed - c.scrapped > c.reserved) THEN 'reserved'
                WHEN EXISTS (SELECT 1 FROM manufacturing.order_components c WHERE c.order_id = o.id AND c.reserved > 0) THEN 'partly_reserved'
                ELSE 'not_reserved' END::text AS reservation_status
    FROM manufacturing.orders o
    WHERE o.status = ANY(@statuses::text[])
      AND (sqlc.narg(site_id)::bigint IS NULL OR o.site_id = sqlc.narg(site_id))
      AND (sqlc.narg(work_center_id)::bigint IS NULL
           OR (o.status = 'draft' AND EXISTS (
                SELECT 1 FROM manufacturing.bom_operations op WHERE op.bom_id = o.bom_id
                  AND (op.work_center_id = sqlc.narg(work_center_id)
                       OR op.work_center_group_id IN (SELECT m.group_id FROM manufacturing.work_center_group_members m
                                                      WHERE m.work_center_id = sqlc.narg(work_center_id)))))
           OR EXISTS (
                SELECT 1 FROM manufacturing.work_orders wo WHERE wo.order_id = o.id AND wo.work_center_id = sqlc.narg(work_center_id)))
      AND (sqlc.narg(variant_id)::int IS NULL OR o.variant_id = sqlc.narg(variant_id))
      AND (sqlc.narg(due_from)::date IS NULL OR o.due_date >= sqlc.narg(due_from))
      AND (sqlc.narg(due_to)::date IS NULL OR o.due_date <= sqlc.narg(due_to))
      AND (NOT @late_only::boolean OR (o.due_date < @today::date AND o.status IN ('draft', 'confirmed', 'in_progress')))
      AND (sqlc.narg(origin)::text IS NULL OR o.origin_kind = sqlc.narg(origin))
      AND (sqlc.narg(responsible)::uuid IS NULL OR o.responsible_user_id = sqlc.narg(responsible))
      AND (NOT @search::boolean OR o.number = @number_match OR o.variant_id = ANY(@variant_ids::int[]))
)
SELECT f.*, count(*) OVER ()::int AS total
FROM filtered f
WHERE sqlc.narg(reservation)::text IS NULL OR f.reservation_status = sqlc.narg(reservation)
ORDER BY
    CASE WHEN @sort::text = 'priority' THEN (f.priority = 'urgent') END DESC NULLS LAST,
    CASE WHEN @sort::text = 'number' THEN f.number END DESC,
    CASE WHEN @sort::text = 'number' THEN f.suffix END DESC,
    f.due_date ASC NULLS LAST, f.number, f.suffix
LIMIT @page_size OFFSET @page_offset;

-- name: OrderStats :one
-- OrderStats is the overview's order cards (M7): confirmed orders not yet
-- started whose planned start is today or earlier, those in progress, and
-- the open ones due before today.
SELECT (SELECT count(*) FROM manufacturing.orders
        WHERE status = 'confirmed' AND planned_start IS NOT NULL AND planned_start < @tomorrow::timestamptz)::int AS to_start_today,
       (SELECT count(*) FROM manufacturing.orders WHERE status = 'in_progress')::int AS in_progress,
       (SELECT count(*) FROM manufacturing.orders
        WHERE status IN ('draft', 'confirmed', 'in_progress') AND due_date < @today::date)::int AS late;

-- name: CountOpenOrdersNamingVariant :one
-- CountOpenOrdersNamingVariant is how many orders not done or cancelled
-- name the variant as their product or on a component line (M16).
SELECT count(*)::int FROM manufacturing.orders o
WHERE o.status IN ('draft', 'confirmed', 'in_progress')
  AND (o.variant_id = @variant_id
       OR EXISTS (SELECT 1 FROM manufacturing.order_components c WHERE c.order_id = o.id AND c.variant_id = @variant_id)
       OR EXISTS (SELECT 1 FROM manufacturing.order_outputs x WHERE x.order_id = o.id AND x.variant_id = @variant_id));
