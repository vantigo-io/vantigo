package invoices_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	before, beforeTx := lockedContractCalls.count(), lockedContractCalls.txCount()
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), customers: customers, objects: objects, storecove: storecove}
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its locked transactions:\n%s",
				strings.Join(calls, "\n"))
		}
		if calls := lockedContractCalls.txSince(beforeTx); len(calls) > 0 {
			t.Errorf("a holder's command was made outside a locked transaction, so on no transaction of the issue's:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return h
}

// lockedContractCalls is the two kinds of call the lock rule forbids
// (module-boundaries rule 10): a call out of the module — to a directory, a
// billable read, the object store, the SMTP seam or a provider — made from
// inside a transaction that holds locks (invoices.InLockedTx), and a holder's
// transaction-bound command (noteTxCommand) made from outside one. Every
// harness checks both when its test ends. It is one recorder for the package
// because the hook is a package-level one (TestMain); each harness checks only
// what was recorded while it existed, and the stack beside each call names the
// path that made it.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
	// txOutside is every transaction-bound command made outside a lock.
	txOutside []string
}

func (l *lockedCalls) note(ctx context.Context, method string, txBound bool) {
	locked := invoices.InLockedTx(ctx)
	if locked == txBound {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := method + "\n" + string(debug.Stack())
	if locked {
		l.calls = append(l.calls, entry)
		return
	}
	l.txOutside = append(l.txOutside, entry)
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

// forget drops what was recorded after the first n calls and the first nTx
// commands: the recorder's own test, which records both kinds on purpose,
// leaves nothing for a later test to read.
func (l *lockedCalls) forget(n, nTx int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls, l.txOutside = l.calls[:min(n, len(l.calls))], l.txOutside[:min(nTx, len(l.txOutside))]
}

func (l *lockedCalls) txCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.txOutside)
}

func (l *lockedCalls) txSince(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n >= len(l.txOutside) {
		return nil
	}
	return slices.Clone(l.txOutside[n:])
}

// contractCalls is every call out of the module, each with the caller whose
// request made it, whether it was made inside a locked transaction and
// whether it is a holder's transaction-bound command — the whole record
// lockedContractCalls keeps only the forbidden part of. A test asserts that a
// call happened, and how, through the user it signed in: tests run in
// parallel against one package-level hook, and a caller's id is what tells
// one test's calls from another's.
var contractCalls = &allCalls{}

// contractCall is one call out of the module.
type contractCall struct {
	method  string
	userID  uuid.UUID
	locked  bool
	txBound bool
}

type allCalls struct {
	mu    sync.Mutex
	calls []contractCall
}

