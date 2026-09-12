-- name: InsertProduct :one
-- InsertProduct creates a product row (CreateProductEndpoint.cs:86-101).
-- created_at and updated_at are the same instant, supplied by the caller
-- from Deps.Clock().
INSERT INTO products.products (
    name, description, category_id, type, status, tax_category_id, created_at, updated_at
) VALUES (
    @name, @description, @category_id, @type, @status, @tax_category_id, @now::timestamptz, @now::timestamptz
)
RETURNING id, name, description, category_id, type, status, tax_category_id, created_at, updated_at;

-- name: GetProductByID :one
-- GetProductByID fetches one product's own row, no joins.
SELECT id, name, description, category_id, type, status, tax_category_id, created_at, updated_at
FROM products.products
WHERE id = @id;

-- name: UpdateProduct :one
-- UpdateProduct applies PUT /products/{id}'s validated shared fields
-- (UpdateProductEndpoint.cs:53-64). request.Variants is never read here
-- (products inventory §7 oddity 2): the handler never passes variant data
-- to this query.
UPDATE products.products
SET name = @name,
    description = @description,
    category_id = @category_id,
    type = @type,
    status = @status,
    tax_category_id = @tax_category_id,
    updated_at = @updated_at::timestamptz
WHERE id = @id
RETURNING id, name, description, category_id, type, status, tax_category_id, created_at, updated_at;

-- name: ArchiveProduct :one
-- ArchiveProduct is DeleteProductsById's status transition
-- (ArchiveProductEndpoint.cs:28-32), applied only once the handler has
-- confirmed the product is not already Discontinued.
UPDATE products.products
SET status = 'Discontinued', updated_at = @now::timestamptz
WHERE id = @id
RETURNING id, name, description, category_id, type, status, tax_category_id, created_at, updated_at;

-- name: GetCategoryRef :one
-- GetCategoryRef is the compact category reference a product embeds
-- (ProductCategoryResponse), and doubles as the categoryId existence check
-- CreateProductEndpoint/UpdateProductEndpoint run before saving: pgx.ErrNoRows
-- means "does not exist".
SELECT id, name FROM products.product_categories WHERE id = @id;

-- name: ListCategoryRefs :many
-- ListCategoryRefs batch-loads category references for a page of products
-- (GetProductsEndpoint), avoiding one query per row.
SELECT id, name FROM products.product_categories WHERE id = ANY(@ids::int[]);

-- name: ListCategoryParents :many
-- ListCategoryParents is the whole id->parentId adjacency list GetProducts
-- resolves categoryId+descendants filtering from in Go
-- (ProductCategoryHierarchy.GetSelfAndDescendantIds), the same "catalog is
-- assumed small, no pagination" premise the .NET code documents.
SELECT id, parent_id FROM products.product_categories;

-- name: GetTaxCategoryRef :one
-- GetTaxCategoryRef is the tax category a product embeds
-- (ProductTaxCategoryResponse), and doubles as the taxCategoryId existence
-- check CreateProductEndpoint/UpdateProductEndpoint run before saving.
SELECT id, name, kind, rate FROM products.tax_categories WHERE id = @id;

-- name: ListTaxCategoryRefs :many
-- ListTaxCategoryRefs batch-loads tax category references for a page of
-- products (GetProductsEndpoint).
SELECT id, name, kind, rate FROM products.tax_categories WHERE id = ANY(@ids::int[]);

