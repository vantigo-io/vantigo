-- BoM depth (manufacturing phase 2 M120–M128): a BoM's by-products and an
-- order's, what productions made of them, unbuilds, and the writes a split and
-- a merge make on orders, their lines and their work orders.

-- name: OutputsOfBoms :many
-- OutputsOfBoms is the by-products of the BoMs, by BoM and sort order.
SELECT * FROM manufacturing.bom_outputs
WHERE bom_id = ANY(@bom_ids::bigint[])
ORDER BY bom_id, sort_order, id;

-- name: DeleteBomOutputs :exec
-- DeleteBomOutputs removes every by-product of the BoM: an edit replaces them.
DELETE FROM manufacturing.bom_outputs WHERE bom_id = @bom_id;

-- name: InsertBomOutput :exec
INSERT INTO manufacturing.bom_outputs (bom_id, variant_id, quantity, cost_share, sort_order)
VALUES (@bom_id, @variant_id, @quantity, @cost_share, @sort_order);

-- name: DefaultBomsMakingVariants :many
-- DefaultBomsMakingVariants is, per variant among variant_ids, the default
-- active BoMs that make it as a by-product: the cycle walk's edge from a
-- by-product to its BoM's components (M120).
SELECT x.variant_id AS output_id, b.id AS bom_id
FROM manufacturing.bom_outputs x
JOIN manufacturing.boms b ON b.id = x.bom_id
WHERE x.variant_id = ANY(@variant_ids::int[]) AND b.is_default AND b.status = 'active'
ORDER BY x.variant_id, b.id;

-- name: InsertOrderOutput :one
INSERT INTO manufacturing.order_outputs (order_id, variant_id, sku, product_name, unit, quantity, cost_share, expected, sort_order)
VALUES (@order_id, @variant_id, @sku, @product_name, @unit, @quantity, @cost_share, @expected, @sort_order)
RETURNING *;

-- name: OutputsOfOrders :many
-- OutputsOfOrders is the orders' by-products, by order and sort order.
SELECT * FROM manufacturing.order_outputs
WHERE order_id = ANY(@order_ids::bigint[])
ORDER BY order_id, sort_order, id;

-- name: OutputVariantsOfOrder :many
-- OutputVariantsOfOrder is the order's by-product variants, for the lock
-- order's re-read (M26a step 3).
SELECT DISTINCT variant_id FROM manufacturing.order_outputs WHERE order_id = @order_id ORDER BY variant_id;

-- name: SetOutputExpected :exec
UPDATE manufacturing.order_outputs SET expected = @expected WHERE id = @id;

-- name: AddOutputProduced :exec
UPDATE manufacturing.order_outputs SET produced = produced + @produced WHERE id = @id;

-- name: InsertProductionByproduct :exec
INSERT INTO manufacturing.production_byproducts (production_id, output_id, lot_id, lot_number, quantity)
VALUES (@production_id, @output_id, sqlc.narg(lot_id), sqlc.narg(lot_number), @quantity);

-- name: ProductionByproducts :many
-- ProductionByproducts is what productions made of their orders' by-products,
-- by production and the by-product's sort order.
SELECT p.*, x.variant_id, x.sku, x.product_name, x.unit
FROM manufacturing.production_byproducts p JOIN manufacturing.order_outputs x ON x.id = p.output_id
WHERE p.production_id = ANY(@production_ids::bigint[])
ORDER BY p.production_id, x.sort_order, x.id, p.id;

-- name: CountWorkOfOrder :one
-- CountWorkOfOrder is how many time logs and registered quantities the
-- order's work orders have: a split or a merge refuses an order with one
-- (M124, M125).
SELECT ((SELECT count(*) FROM manufacturing.time_logs l
         JOIN manufacturing.work_orders w ON w.id = l.work_order_id
         WHERE w.order_id = @order_id)
      + (SELECT count(*) FROM manufacturing.work_order_quantities q
         JOIN manufacturing.work_orders w ON w.id = q.work_order_id
         WHERE w.order_id = @order_id))::int AS work;

