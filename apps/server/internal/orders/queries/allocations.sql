-- name: DueOrderLines :many
-- DueOrderLines is, for contracts.BillableOrders.DueOrders (O37), every
-- product line of a draft or confirmed order with a quantity to invoice, its
-- order's header beside it, in order and position order. To invoice is, for
-- a line invoiced on order, ordered − cancelled − invoiced − what draft
-- allocations hold (O36); a line invoiced on delivery is C's. The orders are
-- the customers named, the orders named, or — with every — all; an order
-- whose delivery date falls after until is not yet due (a NULL until is no
-- bound, and an order with no delivery date is always due). At most
-- row_limit rows: the caller asks one more than it answers, to know there
-- was more.
SELECT o.id AS order_id, o.number AS order_number, o.customer_id, o.currency::text AS currency, o.project_id,
       o.your_reference, o.delivery_date, o.delivery_address_id, o.delivery_name, o.delivery_address_line1,
       o.delivery_address_line2, o.delivery_postal_code, o.delivery_city, o.delivery_country,
       o.payment_terms_days, o.status AS order_status, o.on_hold,
       l.id AS line_id, l.position, l.description, l.unit, l.unit_price, l.discount_percent,
       l.vat_code_id, l.invoice_policy,
       (l.quantity - l.qty_cancelled - l.qty_invoiced - coalesce(a.allocated, 0))::numeric(12,3) AS to_invoice
FROM orders.orders o
JOIN orders.lines l ON l.owner_kind = 'order' AND l.owner_id = o.id
LEFT JOIN (
    SELECT line_id, sum(quantity) AS allocated
    FROM orders.invoice_allocations
    WHERE state = 'draft'
    GROUP BY line_id
) a ON a.line_id = l.id
WHERE o.status IN ('draft', 'confirmed')
  AND (sqlc.arg(every)::boolean
       OR o.customer_id = ANY(sqlc.arg(customer_ids)::integer[])
       OR o.id = ANY(sqlc.arg(order_ids)::bigint[]))
  AND (sqlc.narg(until)::date IS NULL OR o.delivery_date IS NULL OR o.delivery_date <= sqlc.narg(until)::date)
  AND l.kind = 'product' AND l.invoice_policy = 'order'
  AND l.quantity IS NOT NULL AND l.unit_price IS NOT NULL AND l.vat_code_id IS NOT NULL
  AND l.quantity - l.qty_cancelled - l.qty_invoiced - coalesce(a.allocated, 0) > 0
ORDER BY o.id, l.position
LIMIT sqlc.arg(row_limit);
