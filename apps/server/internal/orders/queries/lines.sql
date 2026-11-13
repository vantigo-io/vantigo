-- name: InsertLine :one
-- InsertLine writes one line of a quote, an order, a template or a
-- subscription (O15), with a new id; a subscription's line may be dated and
-- name the first version of its chain (O44), and an order's kit child line
-- its kit line and per kit (manufacturing phase 2 M145). The amounts are
-- docmath's (O17), computed by the caller.
INSERT INTO orders.lines (
    owner_kind, owner_id, position, kind, variant_id, sku, name, unit, description,
    quantity, unit_price, discount_percent, packaging_id, packaging_quantity,
    price_source, vat_code_id, vat_code, vat_rate_percent, vat_category,
    line_gross, line_allowance, line_net, external_line_id, invoice_policy, optional, delivery_method,
    product_id, packaging_name, valid_from, valid_to, chain_id, parent_line_id, per_kit
) VALUES (
    sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(position), sqlc.arg(kind), sqlc.narg(variant_id),
    sqlc.narg(sku), sqlc.narg(name), sqlc.arg(unit), sqlc.arg(description), sqlc.narg(quantity),
    sqlc.narg(unit_price), sqlc.arg(discount_percent), sqlc.narg(packaging_id), sqlc.narg(packaging_quantity),
    sqlc.narg(price_source), sqlc.narg(vat_code_id), sqlc.narg(vat_code), sqlc.narg(vat_rate_percent),
    sqlc.narg(vat_category), sqlc.arg(line_gross), sqlc.arg(line_allowance), sqlc.arg(line_net),
    sqlc.narg(external_line_id), sqlc.arg(invoice_policy), sqlc.arg(optional), sqlc.arg(delivery_method),
    sqlc.narg(product_id), sqlc.narg(packaging_name), sqlc.narg(valid_from), sqlc.narg(valid_to), sqlc.narg(chain_id),
    sqlc.narg(parent_line_id), sqlc.narg(per_kit)
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
    line_gross, line_allowance, line_net, external_line_id, invoice_policy, optional, delivery_method,
    product_id, packaging_name, valid_from, valid_to, chain_id, parent_line_id, per_kit
) OVERRIDING SYSTEM VALUE VALUES (
    sqlc.arg(id), sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(position), sqlc.arg(kind), sqlc.narg(variant_id),
    sqlc.narg(sku), sqlc.narg(name), sqlc.arg(unit), sqlc.arg(description), sqlc.narg(quantity),
    sqlc.narg(unit_price), sqlc.arg(discount_percent), sqlc.narg(packaging_id), sqlc.narg(packaging_quantity),
    sqlc.narg(price_source), sqlc.narg(vat_code_id), sqlc.narg(vat_code), sqlc.narg(vat_rate_percent),
    sqlc.narg(vat_category), sqlc.arg(line_gross), sqlc.arg(line_allowance), sqlc.arg(line_net),
    sqlc.narg(external_line_id), sqlc.arg(invoice_policy), sqlc.arg(optional), sqlc.arg(delivery_method),
    sqlc.narg(product_id), sqlc.narg(packaging_name), sqlc.narg(valid_from), sqlc.narg(valid_to), sqlc.narg(chain_id),
    sqlc.narg(parent_line_id), sqlc.narg(per_kit)
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
