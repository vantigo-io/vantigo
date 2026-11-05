-- Images of a product (messenger design N13, delivery P). Every write runs in
-- one transaction that first takes the product's image lock
-- (LockProductImages), so a product's images, its order and its one primary
-- change one writer at a time.

-- name: LockProductImages :exec
-- LockProductImages takes the transaction-scoped advisory lock of one
-- product's images, keyed by this delivery's migration number and the product. It is no row lock on the product: an Inventory posting
-- holds the product FOR SHARE (ProductCatalog.LockVariants) and must not wait
-- for a picture.
SELECT pg_advisory_xact_lock(1130, @product_id::int);

-- name: ListProductImages :many
-- ListProductImages is a product's images in their order.
SELECT * FROM products.product_images
WHERE product_id = @product_id
ORDER BY sort_order, id;

-- name: ListProductImagesByUse :many
-- ListProductImagesByUse is a product's images marked for one use, in their
-- order (ProductCatalog.ProductImages).
SELECT * FROM products.product_images
WHERE product_id = @product_id AND @use::text = ANY(uses)
ORDER BY sort_order, id;

-- name: GetProductImage :one
-- GetProductImage is one image of a product; no row is the 404.
SELECT * FROM products.product_images
WHERE id = @id AND product_id = @product_id;

-- name: GetProductImageByID :one
-- GetProductImageByID is ProductCatalog.OpenProductImage's lookup.
SELECT * FROM products.product_images WHERE id = @id;

-- name: NextProductImageSortOrder :one
-- NextProductImageSortOrder is the place after a product's last image.
SELECT COALESCE(MAX(sort_order) + 1, 0)::int FROM products.product_images WHERE product_id = @product_id;

-- name: ProductHasPrimaryImage :one
SELECT EXISTS(SELECT 1 FROM products.product_images WHERE product_id = @product_id AND is_primary);

-- name: InsertProductImage :one
INSERT INTO products.product_images (
    product_id, storage_key, thumb_storage_key, content_type, byte_size, width, height, sha256,
    alt_text, sort_order, is_primary, uses, created_at, created_by
) VALUES (
    @product_id, @storage_key, @thumb_storage_key, @content_type, @byte_size, @width, @height, @sha256,
    @alt_text, @sort_order, @is_primary, @uses::text[], @now::timestamptz, @created_by
)
RETURNING *;

-- name: UpdateProductImage :one
-- UpdateProductImage sets an image's alt text and uses.
UPDATE products.product_images
SET alt_text = @alt_text, uses = @uses::text[]
WHERE id = @id
RETURNING *;

-- name: SetProductImageSortOrder :exec
UPDATE products.product_images SET sort_order = @sort_order WHERE id = @id;

-- name: ClearPrimaryProductImage :exec
-- ClearPrimaryProductImage runs before SetPrimaryProductImage: the partial
-- unique index allows one primary, and is checked row by row.
UPDATE products.product_images SET is_primary = false WHERE product_id = @product_id AND is_primary;

-- name: SetPrimaryProductImage :exec
UPDATE products.product_images SET is_primary = true WHERE id = @id;

-- name: DeleteProductImage :exec
DELETE FROM products.product_images WHERE id = @id;
