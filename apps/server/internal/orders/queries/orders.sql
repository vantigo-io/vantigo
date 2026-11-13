-- name: LockOrder :one
-- LockOrder reads an order under its row lock: every write of an order takes
-- it, after the variants' locks (MB rule 21), and an event about the order is
-- published only while it is held (O3).
SELECT * FROM orders.orders WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: OrderByClientRequest :one
-- OrderByClientRequest is the order an earlier create with this
-- clientRequestId made (O27). No row is pgx.ErrNoRows.
SELECT * FROM orders.orders WHERE client_request_id = sqlc.arg(client_request_id);

-- name: OrderByExternal :one
-- OrderByExternal is the order a channel already handed in under its own
-- reference (O27). No row is pgx.ErrNoRows.
SELECT * FROM orders.orders WHERE channel = sqlc.arg(channel) AND external_id = sqlc.arg(external_id);

-- name: UpdateOrderHeader :one
-- UpdateOrderHeader replaces an order's header, its snapshot and its totals
-- at version: a draft's save (the snapshot refreshed, O14) and a confirmed
-- order's edit (only what O26 allows changed, the version bumped). The caller
-- holds the row.
UPDATE orders.orders SET
    customer_id = sqlc.arg(customer_id), customer_number = sqlc.narg(customer_number),
    customer_name = sqlc.arg(customer_name), customer_type = sqlc.narg(customer_type),
    customer_organisation_number = sqlc.narg(customer_organisation_number),
    customer_foreign_id = sqlc.narg(customer_foreign_id), customer_country = sqlc.narg(customer_country),
    invoice_address_line1 = sqlc.narg(invoice_address_line1), invoice_address_line2 = sqlc.narg(invoice_address_line2),
    invoice_postal_code = sqlc.narg(invoice_postal_code), invoice_city = sqlc.narg(invoice_city),
    invoice_country = sqlc.narg(invoice_country), delivery_address_id = sqlc.narg(delivery_address_id),
    delivery_name = sqlc.narg(delivery_name), delivery_address_line1 = sqlc.narg(delivery_address_line1),
    delivery_address_line2 = sqlc.narg(delivery_address_line2), delivery_postal_code = sqlc.narg(delivery_postal_code),
    delivery_city = sqlc.narg(delivery_city), delivery_country = sqlc.narg(delivery_country),
    contact_id = sqlc.narg(contact_id), contact_name = sqlc.narg(contact_name),
    contact_email = sqlc.narg(contact_email), contact_phone = sqlc.narg(contact_phone),
    customer_invoice_email = sqlc.narg(customer_invoice_email),
    your_reference = sqlc.arg(your_reference), salesperson_id = sqlc.narg(salesperson_id),
    salesperson_display = sqlc.narg(salesperson_display), project_id = sqlc.narg(project_id),
    currency = sqlc.arg(currency), prices_include_vat = sqlc.arg(prices_include_vat),
    payment_terms_days = sqlc.arg(payment_terms_days), discount_visible = sqlc.arg(discount_visible),
    language = sqlc.arg(language), intro_text = sqlc.arg(intro_text), closing_text = sqlc.arg(closing_text),
    internal_note = sqlc.arg(internal_note), delivery_date = sqlc.narg(delivery_date),
    net_total = sqlc.arg(net_total), vat_total = sqlc.arg(vat_total), gross_total = sqlc.arg(gross_total),
    delivery_status = sqlc.arg(delivery_status), invoice_status = sqlc.arg(invoice_status),
    version = sqlc.arg(version), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ConfirmOrderRow :one
-- ConfirmOrderRow confirms a draft (O18): who and when. The snapshot and the
-- prices freeze because nothing but a confirmed edit writes them after.
UPDATE orders.orders
SET status = 'confirmed', confirmed_at = sqlc.arg(now), confirmed_by_user_id = sqlc.narg(by_user_id),
    confirmed_by_display = sqlc.narg(by_display), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND status = 'draft'
RETURNING *;

-- name: SetOrderHold :one
-- SetOrderHold puts an order on hold with its reason, or releases it (O18).
UPDATE orders.orders
SET on_hold = sqlc.arg(on_hold), hold_reason = sqlc.narg(hold_reason), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetOrderLocked :one
-- SetOrderLocked locks or unlocks an order (O18).
UPDATE orders.orders SET locked = sqlc.arg(locked), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CancelOrderRow :one
UPDATE orders.orders
SET status = 'cancelled', cancelled_at = sqlc.arg(now), on_hold = false, hold_reason = NULL, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetOrderProgress :one
-- SetOrderProgress stores an order's derived delivery and invoice status
-- (O18), recomputed by every transaction that changes a bucket, and its
-- version.
UPDATE orders.orders
SET delivery_status = sqlc.arg(delivery_status), invoice_status = sqlc.arg(invoice_status),
    version = sqlc.arg(version), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetOrderSite :one
