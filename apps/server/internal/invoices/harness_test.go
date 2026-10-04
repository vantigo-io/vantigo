package invoices_test

import (
	"bytes"
	"context"
	"io"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// harness is one invoices installation for one test: internal/modtest's shared
// harness composing this module beside identity, every exchange validated
// against invoices.yaml through the package recorder, plus the fake customer
// directory, the fake object store and the test Storecove it was composed
// with.
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
	// storecove is the access point every harness points
	// INVOICES_STORECOVE_BASE_URL at, reached over its own TLS listener
	// through Deps.HTTPTransport, so no test ever calls the real provider.
	storecove *storecovetest.Server
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
	storecove := storecovetest.New(t)
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(invoices.Module()),
		modtest.WithDirectory(customers),
		modtest.WithEnv("INVOICES_STORECOVE_BASE_URL", storecove.URL()),
		modtest.WithTransport(storecove.Transport()),
	}
	if objects != nil {
		base = append(base, modtest.WithObjectStore(objects))
	}
	before := lockedContractCalls.count()
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), customers: customers, objects: objects, storecove: storecove}
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its locked transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return h
}

// lockedContractCalls is every call out of the module — to the customer
// directory, the object store or the SMTP seam — made from inside a transaction that
// holds locks (invoices.InLockedTx). The rule is that none ever is (D6, D7),
// and every harness checks it when its test ends. It is one recorder for the
// package because the hook is a package-level one (TestMain); each harness
// checks only what was recorded while it existed, and the stack beside each
// call names the path that made it.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if !invoices.InLockedTx(ctx) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, method+"\n"+string(debug.Stack()))
}

func (l *lockedCalls) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func (l *lockedCalls) since(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n >= len(l.calls) {
		return nil
	}
	return slices.Clone(l.calls[n:])
}

// contractCalls is every call out of the module, each with the caller whose
// request made it and whether it was made inside a locked transaction — the
// whole record lockedContractCalls keeps only the forbidden part of. A test
// asserts that a call happened, and how, through the user it signed in:
// tests run in parallel against one package-level hook, and a caller's id is
// what tells one test's calls from another's.
var contractCalls = &allCalls{}

// contractCall is one call out of the module.
type contractCall struct {
	method string
	userID uuid.UUID
	locked bool
}

type allCalls struct {
	mu    sync.Mutex
	calls []contractCall
}

func (a *allCalls) note(ctx context.Context, method string) {
	p, _ := contracts.PrincipalFrom(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, contractCall{method: method, userID: p.UserID, locked: invoices.InLockedTx(ctx)})
}

// by is every call a request of userID's made, in order.
func (a *allCalls) by(userID uuid.UUID) []contractCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []contractCall
	for _, c := range a.calls {
		if c.userID == userID {
			out = append(out, c)
		}
	}
	return out
}

// noteContractCall is the hook TestMain installs: every call is recorded, and
// one made under a lock is also recorded as a failure.
func noteContractCall(ctx context.Context, method string) {
	lockedContractCalls.note(ctx, method)
	contractCalls.note(ctx, method)
}

// fakeCustomers is contracts.CustomerDirectory over billing profiles a test
// puts in. It models what the invoices gates read — Status and MergedInto on
// the billing profile (D10) — and is safe for concurrent use, because the
// handlers of one harness read it from many requests at once.
type fakeCustomers struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
	// onProfile, when set, runs after every BillingProfile read — what
	// happens between the directory read and the issue's transaction.
	onProfile func(id int32)
	// profileErr, when set, is what every BillingProfile read fails with.
	profileErr error
}

var _ contracts.CustomerDirectory = (*fakeCustomers)(nil)

// The customers the fake directory knows:
//   - customerAcme, a Norwegian business with an organisation number, an
//     invoice address in Oslo, a buyer reference and 30 days' terms;
//   - customerPerson, a private person with an address, invoiced in English;
//   - customerForeign, a Swedish business with no Norwegian number;
//   - customerNoTerms, a business that decided neither terms nor a reference;
//   - customerDisabled, customerArchived and customerMerged (merged into
//     Acme), the three gates;
//   - customerEuro, whose profile invoices in EUR;
//   - customerNoAddress, a person with no address at all.
const (
	customerAcme      = 2001
	customerPerson    = 2002
	customerForeign   = 2003
	customerNoTerms   = 2004
	customerDisabled  = 2005
	customerArchived  = 2006
	customerMerged    = 2007
	customerEuro      = 2008
	customerNoAddress = 2009
	customerUnknown   = 9999
)

