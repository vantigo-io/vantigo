-- Deliveries (orders design O47): a draft per order, picked and posted or
-- cancelled. Every write takes the order's row first (LockOrder), after the
-- variants' locks, then the delivery's.

-- name: InsertDelivery :one
-- InsertDelivery writes a new draft delivery of an order from a site.
INSERT INTO orders.deliveries (number, order_id, site_id, status, created_by_user_id, created_by_display, created_at, updated_at)
VALUES (sqlc.arg(number), sqlc.arg(order_id), sqlc.arg(site_id), 'draft', sqlc.narg(created_by_user_id),
        sqlc.narg(created_by_display), sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: InsertDeliveryLine :one
INSERT INTO orders.delivery_lines (delivery_id, position, order_line_id, variant_id, from_location_id, from_location_code, quantity)
VALUES (sqlc.arg(delivery_id), sqlc.arg(position), sqlc.arg(order_line_id), sqlc.arg(variant_id),
        sqlc.narg(from_location_id), sqlc.narg(from_location_code), sqlc.arg(quantity))
RETURNING *;

-- name: DeleteDeliveryLines :exec
-- DeleteDeliveryLines removes a draft's lines: a save writes them again.
DELETE FROM orders.delivery_lines WHERE delivery_id = sqlc.arg(delivery_id);

-- name: GetDelivery :one
-- GetDelivery reads one delivery. No row is pgx.ErrNoRows.
SELECT * FROM orders.deliveries WHERE id = sqlc.arg(id);

-- name: LockDelivery :one
-- LockDelivery reads a delivery under its row lock, after its order's.
SELECT * FROM orders.deliveries WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: DeliveryLinesOf :many
-- DeliveryLinesOf is a delivery's lines in position order.
SELECT * FROM orders.delivery_lines WHERE delivery_id = sqlc.arg(delivery_id) ORDER BY position;

-- name: DeliveriesOfOrder :many
-- DeliveriesOfOrder is an order's deliveries, newest first.
SELECT * FROM orders.deliveries WHERE order_id = sqlc.arg(order_id) ORDER BY id DESC;

-- name: DraftDeliveredOfOrder :many
-- DraftDeliveredOfOrder is, per order line, what the order's draft deliveries
-- other than except_id hold of it: a line's open quantity for a new delivery
-- is ordered less cancelled less delivered less this (O47).
SELECT dl.order_line_id, sum(dl.quantity)::numeric(12,3) AS quantity
FROM orders.delivery_lines dl
JOIN orders.deliveries d ON d.id = dl.delivery_id
WHERE d.order_id = sqlc.arg(order_id) AND d.status = 'draft' AND d.id <> sqlc.arg(except_id)
GROUP BY dl.order_line_id;

-- name: SetDeliveryTouched :one
-- SetDeliveryTouched bumps a draft's version after its lines were written.
UPDATE orders.deliveries SET version = version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: PostDeliveryRow :one
-- PostDeliveryRow posts a draft (O47): the movement Inventory posted, who and
-- when.
UPDATE orders.deliveries
SET status = 'posted', movement_id = sqlc.arg(movement_id), posted_at = sqlc.arg(now),
    posted_by_user_id = sqlc.narg(by_user_id), posted_by_display = sqlc.narg(by_display),
    version = version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND status = 'draft'
RETURNING *;

-- name: CancelDeliveryRow :one
UPDATE orders.deliveries
SET status = 'cancelled', cancelled_at = sqlc.arg(now), version = version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND status = 'draft'
RETURNING *;

-- name: CancelDraftDeliveriesOfOrder :many
-- CancelDraftDeliveriesOfOrder cancels every draft delivery of an order: the
-- order's cancel leaves nothing for them to deliver.
UPDATE orders.deliveries
SET status = 'cancelled', cancelled_at = sqlc.arg(now), version = version + 1, updated_at = sqlc.arg(now)
WHERE order_id = sqlc.arg(order_id) AND status = 'draft'
RETURNING id;

-- name: SetDeliveryPackingSlip :exec
-- SetDeliveryPackingSlip links a posted delivery's stored packing slip.
UPDATE orders.deliveries SET packing_slip_document_id = sqlc.arg(document_id)
WHERE id = sqlc.arg(id);

-- name: OrderHasDeliveries :one
-- OrderHasDeliveries reports whether an order has a draft or posted
-- delivery: its site no longer changes (order_has_deliveries).
SELECT EXISTS (SELECT 1 FROM orders.deliveries WHERE order_id = sqlc.arg(order_id) AND status IN ('draft', 'posted'))::boolean;

-- name: LinesInDraftDeliveries :many
-- LinesInDraftDeliveries is the order lines a draft delivery of the order
-- names: such a line is not removed from the order.
SELECT DISTINCT dl.order_line_id
FROM orders.delivery_lines dl
JOIN orders.deliveries d ON d.id = dl.delivery_id
WHERE d.order_id = sqlc.arg(order_id) AND d.status = 'draft';

-- name: ListDeliveries :many
-- ListDeliveries is one page of deliveries, newest first, with their
-- order's number and customer.
SELECT sqlc.embed(d), o.number AS order_number, o.customer_id, o.customer_name,
       (SELECT count(*) FROM orders.delivery_lines dl WHERE dl.delivery_id = d.id)::integer AS lines
FROM orders.deliveries d
JOIN orders.orders o ON o.id = d.order_id
WHERE (cardinality(sqlc.arg(statuses)::text[]) = 0 OR d.status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.narg(order_id)::bigint IS NULL OR d.order_id = sqlc.narg(order_id))
  AND (sqlc.narg(customer_id)::integer IS NULL OR o.customer_id = sqlc.narg(customer_id))
  AND (sqlc.narg(site_id)::bigint IS NULL OR d.site_id = sqlc.narg(site_id))
  AND (sqlc.arg(q)::text = '' OR d.number::text = sqlc.arg(q)::text OR o.number::text = sqlc.arg(q)::text
       OR o.customer_name ILIKE '%' || sqlc.arg(q)::text || '%')
ORDER BY d.id DESC
LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: CountDeliveries :one
SELECT count(*)::integer
FROM orders.deliveries d
JOIN orders.orders o ON o.id = d.order_id
WHERE (cardinality(sqlc.arg(statuses)::text[]) = 0 OR d.status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.narg(order_id)::bigint IS NULL OR d.order_id = sqlc.narg(order_id))
  AND (sqlc.narg(customer_id)::integer IS NULL OR o.customer_id = sqlc.narg(customer_id))
  AND (sqlc.narg(site_id)::bigint IS NULL OR d.site_id = sqlc.narg(site_id))
  AND (sqlc.arg(q)::text = '' OR d.number::text = sqlc.arg(q)::text OR o.number::text = sqlc.arg(q)::text
       OR o.customer_name ILIKE '%' || sqlc.arg(q)::text || '%');

-- name: DeliveriesOfOrders :many
-- DeliveriesOfOrders is every delivery of the orders, for an export.
SELECT * FROM orders.deliveries WHERE order_id = ANY(sqlc.arg(order_ids)::bigint[]) ORDER BY order_id, id;

-- name: DeliveryLinesOfDeliveries :many
SELECT * FROM orders.delivery_lines WHERE delivery_id = ANY(sqlc.arg(delivery_ids)::bigint[]) ORDER BY delivery_id, position;

-- name: DeleteDraftDeliveryLinesOf :many
-- DeleteDraftDeliveryLinesOf removes the lines of the order's draft
-- deliveries that name the order lines: a cancel of what remains leaves
-- nothing of them to deliver. It answers the drafts that lost a line.
DELETE FROM orders.delivery_lines dl
USING orders.deliveries d
WHERE d.id = dl.delivery_id AND d.order_id = sqlc.arg(order_id) AND d.status = 'draft'
  AND dl.order_line_id = ANY(sqlc.arg(order_line_ids)::bigint[])
RETURNING dl.delivery_id;

-- name: CancelEmptyDraftDeliveries :exec
-- CancelEmptyDraftDeliveries cancels the drafts among ids left without a
-- line, and bumps the others' version: their lines changed.
UPDATE orders.deliveries d
SET status = CASE WHEN EXISTS (SELECT 1 FROM orders.delivery_lines dl WHERE dl.delivery_id = d.id) THEN 'draft' ELSE 'cancelled' END,
    cancelled_at = CASE WHEN EXISTS (SELECT 1 FROM orders.delivery_lines dl WHERE dl.delivery_id = d.id) THEN NULL ELSE sqlc.arg(now)::timestamptz END,
    version = version + 1, updated_at = sqlc.arg(now)
WHERE d.id = ANY(sqlc.arg(ids)::bigint[]) AND d.status = 'draft';
