-- name: GetSupplierItem :one
SELECT * FROM purchasing.supplier_items WHERE id = sqlc.arg(id);

-- name: LockSupplierItem :one
SELECT * FROM purchasing.supplier_items WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LockVariantItems :exec
-- LockVariantItems serialises the writers of one variant's supplier items
-- (P11): the preferred flag is judged across the variant's items, and the
-- first item of a variant is preferred, so two writers of one variant take
-- this transaction-scoped advisory lock first.
SELECT pg_advisory_xact_lock(hashtext('purchasing.supplier_items'), sqlc.arg(variant_id)::integer);

-- name: FilterSupplierItems :many
-- FilterSupplierItems is the supplier items of a supplier, of some variants,
-- or both; every item when neither is given. The caller sorts and pages them
-- after it has read the suppliers' names and the variants' SKUs.
SELECT * FROM purchasing.supplier_items
WHERE (sqlc.narg(supplier_id)::bigint IS NULL OR supplier_id = sqlc.narg(supplier_id))
  AND (cardinality(sqlc.arg(variant_ids)::integer[]) = 0 OR variant_id = ANY(sqlc.arg(variant_ids)::integer[]))
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: SupplierItemsOf :many
-- SupplierItemsOf is one supplier's items of the variants given: what an
-- order line takes its defaults from (P16).
SELECT * FROM purchasing.supplier_items
WHERE supplier_id = sqlc.arg(supplier_id) AND variant_id = ANY(sqlc.arg(variant_ids)::integer[]);

-- name: SupplierItemExists :one
SELECT EXISTS (
    SELECT 1 FROM purchasing.supplier_items WHERE supplier_id = sqlc.arg(supplier_id) AND variant_id = sqlc.arg(variant_id)
)::boolean;

-- name: VariantHasPreferred :one
SELECT EXISTS (SELECT 1 FROM purchasing.supplier_items WHERE variant_id = sqlc.arg(variant_id) AND preferred)::boolean;

-- name: ClearPreferred :exec
-- ClearPreferred takes the preference off the variant's other items, before
-- one is made preferred (the partial unique index holds one per variant).
UPDATE purchasing.supplier_items SET preferred = false, revision = revision + 1, updated_at = sqlc.arg(now)
WHERE variant_id = sqlc.arg(variant_id) AND preferred AND id <> sqlc.arg(except_id);

-- name: PromoteOldestItem :exec
-- PromoteOldestItem makes the variant's oldest item preferred, after the
-- preferred one was deleted.
UPDATE purchasing.supplier_items SET preferred = true, revision = revision + 1, updated_at = sqlc.arg(now)
WHERE id = (SELECT min(si.id) FROM purchasing.supplier_items si WHERE si.variant_id = sqlc.arg(variant)::integer);

-- name: InsertSupplierItem :one
INSERT INTO purchasing.supplier_items (
    supplier_id, variant_id, supplier_item_number, supplier_item_name, price, currency, price_set_on, price_set_by,
    price_set_by_user_id, packaging_id, lead_time_days, minimum_quantity, order_multiple, preferred, created_at, updated_at
) VALUES (
    sqlc.arg(supplier_id), sqlc.arg(variant_id), sqlc.narg(supplier_item_number), sqlc.narg(supplier_item_name),
    sqlc.narg(price), sqlc.arg(currency), sqlc.narg(price_set_on), sqlc.narg(price_set_by), sqlc.narg(price_set_by_user_id),
    sqlc.narg(packaging_id), sqlc.narg(lead_time_days), sqlc.narg(minimum_quantity), sqlc.narg(order_multiple),
    sqlc.arg(preferred), sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: UpdateSupplierItem :one
UPDATE purchasing.supplier_items SET
    supplier_item_number = sqlc.narg(supplier_item_number),
    supplier_item_name   = sqlc.narg(supplier_item_name),
    price                = sqlc.narg(price),
    currency             = sqlc.arg(currency),
    price_set_on         = sqlc.narg(price_set_on),
    price_set_by         = sqlc.narg(price_set_by),
    price_set_by_user_id = sqlc.narg(price_set_by_user_id),
    packaging_id         = sqlc.narg(packaging_id),
    lead_time_days       = sqlc.narg(lead_time_days),
    minimum_quantity     = sqlc.narg(minimum_quantity),
    order_multiple       = sqlc.narg(order_multiple),
    preferred            = sqlc.arg(preferred),
    revision             = revision + 1,
    updated_at           = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteSupplierItem :exec
DELETE FROM purchasing.supplier_items WHERE id = sqlc.arg(id);

-- name: SupplierItemCounts :many
-- SupplierItemCounts is how many items each supplier named has.
SELECT supplier_id, count(*)::integer AS items FROM purchasing.supplier_items
WHERE supplier_id = ANY(sqlc.arg(supplier_ids)::bigint[])
GROUP BY supplier_id;

-- name: OpenOrderCounts :many
-- OpenOrderCounts is how many orders sent and not received, closed or
-- cancelled each supplier named has.
SELECT supplier_id, count(*)::integer AS orders FROM purchasing.orders
WHERE supplier_id = ANY(sqlc.arg(supplier_ids)::bigint[]) AND status IN ('sent', 'partially_received')
GROUP BY supplier_id;
