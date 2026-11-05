package products

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	entries := []contracts.VariantEntry{variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus, row.TaxCategoryID, row.Description, row.CategoryID, row.CategoryName, row.OptionValues, row.WeightKg)}
	if err := withPackagings(ctx, c.q, entries); err != nil {
		return nil, err
	}
	return &entries[0], nil
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
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus, row.TaxCategoryID, row.Description, row.CategoryID, row.CategoryName, row.OptionValues, row.WeightKg))
	}
	if err := withPackagings(ctx, c.q, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// ListPrice is the variant's effective price in currency at moment, nil
// when it has none. It loads the variant's price rows with the same query
// the Prices sub-resource uses (ListPricesByVariant), maps them through
// domainPriceFromRow the same way responses.go's variantResponse does, and
// reuses pricing.go's getEffectivePrice — the single source of the
// campaign-beats-base, currency-case-insensitive rule — rather than
// restating it here. The amount is the chosen row's numeric as exact decimal
// text (orders design O6), never the float the rule compares with.
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
	for _, r := range rows {
		if r.ID == price.ID {
			return &contracts.Money{Amount: ratFromNumeric(r.Amount).FloatString(2), Currency: r.Currency}, nil
		}
	}
	return nil, fmt.Errorf("products: catalog list price: price %d is not among the variant's rows", price.ID)
}

// variantEntry is a catalog row as the contract answers it; every catalog
// query selects the same columns, each into its own sqlc row type.
func variantEntry(id, productID int32, productName, sku string, barcode *string, unit, productType, productStatus string,
	taxCategoryID int32, description *string, categoryID *int32, categoryName *string, optionValues []byte, weightKg pgtype.Numeric,
) contracts.VariantEntry {
	entry := contracts.VariantEntry{
		ID: id, ProductID: productID, ProductName: productName, SKU: sku, Barcode: barcode,
		Unit: unit, ProductType: productType, ProductStatus: productStatus, TaxCategoryID: taxCategoryID,
		CategoryID: categoryID,
	}
	if description != nil {
		entry.Description = *description
	}
	if categoryName != nil {
		entry.CategoryName = *categoryName
	}
	// The weight in grams: weight_kg has three decimals, so it is whole
	// grams exactly. None stays nil.
	if weightKg.Valid {
		if grams := new(big.Rat).Mul(ratFromNumeric(weightKg), big.NewRat(1000, 1)); grams.IsInt() {
			n := grams.Num().Int64()
			entry.WeightGrams = &n
		}
	}
	// The option values as stored, keyed by the option's name as it was
	// typed; none stays nil.
	if values := unmarshalOptionValues(optionValues); len(values) > 0 {
		entry.OptionValues = values
	}
	return entry
}

// withPackagings fills every entry's Packagings, by sort order then id, in
// one query on q — the pool, or LockVariants' caller's transaction, where
// the variants are locked FOR SHARE and so their packagings cannot change
// until it ends (every packaging write takes the variant FOR UPDATE first).
func withPackagings(ctx context.Context, q *store.Queries, entries []contracts.VariantEntry) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]int32, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	rows, err := packagingsByVariant(ctx, q, ids)
	if err != nil {
		return err
	}
	for i := range entries {
		// None stays nil: an empty slice either way, and the entry of a
		// variant without packagings equal to one built without them.
		var list []contracts.PackagingEntry
		for _, p := range rows[entries[i].ID] {
			list = append(list, contracts.PackagingEntry{
				ID: p.ID, Name: p.Name, Kind: p.Kind, Quantity: quantityText(p.Quantity),
				Barcode: p.Barcode, SortOrder: p.SortOrder,
			})
		}
		entries[i].Packagings = list
	}
	return nil
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
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus, row.TaxCategoryID, row.Description, row.CategoryID, row.CategoryName, row.OptionValues, row.WeightKg))
	}
	if err := withPackagings(ctx, c.q, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// LookupCode answers every variant whose SKU equals code, ignoring case, or
// whose barcode equals it exactly, then every packaging whose barcode equals
// it exactly, with the packaging set (inventory design I22, I24).
func (c *catalog) LookupCode(ctx context.Context, code string) ([]contracts.CodeMatch, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, nil
	}
	rows, err := c.q.CatalogLookupCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("products: catalog lookup code: %w", err)
	}
	packagingRows, err := c.q.CatalogLookupPackagingCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("products: catalog lookup packaging code: %w", err)
	}
	entries := make([]contracts.VariantEntry, 0, len(rows)+len(packagingRows))
	for _, row := range rows {
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus, row.TaxCategoryID, row.Description, row.CategoryID, row.CategoryName, row.OptionValues, row.WeightKg))
	}
	for _, row := range packagingRows {
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus, row.TaxCategoryID, row.Description, row.CategoryID, row.CategoryName, row.OptionValues, row.WeightKg))
	}
	if err := withPackagings(ctx, c.q, entries); err != nil {
		return nil, err
	}
	matches := make([]contracts.CodeMatch, 0, len(entries))
	for i, entry := range entries {
		match := contracts.CodeMatch{Variant: entry}
		if i >= len(rows) {
			packagingID := packagingRows[i-len(rows)].PackagingID
			for j := range entry.Packagings {
				if entry.Packagings[j].ID == packagingID {
					match.Packaging = &entry.Packagings[j]
				}
			}
		}
		matches = append(matches, match)
	}
	return matches, nil
}

