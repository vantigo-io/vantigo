package projects_test

import (
	"context"
	"fmt"
	"maps"
	"math"
	"math/big"
	"net/http"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// newHarness is one projects installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against projects.yaml through the package recorder. It stubs
// Deps.Directory and Deps.Products with fakes — depguard forbids
// internal/projects/** from importing internal/customers or
// internal/products, even in tests, so both are built directly against
// internal/contracts, the same seam energy uses for customers. The user
// directory is not stubbed: identity is always composed, and it provides the
// real one.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, newFakeCatalog(), opts...)
}

// newHarnessWithoutProducts is newHarness with the products module off:
// Deps.Products stays nil, which is the optional contract's absent case
// (D10) and what billingLinesAvailable answers false for.
func newHarnessWithoutProducts(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, nil, opts...)
}

// newHarnessWithCatalog is newHarness over a catalog the test keeps a handle
// on, for a case whose subject is the catalog changing under a stored line —
// a variant products drops after a line was pinned to it.
func newHarnessWithCatalog(t *testing.T, catalog *fakeCatalog, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, catalog, opts...)
}

// newHarnessWithActuals is newHarness with a module that reports what has
// been logged against projects composed beside it — the economy read's other
// half. depguard forbids internal/projects/** from importing internal/time
// even in a test, so the provider is a fake built directly against
// internal/contracts and handed to Deps.Actuals through modtest.WithActuals,
// exactly the seam the product catalog uses.
//
// newHarness itself deliberately leaves it out: a nil Deps.Actuals is a real
// installation — one with no time tracking — and it is what every test of
// that case drives.
func newHarnessWithActuals(t *testing.T, actuals *fakeActuals, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, newFakeCatalog(), append([]modtest.Option{modtest.WithActuals(actuals)}, opts...)...)
}

// newHarnessWithExpenses is newHarness with a module that reports what a
// project's expenses cost and bill composed beside it, and *no* time
// tracking: the combination an installation running expenses without time
// runs, which is a real one — the two contracts are independent slots.
// depguard forbids internal/projects/** from importing internal/expenses even
// in a test, so the provider is a fake built directly against
// internal/contracts and handed to Deps.Expenses through modtest.WithExpenses,
// exactly the seam the actuals contract uses.
func newHarnessWithExpenses(t *testing.T, expenses *fakeExpenses, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, newFakeCatalog(), append([]modtest.Option{modtest.WithExpenses(expenses)}, opts...)...)
}

// newHarnessWithActualsAndExpenses is the fourth combination: both optional
// contracts present, which is the installation the economy read was designed
// for and the only one where a margin spans labour and expenses at once.
//
// The four helpers — newHarness (neither), newHarnessWithActuals (time only),
// newHarnessWithExpenses (expenses only) and this one — are named rather than
// assembled at each call site because "which modules are installed" is the
// axis half the cases here vary along, and a test that composed its own
// option list would make that axis invisible.
func newHarnessWithActualsAndExpenses(t *testing.T, actuals *fakeActuals, expenses *fakeExpenses, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, newFakeCatalog(),
		append([]modtest.Option{modtest.WithActuals(actuals), modtest.WithExpenses(expenses)}, opts...)...)
}

// newProjectsHarness is the one composition every harness here is: the
// catalog is the only thing they differ in, nil for "products disabled", so
// it is the only thing any of them says. A second copy of the option list
// would drift the moment this module grows another dependency.
//
// Every harness also carries the module's locking guarantee out of its test:
// nothing this module asks of another module is asked while one of its
// transactions holds the project's row lock (docs/projects.md's "Locking").
// The check is the whole suite's, not one path's — whichever write a future
// change introduces it on, the call is reported and the test that made it
// fails.
func newProjectsHarness(t *testing.T, catalog *fakeCatalog, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(projects.Module()),
		modtest.WithDirectory(fakeDirectory{}),
	}
	if catalog != nil {
		base = append(base, modtest.WithProducts(catalog))
	}
	before := lockedContractCalls.count()
	h := modtest.New(t, append(base, opts...)...)
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a cross-module call was made while this module held a project's row lock:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return h
}

// lockedContractCalls is every cross-module call the module made from inside a
// transaction that holds the project's row lock (projects.InLockedTx). The
// directories read through the same connection pool as the module, so a
// transaction holding row locks while it waits for one of them can starve the
// pool under load; the rule is that none ever does, and every harness checks
// it when its test ends. A real pool small enough to starve cannot be had here
// — modtest's pools are a fixed testdb.PoolMaxConns, and the fakes use no
// connection at all — so the call itself is what is caught.
//
// It is one recorder for the package rather than one per harness because the
// hook it is installed as is a package-level one (TestMain), and the module's
// own call sites carry no harness with them. Each harness therefore checks
// only what was recorded while it existed, and the stack trace beside each
// call names the path that actually made it — which is what attributes a
// violation when parallel tests overlap.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

// note is the hook itself. A call from outside any locked transaction — which
// is every call the module is supposed to make — costs one context lookup and
// nothing else.
func (l *lockedCalls) note(ctx context.Context, method string) {
	if !projects.InLockedTx(ctx) {
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

// The customers the fake directory knows. 1003 is archived, which resolves
// and is allowed: a project can outlive the relationship that started it
// (design §4.1).
const (
	customerKraftVerket     = 1001
	customerAcme            = 1002
	customerArchived        = 1003
	customerUnknown         = 9999
	customerKraftVerketName = "Kraft-Verket"
	customerAcmeName        = "Acme Industrier AS"
	customerArchivedName    = "Nedlagt Handel AS"
)

// fakeDirectory is contracts.CustomerDirectory over three customers.
// Everything else does not exist, which is what makes the unknown-customer
// validation rule testable without composing customers beside this module.
// Contact, ContactsByEmail and BillingProfile satisfy the interface and are
// never exercised here: projects never resolves a contact or a billing
// profile. Customers (the batch lookup the list page uses,
// customerNamesForPage/projects_list.go) answers from the same three
// customers as Customer, in ascending-id order, the same order
// contracts.CustomerDirectory.Customers promises.
type fakeDirectory struct{}

var _ contracts.CustomerDirectory = fakeDirectory{}

func (fakeDirectory) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	switch id {
	case customerKraftVerket:
		return &contracts.CustomerEntry{ID: id, Name: customerKraftVerketName}, nil
	case customerAcme:
		return &contracts.CustomerEntry{ID: id, Name: customerAcmeName}, nil
	case customerArchived:
		return &contracts.CustomerEntry{ID: id, Name: customerArchivedName, Archived: true}, nil
	default:
		return nil, nil
	}
}

func (fakeDirectory) Customers(_ context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	known := map[int32]contracts.CustomerEntry{
		customerKraftVerket: {ID: customerKraftVerket, Name: customerKraftVerketName},
		customerAcme:        {ID: customerAcme, Name: customerAcmeName},
		customerArchived:    {ID: customerArchived, Name: customerArchivedName, Archived: true},
	}
	seen := map[int32]bool{}
	entries := []contracts.CustomerEntry{}
	for _, id := range ids {
		if entry, ok := known[id]; ok && !seen[id] {
			seen[id] = true
			entries = append(entries, entry)
		}
	}
	slices.SortFunc(entries, func(a, b contracts.CustomerEntry) int { return int(a.ID - b.ID) })
	return entries, nil
}

func (fakeDirectory) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (fakeDirectory) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return nil, nil
}

