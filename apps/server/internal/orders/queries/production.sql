-- Orders' side of make-to-order (manufacturing design M43, M48, M48a): the
-- sales order lines Manufacturing reads through contracts.SalesOrderLines,
-- and the produced-ready-to-reserve mark the orders.mto-reserve handler
-- writes.

-- name: SalesOrderLinesByIDs :many
-- SalesOrderLinesByIDs is the product lines naming a variant among ids that
-- are a sales order's, with their open quantity (ordered less cancelled less
-- delivered, at least 0) and the order's number, customer, delivery date,
-- site and status.
SELECT l.id, l.owner_id AS order_id, o.number, o.customer_id, l.variant_id::int AS variant_id,
       GREATEST(l.quantity - l.qty_cancelled - l.qty_delivered, 0)::numeric(14, 3) AS open_quantity,
       o.delivery_date, o.site_id, o.status
FROM orders.lines l
JOIN orders.orders o ON o.id = l.owner_id
WHERE l.owner_kind = 'order' AND l.kind = 'product' AND l.variant_id IS NOT NULL AND l.quantity IS NOT NULL
  AND l.id = ANY(@ids::bigint[])
ORDER BY l.owner_id, l.position;

-- name: SalesOrderLinesOfOrder :many
-- SalesOrderLinesOfOrder is SalesOrderLinesByIDs for every such line of one
-- order, in line order.
SELECT l.id, l.owner_id AS order_id, o.number, o.customer_id, l.variant_id::int AS variant_id,
       GREATEST(l.quantity - l.qty_cancelled - l.qty_delivered, 0)::numeric(14, 3) AS open_quantity,
       o.delivery_date, o.site_id, o.status
FROM orders.lines l
JOIN orders.orders o ON o.id = l.owner_id
WHERE l.owner_kind = 'order' AND l.kind = 'product' AND l.variant_id IS NOT NULL AND l.quantity IS NOT NULL
  AND l.owner_id = @order_id
ORDER BY l.position;

-- name: OrderOfLine :one
-- OrderOfLine is the sales order an order line belongs to.
SELECT owner_id FROM orders.lines WHERE id = @id AND owner_kind = 'order';

-- name: MarkLineReady :exec
-- MarkLineReady marks a sales order line produced, ready to reserve (M48a),
-- by one manufacturing order closed for it: once per order, however often
-- its event is handled.
INSERT INTO orders.line_production_ready (line_id, order_id, manufacturing_order_id, site_id, produced, marked_at)
VALUES (@line_id, @order_id, @manufacturing_order_id, @site_id, @produced, @now)
ON CONFLICT (line_id, manufacturing_order_id) DO NOTHING;

-- name: ReadyLinesOfOrder :many
-- ReadyLinesOfOrder is the marks of one sales order's lines, one per line:
-- the latest manufacturing order closed for it and its site, and what every
-- order closed for it produced in all.
SELECT DISTINCT ON (line_id) line_id, manufacturing_order_id, site_id,
       (SUM(produced) OVER (PARTITION BY line_id))::numeric(14, 3) AS produced
FROM orders.line_production_ready
WHERE order_id = @order_id
ORDER BY line_id, marked_at DESC, manufacturing_order_id DESC;

-- name: OpenGoodsLinesAt :many
-- OpenGoodsLinesAt is the open goods lines of confirmed sales orders
-- fulfilled from the site, of the variants given (manufacturing phase 2
-- M107a): delivered by delivery documents, with something open (ordered less
-- cancelled less delivered), by order then line.
SELECT l.id, l.owner_id AS order_id, l.variant_id::int AS variant_id,
       (l.quantity - l.qty_cancelled - l.qty_delivered)::numeric(14, 3) AS open_quantity
FROM orders.lines l
JOIN orders.orders o ON o.id = l.owner_id
WHERE l.owner_kind = 'order' AND l.kind = 'product' AND l.delivery_method = 'delivery'
  AND l.variant_id = ANY(@variant_ids::int[]) AND l.quantity IS NOT NULL
  AND l.quantity - l.qty_cancelled - l.qty_delivered > 0
  AND o.status = 'confirmed' AND o.site_id = @site_id::bigint
ORDER BY l.owner_id, l.id;
