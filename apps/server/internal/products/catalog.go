package products

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// catalog is this module's contracts.ProductCatalog, the one sanctioned way
// another module reads products' catalog data. It is read-only and holds
// nothing but the queries: Compose builds it once, before any module
// mounts, and hands it to every module including this one.
type catalog struct {
	q *store.Queries
}

var _ contracts.ProductCatalog = (*catalog)(nil)

// newCatalog is Module's Products: the constructor Compose calls with the
// dependencies it was given.
func newCatalog(d module.Deps) contracts.ProductCatalog {
	return &catalog{q: store.New(d.Pool)}
}

// Variant looks up a variant by id.
func (c *catalog) Variant(ctx context.Context, id int32) (*contracts.VariantEntry, error) {
	row, err := c.q.CatalogVariant(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: catalog variant: %w", err)
	}
	entry := variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus)
	return &entry, nil
}

// Variants looks up variants by id. An id that does not exist is simply
// absent from the result, not an error. An empty ids answers an empty
// result without querying: ANY($1) on an empty array is a valid but
// pointless round trip.
func (c *catalog) Variants(ctx context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := c.q.CatalogVariants(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("products: catalog variants: %w", err)
	}
	entries := make([]contracts.VariantEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus))
	}
	return entries, nil
}

// ListPrice is the variant's effective price in currency at moment, nil
// when it has none. It loads the variant's price rows with the same query
// the Prices sub-resource uses (ListPricesByVariant), maps them through
// domainPriceFromRow the same way responses.go's variantResponse does, and
// reuses pricing.go's getEffectivePrice — the single source of the
// campaign-beats-base, currency-case-insensitive rule — rather than
// restating it here.
func (c *catalog) ListPrice(ctx context.Context, variantID int32, currency string, at time.Time) (*contracts.Money, error) {
	rows, err := c.q.ListPricesByVariant(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("products: catalog list price: %w", err)
	}
	prices := make([]productPrice, len(rows))
	for i, r := range rows {
		prices[i] = domainPriceFromRow(r)
	}
	price, ok := getEffectivePrice(prices, currency, at)
	if !ok {
		return nil, nil
	}
	return &contracts.Money{Amount: price.Amount, Currency: price.Currency}, nil
}

// variantEntry is a catalog row as the contract answers it; every catalog
// query selects the same columns, each into its own sqlc row type.
func variantEntry(id, productID int32, productName, sku string, barcode *string, unit, productType, productStatus string) contracts.VariantEntry {
	return contracts.VariantEntry{
		ID: id, ProductID: productID, ProductName: productName, SKU: sku, Barcode: barcode,
		Unit: unit, ProductType: productType, ProductStatus: productStatus,
	}
}

// VariantsOfProduct is every variant of the product, by id; empty when the
// product does not exist.
func (c *catalog) VariantsOfProduct(ctx context.Context, productID int32) ([]contracts.VariantEntry, error) {
	rows, err := c.q.CatalogVariantsOfProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("products: catalog variants of product: %w", err)
	}
	entries := make([]contracts.VariantEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus))
	}
	return entries, nil
}

// LookupCode answers every variant whose SKU equals code, ignoring case, or
// whose barcode equals it exactly. Packagings join in Inventory's delivery B.
func (c *catalog) LookupCode(ctx context.Context, code string) ([]contracts.CodeMatch, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, nil
	}
	rows, err := c.q.CatalogLookupCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("products: catalog lookup code: %w", err)
	}
	matches := make([]contracts.CodeMatch, 0, len(rows))
	for _, row := range rows {
		matches = append(matches, contracts.CodeMatch{
			Variant: variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus),
		})
	}
	return matches, nil
}

// searchVariantsMax bounds SearchVariants whatever limit the caller asks.
const searchVariantsMax = 50

// SearchVariants answers at most limit goods variants whose SKU, product
// name or barcode contains q, ignoring case, by SKU, through the product
// list's own likePattern, so LIKE's wildcards in q match literally.
func (c *catalog) SearchVariants(ctx context.Context, q string, limit int) ([]contracts.VariantEntry, error) {
	q = strings.TrimSpace(q)
	if q == "" || limit <= 0 {
		return nil, nil
	}
	limit = min(limit, searchVariantsMax)
	rows, err := c.q.CatalogSearchVariants(ctx, store.CatalogSearchVariantsParams{Pattern: likePattern(q), MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("products: catalog search variants: %w", err)
	}
	entries := make([]contracts.VariantEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus))
	}
	return entries, nil
}

// LockVariants takes FOR SHARE on the variants and their products, in
// variant id order, on the caller's transaction, and answers the entries
// (inventory design I22). An id that does not exist is absent.
func (c *catalog) LockVariants(ctx context.Context, tx pgx.Tx, ids []int32) ([]contracts.VariantEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := store.New(tx).CatalogLockVariants(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("products: catalog lock variants: %w", err)
	}
	entries := make([]contracts.VariantEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus))
	}
	return entries, nil
}