func (fakeDirectory) BillingProfile(context.Context, int32) (*contracts.CustomerBillingProfile, error) {
	return nil, nil
}

// fakeCatalog is contracts.ProductCatalog over two service variants, the two
// a project bills hours against. It is what makes "products enabled" a real
// composition rather than a flag: the billing lines read it for the product
// name, SKU and unit they embed (D15) and for their list price.
//
// A variant it does not know is (nil, nil) and absent from Variants, never an
// error, which is what a line whose variant products has since dropped is
// rendered from. Its list prices are in NOK only, so a project in another
// currency exercises the "priced variant, no price in this currency" case
// without a second fixture.
type fakeCatalog struct {
	variants map[int32]contracts.VariantEntry
	prices   map[int32]float64

	// variantErr, variantsErr and listPriceErr are what each lookup answers
	// instead of looking anything up: a degraded products module, which is a
	// different thing from a variant it does not know (nil, nil) and has to be
	// handled differently — a line whose variant is unchanged must stay
	// editable either way, and a line being *rendered* must come back without
	// the fields that particular call would have supplied rather than not at
	// all.
	//
	// They are per method because the response's two catalog-derived parts
	// fail independently and the contract promises exactly that: names come
	// from Variants, the list price from ListPrice, and a line keeps whichever
	// of them answered. fail() sets all three at once for a test whose subject
	// is simply "products is down".
	variantErr   error
	variantsErr  error
	listPriceErr error
}

var _ contracts.ProductCatalog = (*fakeCatalog)(nil)

const (
	variantProjectManagerHour = 2001
	variantDeveloperHour      = 2002
	variantUnknown            = 2999
	catalogCurrency           = "NOK"
)

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{
		variants: map[int32]contracts.VariantEntry{
			variantProjectManagerHour: {
				ID: variantProjectManagerHour, ProductID: 3001, ProductName: "Project manager hour",
				SKU: "PM-H", Unit: "hour", ProductType: "Service", ProductStatus: "Active",
			},
			variantDeveloperHour: {
				ID: variantDeveloperHour, ProductID: 3002, ProductName: "Developer hour",
				SKU: "DEV-H", Unit: "hour", ProductType: "Service", ProductStatus: "Active",
			},
		},
		prices: map[int32]float64{
			variantProjectManagerHour: 1600,
			variantDeveloperHour:      1250,
		},
	}
}

// forget drops a variant the way products dropping it would look from here:
// the catalog stops knowing it, while the lines already pinned to it stay in
// the table. It is not concurrency-safe, so a test that calls it drives its
// own harness (newHarnessWithCatalog) rather than sharing one.
func (c *fakeCatalog) forget(id int32) {
	delete(c.variants, id)
	delete(c.prices, id)
}

// fail makes every later lookup — Variant, Variants and ListPrice alike —
// answer err, the way a products module that is wholly down looks from here.
// failVariant, failVariants and failListPrice degrade one method at a time,
// which is what a test of a *partial* outage needs: the response's names and
// its list price come from different calls and a line keeps whichever of them
// answered. Like forget, none of them is concurrency-safe, so a test that
// calls one drives its own harness.
func (c *fakeCatalog) fail(err error) {
	c.failVariant(err)
	c.failVariants(err)
	c.failListPrice(err)
}

func (c *fakeCatalog) failVariant(err error)   { c.variantErr = err }
func (c *fakeCatalog) failVariants(err error)  { c.variantsErr = err }
func (c *fakeCatalog) failListPrice(err error) { c.listPriceErr = err }