func (a *allCalls) note(ctx context.Context, method string, txBound bool) {
	p, _ := contracts.PrincipalFrom(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, contractCall{method: method, userID: p.UserID, locked: invoices.InLockedTx(ctx), txBound: txBound})
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
// one the lock rule forbids is also recorded as a failure.
func noteContractCall(ctx context.Context, method string, txBound bool) {
	lockedContractCalls.note(ctx, method, txBound)
	contractCalls.note(ctx, method, txBound)
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
	// onGet, when set, is called with each key read, before the read and
	// outside the store's lock.
	onGet func(key string)
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

// beforeGets calls fn with the key of every read from then on, nil to stop.
func (s *fakeObjectStore) beforeGets(fn func(key string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onGet = fn
}

func (s *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	onGet := s.onGet
	s.mu.Unlock()
	if onGet != nil {
		onGet(key)
	}
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

// fakeBillable is the three billable reads (contracts.BillableHours,
// BillableExpenses, BillableMilestones) over rows a test puts in, for the
// held sources' freshness and refresh without composing time, expenses or
// projects (depguard keeps them out of this package's tests). Every read is
// recorded with what it asked for and whether it was made inside one of this
// module's locked transactions — which it never may be — and the harness's
// contract-call hook checks the same through the accessors. Safe for
// concurrent use.
type fakeBillable struct {
	mu         sync.Mutex
	hours      map[int64]contracts.BillableHour
	expenses   map[int64]contracts.BillableExpense
	milestones map[int64]contracts.BillableMilestone
	calls      []billableCall
	// onRead, when set, runs once, after the next read and outside the
	// fake's lock: what happens between a refresh's read and its save's
	// lock.
	onRead func()
	// failure, when set, is what every read fails with.
	failure error
	// more is what every page answers as More: the provider had more than
	// one page.
	more bool
}

// billableCall is one read: which, by which ids, and whether under a lock.
type billableCall struct {
	method string
	ids    []int64
	until  time.Time
	locked bool
}

var (
	_ contracts.BillableHours      = (*fakeBillable)(nil)
	_ contracts.BillableExpenses   = (*fakeBillable)(nil)
	_ contracts.BillableMilestones = (*fakeBillable)(nil)
)

func newFakeBillable() *fakeBillable {
	return &fakeBillable{
		hours: map[int64]contracts.BillableHour{}, expenses: map[int64]contracts.BillableExpense{},
		milestones: map[int64]contracts.BillableMilestone{},
	}
}

// options composes the fake as all three reads.
func (f *fakeBillable) options() []modtest.Option {
	return []modtest.Option{modtest.WithBillableHours(f), modtest.WithBillableExpenses(f), modtest.WithBillableMilestones(f)}
}

// putHour, putExpense and putMilestone make a row billable as given;
// drop makes it no longer billable.
func (f *fakeBillable) putHour(h contracts.BillableHour) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hours[h.ID] = h
}

func (f *fakeBillable) putExpense(e contracts.BillableExpense) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expenses[e.ID] = e
}

func (f *fakeBillable) putMilestone(m contracts.BillableMilestone) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.milestones[m.ID] = m
}

// afterNextRead sets onRead.
func (f *fakeBillable) afterNextRead(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onRead = fn
}

// fail makes every read fail with err, nil to stop.
func (f *fakeBillable) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failure = err
}

// reads is every read made so far.
func (f *fakeBillable) reads() []billableCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// record notes one read and answers the hook to run after it and the failure.
func (f *fakeBillable) record(ctx context.Context, method string, req contracts.BillableRequest) (func(), error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, billableCall{method: method, ids: slices.Clone(req.IDs), until: req.Until, locked: invoices.InLockedTx(ctx)})
	after := f.onRead
	f.onRead = nil
	return after, f.failure
}

// answerMore makes every page say More, as a provider with more than one
// page of work does.
func (f *fakeBillable) answerMore(more bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.more = more
}

// wanted reports whether a row of project with id, dated date, is what req
// asks for: by id, or by project on or before Until.
func wanted(req contracts.BillableRequest, id int64, project int32, date time.Time) bool {
	if len(req.IDs) > 0 {
		return slices.Contains(req.IDs, id)
	}
	return slices.Contains(req.ProjectIDs, project) && (req.Until.IsZero() || !date.After(req.Until))
}

