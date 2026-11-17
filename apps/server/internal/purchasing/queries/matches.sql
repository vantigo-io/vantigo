-- The invoice match (purchasing design §6, delivery D): the matches, what
-- Purchasing knows of an invoice from Accounting's events, the handled
-- events, the posted invoices' follow-ups and Register invoice's requests.

-- name: EnsureInvoiceState :exec
-- EnsureInvoiceState makes the invoice's row when it has none; the caller
-- then takes it FOR UPDATE: the first lock of every write of the invoice's
-- matches.
INSERT INTO purchasing.invoice_states (supplier_invoice_id, updated_at) VALUES (sqlc.arg(id), sqlc.arg(now))
ON CONFLICT (supplier_invoice_id) DO NOTHING;

-- name: InvoiceStateForUpdate :one
SELECT * FROM purchasing.invoice_states WHERE supplier_invoice_id = sqlc.arg(id) FOR UPDATE;

-- name: SetInvoiceState :exec
UPDATE purchasing.invoice_states SET state = sqlc.arg(state), copy_id = sqlc.narg(copy_id), updated_at = sqlc.arg(now)
WHERE supplier_invoice_id = sqlc.arg(id);

-- name: InvoiceMatches :many
SELECT * FROM purchasing.invoice_matches WHERE supplier_invoice_id = sqlc.arg(id) ORDER BY line_position;

-- name: InvoiceMatchesOf :many
SELECT * FROM purchasing.invoice_matches WHERE supplier_invoice_id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY supplier_invoice_id, line_position;

-- name: OrderMatches :many
SELECT * FROM purchasing.invoice_matches WHERE order_id = sqlc.arg(order_id) ORDER BY supplier_invoice_id, line_position;

-- name: InsertMatch :one
INSERT INTO purchasing.invoice_matches (
    supplier_invoice_id, line_position, order_id, order_line_id, kind, quantity, line_net, invoice_number,
    invoice_date, invoice_currency, state, posted_at, matched_by_user_id, matched_by_display, created_at, updated_at
) VALUES (
    sqlc.arg(supplier_invoice_id), sqlc.arg(line_position), sqlc.arg(order_id), sqlc.arg(order_line_id), sqlc.arg(kind),
    sqlc.arg(quantity), sqlc.arg(line_net), sqlc.arg(invoice_number), sqlc.arg(invoice_date), sqlc.arg(invoice_currency),
    sqlc.arg(state), sqlc.narg(posted_at), sqlc.narg(matched_by_user_id), sqlc.narg(matched_by_display), sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: DeleteMatch :exec
DELETE FROM purchasing.invoice_matches WHERE id = sqlc.arg(id);

-- name: PostInvoiceMatches :exec
-- PostInvoiceMatches fixes an invoice's provisional matches (P37).
UPDATE purchasing.invoice_matches SET state = 'posted', posted_at = sqlc.arg(now), updated_at = sqlc.arg(now),
    invoice_currency = sqlc.arg(currency), invoice_date = sqlc.arg(invoice_date), invoice_number = sqlc.arg(invoice_number)
WHERE supplier_invoice_id = sqlc.arg(id) AND state = 'provisional';

-- name: RefreshMatchFacts :exec
-- RefreshMatchFacts writes what Accounting answers of an invoice and of the
-- match's line now.
UPDATE purchasing.invoice_matches SET line_net = sqlc.arg(line_net), invoice_number = sqlc.arg(invoice_number),
    invoice_date = sqlc.arg(invoice_date), invoice_currency = sqlc.arg(currency), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: AddInvoiced :exec
-- AddInvoiced moves an order line's invoiced quantity by delta (a credit
-- note's and a release's negative). Every caller judges, under the order's
-- row lock, that it stays at 0 or more (A14), so it always equals the sum of
-- the line's matches.
UPDATE purchasing.order_lines SET invoiced = invoiced + sqlc.arg(delta)::numeric WHERE id = sqlc.arg(id);

-- name: LockOrdersByID :many
SELECT * FROM purchasing.orders WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY id FOR UPDATE;

-- name: SuggestedOrders :many
-- SuggestedOrders is the supplier's sent orders whose number is one of the
-- digit runs of an invoice's reference (P36).
SELECT * FROM purchasing.orders
WHERE supplier_id = sqlc.arg(supplier_id) AND number = ANY(sqlc.arg(numbers)::bigint[])
  AND status IN ('sent', 'partially_received', 'received', 'closed')
ORDER BY number;

-- name: MatchedOrdersOf :many
SELECT * FROM purchasing.orders WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY number, id;

-- name: InsertInvoiceEvent :execrows
INSERT INTO purchasing.invoice_events (event_id, name, supplier_invoice_id, handled_at)
VALUES (sqlc.arg(event_id), sqlc.arg(name), sqlc.arg(supplier_invoice_id), sqlc.arg(now))
ON CONFLICT (event_id) DO NOTHING;