-- name: SetOrderQuantityAndDue :one
-- SetOrderQuantityAndDue is a split's or a merge's new quantity and due date
-- of the order it keeps (M124, M125).
UPDATE manufacturing.orders SET quantity = @quantity, due_date = @due_date, revision = revision + 1, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: SetOrderMergedInto :one
-- SetOrderMergedInto cancels an order merged into another (M125).
UPDATE manufacturing.orders SET status = 'cancelled', merged_into_id = @merged_into_id, closed_at = @now::timestamptz,
    planned_start = NULL, planned_finish = NULL, revision = revision + 1, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: AddComponentRequired :exec
-- AddComponentRequired raises a merge survivor's line by a merged order's.
UPDATE manufacturing.order_components SET required = required + @required WHERE id = @id;

-- name: SetComponentRequiredOnly :exec
UPDATE manufacturing.order_components SET required = @required WHERE id = @id;

-- name: SetWorkOrderExpected :exec
-- SetWorkOrderExpected recomputes a work order's expected minutes for its
-- order's new quantity (M126): only before any time is logged.
UPDATE manufacturing.work_orders SET expected_minutes = @expected_minutes, revision = revision + 1 WHERE id = @id;

-- name: CopyWorkOrderChecks :exec
-- CopyWorkOrderChecks copies one work order's checks onto a split part's work
-- order (M124), as the confirm snapshotted them.
INSERT INTO manufacturing.work_order_checks (work_order_id, template_id, sequence, kind, title, instructions, nominal, min_value, max_value,
                                             unit, frequency, quarantine_on_fail)
SELECT @to_work_order_id, c.template_id, c.sequence, c.kind, c.title, c.instructions, c.nominal, c.min_value, c.max_value, c.unit,
       c.frequency, c.quarantine_on_fail
FROM manufacturing.work_order_checks c
WHERE c.work_order_id = @from_work_order_id
ORDER BY c.sequence, c.id;

-- name: NextUnbuildNumber :one
-- NextUnbuildNumber allocates the next UB-<n> under the counter row's lock
-- (M123).
INSERT INTO manufacturing.counters (counter_name, next_value) VALUES ('unbuild', 2)
ON CONFLICT (counter_name) DO UPDATE SET next_value = manufacturing.counters.next_value + 1
RETURNING (next_value - 1)::bigint AS number;

-- name: InsertUnbuild :one
INSERT INTO manufacturing.unbuilds (number, order_id, bom_id, variant_id, sku, product_name, unit, quantity, lot_id, lot_number, site_id,
                                    from_location_id, to_location_id, note, posted_by_user_id, posted_by_display, posted_at, client_request_id,
                                    request_hash)
VALUES (@number, sqlc.narg(order_id), @bom_id, @variant_id, @sku, @product_name, @unit, @quantity, sqlc.narg(lot_id), sqlc.narg(lot_number),
        @site_id, @from_location_id, @to_location_id, @note, @posted_by_user_id, @posted_by_display, @now, @client_request_id, @request_hash)
RETURNING *;

-- name: SetUnbuildMovement :exec
-- SetUnbuildMovement records the unbuild's movement, and whether Inventory
-- quarantined what it restored (M123).
UPDATE manufacturing.unbuilds SET movement_id = @movement_id, quarantined = @quarantined WHERE id = @id;

-- name: GetUnbuildForUpdate :one
SELECT * FROM manufacturing.unbuilds WHERE id = @id FOR UPDATE;

-- name: MarkUnbuildReleased :exec
-- MarkUnbuildReleased records the release of the unbuild's quarantine (M123).
UPDATE manufacturing.unbuilds
SET released_at = @now::timestamptz, released_by_user_id = sqlc.narg(released_by_user_id), released_by_display = @released_by_display
WHERE id = @id;

-- name: InsertUnbuildLine :exec
INSERT INTO manufacturing.unbuild_lines (unbuild_id, line_no, variant_id, sku, product_name, unit, quantity, lot_id, lot_number)
VALUES (@unbuild_id, @line_no, @variant_id, @sku, @product_name, @unit, @quantity, sqlc.narg(lot_id), sqlc.narg(lot_number));