func (f *fakeBillable) BillableHours(ctx context.Context, req contracts.BillableRequest) (contracts.BillableHoursPage, error) {
	after, err := f.record(ctx, "BillableHours", req)
	if after != nil {
		defer after()
	}
	if err != nil {
		return contracts.BillableHoursPage{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	page := contracts.BillableHoursPage{Hours: []contracts.BillableHour{}, More: f.more}
	for _, h := range f.hours {
		if wanted(req, h.ID, h.ProjectID, h.Date) {
			page.Hours = append(page.Hours, h)
		}
	}
	slices.SortFunc(page.Hours, func(a, b contracts.BillableHour) int { return int(a.ID - b.ID) })
	return page, nil
}

func (f *fakeBillable) BillableExpenses(ctx context.Context, req contracts.BillableRequest) (contracts.BillableExpensesPage, error) {
	after, err := f.record(ctx, "BillableExpenses", req)
	if after != nil {
		defer after()
	}
	if err != nil {
		return contracts.BillableExpensesPage{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	page := contracts.BillableExpensesPage{Expenses: []contracts.BillableExpense{}, More: f.more}
	for _, e := range f.expenses {
		if wanted(req, e.ID, e.ProjectID, e.Date) {
			page.Expenses = append(page.Expenses, e)
		}
	}
	slices.SortFunc(page.Expenses, func(a, b contracts.BillableExpense) int { return int(a.ID - b.ID) })
	return page, nil
}

func (f *fakeBillable) BillableMilestones(ctx context.Context, req contracts.BillableRequest) (contracts.BillableMilestonesPage, error) {
	after, err := f.record(ctx, "BillableMilestones", req)
	if after != nil {
		defer after()
	}
	if err != nil {
		return contracts.BillableMilestonesPage{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	page := contracts.BillableMilestonesPage{Milestones: []contracts.BillableMilestone{}, More: f.more}
	for _, m := range f.milestones {
		if wanted(req, m.ID, m.ProjectID, m.ReadyAt) {
			page.Milestones = append(page.Milestones, m)
		}
	}
	slices.SortFunc(page.Milestones, func(a, b contracts.BillableMilestone) int { return int(a.ID - b.ID) })
	return page, nil
}

// fakeHolders is one contracts.InvoicedWorkHolder per kind of work, for the
// issue's write-back (rule 10) without composing time, expenses or projects
// (depguard keeps them out of this package's tests). Every command is
// recorded with what it was handed and with what it saw through the pgx.Tx it
// was handed — pg_current_xact_id(), and the document's status and written
// VAT snapshots — so a test can prove it rode the issue's own transaction
// after the number and before any write; and the order the kinds were called
// in. A holder refuses a source by id, or fails, on demand. Safe for
// concurrent use.
type fakeHolders struct {
	mu    sync.Mutex
	kinds map[contracts.WorkSourceKind]*fakeHolder
	order []contracts.WorkSourceKind
	// onCall, when set, runs inside every command, after the record.
	onCall func()
}

// fakeHolder is one kind's holder.
type fakeHolder struct {
	all    *fakeHolders
	kind   contracts.WorkSourceKind
	calls  []holderCall
	refuse map[int64]string
	fail   error
	// disclaimed, when set, makes Kinds answer none: the holder composed,
	// but claiming no kind any more.
	disclaimed bool
}

// holderCall is one command: mark or release, its ref and sources, and what
// the holder saw on the transaction it was handed.
type holderCall struct {
	op          string
	ref         contracts.InvoiceRef
	sources     []contracts.WorkSource
	xact        string
	status      string
	snapshotted int
	locked      bool
}

var _ contracts.InvoicedWorkHolder = (*fakeHolder)(nil)

func newFakeHolders() *fakeHolders {
	f := &fakeHolders{kinds: map[contracts.WorkSourceKind]*fakeHolder{}}
	for _, k := range contracts.InvoicedWorkOrder {
		f.kinds[k] = &fakeHolder{all: f, kind: k, refuse: map[int64]string{}}
	}
	return f
}

// options composes the holders of kinds — every kind when none is named — in
// the reverse of the lock order, so an issue that called them as composed, or
// in a map's order, would not happen to call them in the right one.
func (f *fakeHolders) options(kinds ...contracts.WorkSourceKind) []modtest.Option {
	if len(kinds) == 0 {
		kinds = contracts.InvoicedWorkOrder
	}
	var holders []contracts.InvoicedWorkHolder
	for i := len(kinds) - 1; i >= 0; i-- {
		holders = append(holders, f.kinds[kinds[i]])
	}
	return []modtest.Option{modtest.WithInvoicedWork(holders...)}
}

// refuseSource makes kind's holder refuse source id with code, "" to stop.
func (f *fakeHolders) refuseSource(kind contracts.WorkSourceKind, id int64, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code == "" {
		delete(f.kinds[kind].refuse, id)
		return
	}
	f.kinds[kind].refuse[id] = code
}

// failWith makes kind's holder fail every command with err, nil to stop.
func (f *fakeHolders) failWith(kind contracts.WorkSourceKind, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds[kind].fail = err
}

// disclaim makes kind's holder claim no kind from then on, as a holder
// composed without it would.
func (f *fakeHolders) disclaim(kind contracts.WorkSourceKind) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds[kind].disclaimed = true
}

// duringCall sets onCall.
func (f *fakeHolders) duringCall(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onCall = fn
}

// calledOrder is the kinds in the order their commands were made.
func (f *fakeHolders) calledOrder() []contracts.WorkSourceKind {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.order)
}

// callsOf is every command kind's holder was handed.
func (f *fakeHolders) callsOf(kind contracts.WorkSourceKind) []holderCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.kinds[kind].calls)
}

func (h *fakeHolder) Kinds() []contracts.WorkSourceKind {
	h.all.mu.Lock()
	defer h.all.mu.Unlock()
	if h.disclaimed {
		return nil
	}
	return []contracts.WorkSourceKind{h.kind}
}

func (h *fakeHolder) MarkInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	return h.command(ctx, tx, "mark", ref, sources)
}

func (h *fakeHolder) ReleaseInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	return h.command(ctx, tx, "release", ref, sources)
}