-- name: QueueFollowup :exec
INSERT INTO purchasing.invoice_followups (supplier_invoice_id, requested_at, next_attempt_at)
VALUES (sqlc.arg(id), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (supplier_invoice_id) DO UPDATE SET requested_at = EXCLUDED.requested_at,
    next_attempt_at = EXCLUDED.next_attempt_at, attempts = 0, last_error = NULL, completed_at = NULL;

-- name: ClaimFollowup :one
-- ClaimFollowup takes the oldest due follow-up and pushes it out by the
-- lease, counting the attempt: two workers never take one.
UPDATE purchasing.invoice_followups SET next_attempt_at = sqlc.arg(lease_until), attempts = attempts + 1
WHERE supplier_invoice_id = (
    SELECT f.supplier_invoice_id FROM purchasing.invoice_followups f
    WHERE f.completed_at IS NULL AND f.next_attempt_at <= sqlc.arg(now) AND f.attempts < sqlc.arg(max_attempts)
    ORDER BY f.next_attempt_at, f.supplier_invoice_id LIMIT 1 FOR UPDATE SKIP LOCKED)
RETURNING *;

-- name: CompleteFollowup :exec
UPDATE purchasing.invoice_followups SET completed_at = sqlc.arg(now), last_error = NULL
WHERE supplier_invoice_id = sqlc.arg(id) AND requested_at = sqlc.arg(requested_at);

-- name: FailFollowup :exec
UPDATE purchasing.invoice_followups SET last_error = sqlc.arg(last_error), next_attempt_at = sqlc.arg(next_attempt_at)
WHERE supplier_invoice_id = sqlc.arg(id);

-- name: SupplierItemsFor :many
-- SupplierItemsFor is the supplier's items of the variants, locked for the
-- last price (P39).
SELECT * FROM purchasing.supplier_items
WHERE supplier_id = sqlc.arg(supplier_id) AND variant_id = ANY(sqlc.arg(variant_ids)::integer[])
ORDER BY id FOR UPDATE;

-- name: SetInvoicePrice :exec
UPDATE purchasing.supplier_items SET price = sqlc.arg(price), price_set_on = sqlc.arg(set_on), price_set_by = 'invoice',
    price_set_by_user_id = NULL, revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: RegistrationByRequest :one
SELECT * FROM purchasing.invoice_registrations WHERE client_request_id = sqlc.arg(client_request_id);

-- name: InsertRegistration :exec
INSERT INTO purchasing.invoice_registrations (client_request_id, request_hash, order_id, supplier_invoice_id, lines, lines_left, created_at)
VALUES (sqlc.arg(client_request_id), sqlc.arg(request_hash), sqlc.arg(order_id), sqlc.arg(supplier_invoice_id),
    sqlc.arg(lines), sqlc.arg(lines_left), sqlc.arg(now));

-- name: ReceivedAsOf :many
-- ReceivedAsOf is, per order line, what its receipts brought in on or
-- before the day and had not been taken back by the instant that day ends
-- (P41).
SELECT rl.order_line_id, sum(rl.quantity)::numeric AS received
FROM purchasing.receipt_lines rl JOIN purchasing.receipts r ON r.id = rl.receipt_id
WHERE r.received_on <= sqlc.arg(as_of)::date AND (r.undone_at IS NULL OR r.undone_at >= sqlc.arg(day_end)::timestamptz)
GROUP BY rl.order_line_id;

-- name: PostedInvoicedAsOf :many
-- PostedInvoicedAsOf is, per order line, what posted invoices dated on or
-- before the day bill, less their posted credit notes (P41).
SELECT order_line_id,
    sum(CASE WHEN kind = 'credit_note' THEN -quantity ELSE quantity END)::numeric AS invoiced
FROM purchasing.invoice_matches
WHERE state = 'posted' AND invoice_date <= sqlc.arg(as_of)::date
GROUP BY order_line_id;

-- name: LinesForAccrual :many
-- LinesForAccrual is every line of an order sent on or before the day that
-- anything could be received on, with its order, by supplier and number.
SELECT l.*, o.number AS order_number, o.supplier_id AS order_supplier_id, o.supplier_name AS order_supplier_name,
    o.currency AS order_currency
FROM purchasing.order_lines l JOIN purchasing.orders o ON o.id = l.order_id
WHERE o.status NOT IN ('draft', 'cancelled')
  AND (sqlc.narg(supplier_id)::bigint IS NULL OR o.supplier_id = sqlc.narg(supplier_id))
ORDER BY o.supplier_name, o.supplier_id, o.number, l.position;

-- name: SupplierMatches :many
-- SupplierMatches is every match on an order naming the supplier, for its
-- export (P7): ids, quantities and the invoices' numbers, no personal data.
SELECT m.* FROM purchasing.invoice_matches m JOIN purchasing.orders o ON o.id = m.order_id
WHERE o.supplier_id = sqlc.arg(supplier_id) ORDER BY m.order_id, m.supplier_invoice_id, m.line_position;
