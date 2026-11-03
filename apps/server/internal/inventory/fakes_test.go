package inventory_test

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
)

// fakeCatalog is contracts.ProductCatalog over the variants a test seeds,
// in place of Products, which depguard keeps out of this package. It records
// every LockVariants call — the ids and whether a transaction was given — so
// a test can prove a posting locks its variants first, in id order.
type fakeCatalog struct {
	mu       sync.Mutex
	variants map[int32]contracts.VariantEntry
	locks    [][]int32
	err      error
}

var _ contracts.ProductCatalog = (*fakeCatalog)(nil)

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{variants: map[int32]contracts.VariantEntry{}}
}

// seed adds or replaces variants.
func (c *fakeCatalog) seed(entries ...contracts.VariantEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		c.variants[e.ID] = e
	}
}

// remove forgets a variant, as a delete in Products would.
func (c *fakeCatalog) remove(id int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.variants, id)
}

// lockCalls is every LockVariants call's ids, in order.
func (c *fakeCatalog) lockCalls() [][]int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.locks)
}

// sorted is the seeded variants by id.
func (c *fakeCatalog) sorted() []contracts.VariantEntry {
	out := make([]contracts.VariantEntry, 0, len(c.variants))
	for _, v := range c.variants {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b contracts.VariantEntry) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (c *fakeCatalog) Variant(ctx context.Context, id int32) (*contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.Variant")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	v, ok := c.variants[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (c *fakeCatalog) Variants(ctx context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.Variants")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	var out []contracts.VariantEntry
	for _, id := range ids {
		if v, ok := c.variants[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (c *fakeCatalog) ListPrice(ctx context.Context, _ int32, _ string, _ time.Time) (*contracts.Money, error) {
	lockedContractCalls.note(ctx, "fake Products.ListPrice")
	return nil, nil
}

func (c *fakeCatalog) VariantsOfProduct(ctx context.Context, productID int32) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.VariantsOfProduct")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	var out []contracts.VariantEntry
	for _, v := range c.sorted() {
		if v.ProductID == productID {
			out = append(out, v)
		}
	}
	return out, nil
}

func (c *fakeCatalog) LookupCode(ctx context.Context, code string) ([]contracts.CodeMatch, error) {
	lockedContractCalls.note(ctx, "fake Products.LookupCode")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	var out []contracts.CodeMatch
	for _, v := range c.sorted() {
		if strings.EqualFold(v.SKU, code) || (v.Barcode != nil && *v.Barcode == code) {
			out = append(out, contracts.CodeMatch{Variant: v})
		}
	}
	return out, nil
}

func (c *fakeCatalog) SearchVariants(ctx context.Context, q string, limit int) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.SearchVariants")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	q = strings.ToLower(q)
	var out []contracts.VariantEntry
	for _, v := range c.sorted() {
		if v.ProductType != "Goods" {
			continue
		}
		if strings.Contains(strings.ToLower(v.SKU), q) || strings.Contains(strings.ToLower(v.ProductName), q) ||
			(v.Barcode != nil && strings.Contains(strings.ToLower(*v.Barcode), q)) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b contracts.VariantEntry) int { return cmp.Compare(a.SKU, b.SKU) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// LockVariants answers the seeded variants in id order and records the call.
// It takes no lock: the race it guards is Products', tested there.
func (c *fakeCatalog) LockVariants(ctx context.Context, tx pgx.Tx, ids []int32) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, inventory.LockVariantsCall)
	c.mu.Lock()
	defer c.mu.Unlock()
	if tx == nil {
		panic("LockVariants without a transaction")
	}
	c.locks = append(c.locks, slices.Clone(ids))
	if c.err != nil {
		return nil, c.err
	}
	sortedIDs := slices.Clone(ids)
	slices.Sort(sortedIDs)
	var out []contracts.VariantEntry
	for _, id := range slices.Compact(sortedIDs) {
		if v, ok := c.variants[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// goods is a seeded goods variant of product productID, active.
func goods(id, productID int32, sku, name, unit string) contracts.VariantEntry {
	return contracts.VariantEntry{ID: id, ProductID: productID, SKU: sku, ProductName: name, Unit: unit, ProductType: "Goods", ProductStatus: "Active"}
}
