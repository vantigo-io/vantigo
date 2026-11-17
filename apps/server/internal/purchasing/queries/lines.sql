-- name: OrderLines :many
SELECT * FROM purchasing.order_lines WHERE order_id = sqlc.arg(order_id) ORDER BY position, id;

-- name: OrderLinesOf :many
-- OrderLinesOf is the lines of many orders, by order then position.
SELECT * FROM purchasing.order_lines WHERE order_id = ANY(sqlc.arg(order_ids)::bigint[]) ORDER BY order_id, position, id;

-- name: ParkLinePositions :exec
-- ParkLinePositions moves an order's lines out of the way of the positions a
-- save writes, so the unique (order, position) index never sees two lines
-- at one place while they are renumbered.
UPDATE purchasing.order_lines SET position = position + 100000 WHERE order_id = sqlc.arg(order_id);

-- name: InsertLine :one
INSERT INTO purchasing.order_lines (
    order_id, position, kind, variant_id, sku, name, product_type, supplier_item_number, description, unit,
    packaging_id, packaging_name, packaging_factor, order_quantity, quantity, unit_price, discount_percent, line_net,
    expected_on
) VALUES (
    sqlc.arg(order_id), sqlc.arg(position), sqlc.arg(kind), sqlc.narg(variant_id), sqlc.narg(sku), sqlc.arg(name),
    sqlc.narg(product_type), sqlc.narg(supplier_item_number), sqlc.arg(description), sqlc.arg(unit),
    sqlc.narg(packaging_id), sqlc.narg(packaging_name), sqlc.narg(packaging_factor), sqlc.arg(order_quantity),
    sqlc.arg(quantity), sqlc.arg(unit_price), sqlc.arg(discount_percent), sqlc.arg(line_net), sqlc.narg(expected_on)
)
RETURNING *;

-- name: UpdateLine :one
UPDATE purchasing.order_lines SET
    position             = sqlc.arg(position),
    supplier_item_number = sqlc.narg(supplier_item_number),
    description          = sqlc.arg(description),
    unit                 = sqlc.arg(unit),
    packaging_id         = sqlc.narg(packaging_id),
    packaging_name       = sqlc.narg(packaging_name),
    packaging_factor     = sqlc.narg(packaging_factor),
    order_quantity       = sqlc.arg(order_quantity),
    quantity             = sqlc.arg(quantity),
    unit_price           = sqlc.arg(unit_price),
    discount_percent     = sqlc.arg(discount_percent),
    line_net             = sqlc.arg(line_net),
    expected_on          = sqlc.narg(expected_on)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteLine :exec
DELETE FROM purchasing.order_lines WHERE id = sqlc.arg(id);

-- name: SetLineConfirmedOn :exec
UPDATE purchasing.order_lines SET confirmed_on = sqlc.narg(confirmed_on) WHERE id = sqlc.arg(id);

-- name: CloseLine :exec
UPDATE purchasing.order_lines SET closed_at = sqlc.arg(closed_at) WHERE id = sqlc.arg(id) AND closed_at IS NULL;

-- name: CloseOpenLines :execrows
-- CloseOpenLines ends every line of the order that is not closed yet (A16),
-- all at one instant, the order's own closed_at: Reopen opens exactly those.
UPDATE purchasing.order_lines SET closed_at = sqlc.arg(closed_at)
WHERE order_id = sqlc.arg(order_id) AND closed_at IS NULL;

-- name: ReopenLines :execrows
-- ReopenLines opens again the lines the order's close ended (P18).
UPDATE purchasing.order_lines SET closed_at = NULL
WHERE order_id = sqlc.arg(order_id) AND closed_at = sqlc.arg(closed_at)::timestamptz;

-- name: InsertChange :exec
INSERT INTO purchasing.order_changes (order_id, actor_user_id, actor_display, at, actions)
VALUES (sqlc.arg(order_id), sqlc.narg(actor_user_id), sqlc.narg(actor_display), sqlc.arg(at), sqlc.arg(actions));

-- name: ChangesOf :many
SELECT * FROM purchasing.order_changes WHERE order_id = sqlc.arg(order_id) ORDER BY id DESC;