func newFakeCustomers() *fakeCustomers {
	oslo := &contracts.CustomerAddressEntry{Line1: "Kundeveien 2", PostalCode: "0150", City: "Oslo", Country: "no"}
	thirty := int32(30)
	merged := int32(customerAcme)
	business := func(id int32, number int64, name string) contracts.CustomerBillingProfile {
		return contracts.CustomerBillingProfile{
			ID: id, CustomerNumber: number, Name: name, Type: "business", Status: "active",
			LegalCountry: "no", LegalID: "923609016", LegalName: name + " Norge", InvoiceAddress: oslo,
		}
	}
	acme := business(customerAcme, 10001, "Acme AS")
	acme.PaymentTermsDays, acme.BuyerReference, acme.PeppolID, acme.GLN = &thirty, "PO-77", "0192:923609016", "7080000000001"
	noTerms := business(customerNoTerms, 10004, "Uten Vilkår AS")
	noTerms.LegalName = ""
	disabled := business(customerDisabled, 10005, "Sperret AS")
	disabled.Status = "disabled"
	archived := business(customerArchived, 10006, "Arkivert AS")
	archived.Status, archived.Archived = "archived", true
	mergedAway := business(customerMerged, 10007, "Slått Sammen AS")
	mergedAway.Status, mergedAway.Archived, mergedAway.MergedInto = "archived", true, &merged
	euro := business(customerEuro, 10008, "Euro AS")
	euro.Currency = "EUR"
	return &fakeCustomers{profiles: map[int32]contracts.CustomerBillingProfile{
		customerAcme: acme,
		customerPerson: {
			ID: customerPerson, CustomerNumber: 10002, Name: "Kari Nordmann", Type: "person", Status: "active",
			Language: "en", InvoiceAddress: &contracts.CustomerAddressEntry{Line1: "Hjemveien 5", PostalCode: "5003", City: "Bergen", Country: "no"},
		},
		customerForeign: {
			ID: customerForeign, CustomerNumber: 10003, Name: "Svenska AB", Type: "business", Status: "active",
			LegalCountry: "se", LegalID: "556677889901", LegalName: "Svenska Aktiebolaget AB", Language: "en",
			InvoiceAddress: &contracts.CustomerAddressEntry{Line1: "Storgatan 1", PostalCode: "111 22", City: "Stockholm", Country: "se"},
		},
		customerNoTerms:  noTerms,
		customerDisabled: disabled,
		customerArchived: archived,
		customerMerged:   mergedAway,
		customerEuro:     euro,
		customerNoAddress: {
			ID: customerNoAddress, CustomerNumber: 10009, Name: "Uten Adresse", Type: "person", Status: "active",
		},
	}}
}

// setStatus moves a customer to status, as the customers module would.
func (f *fakeCustomers) setStatus(id int32, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.profiles[id]
	p.Status, p.Archived = status, status == "archived"
	f.profiles[id] = p
}

func (f *fakeCustomers) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	return &contracts.CustomerEntry{ID: p.ID, Name: p.Name, Archived: p.Status == "archived", MergedInto: p.MergedInto}, nil
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
	p, ok := f.profiles[id]
	after, failure := f.onProfile, f.profileErr
	f.mu.Unlock()
	if after != nil {
		after(id)
	}
	if failure != nil {
		return nil, failure
	}
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// edit changes one customer's profile, as the customers module would.
func (f *fakeCustomers) edit(id int32, change func(*contracts.CustomerBillingProfile)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.profiles[id]
	change(&p)
	f.profiles[id] = p
}

// afterProfileRead sets onProfile.
func (f *fakeCustomers) afterProfileRead(fn func(id int32)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onProfile = fn
}

// failProfiles makes every BillingProfile read fail with err, nil to stop.
func (f *fakeCustomers) failProfiles(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profileErr = err
}

// fakeObjectStore is an in-memory storage.ObjectStore that records every Put
// and every Delete — the module must never call one (D7) — and fails a Put or
// a Get on demand.
type fakeObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	puts    int
	deletes int
	putErr  error
	getErr  error
}

// failPuts makes every Put fail with err, nil to stop.
func (s *fakeObjectStore) failPuts(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putErr = err
}

// failGets makes every Get fail with err, nil to stop.
func (s *fakeObjectStore) failGets(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getErr = err
}

// replace swaps an object's bytes behind the module's back.
func (s *fakeObjectStore) replace(key string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = body
}

// lose removes an object behind the module's back.
func (s *fakeObjectStore) lose(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
}

// stored is every key in the store and the number of Puts and Deletes made.
func (s *fakeObjectStore) stored() (keys []string, puts, deletes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.objects {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys, s.puts, s.deletes
}

// object is one object's bytes.
func (s *fakeObjectStore) object(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objects[key]
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
	if s.putErr != nil {
		return s.putErr
	}
	s.puts++
	s.objects[key] = body
	return nil
}

func (s *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
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
