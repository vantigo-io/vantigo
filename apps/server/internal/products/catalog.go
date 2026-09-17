package products

import (
	"context"
	"errors"
	"fmt"
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
	entry := contracts.VariantEntry{
		ID: row.ID, ProductID: row.ProductID, ProductName: row.ProductName,
		SKU: row.Sku, Unit: row.Unit, ProductType: row.ProductType, ProductStatus: row.ProductStatus,
	}
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
		entries = append(entries, contracts.VariantEntry{
			ID: row.ID, ProductID: row.ProductID, ProductName: row.ProductName,
			SKU: row.Sku, Unit: row.Unit, ProductType: row.ProductType, ProductStatus: row.ProductStatus,
		})
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
