-- Receipts (purchasing design §4, delivery B): the receipt and its lines,
-- the order lines' received quantities, and the delivered mark of a text or
-- service line.

-- name: InsertReceipt :one
INSERT INTO purchasing.receipts (
    number, order_id, site_id, received_on, delivery_note, note, received_by_user_id, received_by_display,
    client_request_id, request_hash, created_at
) VALUES (
    sqlc.arg(number), sqlc.arg(order_id), sqlc.arg(site_id), sqlc.arg(received_on), sqlc.arg(delivery_note),
    sqlc.arg(note), sqlc.narg(received_by_user_id), sqlc.narg(received_by_display), sqlc.arg(client_request_id),
    sqlc.arg(request_hash), sqlc.arg(now)
)
RETURNING *;

-- name: SetReceiptMovement :exec
UPDATE purchasing.receipts SET movement_id = sqlc.arg(movement_id) WHERE id = sqlc.arg(id);

-- name: InsertReceiptLine :exec
INSERT INTO purchasing.receipt_lines (
    receipt_id, position, order_line_id, variant_id, location_id, quantity, entered_quantity, in_packaging,
    packaging_name, lot_id, lot_number, supplier_lot, production_date, best_before, expiry, serials, unit_price, currency
) VALUES (
    sqlc.arg(receipt_id), sqlc.arg(position), sqlc.arg(order_line_id), sqlc.arg(variant_id), sqlc.arg(location_id),
    sqlc.arg(quantity), sqlc.arg(entered_quantity), sqlc.arg(in_packaging), sqlc.narg(packaging_name), sqlc.narg(lot_id),
    sqlc.narg(lot_number), sqlc.narg(supplier_lot), sqlc.narg(production_date), sqlc.narg(best_before), sqlc.narg(expiry),
    sqlc.arg(serials)::text[], sqlc.arg(unit_price), sqlc.arg(currency)
);

-- name: GetReceipt :one
SELECT * FROM purchasing.receipts WHERE id = sqlc.arg(id);

-- name: LockReceipt :one
SELECT * FROM purchasing.receipts WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ReceiptByRequest :one
-- ReceiptByRequest is the receipt a Receive of this clientRequestId made
-- (A11).
SELECT * FROM purchasing.receipts WHERE client_request_id = sqlc.arg(client_request_id);

-- name: ReceiptsOfOrder :many
SELECT * FROM purchasing.receipts WHERE order_id = sqlc.arg(order_id) ORDER BY id DESC;

-- name: ReceiptLinesOf :many
-- ReceiptLinesOf is the lines of the receipts given, by receipt then
-- position.
SELECT l.*, coalesce(ol.sku, '')::text AS sku, ol.name, ol.unit
FROM purchasing.receipt_lines l JOIN purchasing.order_lines ol ON ol.id = l.order_line_id
WHERE l.receipt_id = ANY(sqlc.arg(receipt_ids)::bigint[])
ORDER BY l.receipt_id, l.position;

-- name: AddReceived :exec
-- AddReceived moves an order line's received quantity by delta, which an
-- undo makes negative; the CHECK keeps it at 0 or more.
UPDATE purchasing.order_lines SET received = received + sqlc.arg(delta)::numeric WHERE id = sqlc.arg(id);

-- name: MarkReceiptUndone :one
UPDATE purchasing.receipts SET
    undone_at            = sqlc.arg(now),
    undone_by_user_id    = sqlc.narg(undone_by_user_id),
    undone_by_display    = sqlc.narg(undone_by_display),
    undo_note            = sqlc.narg(undo_note),
    reversal_movement_id = sqlc.arg(reversal_movement_id)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetLineDelivered :exec
UPDATE purchasing.order_lines SET delivered_at = sqlc.narg(delivered_at) WHERE id = sqlc.arg(id);