-- name: GetUnbuild :one
SELECT sqlc.embed(u), o.number AS order_number, o.suffix AS order_suffix
FROM manufacturing.unbuilds u LEFT JOIN manufacturing.orders o ON o.id = u.order_id
WHERE u.id = @id;

-- name: UnbuildByClientRequest :one
SELECT * FROM manufacturing.unbuilds WHERE client_request_id = @client_request_id;

-- name: UnbuildLines :many
SELECT * FROM manufacturing.unbuild_lines WHERE unbuild_id = ANY(@unbuild_ids::bigint[]) ORDER BY unbuild_id, line_no;

-- name: ListUnbuilds :many
-- ListUnbuilds is a page of unbuilds, of one order when order_id is set,
-- newest first, with the filtered total.
SELECT sqlc.embed(u), o.number AS order_number, o.suffix AS order_suffix, count(*) OVER ()::int AS total
FROM manufacturing.unbuilds u LEFT JOIN manufacturing.orders o ON o.id = u.order_id
WHERE sqlc.narg(order_id)::bigint IS NULL OR u.order_id = sqlc.narg(order_id)
ORDER BY u.posted_at DESC, u.id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: UnbuiltOfOrders :many
-- UnbuiltOfOrders is what was unbuilt from each order, per finished lot (0
-- for an untracked product), for the limit an unbuild judges (M123).
SELECT order_id::bigint AS order_id, COALESCE(lot_id, 0)::bigint AS lot_id, sum(quantity)::numeric(14, 3) AS quantity
FROM manufacturing.unbuilds
WHERE order_id = ANY(@order_ids::bigint[])
GROUP BY order_id, COALESCE(lot_id, 0)
ORDER BY order_id, lot_id;

-- name: OrderProductionsOfLot :many
-- OrderProductionsOfLot is the order's productions that put good output into
-- the lot, each with what it put there and what its consumption made — its
-- good quantity and its output scrap (M123).
SELECT p.id, (p.quantity + p.output_scrap)::numeric(14, 3) AS production_quantity, po.quantity AS lot_quantity
FROM manufacturing.productions p
JOIN manufacturing.production_outputs po ON po.production_id = p.id
WHERE p.order_id = @order_id AND po.lot_id = @lot_id
ORDER BY p.id;

-- name: OrderProductionsGood :many
-- OrderProductionsGood is every production of the order with good output,
-- with what its consumption made: its good quantity and its output scrap.
SELECT p.id, (p.quantity + p.output_scrap)::numeric(14, 3) AS production_quantity, p.quantity AS lot_quantity
FROM manufacturing.productions p
WHERE p.order_id = @order_id AND p.quantity > 0
ORDER BY p.id;

-- name: ProductionConsumption :many
-- ProductionConsumption is what each production consumed per component line
-- (scrap left out): the line's variant, its consumed total, and per lot taken
-- what it consumed of it (lot_id 0 and the line's total when it took no lot).
SELECT l.production_id, c.id AS component_id, c.variant_id, c.sku, c.product_name, c.unit, c.sort_order,
       COALESCE(ll.lot_id, 0)::bigint AS lot_id, COALESCE(ll.lot_number, '')::text AS lot_number,
       COALESCE(ll.consumed, l.consumed)::numeric(14, 3) AS consumed
FROM manufacturing.production_lines l
JOIN manufacturing.order_components c ON c.id = l.component_id
LEFT JOIN manufacturing.production_line_lots ll ON ll.production_id = l.production_id AND ll.component_id = l.component_id
WHERE l.production_id = ANY(@production_ids::bigint[])
ORDER BY c.sort_order, c.id, l.production_id, ll.lot_number, ll.location_id;

-- name: OrderMadeByproducts :one
-- OrderMadeByproducts reports whether the order's productions made any of its
-- by-products: an unbuild from such an order is refused (M123).
SELECT EXISTS (SELECT 1 FROM manufacturing.order_outputs WHERE order_id = @order_id AND produced > 0)::bool AS made;

-- name: CountOpenSplitParts :one
-- CountOpenSplitParts is how many parts split from the order are still open:
-- the original is not cancelled while one is (M124).
SELECT count(*)::int FROM manufacturing.orders
WHERE split_from_id = @order_id::bigint AND status NOT IN ('done', 'cancelled');