func (c *fakeCatalog) Variant(_ context.Context, id int32) (*contracts.VariantEntry, error) {
	if c.variantErr != nil {
		return nil, c.variantErr
	}
	v, ok := c.variants[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (c *fakeCatalog) Variants(_ context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	if c.variantsErr != nil {
		return nil, c.variantsErr
	}
	out := make([]contracts.VariantEntry, 0, len(ids))
	for _, id := range ids {
		if v, ok := c.variants[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (c *fakeCatalog) ListPrice(_ context.Context, variantID int32, currency string, _ time.Time) (*contracts.Money, error) {
	if c.listPriceErr != nil {
		return nil, c.listPriceErr
	}
	price, ok := c.prices[variantID]
	if !ok || currency != catalogCurrency {
		return nil, nil
	}
	return &contracts.Money{Amount: price, Currency: currency}, nil
}

// fakeActuals is contracts.ProjectActuals over whatever a test says has been
// logged. It is the economy read's only source of hours, so it carries the
// three things a test of that read needs to say: what was logged (set), that
// the module that owns the hours could not answer (fail), and what was
// actually asked of it (requests) — the last because "exactly one call, with
// the project's own currency, outside any transaction" is a rule of the read
// rather than a detail of it.
//
// onCall runs inside Actuals, which is how a test proves the handler holds no
// lock while it waits: the hook takes the project's row lock on another
// connection and would be refused if the request were holding one.
//
// A project nothing was set for answers zero-valued totals and no lines,
// which is what the provider answers for a project nobody has logged against.
type fakeActuals struct {
	mu       sync.Mutex
	entries  map[int32]contracts.ProjectActualsEntry
	err      error
	requests []contracts.ActualsRequest
	batches  [][]contracts.ActualsRequest
	onCall   func(context.Context, contracts.ActualsRequest)
}

var _ contracts.ProjectActuals = (*fakeActuals)(nil)

func newFakeActuals() *fakeActuals {
	return &fakeActuals{entries: map[int32]contracts.ProjectActualsEntry{}}
}

// set says what has been logged against one project: its totals, and the
// per-line split the provider reports — only lines anything is on, by line id
// ascending, with the work logged on no line last, exactly as the contract
// promises.
func (f *fakeActuals) set(projectID int32, totals contracts.ActualsTotals, lines ...contracts.LineActuals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[projectID] = contracts.ProjectActualsEntry{Totals: totals, Lines: lines}
}

// setWorkTypes says what was logged per work type on a project whose totals
// set has already given — the provider's per-type split (work types design
// D4): ids and figures, by id, as the contract promises. The names are this
// module's own, read from projects.work_types when the economy renders them.
// Call it after set, which replaces the whole entry and so drops a split
// given before it.
func (f *fakeActuals) setWorkTypes(projectID int32, types ...contracts.WorkTypeActuals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.entries[projectID]
	entry.WorkTypes = types
	f.entries[projectID] = entry
}

// fail makes every later call answer err, the way a saturated pool or a
// degraded time module looks from here. It is "could not read", never "there
// is nothing".
func (f *fakeActuals) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// during registers a hook run inside every Actuals call, before it answers.
func (f *fakeActuals) during(hook func(context.Context, contracts.ActualsRequest)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onCall = hook
}

// asked is every request the provider has been handed, in order: the call
// log a test reads to prove the read asks once and asks for the right
// currency.
func (f *fakeActuals) asked() []contracts.ActualsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *fakeActuals) Actuals(ctx context.Context, req contracts.ActualsRequest) (contracts.ProjectActualsEntry, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	hook, err, entry := f.onCall, f.err, f.entries[req.ProjectID]
	f.mu.Unlock()

	if hook != nil {
		hook(ctx, req)
	}
	if err != nil {
		return contracts.ProjectActualsEntry{}, err
	}
	return entry, nil
}

// batched is every batch the provider has been handed, in order — the log a
// portfolio test reads to prove one request costs exactly one call into the
// module that owns the hours, however many projects it is about. asked() is
// the same calls flattened to one entry per project.
func (f *fakeActuals) batched() [][]contracts.ActualsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.batches)
}

// ActualsForProjects answers each request from the same entries Actuals does,
// and refuses what the real provider refuses: a batch past
// contracts.MaxActualsRequests, and one project named twice. Both are the
// caller's bug, so a consumer that builds a batch badly fails the test that
// built it rather than quietly getting an answer the real module would never
// have given.
func (f *fakeActuals) ActualsForProjects(ctx context.Context, reqs []contracts.ActualsRequest) (map[int32]contracts.ActualsTotals, error) {
	f.mu.Lock()
	f.batches = append(f.batches, slices.Clone(reqs))
	f.mu.Unlock()

	if len(reqs) > contracts.MaxActualsRequests {
		return nil, fmt.Errorf("projects test: %d projects in one batch, at most %d", len(reqs), contracts.MaxActualsRequests)
	}
	seen := make(map[int32]bool, len(reqs))
	for _, req := range reqs {
		if seen[req.ProjectID] {
			return nil, fmt.Errorf("projects test: project %d asked for twice in one batch", req.ProjectID)
		}
		seen[req.ProjectID] = true
	}

	out := make(map[int32]contracts.ActualsTotals, len(reqs))
	for _, req := range reqs {
		entry, err := f.Actuals(ctx, req)
		if err != nil {
			return nil, err
		}
		out[req.ProjectID] = entry.Totals
	}
	return out, nil
}

// fakeExpenses is contracts.ProjectExpenses over whatever a test says has
// been recorded. It carries the same three things fakeActuals does — what was
// recorded (set), that the module that owns the expenses could not answer
// (fail), and what was actually asked of it (asked, batched) — because the
// rules of the read are the same rules: one call per request, over the whole
// capped set at once, outside any transaction.
//
// It models the one place the two contracts differ, and the one task 3's
// consumer is most likely to get wrong: **a project nothing was recorded
// against is absent from the map**, where fakeActuals answers a zero-valued
// entry for every project it was asked about. A consumer that indexed the map
// blindly would read a project with a receipt on it as a project with none.
type fakeExpenses struct {
	mu      sync.Mutex
	entries map[int32]contracts.ProjectExpenseTotals
	err     error
	batches [][]int32
	onCall  func(context.Context, []int32)
}

var _ contracts.ProjectExpenses = (*fakeExpenses)(nil)

func newFakeExpenses() *fakeExpenses {
	return &fakeExpenses{entries: map[int32]contracts.ProjectExpenseTotals{}}
}

// set says what has been recorded against one project: one entry per currency
// anything is in, by currency code ascending, and the date of the most
// recently dated line over all of them.
func (f *fakeExpenses) set(projectID int32, totals contracts.ProjectExpenseTotals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[projectID] = totals
}

// setSupplierInvoices says which part of one currency's figures, already set
// for projectID, is supplier invoices (supplier invoices design D3) — the
// sub-figure the real provider carries beside the buckets and leaves nil when
// a currency has none. set comes first; this only adds the split to it.
func (f *fakeExpenses) setSupplierInvoices(projectID int32, currency string, split contracts.ExpenseSplit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	totals := f.entries[projectID]
	currencies := slices.Clone(totals.Currencies)
	for i := range currencies {
		if currencies[i].Currency == currency {
			one := split
			currencies[i].SupplierInvoices = &one
		}
	}
	totals.Currencies = currencies
	f.entries[projectID] = totals
}

// spentSplit is a supplier-invoice split with Total derived from its three
// buckets, the way spentInCurrency derives a currency's.
func spentSplit(approved, submitted, draft contracts.ExpenseBucket) contracts.ExpenseSplit {
	return contracts.ExpenseSplit{
		Approved: approved, Submitted: submitted, Draft: draft,
		Total: contracts.ExpenseBucket{
			Count:      approved.Count + submitted.Count + draft.Count,
			CostAmount: addedAmounts(approved.CostAmount, submitted.CostAmount, draft.CostAmount),
			BillAmount: addedAmounts(approved.BillAmount, submitted.BillAmount, draft.BillAmount),
		},
	}
}

// fail makes every later call answer err. It is "could not be read", never
// "there is nothing" — the distinction the contract's own doc comment turns
// into a 500 rather than a project that has apparently spent nothing.
func (f *fakeExpenses) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// during registers a hook run inside every call, before it answers — how a
// test proves the handler holds no lock while it waits.
func (f *fakeExpenses) during(hook func(context.Context, []int32)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onCall = hook
}

// asked is every project id the provider has been handed, in order and
// flattened across batches; batched keeps the batches apart, which is what a
// portfolio test reads to prove one request costs exactly one call however
// many projects it is about.
func (f *fakeExpenses) asked() []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []int32{}
	for _, batch := range f.batches {
		out = append(out, batch...)
	}
	return out
}

func (f *fakeExpenses) batched() [][]int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.batches)
}

