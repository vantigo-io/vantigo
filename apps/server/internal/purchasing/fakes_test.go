package purchasing_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// fakeCatalog is contracts.ProductCatalog over the variants a test seeds, in
// place of Products, which depguard keeps out of this package. It records
// every LockVariants call. The calls Purchasing never makes are the embedded
// interface's, nil, and would panic.
type fakeCatalog struct {
	contracts.ProductCatalog
	mu       sync.Mutex
	variants map[int32]contracts.VariantEntry
	locks    [][]int32
}

func newFakeCatalog() *fakeCatalog { return &fakeCatalog{variants: map[int32]contracts.VariantEntry{}} }

func (c *fakeCatalog) seed(entries ...contracts.VariantEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		c.variants[e.ID] = e
	}
}

func (c *fakeCatalog) update(id int32, fn func(*contracts.VariantEntry)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.variants[id]
	fn(&v)
	c.variants[id] = v
}

func (c *fakeCatalog) lockCalls() [][]int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.locks)
}

func (c *fakeCatalog) Variant(ctx context.Context, id int32) (*contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.Variant")
	c.mu.Lock()
	defer c.mu.Unlock()
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
	var out []contracts.VariantEntry
	for _, id := range ids {
		if v, ok := c.variants[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (c *fakeCatalog) LookupSKUs(ctx context.Context, skus []string) (map[string][]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.LookupSKUs")
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string][]contracts.VariantEntry{}
	for _, sku := range skus {
		for _, v := range c.variants {
			if strings.EqualFold(v.SKU, sku) {
				out[sku] = append(out[sku], v)
			}
		}
	}
	return out, nil
}

func (c *fakeCatalog) LookupCode(ctx context.Context, code string) ([]contracts.CodeMatch, error) {
	lockedContractCalls.note(ctx, "fake Products.LookupCode")
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []contracts.CodeMatch
	for _, v := range c.variants {
		if v.Barcode != nil && *v.Barcode == code {
			out = append(out, contracts.CodeMatch{Variant: v})
		}
		for _, p := range v.Packagings {
			if p.Barcode != nil && *p.Barcode == code {
				pk := p
				out = append(out, contracts.CodeMatch{Variant: v, Packaging: &pk})
			}
		}
	}
	return out, nil
}

func (c *fakeCatalog) SearchVariants(ctx context.Context, q string, s contracts.VariantSearch) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.SearchVariants")
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []contracts.VariantEntry
	for _, v := range c.variants {
		if q != "" && !strings.Contains(strings.ToLower(v.SKU+" "+v.ProductName), strings.ToLower(q)) {
			continue
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b contracts.VariantEntry) int { return strings.Compare(a.SKU, b.SKU) })
	if s.Limit > 0 && len(out) > s.Limit {
		out = out[:s.Limit]
	}
	return out, nil
}

func (c *fakeCatalog) LockVariants(ctx context.Context, tx pgx.Tx, ids []int32) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, purchasing.LockVariantsCall)
	if tx == nil {
		panic("LockVariants without a transaction")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.locks = append(c.locks, slices.Clone(ids))
	var out []contracts.VariantEntry
	for _, id := range ids {
		if v, ok := c.variants[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// fakeDirectory is contracts.SupplierDirectory over the suppliers a test
// seeds, in place of Accounting. It records every LockSuppliers call, which
// must come after a LockVariants of the same transaction when there is one.
type fakeDirectory struct {
	mu        sync.Mutex
	suppliers map[int64]contracts.SupplierEntry
	locks     [][]int64
}

var _ contracts.SupplierDirectory = (*fakeDirectory)(nil)

func newFakeDirectory() *fakeDirectory {
	return &fakeDirectory{suppliers: map[int64]contracts.SupplierEntry{}}
}

func (d *fakeDirectory) seed(entries ...contracts.SupplierEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range entries {
		d.suppliers[e.ID] = e
	}
}

func (d *fakeDirectory) update(id int64, fn func(*contracts.SupplierEntry)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.suppliers[id]
	fn(&e)
	d.suppliers[id] = e
}

func (d *fakeDirectory) lockCalls() [][]int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.locks)
}

func (d *fakeDirectory) Supplier(ctx context.Context, id int64) (*contracts.SupplierEntry, error) {
	lockedContractCalls.note(ctx, "fake SupplierDirectory.Supplier")
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.suppliers[id]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (d *fakeDirectory) Suppliers(ctx context.Context, ids []int64) ([]contracts.SupplierEntry, error) {
	lockedContractCalls.note(ctx, "fake SupplierDirectory.Suppliers")
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []contracts.SupplierEntry
	for _, id := range ids {
		if e, ok := d.suppliers[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func (d *fakeDirectory) SearchSuppliers(ctx context.Context, q string, limit int) ([]contracts.SupplierEntry, error) {
	lockedContractCalls.note(ctx, "fake SupplierDirectory.SearchSuppliers")
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []contracts.SupplierEntry
	for _, e := range d.suppliers {
		if q == "" || strings.Contains(strings.ToLower(e.Name), strings.ToLower(q)) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b contracts.SupplierEntry) int { return int(a.Number - b.Number) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (d *fakeDirectory) LockSuppliers(ctx context.Context, tx pgx.Tx, ids []int64) ([]contracts.SupplierEntry, error) {
	lockedContractCalls.note(ctx, purchasing.LockSuppliersCall)
	if tx == nil {
		panic("LockSuppliers without a transaction")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.locks = append(d.locks, slices.Clone(ids))
	var out []contracts.SupplierEntry
	for _, id := range ids {
		if e, ok := d.suppliers[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// fakeStock is contracts.Stock's site list, in place of Inventory.
type fakeStock struct {
	contracts.Stock
	sites []contracts.StockSite
	// holding is what Stock.Locations answers (receipts_fakes_test.go).
	holding []contracts.StockLocation
}

func (f *fakeStock) Sites(ctx context.Context) ([]contracts.StockSite, error) {
	lockedContractCalls.note(ctx, "fake Stock.Sites")
	return slices.Clone(f.sites), nil
}

// fakeSalesTax is contracts.SalesTax's seller, in place of Invoices.
type fakeSalesTax struct {
	contracts.SalesTax
	seller contracts.SellerProfile
}

func (f *fakeSalesTax) Seller(ctx context.Context) (contracts.SellerProfile, error) {
	lockedContractCalls.note(ctx, "fake SalesTax.Seller")
	return f.seller, nil
}

// fakeObjectStore is an in-memory storage.ObjectStore; failPuts makes the
// next puts fail.
type fakeObjectStore struct {
	mu       sync.Mutex
	objects  map[string][]byte
	failPuts int
}

// failNextPuts makes the next n puts fail.
func (s *fakeObjectStore) failNextPuts(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failPuts = n
}

var _ storage.ObjectStore = (*fakeObjectStore)(nil)

func newFakeObjectStore() *fakeObjectStore { return &fakeObjectStore{objects: map[string][]byte{}} }

func (s *fakeObjectStore) Put(ctx context.Context, key string, r io.Reader, _ string) error {
	lockedContractCalls.note(ctx, "fake Storage.Put")
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failPuts > 0 {
		s.failPuts--
		return errors.New("fake store: put refused")
	}
	s.objects[key] = body
	return nil
}

func (s *fakeObjectStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	lockedContractCalls.note(ctx, "fake Storage.Get")
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (s *fakeObjectStore) Exists(ctx context.Context, key string) (bool, error) {
	lockedContractCalls.note(ctx, "fake Storage.Exists")
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}

func (s *fakeObjectStore) Delete(ctx context.Context, key string) error {
	lockedContractCalls.note(ctx, "fake Storage.Delete")
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *fakeObjectStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}
