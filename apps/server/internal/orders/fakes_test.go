package orders_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// fakeCatalog is contracts.ProductCatalog over the variants and list prices a
// test seeds, in place of Products, which depguard keeps out of this package.
// It records every LockVariants call, so a test can prove a write locks its
// variants first.
type fakeCatalog struct {
	mu       sync.Mutex
	variants map[int32]contracts.VariantEntry
	prices   map[string]string // variant/currency → amount
	locks    [][]int32
}

var _ contracts.ProductCatalog = (*fakeCatalog)(nil)

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{variants: map[int32]contracts.VariantEntry{}, prices: map[string]string{}}
}

func priceKey(id int32, currency string) string {
	return fmt.Sprintf("%d/%s", id, strings.ToUpper(currency))
}

// seed adds or replaces variants.
func (c *fakeCatalog) seed(entries ...contracts.VariantEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		c.variants[e.ID] = e
	}
}

// setPrice sets a variant's list price in a currency; "" removes it.
func (c *fakeCatalog) setPrice(id int32, currency, amount string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if amount == "" {
		delete(c.prices, priceKey(id, currency))
		return
	}
	c.prices[priceKey(id, currency)] = amount
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

func (c *fakeCatalog) ListPrice(ctx context.Context, id int32, currency string, _ time.Time) (*contracts.Money, error) {
	lockedContractCalls.note(ctx, "fake Products.ListPrice")
	c.mu.Lock()
	defer c.mu.Unlock()
	amount, ok := c.prices[priceKey(id, currency)]
	if !ok {
		return nil, nil
	}
	return &contracts.Money{Amount: amount, Currency: strings.ToUpper(currency)}, nil
}

func (c *fakeCatalog) VariantsOfProduct(ctx context.Context, _ int32) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.VariantsOfProduct")
	return nil, nil
}

func (c *fakeCatalog) LookupCode(ctx context.Context, _ string) ([]contracts.CodeMatch, error) {
	lockedContractCalls.note(ctx, "fake Products.LookupCode")
	return nil, nil
}

func (c *fakeCatalog) LookupSKUs(ctx context.Context, _ []string) (map[string][]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.LookupSKUs")
	return map[string][]contracts.VariantEntry{}, nil
}

func (c *fakeCatalog) SearchVariants(ctx context.Context, _ string, _ contracts.VariantSearch) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, "fake Products.SearchVariants")
	return nil, nil
}

func (c *fakeCatalog) LockVariants(ctx context.Context, tx pgx.Tx, ids []int32) ([]contracts.VariantEntry, error) {
	lockedContractCalls.note(ctx, orders.LockVariantsCall)
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

// fakeSalesTax is contracts.SalesTax over the codes, the category mapping
// and the seller a test seeds, in place of Invoices.
type fakeSalesTax struct {
	mu         sync.Mutex
	codes      []contracts.VATCodeEntry
	categories map[int32]int32 // tax category → code id
	sellerP    contracts.SellerProfile
}

var _ contracts.SalesTax = (*fakeSalesTax)(nil)

func newFakeSalesTax() *fakeSalesTax {
	return &fakeSalesTax{categories: map[int32]int32{}, sellerP: contracts.SellerProfile{
		LegalName: "Vantigo Demo AS", OrganisationNumber: "923609016", VATRegistered: true, AddressLine1: "Storgata 1",
		PostalCode: "0155", City: "Oslo", Country: "no", Email: "post@demo.example",
	}}
}

// mapCategory maps a tax category to a code.
func (f *fakeSalesTax) mapCategory(category, code int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.categories[category] = code
}

func (f *fakeSalesTax) VATCodes(ctx context.Context, _ time.Time) ([]contracts.VATCodeEntry, error) {
	lockedContractCalls.note(ctx, "fake SalesTax.VATCodes")
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.codes), nil
}

func (f *fakeSalesTax) ForTaxCategories(ctx context.Context, ids []int32, _ time.Time) (map[int32]contracts.VATCodeEntry, error) {
	lockedContractCalls.note(ctx, "fake SalesTax.ForTaxCategories")
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int32]contracts.VATCodeEntry{}
	for _, id := range ids {
		code, ok := f.categories[id]
		if !ok {
			continue
		}
		for _, c := range f.codes {
			if c.ID == code {
				out[id] = c
			}
		}
	}
	return out, nil
}

func (f *fakeSalesTax) Seller(ctx context.Context) (contracts.SellerProfile, error) {
	lockedContractCalls.note(ctx, "fake SalesTax.Seller")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sellerP, nil
}

// fakeDirectory is contracts.CustomerDirectory over the billing profiles a
// test seeds, in place of Customers.
type fakeDirectory struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
}

var _ contracts.CustomerDirectory = (*fakeDirectory)(nil)

func newFakeDirectory() *fakeDirectory {
	return &fakeDirectory{profiles: map[int32]contracts.CustomerBillingProfile{}}
}

// seed adds or replaces a profile.
func (d *fakeDirectory) seed(p contracts.CustomerBillingProfile) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.profiles[p.ID] = p
}

// update changes a seeded profile.
func (d *fakeDirectory) update(id int32, fn func(*contracts.CustomerBillingProfile)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.profiles[id]
	fn(&p)
	d.profiles[id] = p
}

func (d *fakeDirectory) Customer(ctx context.Context, id int32) (*contracts.CustomerEntry, error) {
	lockedContractCalls.note(ctx, "fake Directory.Customer")
	return nil, nil
}

func (d *fakeDirectory) Customers(ctx context.Context, _ []int32) ([]contracts.CustomerEntry, error) {
	lockedContractCalls.note(ctx, "fake Directory.Customers")
	return []contracts.CustomerEntry{}, nil
}

func (d *fakeDirectory) CustomersByLegalID(ctx context.Context, _ string, _ []string) (map[string][]contracts.CustomerEntry, error) {
	lockedContractCalls.note(ctx, "fake Directory.CustomersByLegalID")
	return map[string][]contracts.CustomerEntry{}, nil
}

func (d *fakeDirectory) Contact(ctx context.Context, _ int32) (*contracts.ContactEntry, error) {
	lockedContractCalls.note(ctx, "fake Directory.Contact")
	return nil, nil
}

func (d *fakeDirectory) ContactsByEmail(ctx context.Context, _ string) ([]contracts.ContactMatch, error) {
	lockedContractCalls.note(ctx, "fake Directory.ContactsByEmail")
	return nil, nil
}

func (d *fakeDirectory) ContactsForCustomer(ctx context.Context, _ int32) ([]contracts.ContactEntry, error) {
	lockedContractCalls.note(ctx, "fake Directory.ContactsForCustomer")
	return []contracts.ContactEntry{}, nil
}

func (d *fakeDirectory) BillingProfile(ctx context.Context, id int32, opts contracts.BillingProfileOptions) (*contracts.CustomerBillingProfile, error) {
	lockedContractCalls.note(ctx, "fake Directory.BillingProfile")
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.profiles[id]
	if !ok {
		return nil, nil
	}
	if !opts.WithDelivery {
		p.DeliveryAddresses = nil
	}
	if !opts.WithContacts {
		p.Contacts = nil
	}
	return &p, nil
}

// fakeObjectStore is an in-memory storage.ObjectStore.
type fakeObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
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

// keys is every stored key.
func (s *fakeObjectStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.objects))
	for k := range s.objects {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