// ExpensesForProjects refuses what the real provider refuses — a batch past
// contracts.MaxExpensesProjects, and one project named twice — so a consumer
// that builds a batch badly fails the test that built it rather than quietly
// getting an answer the real module would never have given.
func (f *fakeExpenses) ExpensesForProjects(ctx context.Context, projectIDs []int32) (map[int32]contracts.ProjectExpenseTotals, error) {
	f.mu.Lock()
	f.batches = append(f.batches, slices.Clone(projectIDs))
	hook, err := f.onCall, f.err
	f.mu.Unlock()

	if hook != nil {
		hook(ctx, projectIDs)
	}
	if err != nil {
		return nil, err
	}
	if len(projectIDs) > contracts.MaxExpensesProjects {
		return nil, fmt.Errorf("projects test: %d projects in one batch, at most %d", len(projectIDs), contracts.MaxExpensesProjects)
	}
	seen := make(map[int32]bool, len(projectIDs))
	for _, id := range projectIDs {
		if seen[id] {
			return nil, fmt.Errorf("projects test: project %d asked for twice in one batch", id)
		}
		seen[id] = true
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[int32]contracts.ProjectExpenseTotals, len(projectIDs))
	for _, id := range projectIDs {
		if entry, ok := f.entries[id]; ok {
			out[id] = entry
		}
	}
	return out, nil
}

// spentBucket is one of the three expense buckets: how many lines are in it,
// what they cost the company and what their billable lines will charge, the
// amounts as the decimal text the contract uses.
func spentBucket(count int64, cost, bill string) contracts.ExpenseBucket {
	return contracts.ExpenseBucket{Count: count, CostAmount: cost, BillAmount: bill}
}

// spentInCurrency is one currency's figures with Total derived from the three
// buckets, which is what the real provider answers whenever nothing lands on
// a rounding boundary. A test whose subject *is* that boundary assigns Total
// itself afterwards.
func spentInCurrency(currency string, approved, submitted, draft contracts.ExpenseBucket) contracts.CurrencyExpenses {
	return contracts.CurrencyExpenses{
		Currency: currency, Approved: approved, Submitted: submitted, Draft: draft,
		Total: contracts.ExpenseBucket{
			Count:      approved.Count + submitted.Count + draft.Count,
			CostAmount: addedAmounts(approved.CostAmount, submitted.CostAmount, draft.CostAmount),
			BillAmount: addedAmounts(approved.BillAmount, submitted.BillAmount, draft.BillAmount),
		},
	}
}

// recordedExpenses is one project's whole answer: its currencies and the day
// its most recently dated line was for. The contract promises a date on every
// project that is in the map at all, so it is not optional here either.
func recordedExpenses(lastEntryDate string, currencies ...contracts.CurrencyExpenses) contracts.ProjectExpenseTotals {
	return contracts.ProjectExpenseTotals{Currencies: currencies, LastEntryDate: &lastEntryDate}
}

// loggedHours is a count of hours as the contract carries it — int64
// hundredths, exact — so a test writes 7.5 and the fake reports 750.
func loggedHours(h float64) int64 { return int64(math.Round(h * 100)) }

// loggedBucket is one of the three buckets: its hours, what they bill and
// what they cost, the amounts as the decimal text the contract uses.
func loggedBucket(h float64, bill, cost string) contracts.ActualsBucket {
	return contracts.ActualsBucket{HoursHundredths: loggedHours(h), BillAmount: bill, CostAmount: cost}
}

// loggedTotals is the three buckets plus the figures that span them, with
// billable defaulting to every hour logged — the ordinary case — so a test
// only says otherwise when that is its subject.
//
// Total is derived from the three buckets, which is what the real provider
// answers whenever nothing lands on a rounding boundary. A test whose subject
// *is* that boundary — where the whole, rounded once, differs from the three
// figures added up — assigns Total itself afterwards, the way one assigns
// LastEntryDate.
func loggedTotals(approved, submitted, draft contracts.ActualsBucket) contracts.ActualsTotals {
	hours := approved.HoursHundredths + submitted.HoursHundredths + draft.HoursHundredths
	return contracts.ActualsTotals{
		Approved: approved, Submitted: submitted, Draft: draft,
		Total: contracts.ActualsBucket{
			HoursHundredths: hours,
			BillAmount:      addedAmounts(approved.BillAmount, submitted.BillAmount, draft.BillAmount),
			CostAmount:      addedAmounts(approved.CostAmount, submitted.CostAmount, draft.CostAmount),
		},
		BillableHoursHundredths: hours,
	}
}

// addedAmounts adds the contract's decimal texts exactly and renders the sum
// with two decimals. It is only ever handed figures a test wrote, so a text
// that is not a decimal is that test's own bug and panicking names it at once.
func addedAmounts(texts ...string) string {
	sum := new(big.Rat)
	for _, text := range texts {
		if text == "" {
			continue
		}
		amount, ok := new(big.Rat).SetString(text)
		if !ok {
			panic(fmt.Sprintf("projects test: %q is not a decimal amount", text))
		}
		sum.Add(sum, amount)
	}
	return sum.FloatString(2)
}

// signIn seeds a caller holding projects:access plus whatever else the test
// needs, and returns their client and user id. Every operation requires
// projects:access (D8), so adding it here keeps each test's permission list
// to the thing it is actually about.
func signIn(t *testing.T, h *modtest.Harness, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, append([]string{"projects:access"}, permissions...)...)
}

// createBody is a valid minimal create body, which tests override one field
// of at a time. Kraft-Verket is the customer, so the default project is a
// customer project rather than an internal one.
func createBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"code":        "KVEM1000",
		"name":        "Kraft-Verket modernisering",
		"customerId":  customerKraftVerket,
		"billingType": "time-and-materials",
	}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

// addRole assigns one user one role on one project directly, because the
// endpoints that assign roles are Task 7's: every test whose subject is what
// a member or a viewer may do needs the assignment to exist first, and the
// insert is the same one InsertProjectRole makes.
func addRole(t *testing.T, h *modtest.Harness, projectID int32, userID uuid.UUID, role string) {
	t.Helper()
	h.Exec(t, `INSERT INTO projects.project_roles (project_id, user_id, role, created_at) VALUES ($1, $2, $3, now())`,
		projectID, userID, role)
}

