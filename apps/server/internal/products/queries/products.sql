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
-- VariantSkuExists is AddProductVariantEndpoint's catalog-wide duplicate-SKU
-- check for a single new variant (:46).
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE sku = @sku);

-- name: VariantSkuExistsExcluding :one
-- VariantSkuExistsExcluding is UpdateProductVariantEndpoint's duplicate-SKU
-- check, excluding the variant being renamed (:44).
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE sku = @sku AND id != @id);

-- name: VariantAnySkuExists :one
-- VariantAnySkuExists is CreateProductEndpoint's catalog-wide duplicate-SKU
-- check for a whole batch of new variants at once (:48-49).
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE sku = ANY(@skus::text[]));

-- name: VariantBarcodeExists :one
-- VariantBarcodeExists is AddProductVariantEndpoint's catalog-wide
-- duplicate-barcode check for a single new variant (:51).
SELECT EXISTS(SELECT 1 FROM products.product_variants WHERE barcode = @barcode);

-- name: VariantBarcodeExistsExcluding :one
-- VariantBarcodeExistsExcluding is UpdateProductVariantEndpoint's
-- duplicate-barcode check, excluding the variant being updated (:50), which
-- runs unconditionally even when the SKU did not change.
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
-- DeleteProductVariant is DeleteProductVariantEndpoint's own delete
-- (:25-26), run only once the last-variant guard above has passed.
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
-- DeleteProductPrice is DeleteProductPriceEndpoint's own delete (:29-30):
-- no "last price" rule, a variant may end up with zero prices.
DELETE FROM products.product_prices WHERE id = @id;