func (h *fakeHolder) command(ctx context.Context, tx pgx.Tx, op string, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	call := holderCall{op: op, ref: ref, sources: slices.Clone(sources), locked: invoices.InLockedTx(ctx)}
	if err := tx.QueryRow(ctx, `SELECT pg_current_xact_id()::text`).Scan(&call.xact); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `
		SELECT d.status, (SELECT count(*) FROM invoices.lines l WHERE l.invoice_id = d.id AND l.vat_category IS NOT NULL)
		FROM invoices.invoices d WHERE d.id = $1`, ref.ID).Scan(&call.status, &call.snapshotted); err != nil {
		return err
	}
	f := h.all
	f.mu.Lock()
	h.calls = append(h.calls, call)
	f.order = append(f.order, h.kind)
	during, refuse, fail := f.onCall, maps.Clone(h.refuse), h.fail
	f.mu.Unlock()
	if during != nil {
		during()
	}
	for _, s := range sources {
		if code, ok := refuse[s.ID]; ok {
			return &contracts.WorkSourceRefusal{Source: s, Code: code, Detail: "the fake holder refuses it"}
		}
	}
	return fail
}

// fakeProjects is contracts.ProjectDirectory over projects a test puts in —
// the issue reads whom a held source's project bills and how (D1), the
// uninvoiced view a customer's projects or one project (D3) — composed
// with WithProjects, since depguard keeps projects out of this package's
// tests. Only what this module reads is answered; anything else panics on the
// embedded nil directory. Safe for concurrent use.
type fakeProjects struct {
	contracts.ProjectDirectory
	mu       sync.Mutex
	projects map[int32]contracts.ProjectEntry
}

// newFakeProjects knows project 41 and project 42, both billing Acme by time
// and materials in NOK.
func newFakeProjects() *fakeProjects {
	f := &fakeProjects{projects: map[int32]contracts.ProjectEntry{}}
	for _, id := range []int32{project41, project42} {
		f.put(contracts.ProjectEntry{
			ID: id, Code: fmt.Sprintf("P-%d", id), Name: fmt.Sprintf("Project %d", id), CustomerID: ptrTo(int32(customerAcme)),
			Status: "active", OpenForWork: true, BillingType: "time-and-materials", Currency: ptrTo("NOK"),
		})
	}
	return f
}

// put makes p known as given; edit changes one; drop forgets one.
func (f *fakeProjects) put(p contracts.ProjectEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[p.ID] = p
}

func (f *fakeProjects) edit(id int32, change func(*contracts.ProjectEntry)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[id]
	change(&p)
	f.projects[id] = p
}

// count is how many projects the fake knows.
func (f *fakeProjects) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.projects)
}

func (f *fakeProjects) drop(id int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.projects, id)
}

func (f *fakeProjects) Project(_ context.Context, id int32) (*contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// ProjectsForCustomer answers the customer's projects by id ascending, as
// projects does.
func (f *fakeProjects) ProjectsForCustomer(_ context.Context, customerID int32) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []contracts.ProjectEntry{}
	for _, p := range f.projects {
		if p.CustomerID != nil && *p.CustomerID == customerID {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b contracts.ProjectEntry) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeProjects) Projects(_ context.Context, ids []int32) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []contracts.ProjectEntry{}
	for _, id := range ids {
		if p, ok := f.projects[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}
