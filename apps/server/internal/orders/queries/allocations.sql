-- name: DueOrderLines :many
-- DueOrderLines is, for contracts.BillableOrders.DueOrders (O37), every
-- product line of a draft or confirmed order with a quantity to invoice, its
-- order's header beside it, in order and position order. To invoice is, for
-- a line invoiced on order, ordered − cancelled − invoiced − what draft
-- allocations hold (O36); for a line invoiced on delivery, delivered −
-- invoiced − what draft allocations hold (O48). The orders are
-- the customers named, the orders named, or — with every — all; an order
-- whose delivery date falls after until is not yet due (a NULL until is no
-- bound, and an order with no delivery date is always due). At most
-- row_limit rows: the caller asks one more than it answers, to know there
-- was more. A kit's child line is not invoiced on its own (manufacturing
-- phase 2 M145): its kit line carries the price.
SELECT o.id AS order_id, o.number AS order_number, o.customer_id, o.currency::text AS currency, o.project_id,
       o.your_reference, o.delivery_date, o.delivery_address_id, o.delivery_name, o.delivery_address_line1,
       o.delivery_address_line2, o.delivery_postal_code, o.delivery_city, o.delivery_country,
       o.payment_terms_days, o.status AS order_status, o.on_hold,
       l.id AS line_id, l.position, l.description, l.unit, l.unit_price, l.discount_percent,
       l.vat_code_id, l.invoice_policy,
       ((CASE WHEN l.invoice_policy = 'delivery' THEN l.qty_delivered ELSE l.quantity - l.qty_cancelled END)
        - l.qty_invoiced - coalesce(a.allocated, 0))::numeric(12,3) AS to_invoice
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
  AND l.kind IN ('product', 'shipping') AND l.invoice_policy IN ('order', 'delivery')
  AND l.quantity IS NOT NULL AND l.unit_price IS NOT NULL AND l.vat_code_id IS NOT NULL
  AND l.parent_line_id IS NULL
  AND (CASE WHEN l.invoice_policy = 'delivery' THEN l.qty_delivered ELSE l.quantity - l.qty_cancelled END)
      - l.qty_invoiced - coalesce(a.allocated, 0) > 0
ORDER BY o.id, l.position
LIMIT sqlc.arg(row_limit);

-- name: LineOrders :many
-- LineOrders is, for Allocate, the order each named order line belongs to,
-- read before the locks: a line never moves to another order, so the order
-- ids it answers are the ones to lock. A line that is not an order's is
-- absent.
SELECT id, owner_id AS order_id FROM orders.lines
WHERE id = ANY(sqlc.arg(ids)::bigint[]) AND owner_kind = 'order';

-- name: LockOrdersByIDs :many
-- LockOrdersByIDs takes orders FOR UPDATE in id order: the first of Orders'
-- invoicing locks (O36, O38), before their lines and their allocations.
SELECT * FROM orders.orders WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY id FOR UPDATE;

-- name: LockOrderLinesByIDs :many
-- LockOrderLinesByIDs takes order lines FOR UPDATE in id order, after their
-- orders.
SELECT * FROM orders.lines
WHERE id = ANY(sqlc.arg(ids)::bigint[]) AND owner_kind = 'order'
ORDER BY id FOR UPDATE;

-- name: DraftAllocatedByLine :many
-- DraftAllocatedByLine is what the draft allocations of each named line
-- hold, summed; a line with none is absent.
SELECT line_id, sum(quantity)::numeric(12,3) AS allocated
FROM orders.invoice_allocations
WHERE line_id = ANY(sqlc.arg(ids)::bigint[]) AND state = 'draft'
GROUP BY line_id;

