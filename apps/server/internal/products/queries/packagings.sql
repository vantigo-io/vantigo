-- Packagings (inventory design I24, delivery B): a variant's boxes, cases and
-- pallets. Every write runs in one transaction that first takes the variant's
-- row FOR UPDATE (LockVariantOfProduct), so the packagings of a variant
-- change one writer at a time and never under a posting that holds the
-- variant FOR SHARE (ProductCatalog.LockVariants).

-- name: LockVariantOfProduct :one
-- LockVariantOfProduct takes the variant's row FOR UPDATE, scoped to its
-- product, before any write to its packagings or to its own barcode; no row
-- is the 404.
SELECT id FROM products.product_variants
WHERE id = @id AND product_id = @product_id
FOR UPDATE;

-- name: VariantOfProductExists :one
-- VariantOfProductExists is the packagings list's 404 check.
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE id = @id AND product_id = @product_id);

-- name: ListPackagingsByVariant :many
-- ListPackagingsByVariant is one variant's packagings, by sort order then id.
SELECT * FROM products.variant_packagings
WHERE variant_id = @variant_id
ORDER BY sort_order, id;

-- name: ListPackagingsByVariantIDs :many
-- ListPackagingsByVariantIDs batch-loads the packagings of a set of variants,
-- by variant, then sort order, then id, for the variant responses and the
-- catalog.
SELECT * FROM products.variant_packagings
WHERE variant_id = ANY(@variant_ids::int[])
ORDER BY variant_id, sort_order, id;

-- name: InsertPackaging :one
INSERT INTO products.variant_packagings (
    variant_id, name, kind, contains_packaging_id, contains_count, quantity, barcode,
    weight_kg, length_cm, width_cm, height_cm, sort_order, created_at, updated_at
) VALUES (
    @variant_id, @name, @kind, @contains_packaging_id, @contains_count, @quantity, @barcode,
    @weight_kg, @length_cm, @width_cm, @height_cm, @sort_order, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdatePackaging :one
UPDATE products.variant_packagings
SET name = @name,
    kind = @kind,
    contains_packaging_id = @contains_packaging_id,
    contains_count = @contains_count,
    quantity = @quantity,
    barcode = @barcode,
    weight_kg = @weight_kg,
    length_cm = @length_cm,
    width_cm = @width_cm,
    height_cm = @height_cm,
    sort_order = @sort_order,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SetPackagingQuantity :exec
-- SetPackagingQuantity is a derived packaging's recomputed quantity, written
-- when what it is built from changed.
UPDATE products.variant_packagings
SET quantity = @quantity, updated_at = @now::timestamptz
WHERE id = @id;

-- name: DeletePackaging :exec
DELETE FROM products.variant_packagings WHERE id = @id;

-- name: LockBarcodes :exec
-- LockBarcodes takes the transaction-scoped advisory lock under which a
-- barcode is checked for uniqueness across variants and packagings and then
-- written (inventory design I24); every write of a barcode to either table
-- takes it, so two writers cannot both take one GTIN.
SELECT pg_advisory_xact_lock(hashtext('products:barcodes'));

-- name: PackagingBarcodeExists :one
-- PackagingBarcodeExists is whether a packaging other than exclude_id (0 for
-- none) carries the barcode.
SELECT EXISTS(SELECT 1 FROM products.variant_packagings WHERE barcode = @barcode::text AND id != @exclude_id::int);

-- name: PackagingAnyBarcodeExists :one
-- PackagingAnyBarcodeExists is whether any packaging carries one of the
-- barcodes, for a product created with several variants.
SELECT EXISTS(SELECT 1 FROM products.variant_packagings WHERE barcode = ANY(@barcodes::text[]));

-- name: CatalogLookupPackagingCode :many
-- CatalogLookupPackagingCode is contracts.ProductCatalog.LookupCode's
-- packaging half: a packaging whose barcode equals the code exactly, with
-- its variant's catalog fields.
SELECT k.id AS packaging_id, v.id, v.product_id, p.name AS product_name, v.sku, v.barcode, v.unit, p.type AS product_type, p.status AS product_status,
       p.tax_category_id, p.description, p.category_id, pc.name AS category_name, v.option_values
FROM products.variant_packagings k
JOIN products.product_variants v ON v.id = k.variant_id
JOIN products.products p ON p.id = v.product_id
LEFT JOIN products.product_categories pc ON pc.id = p.category_id
WHERE k.barcode = @code::text
ORDER BY k.id;