-- name: CountProducts :one
-- CountProducts is the total row count GetProductsEndpoint paginates over,
-- the same filters the three ListProductsBy* queries below apply.
SELECT count(*)
FROM products.products p
WHERE (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
  AND (sqlc.narg(category_ids)::int[] IS NULL OR p.category_id = ANY(sqlc.narg(category_ids)::int[]))
  AND (NOT @uncategorized::bool OR p.category_id IS NULL)
  AND (sqlc.narg(search_pattern)::text IS NULL OR (
        p.name ILIKE sqlc.narg(search_pattern)::text OR
        (p.description IS NOT NULL AND p.description ILIKE sqlc.narg(search_pattern)::text) OR
        EXISTS (SELECT 1 FROM products.product_variants v
                 WHERE v.product_id = p.id
                   AND (v.sku ILIKE sqlc.narg(search_pattern)::text OR v.barcode = sqlc.narg(search_exact)::text))
      ));

-- name: ListProductsByID :many
-- ListProductsByID is GetProducts's default sort (id, ascending unless
-- descending is requested), one page of product rows
-- (GetProductsEndpoint.cs:136-148's SortFields.Id branch).
SELECT p.id, p.name, p.description, p.category_id, p.type, p.status, p.tax_category_id, p.created_at, p.updated_at
FROM products.products p
WHERE (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
  AND (sqlc.narg(category_ids)::int[] IS NULL OR p.category_id = ANY(sqlc.narg(category_ids)::int[]))
  AND (NOT @uncategorized::bool OR p.category_id IS NULL)
  AND (sqlc.narg(search_pattern)::text IS NULL OR (
        p.name ILIKE sqlc.narg(search_pattern)::text OR
        (p.description IS NOT NULL AND p.description ILIKE sqlc.narg(search_pattern)::text) OR
        EXISTS (SELECT 1 FROM products.product_variants v
                 WHERE v.product_id = p.id
                   AND (v.sku ILIKE sqlc.narg(search_pattern)::text OR v.barcode = sqlc.narg(search_exact)::text))
      ))
ORDER BY
    CASE WHEN NOT @descending::bool THEN p.id END ASC,
    CASE WHEN @descending::bool THEN p.id END DESC
LIMIT @page_size::int OFFSET @row_offset::int;

-- name: ListProductsByName :many
-- ListProductsByName is GetProducts's sortBy=name path: name first, id as
-- the tie-break both directions (.NET's ThenBy(p => p.Id) /
-- ThenByDescending(p => p.Id)).
SELECT p.id, p.name, p.description, p.category_id, p.type, p.status, p.tax_category_id, p.created_at, p.updated_at
FROM products.products p
WHERE (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
  AND (sqlc.narg(category_ids)::int[] IS NULL OR p.category_id = ANY(sqlc.narg(category_ids)::int[]))
  AND (NOT @uncategorized::bool OR p.category_id IS NULL)
  AND (sqlc.narg(search_pattern)::text IS NULL OR (
        p.name ILIKE sqlc.narg(search_pattern)::text OR
        (p.description IS NOT NULL AND p.description ILIKE sqlc.narg(search_pattern)::text) OR
        EXISTS (SELECT 1 FROM products.product_variants v
                 WHERE v.product_id = p.id
                   AND (v.sku ILIKE sqlc.narg(search_pattern)::text OR v.barcode = sqlc.narg(search_exact)::text))
      ))
ORDER BY
    CASE WHEN NOT @descending::bool THEN p.name END ASC,
    CASE WHEN @descending::bool THEN p.name END DESC,
    CASE WHEN NOT @descending::bool THEN p.id END ASC,
    CASE WHEN @descending::bool THEN p.id END DESC
LIMIT @page_size::int OFFSET @row_offset::int;

-- name: ListProductsBySku :many
-- ListProductsBySku is GetProducts's sortBy=sku path
-- (GetProductsEndpoint.cs:144-145): ascending orders by each product's
-- lowest-SKU variant, descending by its highest-SKU variant — not
-- symmetric, but a faithful port of .NET's own asymmetry
-- (query.OrderBy(Min) for asc, OrderByDescending(Max) for desc).
SELECT p.id, p.name, p.description, p.category_id, p.type, p.status, p.tax_category_id, p.created_at, p.updated_at
FROM products.products p
LEFT JOIN (
    SELECT product_id, min(sku) AS min_sku, max(sku) AS max_sku
    FROM products.product_variants
    GROUP BY product_id
) v ON v.product_id = p.id
WHERE (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
  AND (sqlc.narg(category_ids)::int[] IS NULL OR p.category_id = ANY(sqlc.narg(category_ids)::int[]))
  AND (NOT @uncategorized::bool OR p.category_id IS NULL)
  AND (sqlc.narg(search_pattern)::text IS NULL OR (
        p.name ILIKE sqlc.narg(search_pattern)::text OR
        (p.description IS NOT NULL AND p.description ILIKE sqlc.narg(search_pattern)::text) OR
        EXISTS (SELECT 1 FROM products.product_variants pv
                 WHERE pv.product_id = p.id
                   AND (pv.sku ILIKE sqlc.narg(search_pattern)::text OR pv.barcode = sqlc.narg(search_exact)::text))
      ))
ORDER BY
    CASE WHEN NOT @descending::bool THEN v.min_sku END ASC,
    CASE WHEN @descending::bool THEN v.max_sku END DESC,
    CASE WHEN NOT @descending::bool THEN p.id END ASC,
    CASE WHEN @descending::bool THEN p.id END DESC
LIMIT @page_size::int OFFSET @row_offset::int;

-- name: ProductExists :one
-- ProductExists is AddProductVariantEndpoint's 404 pre-check
-- (AddProductVariantEndpoint.cs:40-43).
SELECT EXISTS(SELECT 1 FROM products.products WHERE id = @id);

-- name: InsertProductVariant :one
-- InsertProductVariant creates a variant row scoped to product_id. Used both
-- by product creation (one or more variants in one transaction) and by
-- AddProductVariantEndpoint (one variant onto an existing product).
INSERT INTO products.product_variants (
    product_id, sku, barcode, unit, standard_cost, weight_kg, length_cm, width_cm, height_cm, option_values,
    created_at, updated_at
) VALUES (
    @product_id, @sku, @barcode, @unit, @standard_cost, @weight_kg, @length_cm, @width_cm, @height_cm, @option_values,
    @now::timestamptz, @now::timestamptz
)
RETURNING id, product_id, sku, barcode, unit, standard_cost, weight_kg, length_cm, width_cm, height_cm,
          option_values, created_at, updated_at;

-- name: GetVariantByProductAndID :one
-- GetVariantByProductAndID is the scoped lookup every variant/price handler
-- performs first: a variant is always addressed through its product
-- (UpdateProductVariantEndpoint.cs:23-25, DeleteProductVariantEndpoint.cs:14).
SELECT id, product_id, sku, barcode, unit, standard_cost, weight_kg, length_cm, width_cm, height_cm,
       option_values, created_at, updated_at
FROM products.product_variants
WHERE id = @id AND product_id = @product_id;

-- name: ListVariantsByProduct :many
-- ListVariantsByProduct lists one product's variants, ordered by id
-- (GetProductVariantsEndpoint.cs:20-24).
SELECT id, product_id, sku, barcode, unit, standard_cost, weight_kg, length_cm, width_cm, height_cm,
       option_values, created_at, updated_at
FROM products.product_variants
WHERE product_id = @product_id
ORDER BY id;

-- name: ListVariantsByProductIDs :many
-- ListVariantsByProductIDs batch-loads variants for a page of products
-- (GetProductsEndpoint), ordered by product then id.
SELECT id, product_id, sku, barcode, unit, standard_cost, weight_kg, length_cm, width_cm, height_cm,
       option_values, created_at, updated_at
FROM products.product_variants
WHERE product_id = ANY(@product_ids::int[])
ORDER BY product_id, id;

-- name: GetProductStatus :one
-- GetProductStatus is UpdateProductVariantEndpoint's fresh status read
-- (:31-34), re-queried per request rather than trusting a cached value,
-- since SKU immutability depends on it.
SELECT status FROM products.products WHERE id = @id;

-- name: VariantSkuExists :one
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE sku = @sku);

-- name: VariantSkuExistsExcluding :one
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE sku = @sku AND id != @id);

-- name: VariantAnySkuExists :one
-- VariantAnySkuExists is CreateProductEndpoint's catalog-wide duplicate-SKU
-- check for a whole batch of new variants at once (:48-49).
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE sku = ANY(@skus::text[]));

-- name: VariantBarcodeExists :one
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE barcode = @barcode);