// milestoneNumericColumns is the subset of insertMilestone's columns that
// are numeric, cast explicitly in the built INSERT: a raw Go float64 or int
// argument otherwise arrives typed float8/int4 over the wire, and Postgres
// only casts that to a numeric(_,_) column on an explicit cast, not an
// assignment one.
var milestoneNumericColumns = map[string]bool{"amount": true, "percent": true, "invoiced_amount": true}

// projectCurrency is the currency one project carries right now, "" for none.
// insertMilestone needs it because a flat amount remembers what it was
// entered in (design §3.2), and a row inserted behind the API has to carry
// the same thing the API would have stamped on it.
func projectCurrency(t *testing.T, h *modtest.Harness, projectID int32) string {
	t.Helper()
	return modtest.One[string](t, h,
		`SELECT coalesce(currency, '') FROM projects.projects WHERE id = $1`, projectID)
}

// insertMilestone inserts one projects.billing_milestones row directly, at
// the SQL level: milestones.go, the CRUD that would otherwise create one, is
// Task 2's, and the guards Task 1 adds (design §3.3's currency guard extended
// to milestones, and the fixed-price guard) need real milestone rows to
// decide against before that API exists. Task 2 reuses it for the same
// reason billing lines' own tests keep insertLineRow around after the POST
// exists: the two rows that operation cannot produce.
//
// overrides is a column name to value map, layered over a valid minimal
// default (one open, amount-priced, 'planned' milestone at position 1); a nil
// value in overrides removes that column from the insert, leaving the
// column's own default (or SQL NULL) to stand — overrides set exactly what
// they name, and nothing else. The one exception is deliberate, not
// incidental: setting "percent" to an actual value, without also naming
// "amount", drops the default amount, since a real milestone never carries
// both (design §3.2) — the DB itself does not enforce that; only Go's rules
// do (there are no CHECK constraints here, house style). Setting "percent"
// to nil (asking for a milestone with neither) does not trigger that — nil
// always means "leave this column out", never "and drop something else too".
func insertMilestone(t *testing.T, h *modtest.Harness, projectID int32, overrides map[string]any) int32 {
	t.Helper()
	now := time.Now().UTC()
	row := map[string]any{
		"project_id":         projectID,
		"name":               "Milestone",
		"amount":             1000.00,
		"status":             "planned",
		"position":           int32(1),
		"created_by_user_id": uuid.New(),
		"created_at":         now,
		"updated_at":         now,
	}
	// A flat amount carries the currency it was entered in, exactly as the API
	// stamps it (design §3.2). The project's own currency is that currency, so
	// the default follows it rather than being another thing to remember;
	// a test that wants a different one names amount_currency itself.
	if currency := projectCurrency(t, h, projectID); currency != "" {
		row["amount_currency"] = currency
	}
	if v, ok := overrides["percent"]; ok && v != nil {
		if _, keepingAmount := overrides["amount"]; !keepingAmount {
			delete(row, "amount")
			delete(row, "amount_currency")
		}
	}
	for col, v := range overrides {
		if v == nil {
			delete(row, col)
			continue
		}
		row[col] = v
	}

	cols := make([]string, 0, len(row))
	for col := range row {
		cols = append(cols, col)
	}
	sort.Strings(cols)

	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, col := range cols {
		placeholder := fmt.Sprintf("$%d", i+1)
		if milestoneNumericColumns[col] {
			placeholder += "::numeric"
		}
		placeholders[i] = placeholder
		args[i] = row[col]
	}
	query := fmt.Sprintf("INSERT INTO projects.billing_milestones (%s) VALUES (%s) RETURNING id",
		strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	return modtest.One[int32](t, h, query, args...)
}

// createProject creates a project with createBody(overrides) and fails the
// test if it is not created. A nil override value removes that field, which
// is how a test builds an internal project (no customerId).
func createProject(t *testing.T, c *modtest.Client, overrides map[string]any) projectJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/projects", createBody(overrides))
	if r.Status != http.StatusCreated {
		t.Fatalf("create project: status %d body %s, want 201", r.Status, r.Body)
	}
	var project projectJSON
	r.JSON(&project)
	return project
}

// projectJSON decodes ProjectResponse. financials is a pointer because its
// absence is the whole of D12: a test that must prove the key is not there
// decodes into a map instead (rawJSON), since a nil pointer cannot tell
// "absent" from "null".
type projectJSON struct {
	Id                    int32            `json:"id"`
	Code                  string           `json:"code"`
	Name                  string           `json:"name"`
	Description           *string          `json:"description"`
	CustomerId            *int32           `json:"customerId"`
	CustomerName          *string          `json:"customerName"`
	Internal              bool             `json:"internal"`
	Status                string           `json:"status"`
	StartDate             *string          `json:"startDate"`
	EndDate               *string          `json:"endDate"`
	BillingType           string           `json:"billingType"`
	BudgetHours           *float64         `json:"budgetHours"`
	Revision              int32            `json:"revision"`
	CreatedAt             time.Time        `json:"createdAt"`
	UpdatedAt             time.Time        `json:"updatedAt"`
	Managers              []personJSON     `json:"managers"`
	Capabilities          capabilitiesJSON `json:"capabilities"`
	BillingLinesAvailable bool             `json:"billingLinesAvailable"`
	Financials            *financialsJSON  `json:"financials"`
}

type personJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
}

type capabilitiesJSON struct {
	CanManage           bool `json:"canManage"`
	CanContribute       bool `json:"canContribute"`
	CanSeeFinancials    bool `json:"canSeeFinancials"`
	CanManageMilestones bool `json:"canManageMilestones"`
	CanSeeCosts         bool `json:"canSeeCosts"`
}

type financialsJSON struct {
	Currency         *string  `json:"currency"`
	FixedPriceAmount *float64 `json:"fixedPriceAmount"`
	BudgetAmount     *float64 `json:"budgetAmount"`
	DefaultBillRate  *float64 `json:"defaultBillRate"`
}