-- Categories (Task 12, EP/Categories/*Endpoint.cs). GetCategoryRef and
-- ListCategoryParents above already cover the compact ref a product embeds
-- and the whole id->parentId adjacency list category CRUD reuses for
-- existence checks and WouldCreateCycle.

-- name: InsertCategory :one
-- InsertCategory is CreateCategoryEndpoint.cs:46-53.
INSERT INTO products.product_categories (name, parent_id)
VALUES (@name, @parent_id)
RETURNING id, name, parent_id;

-- name: GetCategoryByID :one
-- GetCategoryByID is GetCategoryEndpoint's/UpdateCategoryEndpoint's/
-- DeleteCategoryEndpoint's own-row lookup (the full row, parent_id
-- included — unlike GetCategoryRef, which only serves a product's embedded
-- reference).
SELECT id, name, parent_id FROM products.product_categories WHERE id = @id;

-- name: ListCategoriesOrdered :many
-- ListCategoriesOrdered is GetCategoriesEndpoint.cs:20-24: the flat
-- adjacency list, ordered by name then id.
SELECT id, name, parent_id FROM products.product_categories ORDER BY name, id;

-- name: ListCategoryProductCounts :many
-- ListCategoryProductCounts is GetCategoriesEndpoint.cs:26-30's direct
-- (not subtree) product count per category; a category with none is
-- simply absent from the result, GetValueOrDefault's 0 (categories.go
-- fills the gap).
SELECT category_id AS id, count(*) AS count
FROM products.products
WHERE category_id IS NOT NULL
GROUP BY category_id;

-- name: CategoryExists :one
-- CategoryExists is CreateCategoryEndpoint's parentId existence pre-check
-- (:27-28).
SELECT EXISTS(SELECT 1 FROM products.product_categories WHERE id = @id);

-- name: CategorySiblingNameExists :one
-- CategorySiblingNameExists is CreateCategoryEndpoint's duplicate-sibling
-- check (:36-38). IS NOT DISTINCT FROM, not =, so two root categories
-- (parent_id NULL on both sides) collide the same way the table's own
-- NULLS NOT DISTINCT unique index does (products inventory §3).
SELECT EXISTS(
    SELECT 1 FROM products.product_categories
    WHERE parent_id IS NOT DISTINCT FROM @parent_id AND name = @name
);

-- name: CategorySiblingNameExistsExcluding :one
-- CategorySiblingNameExistsExcluding is UpdateCategoryEndpoint's duplicate-
-- sibling check, excluding the category being renamed (:66-68).
SELECT EXISTS(
    SELECT 1 FROM products.product_categories
    WHERE parent_id IS NOT DISTINCT FROM @parent_id AND name = @name AND id != @id
);

-- name: CategoryHasSubcategories :one
-- CategoryHasSubcategories is DeleteCategoryEndpoint's first guard (:27-28).
-- The explicit cast keeps id a plain (never-null) int32 in Go: it is always
-- a real category's own id, even though it is compared against the
-- nullable parent_id column.
SELECT EXISTS(SELECT 1 FROM products.product_categories WHERE parent_id = @id::int);

-- name: CategoryHasProducts :one
-- CategoryHasProducts is DeleteCategoryEndpoint's second guard (:35-36).
SELECT EXISTS(SELECT 1 FROM products.products WHERE category_id = @id::int);

-- name: UpdateCategory :one
-- UpdateCategory applies UpdateCategoryEndpoint's validated name/parentId
-- (:76-78).
UPDATE products.product_categories
SET name = @name, parent_id = @parent_id
WHERE id = @id
RETURNING id, name, parent_id;

-- name: DeleteCategory :exec
DELETE FROM products.product_categories WHERE id = @id;

-- Tax categories (Task 12, EP/TaxCategories/*Endpoint.cs). GetTaxCategoryRef
-- above already covers the compact ref a product embeds.

-- name: InsertTaxCategory :one
-- InsertTaxCategory is CreateTaxCategoryEndpoint.cs:31-33 (TaxCategoryRequest.ToDomain).
INSERT INTO products.tax_categories (name, kind, rate, created_at, updated_at)
VALUES (@name, @kind, @rate, @now::timestamptz, @now::timestamptz)
RETURNING id, name, kind, rate, created_at, updated_at;

-- name: ListTaxCategoriesOrdered :many
-- ListTaxCategoriesOrdered is GetTaxCategoriesEndpoint.cs:15-19: ordered by
-- name.
SELECT id, name, kind, rate FROM products.tax_categories ORDER BY name;

-- name: TaxCategoryNameExists :one
-- TaxCategoryNameExists is CreateTaxCategoryEndpoint's duplicate-name check
-- (:23).
SELECT EXISTS(SELECT 1 FROM products.tax_categories WHERE name = @name);

-- name: TaxCategoryNameExistsExcluding :one
-- TaxCategoryNameExistsExcluding is UpdateTaxCategoryEndpoint's
-- duplicate-name check, excluding the category being renamed (:31-32).
SELECT EXISTS(SELECT 1 FROM products.tax_categories WHERE name = @name AND id != @id);

-- name: TaxCategoryHasProducts :one
-- TaxCategoryHasProducts is DeleteTaxCategoryEndpoint's guard (:21-22).
SELECT EXISTS(SELECT 1 FROM products.products WHERE tax_category_id = @id);

-- name: UpdateTaxCategory :one
-- UpdateTaxCategory applies UpdateTaxCategoryEndpoint's validated fields
-- (:40-43).
UPDATE products.tax_categories
SET name = @name, kind = @kind, rate = @rate, updated_at = @updated_at::timestamptz
WHERE id = @id
RETURNING id, name, kind, rate, created_at, updated_at;

-- name: DeleteTaxCategory :exec
DELETE FROM products.tax_categories WHERE id = @id;

-- Stats (Task 12, EP/ProductStatsEndpoints.cs). Mirrors the customers
-- module's own CustomerStatsSummaryCustomerCounts/CustomerCreationBuckets
-- query shape, the completed broader template.

-- name: ProductStatsSummaryCounts :one
-- ProductStatsSummaryCounts is ProductStatsEndpoints.Summary's four product
-- counts (:42-49): previous_from..period_from is the immediately preceding
-- window of the same length as [period_from, period_to).
SELECT
    count(*) FILTER (WHERE status = 'Active') AS active,
    count(*) FILTER (WHERE status = 'Active' AND created_at < @period_from::timestamptz) AS active_at_period_start,
    count(*) FILTER (WHERE created_at >= @period_from::timestamptz AND created_at < @period_to::timestamptz) AS new_products,
    count(*) FILTER (WHERE created_at >= @previous_from::timestamptz AND created_at < @period_from::timestamptz) AS previous_new_products
FROM products.products;

-- name: ProductStatusCounts :many
-- ProductStatusCounts is ProductStatsEndpoints.Summary's current
-- per-status counts (:51-54), every status present in the table.
SELECT status, count(*) AS value FROM products.products GROUP BY status;

-- name: ProductStatusCountsBefore :many
-- ProductStatusCountsBefore is ProductStatsEndpoints.Summary's per-status
-- counts as of period.From (:55-59), used to compute each status's delta.
SELECT status, count(*) AS value
FROM products.products
WHERE created_at < @before::timestamptz
GROUP BY status;

-- name: ProductCreationBuckets :many
-- ProductCreationBuckets is the timeseries's newProducts metric
-- (ProductStatsEndpoints.cs:92-97): one row per UTC calendar day with at
-- least one product created in [range_from, range_to).
SELECT (created_at AT TIME ZONE 'UTC')::date AS day, count(*) AS value
FROM products.products
WHERE created_at >= @range_from::timestamptz AND created_at < @range_to::timestamptz
GROUP BY day
ORDER BY day;

-- Catalog (Task 3, contracts.ProductCatalog): the read-only cross-module
-- view of a variant, joined with its product for the fields another module
-- may reference — never the detailed attributes (barcode, weight,
-- dimensions) that stay behind products' own contract and permissions.

-- name: CatalogVariant :one
-- CatalogVariant is contracts.ProductCatalog.Variant's lookup.
SELECT v.id, v.product_id, p.name AS product_name, v.sku, v.unit, p.type AS product_type, p.status AS product_status
FROM products.product_variants v
JOIN products.products p ON p.id = v.product_id
WHERE v.id = @id;

-- name: CatalogVariants :many
-- CatalogVariants batch-loads the same fields as CatalogVariant for a set of
-- ids; an id that does not exist is simply absent from the result.
SELECT v.id, v.product_id, p.name AS product_name, v.sku, v.unit, p.type AS product_type, p.status AS product_status
FROM products.product_variants v
JOIN products.products p ON p.id = v.product_id
WHERE v.id = ANY(@ids::int[]);