-- name: VariantBarcodeExistsExcluding :one
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE barcode = @barcode AND id != @id);

-- name: VariantAnyBarcodeExists :one
-- VariantAnyBarcodeExists is CreateProductEndpoint's catalog-wide
-- duplicate-barcode check for a whole batch of new variants at once
-- (:60-61).
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE barcode IS NOT NULL AND barcode = ANY(@barcodes::text[]));

-- name: UpdateProductVariant :one
-- UpdateProductVariant applies PUT .../variants/{variantId}'s validated
-- fields (UpdateProductVariantEndpoint.cs:55-64).
UPDATE products.product_variants
SET sku = @sku,
    barcode = @barcode,
    unit = @unit,
    standard_cost = @standard_cost,
    weight_kg = @weight_kg,
    length_cm = @length_cm,
    width_cm = @width_cm,
    height_cm = @height_cm,
    option_values = @option_values,
    updated_at = @updated_at::timestamptz
WHERE id = @id
RETURNING id, product_id, sku, barcode, unit, standard_cost, weight_kg, length_cm, width_cm, height_cm,
          option_values, created_at, updated_at;

-- name: CountOtherVariantsOfProduct :one
-- CountOtherVariantsOfProduct is DeleteProductVariantEndpoint's last-variant
-- guard (:20-23): whether the product has any variant besides the one being
-- deleted.
SELECT count(*) FROM products.product_variants WHERE product_id = @product_id AND id != @id;