// taskJSON decodes TaskResponse and, since it is that shape plus the two
// project fields, MyTaskResponse as well: one decoder, so a test that reads a
// task through the tree and through my-tasks compares like with like.
// assignee is a pointer because an unassigned task has no key at all.
type taskJSON struct {
	Id            int32         `json:"id"`
	ProjectId     int32         `json:"projectId"`
	ParentTaskId  *int32        `json:"parentTaskId"`
	Title         string        `json:"title"`
	Description   *string       `json:"description"`
	Status        string        `json:"status"`
	Assignee      *assigneeJSON `json:"assignee"`
	StartDate     *string       `json:"startDate"`
	DueDate       *string       `json:"dueDate"`
	EstimateHours *float64      `json:"estimateHours"`
	Position      int32         `json:"position"`
	CompletedAt   *time.Time    `json:"completedAt"`
	Revision      int32         `json:"revision"`
	CreatedAt     time.Time     `json:"createdAt"`
	UpdatedAt     time.Time     `json:"updatedAt"`
	Checklist     checklistJSON `json:"checklist"`
	CommentCount  int32         `json:"commentCount"`
	Subtasks      []taskJSON    `json:"subtasks"`
	ProjectCode   string        `json:"projectCode"`
	ProjectName   string        `json:"projectName"`
}

// assigneeJSON decodes TaskAssignee. active is the user directory's answer,
// exactly as a role assignment's is: an account disabled after the task was
// assigned keeps the assignment and renders inactive.
type assigneeJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	Active      bool      `json:"active"`
}

// checklistJSON decodes a task's checklist progress, which is a pair of
// counts rather than the items themselves: the tree renders progress, the
// drawer reads the items (Task 3).
type checklistJSON struct {
	Total int32 `json:"total"`
	Done  int32 `json:"done"`
}

// tasksPath is a project's task collection; taskPath is one task, which is
// addressed without its project because a task id already names one.
func tasksPath(projectID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/tasks", projectID)
}

func taskPath(taskID int32) string {
	return fmt.Sprintf("/api/v1/projects/tasks/%d", taskID)
}

// taskBody is a valid minimal create body — nothing but a title, since that
// is the only required field — which tests override one field of at a time. A
// nil override value removes that field, the same convention createBody uses.
func taskBody(overrides map[string]any) map[string]any {
	body := map[string]any{"title": "Skriv spesifikasjonen"}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

// createTask creates one task and fails the test unless it was created.
func createTask(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) taskJSON {
	t.Helper()
	r := c.Do(http.MethodPost, tasksPath(projectID), taskBody(overrides))
	if r.Status != http.StatusCreated {
		t.Fatalf("create task: status %d body %s, want 201", r.Status, r.Body)
	}
	var task taskJSON
	r.JSON(&task)
	return task
}

// getTasks reads a project's task tree, failing the test unless it answered
// 200. The order is the endpoint's: top-level tasks by position, each with
// its subtasks by position.
func getTasks(t *testing.T, c *modtest.Client, projectID int32) []taskJSON {
	t.Helper()
	r := c.Do(http.MethodGet, tasksPath(projectID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list tasks: status %d body %s, want 200", r.Status, r.Body)
	}
	var tasks []taskJSON
	r.JSON(&tasks)
	return tasks
}

// milestonesPath is a project's invoice plan; milestonePath is one milestone,
// which is addressed without its project because a milestone id already names
// one — the same shape a task is addressed in.
func milestonesPath(projectID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/milestones", projectID)
}

func milestonePath(milestoneID int32) string {
	return fmt.Sprintf("/api/v1/projects/milestones/%d", milestoneID)
}

func milestonePositionPath(milestoneID int32) string {
	return milestonePath(milestoneID) + "/position"
}

func milestoneStatusPath(milestoneID int32) string {
	return milestonePath(milestoneID) + "/status"
}

// milestoneBody is a valid minimal create body — a name and a flat amount,
// the cheapest milestone there is — which tests override one field of at a
// time. A nil override value removes that field, the same convention
// createBody uses.
func milestoneBody(overrides map[string]any) map[string]any {
	body := map[string]any{"name": "Oppstart", "amount": 100000}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

func postMilestone(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, milestonesPath(projectID), milestoneBody(overrides))
}

// createMilestone is postMilestone for a test that expects it to be created.
func createMilestone(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) milestoneJSON {
	t.Helper()
	r := postMilestone(t, c, projectID, overrides)
	if r.Status != http.StatusCreated {
		t.Fatalf("create milestone: status %d body %s, want 201", r.Status, r.Body)
	}
	var milestone milestoneJSON
	r.JSON(&milestone)
	return milestone
}

func readMilestones(t *testing.T, c *modtest.Client, projectID int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, milestonesPath(projectID), nil)
}

// getMilestones is readMilestones for a test that expects to be shown the
// plan. The order is the endpoint's: by position, cancelled last.
func getMilestones(t *testing.T, c *modtest.Client, projectID int32) milestonePlanJSON {
	t.Helper()
	r := readMilestones(t, c, projectID)
	if r.Status != http.StatusOK {
		t.Fatalf("list milestones: status %d body %s, want 200", r.Status, r.Body)
	}
	var plan milestonePlanJSON
	r.JSON(&plan)
	return plan
}

// getMilestone reads one milestone and fails the test unless it answered 200.
func getMilestone(t *testing.T, c *modtest.Client, milestoneID int32) milestoneJSON {
	t.Helper()
	r := c.Do(http.MethodGet, milestonePath(milestoneID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get milestone: status %d body %s, want 200", r.Status, r.Body)
	}
	var milestone milestoneJSON
	r.JSON(&milestone)
	return milestone
}

// putMilestone sends one full-replace edit and returns the response.
func putMilestone(t *testing.T, c *modtest.Client, m milestoneJSON, overrides map[string]any) *modtest.Response {
	t.Helper()
	body := map[string]any{"name": m.Name, "revision": m.Revision}
	if m.Description != nil {
		body["description"] = *m.Description
	}
	if m.PlannedDate != nil {
		body["plannedDate"] = *m.PlannedDate
	}
	if m.Amount != nil {
		body["amount"] = *m.Amount
	}
	if m.Percent != nil {
		body["percent"] = *m.Percent
	}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return c.Do(http.MethodPut, milestonePath(m.Id), body)
}

// changeMilestone is putMilestone for a test that expects it to be applied.
func changeMilestone(t *testing.T, c *modtest.Client, m milestoneJSON, overrides map[string]any) milestoneJSON {
	t.Helper()
	r := putMilestone(t, c, m, overrides)
	if r.Status != http.StatusOK {
		t.Fatalf("change milestone: status %d body %s, want 200", r.Status, r.Body)
	}
	var changed milestoneJSON
	r.JSON(&changed)
	return changed
}

// moveMilestoneStatus sends one status move and returns the response, whatever
// it is: half the cases in the move matrix are refusals.
func moveMilestoneStatus(t *testing.T, c *modtest.Client, m milestoneJSON, status string, overrides map[string]any) *modtest.Response {
	t.Helper()
	body := map[string]any{"status": status, "revision": m.Revision}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return c.Do(http.MethodPost, milestoneStatusPath(m.Id), body)
}

// movedMilestone is moveMilestoneStatus for a test that expects the move to
// be made, which is also how a test builds a milestone in a later status.
func movedMilestone(t *testing.T, c *modtest.Client, m milestoneJSON, status string, overrides map[string]any) milestoneJSON {
	t.Helper()
	r := moveMilestoneStatus(t, c, m, status, overrides)
	if r.Status != http.StatusOK {
		t.Fatalf("move milestone to %q: status %d body %s, want 200", status, r.Status, r.Body)
	}
	var moved milestoneJSON
	r.JSON(&moved)
	return moved
}

// moveMilestone sends one position change and returns the response.
func moveMilestone(t *testing.T, c *modtest.Client, m milestoneJSON, position int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, milestonePositionPath(m.Id),
		map[string]any{"position": position, "revision": m.Revision})
}

// milestoneJSON decodes BillingMilestoneResponse. Every optional field is a
// pointer: a milestone carries exactly one of amount and percent, and the
// four invoice fields exist only while it is invoiced.
type milestoneJSON struct {
	Id               int32                     `json:"id"`
	ProjectId        int32                     `json:"projectId"`
	Name             string                    `json:"name"`
	Description      *string                   `json:"description"`
	PlannedDate      *string                   `json:"plannedDate"`
	Amount           *float64                  `json:"amount"`
	Percent          *float64                  `json:"percent"`
	EffectiveAmount  *float64                  `json:"effectiveAmount"`
	Currency         *string                   `json:"currency"`
	Status           string                    `json:"status"`
	Position         int32                     `json:"position"`
	Overdue          bool                      `json:"overdue"`
	ReadyAt          *time.Time                `json:"readyAt"`
	ReadyBy          *milestonePersonJSON      `json:"readyBy"`
	InvoicedAt       *time.Time                `json:"invoicedAt"`
	InvoicedBy       *milestonePersonJSON      `json:"invoicedBy"`
	InvoiceReference *string                   `json:"invoiceReference"`
	InvoiceDate      *string                   `json:"invoiceDate"`
	Revision         int32                     `json:"revision"`
	CreatedAt        time.Time                 `json:"createdAt"`
	UpdatedAt        time.Time                 `json:"updatedAt"`
	Capabilities     milestoneCapabilitiesJSON `json:"capabilities"`
}

// milestonePersonJSON decodes BillingMilestonePerson — who marked a milestone
// ready or invoiced. active is the user directory's answer, exactly as a
// task assignee's is: an account disabled afterwards keeps the stamp.
type milestonePersonJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	Active      bool      `json:"active"`
}

