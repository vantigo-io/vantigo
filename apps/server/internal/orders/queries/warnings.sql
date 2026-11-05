-- name: CustomerWarning :one
-- CustomerWarning is a customer's warning (O33). No row is pgx.ErrNoRows.
SELECT * FROM orders.customer_warnings WHERE customer_id = sqlc.arg(customer_id);

-- name: ProductWarning :one
-- ProductWarning is a product's warning (O33). No row is pgx.ErrNoRows.
SELECT * FROM orders.product_warnings WHERE product_id = sqlc.arg(product_id);

-- name: UpsertCustomerWarning :one
-- UpsertCustomerWarning sets a customer's warning, its level, text and the
-- customer's name as it is now; created_at stays as the first write set it.
INSERT INTO orders.customer_warnings (
    customer_id, level, text, subject_name, updated_by_user_id, updated_by_display, created_at, updated_at
) VALUES (
    sqlc.arg(customer_id), sqlc.arg(level), sqlc.arg(text), sqlc.arg(subject_name),
    sqlc.narg(updated_by_user_id), sqlc.narg(updated_by_display), sqlc.arg(now), sqlc.arg(now)
)
ON CONFLICT (customer_id) DO UPDATE SET
    level = EXCLUDED.level, text = EXCLUDED.text, subject_name = EXCLUDED.subject_name,
    updated_by_user_id = EXCLUDED.updated_by_user_id, updated_by_display = EXCLUDED.updated_by_display,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: UpsertProductWarning :one
-- UpsertProductWarning is the product's.
INSERT INTO orders.product_warnings (
    product_id, level, text, subject_name, updated_by_user_id, updated_by_display, created_at, updated_at
) VALUES (
    sqlc.arg(product_id), sqlc.arg(level), sqlc.arg(text), sqlc.arg(subject_name),
    sqlc.narg(updated_by_user_id), sqlc.narg(updated_by_display), sqlc.arg(now), sqlc.arg(now)
)
ON CONFLICT (product_id) DO UPDATE SET
    level = EXCLUDED.level, text = EXCLUDED.text, subject_name = EXCLUDED.subject_name,
    updated_by_user_id = EXCLUDED.updated_by_user_id, updated_by_display = EXCLUDED.updated_by_display,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteCustomerWarning :execrows
DELETE FROM orders.customer_warnings WHERE customer_id = sqlc.arg(customer_id);

-- name: DeleteProductWarning :execrows
DELETE FROM orders.product_warnings WHERE product_id = sqlc.arg(product_id);

-- name: ListCustomerWarnings :many
-- ListCustomerWarnings is every customer warning, by the customer's name.
SELECT * FROM orders.customer_warnings ORDER BY lower(subject_name), customer_id;

-- name: ListProductWarnings :many
-- ListProductWarnings is every product warning, by the product's name.
SELECT * FROM orders.product_warnings ORDER BY lower(subject_name), product_id;

-- name: ProductWarningsOf :many
-- ProductWarningsOf is the warnings of the given products, by product id.
SELECT * FROM orders.product_warnings WHERE product_id = ANY(sqlc.arg(product_ids)::int[]) ORDER BY product_id;

-- name: RepointCustomerWarning :execrows
-- RepointCustomerWarning moves a merged-away customer's warning to the
-- survivor when the survivor has none (O33); the survivor's own is kept.
UPDATE orders.customer_warnings w SET customer_id = sqlc.arg(into_customer_id)
WHERE w.customer_id = sqlc.arg(from_customer_id)
  AND NOT EXISTS (SELECT 1 FROM orders.customer_warnings s WHERE s.customer_id = sqlc.arg(into_customer_id));
