package invoices_test

import (
	"bytes"
	"context"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// harness is one invoices installation for one test: internal/modtest's shared
// harness composing this module beside identity, every exchange validated
// against invoices.yaml through the package recorder, plus the fake customer
// directory and the fake object store it was composed with.
//
// The directory is a fake built directly against internal/contracts —
// depguard forbids internal/invoices/** from importing internal/customers,
// even in tests. modtest always lists customers in MODULES, so "invoices
// requires customers" holds for every harness here.
type harness struct {
	*modtest.Harness
	customers *fakeCustomers
	// objects is nil for an installation without an object store.
	objects *fakeObjectStore
}

// newHarness is an invoices installation with an object store.
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	return newInvoicesHarness(t, newFakeObjectStore(), opts...)
}

// newHarnessWithoutStore is an installation whose operator configured no
// object store: Deps.ObjectStore stays nil and STORAGE_PROVIDER is unset, so
// the module builds the real, fail-closed store.
func newHarnessWithoutStore(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	return newInvoicesHarness(t, nil, opts...)
}

func newInvoicesHarness(t *testing.T, objects *fakeObjectStore, opts ...modtest.Option) *harness {
	t.Helper()
	customers := newFakeCustomers()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(invoices.Module()),
		modtest.WithDirectory(customers),
	}
	if objects != nil {
		base = append(base, modtest.WithObjectStore(objects))
	}
	return &harness{Harness: modtest.New(t, append(base, opts...)...), customers: customers, objects: objects}
}

// fakeCustomers is contracts.CustomerDirectory over billing profiles a test
// puts in. It models what the invoices gates read — Status and MergedInto on
// the billing profile (D10) — and is safe for concurrent use, because the
// handlers of one harness read it from many requests at once.
type fakeCustomers struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
}

var _ contracts.CustomerDirectory = (*fakeCustomers)(nil)

func newFakeCustomers() *fakeCustomers {
	return &fakeCustomers{profiles: map[int32]contracts.CustomerBillingProfile{}}
}

func (f *fakeCustomers) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	return &contracts.CustomerEntry{ID: p.ID, Name: p.Name, Archived: p.Archived}, nil
}

func (f *fakeCustomers) Customers(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	out := []contracts.CustomerEntry{}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	for _, id := range slices.Compact(sorted) {
		entry, _ := f.Customer(ctx, id)
		if entry != nil {
			out = append(out, *entry)
		}
	}
	return out, nil
}

func (f *fakeCustomers) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (f *fakeCustomers) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return []contracts.ContactMatch{}, nil
}

func (f *fakeCustomers) BillingProfile(_ context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// fakeObjectStore is an in-memory storage.ObjectStore that records every Delete,
// because the module must never call one (D7).
type fakeObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	deletes int
}

var _ storage.ObjectStore = (*fakeObjectStore)(nil)

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: map[string][]byte{}}
}

func (s *fakeObjectStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = body
	return nil
}

func (s *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (s *fakeObjectStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}

func (s *fakeObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	delete(s.objects, key)
	return nil
}