// milestoneCapabilitiesJSON decodes BillingMilestoneCapabilities, the eight
// answers that save the frontend re-deriving design §3.2's move table.
type milestoneCapabilitiesJSON struct {
	CanEdit         bool `json:"canEdit"`
	CanDelete       bool `json:"canDelete"`
	CanMarkReady    bool `json:"canMarkReady"`
	CanMarkPlanned  bool `json:"canMarkPlanned"`
	CanMarkInvoiced bool `json:"canMarkInvoiced"`
	CanUndoInvoiced bool `json:"canUndoInvoiced"`
	CanCancel       bool `json:"canCancel"`
	CanReopen       bool `json:"canReopen"`
}

// milestonePlanJSON decodes BillingMilestonePlanResponse: the milestones and
// what they add up to.
type milestonePlanJSON struct {
	Milestones []milestoneJSON     `json:"milestones"`
	Totals     milestoneTotalsJSON `json:"totals"`
}

// milestoneTotalsJSON decodes BillingMilestonePlanTotals. There is no
// cancelled sum: a cancelled milestone bills nothing and may be denominated
// in a currency the project has since moved off, so it is left out of the
// plan's arithmetic entirely. The three optional numbers exist only against a
// fixed price, and unplanned and overPlanned are two sides of one comparison,
// so never both.
type milestoneTotalsJSON struct {
	Currency    *string  `json:"currency"`
	Planned     float64  `json:"planned"`
	Ready       float64  `json:"ready"`
	Invoiced    float64  `json:"invoiced"`
	FixedPrice  *float64 `json:"fixedPrice"`
	Unplanned   *float64 `json:"unplanned"`
	OverPlanned *float64 `json:"overPlanned"`
}

// effectiveAmount is a milestone's effectiveAmount for a test that expects
// there to be one. The field is optional in the contract — absent for a
// cancelled milestone whose percent the project can no longer price — so
// every other assertion has to say which case it is asserting.
func effectiveAmount(t *testing.T, m milestoneJSON) float64 {
	t.Helper()
	if m.EffectiveAmount == nil {
		t.Fatalf("milestone %q has no effectiveAmount", m.Name)
	}
	return *m.EffectiveAmount
}

// milestoneNames and milestonePositions are the two orderings a plan test
// asserts on: which milestones came back, and the numbers they carry.
func milestoneNames(plan milestonePlanJSON) []string {
	out := make([]string, 0, len(plan.Milestones))
	for _, m := range plan.Milestones {
		out = append(out, m.Name)
	}
	return out
}

func milestonePositions(plan milestonePlanJSON) []int32 {
	out := make([]int32, 0, len(plan.Milestones))
	for _, m := range plan.Milestones {
		out = append(out, m.Position)
	}
	return out
}

// economyPath is a project's economy read.
func economyPath(projectID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/economy", projectID)
}

// readEconomy asks for a project's economy and returns whatever came back:
// half the cases here are about what a caller is *not* shown, and one is a
// 404.
func readEconomy(t *testing.T, c *modtest.Client, projectID int32, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, economyPath(projectID), nil, opts...)
}

// getEconomy is readEconomy for a test that expects to be shown the economy.
func getEconomy(t *testing.T, c *modtest.Client, projectID int32) economyJSON {
	t.Helper()
	r := readEconomy(t, c, projectID)
	if r.Status != http.StatusOK {
		t.Fatalf("read the economy: status %d body %s, want 200", r.Status, r.Body)
	}
	var economy economyJSON
	r.JSON(&economy)
	return economy
}