-- SetOrderSite sets the site an order is fulfilled from (O46). The caller
-- holds the row; a confirmed order's reservations are moved on the same
-- transaction.
UPDATE orders.orders SET site_id = sqlc.narg(site_id), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteOrderRow :execrows
DELETE FROM orders.orders WHERE id = sqlc.arg(id);

-- name: SetLineBuckets :exec
-- SetLineBuckets writes an order line's quantity buckets (O15): cancelled,
-- delivered and invoiced. The caller holds the order's row.
UPDATE orders.lines
SET qty_cancelled = sqlc.arg(qty_cancelled), qty_delivered = sqlc.arg(qty_delivered), qty_invoiced = sqlc.arg(qty_invoiced)
WHERE id = sqlc.arg(id) AND owner_kind = 'order';

-- name: InsertOrderChange :one
-- InsertOrderChange writes one entry of an order's change log (O26).
INSERT INTO orders.order_changes (owner_kind, owner_id, version, actor_kind, actor_user_id, actor_display, at, actions)
VALUES ('order', sqlc.arg(owner_id), sqlc.arg(version), sqlc.arg(actor_kind), sqlc.narg(actor_user_id),
        sqlc.narg(actor_display), sqlc.arg(at), sqlc.arg(actions))
RETURNING *;

-- name: OrderChangesOf :many
-- OrderChangesOf is an order's change log, oldest first.
SELECT * FROM orders.order_changes
WHERE owner_kind = 'order' AND owner_id = sqlc.arg(owner_id)
ORDER BY at, id;

-- name: ListOrders :many
-- ListOrders is one page of orders, newest first, filtered (O31a): q matches
-- the number's digits, the customer's name and your reference; open leaves
-- out the cancelled and the done; done keeps only the done, or only those not
-- done. done is confirmed with every product line satisfied: cancelled and
-- invoiced cover what was ordered (and so do cancelled and the larger of
-- delivered and invoiced) — an order whose remainder was cancelled whole is
-- done, while one delivered and not invoiced waits for its invoice. A kit's
-- child line is invoiced as its kit (manufacturing phase 2 M145): only
-- cancelled and the larger of delivered and invoiced must cover it.
SELECT sqlc.embed(o), ((o.status = 'confirmed'
       AND EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL)
       AND NOT EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL
                   AND ((l.parent_line_id IS NULL AND l.qty_cancelled + l.qty_invoiced < l.quantity)
                        OR l.qty_cancelled + greatest(l.qty_delivered, l.qty_invoiced) < l.quantity))))::boolean AS done FROM orders.orders o
WHERE (sqlc.narg(customer_id)::integer IS NULL OR o.customer_id = sqlc.narg(customer_id))
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR o.status = ANY(sqlc.arg(statuses)::text[]))
  AND (cardinality(sqlc.arg(delivery_statuses)::text[]) = 0 OR o.delivery_status = ANY(sqlc.arg(delivery_statuses)::text[]))
  AND (cardinality(sqlc.arg(invoice_statuses)::text[]) = 0 OR o.invoice_status = ANY(sqlc.arg(invoice_statuses)::text[]))
  AND (sqlc.narg(salesperson_id)::uuid IS NULL OR o.salesperson_id = sqlc.narg(salesperson_id))
  AND (NOT sqlc.arg(open)::boolean OR (o.status <> 'cancelled' AND NOT (o.status = 'confirmed'
       AND EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL)
       AND NOT EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL
                   AND ((l.parent_line_id IS NULL AND l.qty_cancelled + l.qty_invoiced < l.quantity)
                        OR l.qty_cancelled + greatest(l.qty_delivered, l.qty_invoiced) < l.quantity)))))
  AND (sqlc.narg(done)::boolean IS NULL OR (o.status = 'confirmed'
       AND EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL)
       AND NOT EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL
                   AND ((l.parent_line_id IS NULL AND l.qty_cancelled + l.qty_invoiced < l.quantity)
                        OR l.qty_cancelled + greatest(l.qty_delivered, l.qty_invoiced) < l.quantity))) = sqlc.narg(done)::boolean)
  AND (NOT sqlc.arg(to_deliver)::boolean OR (o.status = 'confirmed'
       AND EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL
                   AND l.qty_cancelled + l.qty_delivered < l.quantity)))
  AND (sqlc.arg(q)::text = '' OR o.number::text = sqlc.arg(q)::text
       OR o.customer_name ILIKE '%' || sqlc.arg(q)::text || '%'
       OR o.your_reference ILIKE '%' || sqlc.arg(q)::text || '%')