-- name: DeleteProductVariant :exec
DELETE FROM products.product_variants WHERE id = @id;

-- name: InsertProductPrice :one
-- InsertProductPrice appends a price row to a variant
-- (AddProductPriceEndpoint.cs:49-50, UpdateProductPriceEndpoint applies its
-- fields onto an existing row instead — see UpdatePrice below).
INSERT INTO products.product_prices (variant_id, currency, amount, valid_from, valid_to)
VALUES (@variant_id, @currency, @amount, @valid_from, @valid_to)
RETURNING id, variant_id, currency, amount, valid_from, valid_to;

-- name: ListPricesByVariant :many
-- ListPricesByVariant lists one variant's price rows, ordered by currency
-- then validFrom then id (GetProductPricesEndpoint.cs:25-31).
SELECT id, variant_id, currency, amount, valid_from, valid_to
FROM products.product_prices
WHERE variant_id = @variant_id
ORDER BY currency, valid_from, id;

-- name: ListPricesByVariantIDs :many
-- ListPricesByVariantIDs batch-loads prices for a page of products'
-- variants (GetProductsEndpoint), same ordering as ListPricesByVariant.
SELECT id, variant_id, currency, amount, valid_from, valid_to
FROM products.product_prices
WHERE variant_id = ANY(@variant_ids::int[])
ORDER BY variant_id, currency, valid_from, id;

-- name: GetPriceScoped :one
-- GetPriceScoped is UpdateProductPriceEndpoint's/DeleteProductPriceEndpoint's
-- doubly-scoped lookup: the price must belong to the given variant, and that
-- variant to the given product (UpdateProductPriceEndpoint.cs:34-42,
-- DeleteProductPriceEndpoint.cs:20-22).
SELECT pr.id, pr.variant_id, pr.currency, pr.amount, pr.valid_from, pr.valid_to
FROM products.product_prices pr
JOIN products.product_variants v ON v.id = pr.variant_id
WHERE pr.id = @price_id AND pr.variant_id = @variant_id AND v.product_id = @product_id;

-- name: UpdatePrice :one
-- UpdatePrice applies PUT .../prices/{priceId}'s validated fields in place
-- (UpdateProductPriceEndpoint.cs:54-58).
UPDATE products.product_prices
SET currency = @currency, amount = @amount, valid_from = @valid_from, valid_to = @valid_to
WHERE id = @id
RETURNING id, variant_id, currency, amount, valid_from, valid_to;

-- name: DeleteProductPrice :exec
DELETE FROM products.product_prices WHERE id = @id;