// rawEconomy is getEconomy decoded into a map, for the assertions whose whole
// subject is that a key is *not* there: a nil pointer cannot tell "absent"
// from "null", and this module's shaping is by absence.
func rawEconomy(t *testing.T, c *modtest.Client, projectID int32) map[string]any {
	t.Helper()
	r := readEconomy(t, c, projectID)
	if r.Status != http.StatusOK {
		t.Fatalf("read the economy: status %d body %s, want 200", r.Status, r.Body)
	}
	raw := map[string]any{}
	r.JSON(&raw)
	return raw
}

// economyJSON decodes ProjectEconomyResponse. Every shaped field is a
// pointer, because absent is what this endpoint says instead of zero.
type economyJSON struct {
	TimeTracking      bool                  `json:"timeTracking"`
	ExpenseTracking   bool                  `json:"expenseTracking"`
	Expenses          *economyExpensesJSON  `json:"expenses"`
	Currency          *string               `json:"currency"`
	Budget            economyBudgetJSON     `json:"budget"`
	Actuals           *economyActualsJSON   `json:"actuals"`
	Lines             []economyLineJSON     `json:"lines"`
	TaskEstimateHours *float64              `json:"taskEstimateHours"`
	BudgetUsed        *budgetUsedJSON       `json:"budgetUsed"`
	OverBudget        bool                  `json:"overBudget"`
	Milestones        *milestoneTotalsJSON  `json:"milestones"`
	Cost              *economyCostJSON      `json:"cost"`
	WorkTypes         []economyWorkTypeJSON `json:"workTypes"`
}

type economyBudgetJSON struct {
	Hours       *float64 `json:"hours"`
	LinesHours  *float64 `json:"linesHours"`
	Amount      *float64 `json:"amount"`
	FixedPrice  *float64 `json:"fixedPrice"`
	LinesAmount *float64 `json:"linesAmount"`
}

type economyActualsJSON struct {
	Approved         economyBucketJSON `json:"approved"`
	Submitted        economyBucketJSON `json:"submitted"`
	Draft            economyBucketJSON `json:"draft"`
	TotalHours       float64           `json:"totalHours"`
	TotalAmount      *float64          `json:"totalAmount"`
	UnpricedHours    float64           `json:"unpricedHours"`
	BillableHours    float64           `json:"billableHours"`
	NonBillableHours float64           `json:"nonBillableHours"`
	LastEntryDate    *string           `json:"lastEntryDate"`
}

type economyBucketJSON struct {
	Hours  float64  `json:"hours"`
	Amount *float64 `json:"amount"`
}

type economyLineJSON struct {
	BillingLineId  *int32              `json:"billingLineId"`
	Code           *string             `json:"code"`
	Active         *bool               `json:"active"`
	BudgetHours    *float64            `json:"budgetHours"`
	BudgetAmount   *float64            `json:"budgetAmount"`
	Actuals        *economyActualsJSON `json:"actuals"`
	UsedPercent    *float64            `json:"usedPercent"`
	RemainingHours *float64            `json:"remainingHours"`
	OverBudget     bool                `json:"overBudget"`
}

type budgetUsedJSON struct {
	Basis           string  `json:"basis"`
	Percent         float64 `json:"percent"`
	ApprovedPercent float64 `json:"approvedPercent"`
}

type economyCostJSON struct {
	Approved      float64  `json:"approved"`
	Submitted     float64  `json:"submitted"`
	Draft         float64  `json:"draft"`
	Total         float64  `json:"total"`
	ExpenseCost   *float64 `json:"expenseCost"`
	Margin        float64  `json:"margin"`
	UncostedHours float64  `json:"uncostedHours"`
}

// economyExpensesJSON decodes ProjectEconomyExpenses. Every main figure is a
// pointer because the ten of them are present or absent *together*, on the
// project carrying a currency: a project with none reports every line it has
// under otherCurrencies and has no figures of its own at all.
type economyExpensesJSON struct {
	Approved        *expenseBucketJSON    `json:"approved"`
	Submitted       *expenseBucketJSON    `json:"submitted"`
	Draft           *expenseBucketJSON    `json:"draft"`
	TotalCost       *float64              `json:"totalCost"`
	TotalAmount     *float64              `json:"totalAmount"`
	ReadyCount      *int32                `json:"readyCount"`
	ReadyAmount     *float64              `json:"readyAmount"`
	InvoicedCount   *int32                `json:"invoicedCount"`
	InvoicedAmount  *float64              `json:"invoicedAmount"`
	UnpricedCount   *int32                `json:"unpricedCount"`
	OtherCurrencies []expenseCurrencyJSON `json:"otherCurrencies"`
	LastEntryDate   *string               `json:"lastEntryDate"`
	// SupplierInvoices is the part of the buckets that is supplier invoices,
	// absent when there are none.
	SupplierInvoices *economySupplierInvoicesJSON `json:"supplierInvoices"`
}

// economySupplierInvoicesJSON decodes ProjectEconomySupplierInvoices.
type economySupplierInvoicesJSON struct {
	Approved  expenseBucketJSON `json:"approved"`
	Submitted expenseBucketJSON `json:"submitted"`
	Draft     expenseBucketJSON `json:"draft"`
	Total     expenseBucketJSON `json:"total"`
}

// expenseBucketJSON decodes ProjectEconomyExpenseBucket: a count of lines,
// what they cost and what they bill.
type expenseBucketJSON struct {
	Count  int32   `json:"count"`
	Cost   float64 `json:"cost"`
	Amount float64 `json:"amount"`
}

// expenseCurrencyJSON decodes ProjectEconomyExpenseCurrency — one currency
// the project itself is not in, reported rather than converted or dropped.
type expenseCurrencyJSON struct {
	Currency    string  `json:"currency"`
	Count       int32   `json:"count"`
	Cost        float64 `json:"cost"`
	Amount      float64 `json:"amount"`
	ReadyAmount float64 `json:"readyAmount"`
}

// validationProblemJSON decodes the field-error body every §4.1 refusal
// answers with.
type validationProblemJSON struct {
	Title  string              `json:"title"`
	Errors map[string][]string `json:"errors"`
}

// problemJSON decodes the bare RFC 7807 body the two refusals that carry no
// field map answer with: a list's invalid query parameters and an update's
// stale revision.
type problemJSON struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int32  `json:"status"`
}

// paginationJSON decodes common.yaml's PaginationMetadata, shared by the
// project list and the timeline.
type paginationJSON struct {
	Page            int32 `json:"page"`
	PageSize        int32 `json:"pageSize"`
	TotalCount      int32 `json:"totalCount"`
	TotalPages      int32 `json:"totalPages"`
	HasNextPage     bool  `json:"hasNextPage"`
	HasPreviousPage bool  `json:"hasPreviousPage"`
}