-- name: InsertAllocation :one
-- InsertAllocation writes one new draft allocation of an order line at
-- revision 1 (O36): Allocate never rewrites one.
INSERT INTO orders.invoice_allocations (
    line_id, order_id, kind, quantity, unit_price, discount_percent, amount, vat_code_id, state, revision,
    created_at, updated_at
) VALUES (
    sqlc.arg(line_id), sqlc.arg(order_id), 'order_line', sqlc.arg(quantity), sqlc.arg(unit_price),
    sqlc.arg(discount_percent), sqlc.arg(amount), sqlc.arg(vat_code_id), 'draft', 1, sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: AllocationsByIDs :many
-- AllocationsByIDs is the allocations named as they stand, each with its
-- line's text and variant and its order's — or subscription's — references, in id
-- order; an id that does not exist is absent.
SELECT a.id, a.revision, a.kind, coalesce(a.order_id, a.subscription_id)::bigint AS order_id,
       coalesce(o.number, s.number)::bigint AS order_number, a.line_id, a.quantity, a.unit_price,
       a.discount_percent, a.amount, a.vat_code_id, l.description, l.unit, l.variant_id,
       coalesce(o.currency, s.currency)::text AS currency, coalesce(o.project_id, s.project_id) AS project_id,
       coalesce(o.your_reference, s.your_reference)::text AS your_reference, a.state, a.period_start, a.period_end
FROM orders.invoice_allocations a
LEFT JOIN orders.orders o ON o.id = a.order_id
LEFT JOIN orders.subscriptions s ON s.id = a.subscription_id
JOIN orders.lines l ON l.id = a.line_id
WHERE a.id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY a.id;

-- name: ReleaseDraftAllocations :many
-- ReleaseDraftAllocations turns the named allocations still draft released
-- (O36); one invoiced or released already is left as it is. The caller
-- holds their orders, lines and the allocations themselves, in that order
-- (LockAllocationsByIDs), so the rows it writes are already its own.
UPDATE orders.invoice_allocations
SET state = 'released', released_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE id = ANY(sqlc.arg(ids)::bigint[]) AND state = 'draft'
RETURNING id;

-- name: LockOrderDraftAllocations :many
-- LockOrderDraftAllocations takes an order's draft allocations FOR UPDATE
-- in id order, after the order and its lines: the order page's Release
-- draft allocations, before it writes them.
SELECT id FROM orders.invoice_allocations
WHERE order_id = sqlc.arg(order_id) AND state = 'draft'
ORDER BY id FOR UPDATE;

-- name: ReleaseOrderDraftAllocations :many
-- ReleaseOrderDraftAllocations turns every draft allocation of an order
-- released: the order page's Release draft allocations (O36). The caller
-- holds the order's row, its lines and those allocations
-- (LockOrderDraftAllocations).
UPDATE orders.invoice_allocations
SET state = 'released', released_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE order_id = sqlc.arg(order_id) AND state = 'draft'
RETURNING id;

-- name: OrderAllocations :many
-- OrderAllocations is every allocation of an order, in id order: the order
-- page's invoices (O41) and its lines' draft allocations, and the edit rules
-- (O26) under the order's lock.
SELECT * FROM orders.invoice_allocations WHERE order_id = sqlc.arg(order_id) ORDER BY id;

-- name: DeleteForgottenLineAllocations :exec
-- DeleteForgottenLineAllocations deletes a removed line's released
-- allocations that no invoice ever stamped — drafts given back — so the line
-- can go; an allocation with an invoice's stamp keeps the line (O26).
DELETE FROM orders.invoice_allocations
WHERE line_id = sqlc.arg(line_id) AND state = 'released' AND invoice_id IS NULL;

-- name: AllocationOrders :many
-- AllocationOrders is, for the holder, the order or subscription and the
-- line of each named allocation, read before the locks: an allocation never
-- moves, so these are the rows to lock.
SELECT id, order_id, subscription_id, line_id FROM orders.invoice_allocations WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: LockAllocationsByIDs :many
-- LockAllocationsByIDs takes allocations FOR UPDATE in id order, after
-- their orders and their lines (O38).
SELECT * FROM orders.invoice_allocations WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY id FOR UPDATE;

-- name: StampAllocationsInvoiced :many
-- StampAllocationsInvoiced stamps the named draft allocations invoiced with
-- the invoice's id, number and date, at the issue's time (O38).
UPDATE orders.invoice_allocations
SET state = 'invoiced', invoice_id = sqlc.arg(invoice_id), invoice_number = sqlc.arg(invoice_number),
    issue_date = sqlc.arg(issue_date), invoiced_at = sqlc.arg(issued_at), invoiced_by = sqlc.narg(invoiced_by),
    invoiced_by_display = sqlc.narg(invoiced_by_display), updated_at = sqlc.arg(issued_at)
WHERE id = ANY(sqlc.arg(ids)::bigint[]) AND state = 'draft'
RETURNING id;

-- name: ReleaseAllocationsInvoiced :many
-- ReleaseAllocationsInvoiced moves the named allocations invoiced by an
-- invoice to released at the credit note's issue, the stamp kept as history
-- (O38).
UPDATE orders.invoice_allocations
SET state = 'released', released_at = sqlc.arg(released_at), updated_at = sqlc.arg(released_at)
WHERE id = ANY(sqlc.arg(ids)::bigint[]) AND state = 'invoiced' AND invoice_id = sqlc.arg(invoice_id)
RETURNING id;