// lookupSKUsChunk bounds how many SKUs one CatalogLookupSKUs query carries.
const lookupSKUsChunk = 1000

// LookupSKUs answers, for each sku as the caller wrote it, every variant
// whose SKU equals it ignoring case, by id: one query per lookupSKUsChunk
// distinct SKUs and one packagings load for them all. An sku no variant has,
// or a blank one, is absent.
func (c *catalog) LookupSKUs(ctx context.Context, skus []string) (map[string][]contracts.VariantEntry, error) {
	var lowered []string
	seen := map[string]bool{}
	for _, sku := range skus {
		l := strings.ToLower(strings.TrimSpace(sku))
		if l != "" && !seen[l] {
			seen[l] = true
			lowered = append(lowered, l)
		}
	}
	var entries []contracts.VariantEntry
	for start := 0; start < len(lowered); start += lookupSKUsChunk {
		rows, err := c.q.CatalogLookupSKUs(ctx, lowered[start:min(start+lookupSKUsChunk, len(lowered))])
		if err != nil {
			return nil, fmt.Errorf("products: catalog lookup SKUs: %w", err)
		}
		for _, row := range rows {
			entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus, row.TaxCategoryID, row.Description, row.CategoryID, row.CategoryName, row.OptionValues, row.WeightKg))
		}
	}
	if err := withPackagings(ctx, c.q, entries); err != nil {
		return nil, err
	}
	// Each chunk answers by id; the chunks together need not be.
	slices.SortFunc(entries, func(a, b contracts.VariantEntry) int { return cmp.Compare(a.ID, b.ID) })
	byLower := map[string][]contracts.VariantEntry{}
	for _, e := range entries {
		l := strings.ToLower(e.SKU)
		byLower[l] = append(byLower[l], e)
	}
	out := map[string][]contracts.VariantEntry{}
	for _, sku := range skus {
		if found, ok := byLower[strings.ToLower(strings.TrimSpace(sku))]; ok {
			out[sku] = found
		}
	}
	return out, nil
}

// searchVariantsMax bounds SearchVariants whatever limit the caller asks.
const searchVariantsMax = 50

// SearchVariants answers at most s.Limit variants of products of s.Types
// (every type when empty), of active products unless s.IncludeInactive,
// whose SKU, product name or barcode contains q, ignoring case, by SKU,
// through the product list's own likePattern, so LIKE's wildcards in q match
// literally.
func (c *catalog) SearchVariants(ctx context.Context, q string, s contracts.VariantSearch) ([]contracts.VariantEntry, error) {
	q = strings.TrimSpace(q)
	if q == "" || s.Limit <= 0 {
		return nil, nil
	}
	limit := min(s.Limit, searchVariantsMax)
	rows, err := c.q.CatalogSearchVariants(ctx, store.CatalogSearchVariantsParams{
		Types: s.Types, IncludeInactive: s.IncludeInactive, Pattern: likePattern(q), MaxRows: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("products: catalog search variants: %w", err)
	}
	entries := make([]contracts.VariantEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus, row.TaxCategoryID, row.Description, row.CategoryID, row.CategoryName, row.OptionValues, row.WeightKg))
	}
	if err := withPackagings(ctx, c.q, entries); err != nil {
		return nil, err
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
	txq := store.New(tx)
	rows, err := txq.CatalogLockVariants(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("products: catalog lock variants: %w", err)
	}
	entries := make([]contracts.VariantEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, variantEntry(row.ID, row.ProductID, row.ProductName, row.Sku, row.Barcode, row.Unit, row.ProductType, row.ProductStatus, row.TaxCategoryID, row.Description, row.CategoryID, row.CategoryName, row.OptionValues, row.WeightKg))
	}
	if err := withPackagings(ctx, txq, entries); err != nil {
		return nil, err
	}
	return entries, nil
}