ORDER BY o.id DESC
LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: CountOrders :one
SELECT count(*)::integer FROM orders.orders o
WHERE (sqlc.narg(customer_id)::integer IS NULL OR o.customer_id = sqlc.narg(customer_id))
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR o.status = ANY(sqlc.arg(statuses)::text[]))
  AND (cardinality(sqlc.arg(delivery_statuses)::text[]) = 0 OR o.delivery_status = ANY(sqlc.arg(delivery_statuses)::text[]))
  AND (cardinality(sqlc.arg(invoice_statuses)::text[]) = 0 OR o.invoice_status = ANY(sqlc.arg(invoice_statuses)::text[]))
  AND (sqlc.narg(salesperson_id)::uuid IS NULL OR o.salesperson_id = sqlc.narg(salesperson_id))
  AND (NOT sqlc.arg(open)::boolean OR (o.status <> 'cancelled' AND NOT (o.status = 'confirmed'
       AND EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL)
       AND NOT EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL
                   AND ((l.parent_line_id IS NULL AND l.qty_cancelled + l.qty_invoiced < l.quantity)
                        OR l.qty_cancelled + greatest(l.qty_delivered, l.qty_invoiced) < l.quantity)))))
  AND (sqlc.narg(done)::boolean IS NULL OR (o.status = 'confirmed'
       AND EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL)
       AND NOT EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL
                   AND ((l.parent_line_id IS NULL AND l.qty_cancelled + l.qty_invoiced < l.quantity)
                        OR l.qty_cancelled + greatest(l.qty_delivered, l.qty_invoiced) < l.quantity))) = sqlc.narg(done)::boolean)
  AND (NOT sqlc.arg(to_deliver)::boolean OR (o.status = 'confirmed'
       AND EXISTS (SELECT 1 FROM orders.lines l WHERE l.owner_kind = 'order' AND l.owner_id = o.id
                   AND l.kind = 'product' AND l.quantity IS NOT NULL
                   AND l.qty_cancelled + l.qty_delivered < l.quantity)))
  AND (sqlc.arg(q)::text = '' OR o.number::text = sqlc.arg(q)::text
       OR o.customer_name ILIKE '%' || sqlc.arg(q)::text || '%'
       OR o.your_reference ILIKE '%' || sqlc.arg(q)::text || '%');

-- name: OpenQuoteSums :many
-- OpenQuoteSums is a customer's open quotes — draft and sent — counted and
-- their net totals summed, per currency (O31a).
SELECT currency::text AS currency, count(*)::integer AS documents, coalesce(sum(net_total), 0)::numeric(14,2) AS amount
FROM orders.quotes
WHERE customer_id = sqlc.arg(customer_id) AND status IN ('draft', 'sent')
GROUP BY currency
ORDER BY currency;

-- name: OrderedNotDeliveredSums :many
-- OrderedNotDeliveredSums is a customer's confirmed orders with a quantity
-- left to deliver, and that quantity's net value — each line's open quantity
-- (ordered less cancelled less delivered) at its net unit price, line_net over
-- quantity, rounded to øre per line — per currency (O31a).
SELECT t.currency::text AS currency, count(*)::integer AS documents, coalesce(sum(t.amount), 0)::numeric(14,2) AS amount
FROM (
    SELECT o.id, o.currency,
           sum(round(l.line_net * (l.quantity - l.qty_cancelled - l.qty_delivered) / l.quantity, 2)) AS amount
    FROM orders.orders o
    JOIN orders.lines l ON l.owner_kind = 'order' AND l.owner_id = o.id
    WHERE o.customer_id = sqlc.arg(customer_id) AND o.status = 'confirmed' AND l.kind = 'product'
      AND l.quantity > 0 AND l.quantity > l.qty_cancelled + l.qty_delivered
    GROUP BY o.id, o.currency
) t
GROUP BY t.currency
ORDER BY t.currency;

-- name: DeliveredNotInvoicedSums :many
-- DeliveredNotInvoicedSums is a customer's confirmed orders with a delivered
-- quantity not yet invoiced, and its net value, per currency (O31a): what is
-- delivered less what the invoices' issues stamped (O38); a draft invoice's
-- allocation is not invoiced yet. A kit's child line is invoiced as its kit
-- (manufacturing phase 2 M145) and is left out.
SELECT t.currency::text AS currency, count(*)::integer AS documents, coalesce(sum(t.amount), 0)::numeric(14,2) AS amount
FROM (
    SELECT o.id, o.currency,
           sum(round(l.line_net * (l.qty_delivered - l.qty_invoiced) / l.quantity, 2)) AS amount
    FROM orders.orders o
    JOIN orders.lines l ON l.owner_kind = 'order' AND l.owner_id = o.id
    WHERE o.customer_id = sqlc.arg(customer_id) AND o.status = 'confirmed' AND l.kind = 'product'
      AND l.quantity > 0 AND l.qty_delivered > l.qty_invoiced AND l.parent_line_id IS NULL
    GROUP BY o.id, o.currency
) t
GROUP BY t.currency
ORDER BY t.currency;
