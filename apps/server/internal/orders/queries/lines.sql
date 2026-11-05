-- name: InsertLine :one
-- InsertLine writes one line of a quote, an order or a template (O15), with
-- a new id. The amounts are docmath's (O17), computed by the caller.
INSERT INTO orders.lines (
    owner_kind, owner_id, position, kind, variant_id, sku, name, unit, description,
    quantity, unit_price, discount_percent, packaging_id, packaging_quantity,
    price_source, vat_code_id, vat_code, vat_rate_percent, vat_category,
    line_gross, line_allowance, line_net, external_line_id, invoice_policy, optional, delivery_method
) VALUES (
    sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(position), sqlc.arg(kind), sqlc.narg(variant_id),
    sqlc.narg(sku), sqlc.narg(name), sqlc.arg(unit), sqlc.arg(description), sqlc.narg(quantity),
    sqlc.narg(unit_price), sqlc.arg(discount_percent), sqlc.narg(packaging_id), sqlc.narg(packaging_quantity),
    sqlc.narg(price_source), sqlc.narg(vat_code_id), sqlc.narg(vat_code), sqlc.narg(vat_rate_percent),
    sqlc.narg(vat_category), sqlc.arg(line_gross), sqlc.arg(line_allowance), sqlc.arg(line_net),
    sqlc.narg(external_line_id), sqlc.arg(invoice_policy), sqlc.arg(optional), sqlc.arg(delivery_method)
)
RETURNING *;

-- name: InsertLineKeepingID :one
-- InsertLineKeepingID writes a line again under the id it had: a save
-- replaces a document's lines whole, and a line the editor names by id stays
-- that line (O15).
INSERT INTO orders.lines (
    id, owner_kind, owner_id, position, kind, variant_id, sku, name, unit, description,
    quantity, unit_price, discount_percent, packaging_id, packaging_quantity,
    price_source, vat_code_id, vat_code, vat_rate_percent, vat_category,
    line_gross, line_allowance, line_net, external_line_id, invoice_policy, optional, delivery_method
) OVERRIDING SYSTEM VALUE VALUES (
    sqlc.arg(id), sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(position), sqlc.arg(kind), sqlc.narg(variant_id),
    sqlc.narg(sku), sqlc.narg(name), sqlc.arg(unit), sqlc.arg(description), sqlc.narg(quantity),
    sqlc.narg(unit_price), sqlc.arg(discount_percent), sqlc.narg(packaging_id), sqlc.narg(packaging_quantity),
    sqlc.narg(price_source), sqlc.narg(vat_code_id), sqlc.narg(vat_code), sqlc.narg(vat_rate_percent),
    sqlc.narg(vat_category), sqlc.arg(line_gross), sqlc.arg(line_allowance), sqlc.arg(line_net),
    sqlc.narg(external_line_id), sqlc.arg(invoice_policy), sqlc.arg(optional), sqlc.arg(delivery_method)
)
RETURNING *;

-- name: LinesOf :many
-- LinesOf is a document's lines in position order.
SELECT * FROM orders.lines
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id)
ORDER BY position;

-- name: DeleteLinesOf :exec
-- DeleteLinesOf removes every line of a document: a save writes them again,
-- and a deleted document takes its lines with it.
DELETE FROM orders.lines WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id);

-- name: LineCounts :many
-- LineCounts is how many lines each owner of a kind has, for a list.
SELECT owner_id, count(*)::integer AS lines FROM orders.lines
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = ANY(sqlc.arg(owner_ids)::bigint[])
GROUP BY owner_id;
