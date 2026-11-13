-- The analysis views (O35), read from Orders' own tables: the module sums
-- them in Go, exactly, from these rows.

-- name: QuotesSentBetween :many
-- QuotesSentBetween is every quote last sent in [from, to), of one
-- salesperson when one is named, for the hit rate.
SELECT id, status, currency::text AS currency, net_total, salesperson_id, salesperson_display
FROM orders.quotes
WHERE sent_at >= sqlc.arg(from_at) AND sent_at < sqlc.arg(to_at)
  AND (sqlc.narg(salesperson_id)::uuid IS NULL OR salesperson_id = sqlc.narg(salesperson_id)::uuid)
ORDER BY id;

-- name: OrdersConfirmedBetween :many
-- OrdersConfirmedBetween is every order confirmed in [from, to) and not
-- cancelled since, for the intake.
SELECT id, confirmed_at, currency::text AS currency, net_total, salesperson_id, salesperson_display,
       customer_id, customer_name
FROM orders.orders
WHERE status = 'confirmed' AND confirmed_at >= sqlc.arg(from_at) AND confirmed_at < sqlc.arg(to_at)
ORDER BY id;

-- name: IntakeLines :many
-- IntakeLines are the product lines of the given orders, for the intake by
-- product and by category.
SELECT l.owner_id, l.variant_id, l.product_id, l.name, l.sku, l.line_net
FROM orders.lines l
WHERE l.owner_kind = 'order' AND l.owner_id = ANY(sqlc.arg(order_ids)::bigint[]) AND l.kind = 'product'
ORDER BY l.owner_id, l.position;

-- name: BacklogLines :many
-- BacklogLines are the product lines of every confirmed order with something
-- not delivered or not invoiced, each with the latest time a delivery of it
-- was marked (the change log's delivered_marked actions) — the order's
-- confirmation when none was. A kit's child line is invoiced as its kit
-- (manufacturing phase 2 M145): it is here only while it is not delivered.
SELECT l.id, l.owner_id, o.currency::text AS currency, o.confirmed_at, l.quantity, l.qty_cancelled,
       l.qty_delivered, l.qty_invoiced, l.line_net, (l.parent_line_id IS NOT NULL)::boolean AS kit_child,
       coalesce((SELECT max(c.at) FROM orders.order_changes c, jsonb_array_elements(c.actions) a
                 WHERE c.owner_kind = 'order' AND c.owner_id = o.id AND a->>'kind' = 'delivered_marked'
                   AND a->>'lineId' = l.id::text), o.confirmed_at, o.created_at)::timestamptz AS delivered_since
FROM orders.lines l
JOIN orders.orders o ON o.id = l.owner_id
WHERE l.owner_kind = 'order' AND l.kind = 'product' AND o.status = 'confirmed' AND l.quantity > 0
  AND (l.quantity > l.qty_cancelled + l.qty_delivered OR (l.parent_line_id IS NULL AND l.qty_delivered > l.qty_invoiced))
ORDER BY l.owner_id, l.position;
