package expenses_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// contracts.ProjectExpenses as this module provides it: what a project's
// expenses cost and bill, per currency, in the three buckets the unit's
// status puts a line in.
//
// Every test here builds the provider the way Compose does, but with a
// project directory that fails the test the moment anything asks it a
// question: serving these figures must not call back into projects, which
// would be a module cycle at request time. The provider is given nothing but
// the pool.

// forbiddenProjects is a contracts.ProjectDirectory no one may call. It is
// what the provider is composed with here, so a single directory lookup while
// serving fails the test that made it — the rule newExpensesHarness enforces
// for locked transactions, tightened to "never" for this one collaborator.
type forbiddenProjects struct{ t *testing.T }

var _ contracts.ProjectDirectory = (*forbiddenProjects)(nil)

func (f *forbiddenProjects) deny(method string) {
	f.t.Helper()
	f.t.Errorf("the project expenses provider called the project directory (%s): it must serve from this module's own tables alone", method)
}

func (f *forbiddenProjects) Project(context.Context, int32) (*contracts.ProjectEntry, error) {
	f.deny("Project")
	return nil, nil
}

func (f *forbiddenProjects) Role(context.Context, int32, uuid.UUID) (string, error) {
	f.deny("Role")
	return "", nil
}

func (f *forbiddenProjects) BillingLine(context.Context, int32, int32) (*contracts.BillingLineEntry, error) {
	f.deny("BillingLine")
	return nil, nil
}

func (f *forbiddenProjects) BillingLines(context.Context, int32) ([]contracts.BillingLineEntry, error) {
	f.deny("BillingLines")
	return nil, nil
}

func (f *forbiddenProjects) ProjectsForUser(context.Context, uuid.UUID) ([]contracts.ProjectEntry, error) {
	f.deny("ProjectsForUser")
	return nil, nil
}

func (f *forbiddenProjects) ProjectsForCustomer(context.Context, int32) ([]contracts.ProjectEntry, error) {
	f.deny("ProjectsForCustomer")
	return nil, nil
}

func (f *forbiddenProjects) Projects(context.Context, []int32) ([]contracts.ProjectEntry, error) {
	f.deny("Projects")
	return nil, nil
}

func (f *forbiddenProjects) ProjectByCode(context.Context, string) (*contracts.ProjectEntry, error) {
	f.deny("ProjectByCode")
	return nil, nil
}

func (f *forbiddenProjects) Task(context.Context, int32) (*contracts.TaskEntry, error) {
	f.deny("Task")
	return nil, nil
}

func (f *forbiddenProjects) OpenTasksForUser(context.Context, uuid.UUID) ([]contracts.TaskEntry, error) {
	f.deny("OpenTasksForUser")
	return nil, nil
}

func (f *forbiddenProjects) CanLogTime(context.Context, int32, uuid.UUID) (bool, error) {
	f.deny("CanLogTime")
	return false, nil
}

// expensesProvider builds the provider the way module.Compose does — from the
// harness's own Deps, so the pool is the test database's — with the project
// directory replaced by one that may not be called.
func expensesProvider(t *testing.T, h *harness) contracts.ProjectExpenses {
	t.Helper()
	build := expenses.Module().Expenses
	if build == nil {
		t.Fatal("the expenses module declares no Expenses provider")
	}
	// The local is not called "deps": contractsscope_internal_test.go scans
	// every .go file in this package for the two directory fields spelled
	// against that receiver name, which is how it keeps every real
	// cross-module call in contractscalls.go, and it reads test files too.
	d := h.Deps()
	d.Projects = &forbiddenProjects{t: t}
	return build(d)
}

// The dates the fixtures use. Two of them, so "the latest" is a fact a test
// can assert rather than the only date there is.
const (
	expenseDay      = "2026-03-10"
	laterExpenseDay = "2026-03-18"
)

// recordedExpense is one row seeded straight into expenses.entries: the
// provider reads whatever is there, whatever path put it there, and a test
// about statuses, currencies and the invoicing stamp needs combinations no
// single create call can reach.
type recordedExpense struct {
	project    int32
	claim      *int64
	kind       string // "" is an outlay
	date       string // "" is expenseDay
	currency   string // "" is NOK
	gross      string
	vat        any // nil for no VAT entered
	paidBy     any // "employee" or "company"; nil on mileage and per diem
	billable   bool
	billAmount any // nil for a billable line nobody has priced
	status     string
	invoiced   bool
}

// recordExpense seeds one row, so a test reads as the list of what was
// recorded.
func recordExpense(t *testing.T, h *harness, userID uuid.UUID, e recordedExpense) {
	t.Helper()
	kind := e.kind
	if kind == "" {
		kind = "outlay"
	}
	date := e.date
	if date == "" {
		date = expenseDay
	}
	currency := e.currency
	if currency == "" {
		currency = "NOK"
	}
	status := e.status
	if status == "" {
		status = "draft"
	}
	h.Exec(t, `INSERT INTO expenses.entries
	    (user_id, created_by_user_id, claim_id, kind, entry_date, description, currency,
	     gross_amount, vat_amount, paid_by, project_id, billable, bill_amount, status,
	     invoiced_at, created_at, updated_at)
	    VALUES ($1, $1, $2, $3, $4::date, 'seeded', $5, $6::numeric, $7::numeric, $8, $9, $10,
	            $11::numeric, $12, CASE WHEN $13::boolean THEN now() END, now(), now())`,
		userID, e.claim, kind, date, currency, e.gross, e.vat, e.paidBy, e.project, e.billable,
		e.billAmount, status, e.invoiced)
}

// recordClaim seeds a travel claim in a status, and answers its id. Its lines
// are ordinary rows carrying that id — and they take their status from it.
func recordClaim(t *testing.T, h *harness, userID uuid.UUID, project int32, status string) *int64 {
	t.Helper()
	id := modtest.One[int64](t, h.Harness, `INSERT INTO expenses.claims
	    (user_id, created_by_user_id, purpose, departure_at, return_at, project_id, status, created_at, updated_at)
	    VALUES ($1, $1, 'Seeded trip', now(), now(), $2, $3, now(), now())
	    RETURNING id`, userID, project, status)
	return &id
}

// projectExpensesOf is the provider's answer for one project, and fails the
// test when the project is absent from it.
func projectExpensesOf(t *testing.T, p contracts.ProjectExpenses, id int32) contracts.ProjectExpenseTotals {
	t.Helper()
	got, err := p.ExpensesForProjects(t.Context(), []int32{id})
	if err != nil {
		t.Fatalf("expenses for projects: %v", err)
	}
	totals, ok := got[id]
	if !ok {
		t.Fatalf("project %d is absent from %v, want its totals", id, got)
	}
	return totals
}

// currencyOf picks one currency out of a project's totals.
func currencyOf(t *testing.T, totals contracts.ProjectExpenseTotals, code string) contracts.CurrencyExpenses {
	t.Helper()
	for _, c := range totals.Currencies {
		if c.Currency == code {
			return c
		}
	}
	t.Fatalf("no %s among %v", code, totals.Currencies)
	return contracts.CurrencyExpenses{}
}

// wantBucket fails the test unless b is exactly count, cost and bill.
func wantBucket(t *testing.T, name string, b contracts.ExpenseBucket, count int64, cost, bill string) {
	t.Helper()
	if b.Count != count {
		t.Errorf("%s count = %d, want %d", name, b.Count, count)
	}
	if b.CostAmount != cost {
		t.Errorf("%s cost amount = %q, want %q", name, b.CostAmount, cost)
	}
	if b.BillAmount != bill {
		t.Errorf("%s bill amount = %q, want %q", name, b.BillAmount, bill)
	}
}

// TestExpensesModuleProvidesProjectExpenses: the module value carries the
// provider, so Compose has one to resolve at all.
func TestExpensesModuleProvidesProjectExpenses(t *testing.T) {
	t.Parallel()
	if expenses.Module().Expenses == nil {
		t.Fatal("expenses.Module().Expenses is nil: nothing would provide contracts.ProjectExpenses")
	}
}

// Every status lands in its bucket, and a claim's line is judged through its
// claim: the line's own status column stays at the default nobody reads, so a
// trip's approved line belongs in approved and a rejected trip's line in
// draft — beside the rejected standalone expense, which is in draft for the
// same reason time's rejected entry is.
func TestProjectExpensesBucketsEveryStatusThroughTheUnit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	approvedTrip := recordClaim(t, h, user, projectKraftVerket, "approved")
	rejectedTrip := recordClaim(t, h, user, projectKraftVerket, "rejected")

	for _, e := range []recordedExpense{
		{project: projectKraftVerket, gross: "100.00", status: "draft"},
		{project: projectKraftVerket, gross: "200.00", status: "rejected"},
		{project: projectKraftVerket, gross: "400.00", status: "submitted"},
		{project: projectKraftVerket, gross: "800.00", status: "approved"},
		// Both claim lines keep the default 'draft' their column was created
		// with; the claim is what decides.
		{project: projectKraftVerket, claim: approvedTrip, gross: "1600.00"},
		{project: projectKraftVerket, claim: rejectedTrip, gross: "3200.00"},
	} {
		recordExpense(t, h, user, e)
	}

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "approved", nok.Approved, 2, "2400.00", "0.00")
	wantBucket(t, "submitted", nok.Submitted, 1, "400.00", "0.00")
	wantBucket(t, "draft", nok.Draft, 3, "3500.00", "0.00")
	wantBucket(t, "total", nok.Total, 6, "6300.00", "0.00")
}

// Cost is the net, whoever paid: an outlay the company paid costs the project
// exactly what one the employee is reimbursed for does, and the VAT is out of
// both. What the employee is owed is a different figure entirely and never
// appears here.
func TestProjectExpensesCostIsTheNetWhoeverPaid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "125.00", vat: "25.00",
		paidBy: "employee", status: "approved"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "250.00", vat: "50.00",
		paidBy: "company", status: "approved"})

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "approved", nok.Approved, 2, "300.00", "0.00")
}

// Mileage and per diem carry no VAT, so their net is their gross — and a per
// diem day is never billable, so it bills nothing however the figures are
// read.
func TestProjectExpensesMileageAndPerDiemCostTheirGross(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	trip := recordClaim(t, h, user, projectKraftVerket, "approved")
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, kind: "mileage", gross: "300.00",
		status: "approved"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, claim: trip, kind: "per_diem",
		gross: "400.00"})

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "approved", nok.Approved, 2, "700.00", "0.00")
	if nok.UnpricedCount != 0 {
		t.Errorf("unpriced = %d, want 0: neither line is billable, and a non-billable line is never unpriced", nok.UnpricedCount)
	}
}

// A billable outlay bills its stored bill amount — the net plus its markup —
// while it still costs the net. The two figures are independent and both are
// reported.
func TestProjectExpensesBillsTheStoredBillAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "125.00", vat: "25.00",
		paidBy: "employee", billable: true, billAmount: "110.00", status: "approved"})
	// Non-billable, priced by nobody: it costs and bills nothing.
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "50.00",
		paidBy: "company", status: "approved"})

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "approved", nok.Approved, 2, "150.00", "110.00")
	if nok.UnpricedCount != 0 {
		t.Errorf("unpriced = %d, want 0", nok.UnpricedCount)
	}
}

// A billable line with no bill amount — billable mileage with no customer
// rate per kilometre, the case the design names — is counted as unpriced and
// is *not* summed as zero: a missing price is not a price of nothing, and a
// consumer that read it as one would show a project billing less than it
// will.
func TestProjectExpensesCountsABillableLineWithoutAPriceAsUnpriced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, kind: "mileage", gross: "300.00",
		billable: true, status: "approved"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "100.00", paidBy: "company",
		billable: true, billAmount: "125.00", status: "submitted"})
	// Non-billable, and therefore never unpriced: it was not meant to carry a
	// price at all.
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "40.00", paidBy: "company",
		status: "draft"})

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "approved", nok.Approved, 1, "300.00", "0.00")
	wantBucket(t, "submitted", nok.Submitted, 1, "100.00", "125.00")
	wantBucket(t, "draft", nok.Draft, 1, "40.00", "0.00")
	if nok.UnpricedCount != 1 {
		t.Errorf("unpriced = %d, want 1: the billable mileage with no customer rate, and nothing else", nok.UnpricedCount)
	}
	if nok.ReadyCount != 0 {
		t.Errorf("ready = %d, want 0: the approved line has no price and the priced one is not approved", nok.ReadyCount)
	}
}

// Ready and invoiced: ready is the approved, billable, priced line nobody has
// invoiced yet, and invoiced is the stamp. An invoiced line stays in the
// approved bucket — invoicing is not a status here — and is no longer ready.
func TestProjectExpensesSplitsReadyFromInvoiced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	for _, e := range []recordedExpense{
		{project: projectKraftVerket, gross: "100.00", paidBy: "company", billable: true, billAmount: "110.00", status: "approved"},
		{project: projectKraftVerket, gross: "200.00", paidBy: "company", billable: true, billAmount: "220.00", status: "approved", invoiced: true},
		{project: projectKraftVerket, gross: "400.00", paidBy: "company", billable: true, billAmount: "440.00", status: "submitted"},
		{project: projectKraftVerket, gross: "800.00", paidBy: "company", billable: true, status: "approved"},
		{project: projectKraftVerket, gross: "1600.00", paidBy: "company", status: "approved"},
	} {
		recordExpense(t, h, user, e)
	}

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "approved", nok.Approved, 4, "2700.00", "330.00")
	if nok.ReadyCount != 1 || nok.ReadyAmount != "110.00" {
		t.Errorf("ready = %d lines worth %q, want 1 worth \"110.00\": the invoiced one has gone, the submitted one is not approved, the unpriced one has no amount and the last is not billable",
			nok.ReadyCount, nok.ReadyAmount)
	}
	if nok.InvoicedCount != 1 || nok.InvoicedAmount != "220.00" {
		t.Errorf("invoiced = %d lines worth %q, want 1 worth \"220.00\"", nok.InvoicedCount, nok.InvoicedAmount)
	}
	if nok.UnpricedCount != 1 {
		t.Errorf("unpriced = %d, want 1", nok.UnpricedCount)
	}
}

// A claim's line is ready through its claim too: the approval that makes a
// line invoiceable is the trip's.
func TestProjectExpensesReadyThroughTheClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	approved := recordClaim(t, h, user, projectKraftVerket, "approved")
	submitted := recordClaim(t, h, user, projectKraftVerket, "submitted")
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, claim: approved, gross: "100.00",
		paidBy: "employee", billable: true, billAmount: "110.00"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, claim: submitted, gross: "200.00",
		paidBy: "employee", billable: true, billAmount: "220.00"})

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	if nok.ReadyCount != 1 || nok.ReadyAmount != "110.00" {
		t.Errorf("ready = %d lines worth %q, want 1 worth \"110.00\": the approved trip's line, not the submitted trip's",
			nok.ReadyCount, nok.ReadyAmount)
	}
}

// Two currencies on one project are two entries, by code ascending, each
// complete and neither converted: a NOK project with a EUR receipt has two
// figures, not one.
//
// The five figures that are not buckets — ready, invoiced and unpriced — are
// asserted on both currencies here, and that is the whole point of doing it in
// this test: they live on the currency, not on the project, and a summation
// that folded them onto the project would pass every other test in this file
// while reporting a EUR receipt as a NOK amount "ready to invoice". That
// number is in neither currency, and it would land straight in the portfolio's
// readyAmounts.
func TestProjectExpensesReportsEachCurrencyOnItsOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	// NOK: one approved line nobody bills, and one billable outlay nobody has
	// priced. EUR: one approved, billable, priced line — ready to invoice —
	// and one invoiced one.
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "125.00", vat: "25.00",
		paidBy: "employee", status: "approved"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "80.00",
		paidBy: "employee", billable: true, status: "approved"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, currency: "EUR", gross: "50.00",
		paidBy: "employee", billable: true, billAmount: "60.00", status: "approved"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, currency: "EUR", gross: "20.00",
		paidBy: "employee", billable: true, billAmount: "25.00", status: "approved", invoiced: true})

	totals := projectExpensesOf(t, p, projectKraftVerket)
	if len(totals.Currencies) != 2 {
		t.Fatalf("currencies = %v, want two", totals.Currencies)
	}
	if totals.Currencies[0].Currency != "EUR" || totals.Currencies[1].Currency != "NOK" {
		t.Errorf("currencies = %q and %q, want EUR then NOK (ascending)",
			totals.Currencies[0].Currency, totals.Currencies[1].Currency)
	}
	eur, nok := totals.Currencies[0], totals.Currencies[1]
	wantBucket(t, "EUR approved", eur.Approved, 2, "70.00", "85.00")
	wantBucket(t, "NOK approved", nok.Approved, 2, "180.00", "0.00")

	// Ready is the EUR line that has not been invoiced, and nothing at all in
	// NOK — not "the project's ready amount", rendered into both.
	if eur.ReadyCount != 1 || eur.ReadyAmount != "60.00" {
		t.Errorf("EUR ready = %d worth %q, want 1 worth \"60.00\"", eur.ReadyCount, eur.ReadyAmount)
	}
	if nok.ReadyCount != 0 || nok.ReadyAmount != "0.00" {
		t.Errorf("NOK ready = %d worth %q, want none at all: its only billable line has no price",
			nok.ReadyCount, nok.ReadyAmount)
	}
	if eur.InvoicedCount != 1 || eur.InvoicedAmount != "25.00" {
		t.Errorf("EUR invoiced = %d worth %q, want 1 worth \"25.00\"", eur.InvoicedCount, eur.InvoicedAmount)
	}
	if nok.InvoicedCount != 0 || nok.InvoicedAmount != "0.00" {
		t.Errorf("NOK invoiced = %d worth %q, want none", nok.InvoicedCount, nok.InvoicedAmount)
	}
	if nok.UnpricedCount != 1 {
		t.Errorf("NOK unpricedCount = %d, want 1: the billable line nobody has priced", nok.UnpricedCount)
	}
	if eur.UnpricedCount != 0 {
		t.Errorf("EUR unpricedCount = %d, want 0: both its lines carry a price", eur.UnpricedCount)
	}
}

// Total is the ordinary case too: the three buckets' counts and their money,
// on a project where nothing is on a rounding boundary. A consumer reads it
// rather than adding the buckets up.
func TestProjectExpensesTotalAddsUpTheThreeBuckets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	for _, e := range []recordedExpense{
		{project: projectKraftVerket, gross: "125.00", vat: "25.00", paidBy: "employee", billable: true, billAmount: "110.00", status: "approved"},
		{project: projectKraftVerket, gross: "60.00", vat: "10.00", paidBy: "company", billable: true, billAmount: "55.00", status: "submitted"},
		{project: projectKraftVerket, gross: "40.00", paidBy: "company", status: "draft"},
	} {
		recordExpense(t, h, user, e)
	}

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "approved", nok.Approved, 1, "100.00", "110.00")
	wantBucket(t, "submitted", nok.Submitted, 1, "50.00", "55.00")
	wantBucket(t, "draft", nok.Draft, 1, "40.00", "0.00")
	wantBucket(t, "total", nok.Total, 3, "190.00", "165.00")
}

// The line is attributed to its own project, not to its claim's. They never
// disagree — a claim's line always carries its claim's project, which every
// door that writes one keeps — so this pins the choice rather than a state
// the module can reach: whichever column a later change moves, this test says
// which one the figures are grouped by.
func TestProjectExpensesAttributesALineByItsOwnProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	trip := recordClaim(t, h, user, projectKraftVerket, "approved")
	recordExpense(t, h, user, recordedExpense{project: projectEuro, claim: trip, gross: "100.00",
		paidBy: "employee"})

	got, err := p.ExpensesForProjects(t.Context(), []int32{projectKraftVerket, projectEuro})
	if err != nil {
		t.Fatalf("expenses for projects: %v", err)
	}
	if _, ok := got[projectKraftVerket]; ok {
		t.Errorf("project %d is in the result, want it absent: the line carries project %d",
			projectKraftVerket, projectEuro)
	}
	wantBucket(t, "1004 approved", currencyOf(t, got[projectEuro], "NOK").Approved, 1, "100.00", "0.00")
}

// An expense on no project at all reaches no project's figures.
func TestProjectExpensesIgnoresALineWithoutAProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	recordExpense(t, h, user, recordedExpense{gross: "100.00", paidBy: "employee", status: "approved"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "40.00", paidBy: "company",
		status: "approved"})

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "approved", nok.Approved, 1, "40.00", "0.00")
}

// A project with nothing recorded is absent from the map rather than
// zero-valued: there is no currency to report zeroes in, and the provider
// must not invent one.
func TestProjectExpensesLeavesAProjectWithNothingRecordedAbsent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "100.00", paidBy: "company",
		status: "approved"})

	got, err := p.ExpensesForProjects(t.Context(), []int32{projectKraftVerket, projectInternal})
	if err != nil {
		t.Fatalf("expenses for projects: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("result has %d projects, want 1: %d has nothing recorded", len(got), projectInternal)
	}
	if _, ok := got[projectInternal]; ok {
		t.Errorf("project %d is in the result, want it absent", projectInternal)
	}
}

// LastEntryDate is the latest date anything carries, over every bucket and
// every currency — not the latest approved one, and not the latest in the
// project's own currency.
func TestProjectExpensesLastEntryDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, date: expenseDay, gross: "100.00",
		paidBy: "company", status: "approved"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, date: laterExpenseDay, currency: "EUR",
		gross: "10.00", paidBy: "company", status: "draft"})

	totals := projectExpensesOf(t, p, projectKraftVerket)
	if totals.LastEntryDate == nil {
		t.Fatal("last entry date = nil, want the latest date anything carries")
	}
	if *totals.LastEntryDate != laterExpenseDay {
		t.Errorf("last entry date = %q, want %q", *totals.LastEntryDate, laterExpenseDay)
	}
}

// No project ids is an empty map and no query at all: the provider here is
// built on a nil pool, so any query would fail rather than answer.
func TestProjectExpensesWithoutProjectsQueriesNothing(t *testing.T) {
	t.Parallel()
	p := expenses.Module().Expenses(module.Deps{})

	got, err := p.ExpensesForProjects(t.Context(), nil)
	if err != nil {
		t.Fatalf("expenses for projects: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("result = %v, want an empty map", got)
	}
}

// Beyond the cap the batch is refused rather than turned into a query of
// unbounded size; the caller pages. The cap is the portfolio's own, shared
// with contracts.ProjectActuals so one page serves both reads.
func TestProjectExpensesRefusesTooLargeABatch(t *testing.T) {
	t.Parallel()
	p := expenses.Module().Expenses(module.Deps{})

	ids := make([]int32, contracts.MaxExpensesProjects+1)
	for i := range ids {
		ids[i] = int32(i + 1)
	}
	_, err := p.ExpensesForProjects(t.Context(), ids)
	if err == nil {
		t.Fatal("expenses for projects: want an error beyond the cap")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(contracts.MaxExpensesProjects)) {
		t.Errorf("error %q does not name the cap %d", err, contracts.MaxExpensesProjects)
	}
	if contracts.MaxExpensesProjects != contracts.MaxActualsRequests {
		t.Errorf("MaxExpensesProjects = %d, want MaxActualsRequests (%d): one cap for the portfolio's two batches",
			contracts.MaxExpensesProjects, contracts.MaxActualsRequests)
	}
}

// One project named twice in one batch is refused: it has one answer, and two
// requests for it are the caller's bug however they agree.
func TestProjectExpensesRefusesADuplicateProject(t *testing.T) {
	t.Parallel()
	p := expenses.Module().Expenses(module.Deps{})

	_, err := p.ExpensesForProjects(t.Context(), []int32{projectKraftVerket, projectEuro, projectKraftVerket})
	if err == nil {
		t.Fatal("expenses for projects: want an error when one project is named twice")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(projectKraftVerket)) {
		t.Errorf("error %q does not name the repeated project %d", err, projectKraftVerket)
	}
}

// The cap itself is allowed: a full page of projects answers, and the ones
// with nothing recorded are simply not in it.
func TestProjectExpensesAcceptsTheCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "100.00", paidBy: "company",
		status: "approved"})

	ids := make([]int32, contracts.MaxExpensesProjects)
	for i := range ids {
		ids[i] = int32(i + 1)
	}
	got, err := p.ExpensesForProjects(t.Context(), ids)
	if err != nil {
		t.Fatalf("expenses for projects: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("result has %d projects, want 1: only %d has anything recorded", len(got), projectKraftVerket)
	}
}

// The provider never asks the project directory anything while it serves —
// which is what expensesProvider's forbiddenProjects proves on every test
// here, and what this one says out loud for the batch path.
func TestProjectExpensesAsksTheProjectDirectoryNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, gross: "100.00", paidBy: "company",
		billable: true, billAmount: "110.00", status: "approved"})

	if _, err := p.ExpensesForProjects(t.Context(), []int32{projectKraftVerket, projectEuro, projectInternal}); err != nil {
		t.Fatalf("expenses for projects: %v", err)
	}
}

// TestProjectExpensesFollowsTheModulesOwnWrites is the one test here that
// records nothing in SQL. Every other fixture in this file is a raw INSERT,
// which is the only way to reach the status matrix, but it also means they
// would all still pass if this module stored a bill amount in another column
// or stamped invoicing on another field. This one drives the real doors —
// record, price, submit, approve, invoice — and asserts the provider's figures
// after each, so the summation is tied to what the module actually writes.
func TestProjectExpensesFollowsTheModulesOwnWrites(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve")
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	p := expensesProvider(t, h)

	// A 1250.00 receipt with 250.00 of VAT: the project's cost is the 1000.00
	// net, whatever the customer is charged for it.
	entry := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true, "vatAmount": 250.00,
	}))
	// Priced from the project's side: the net plus a ten per cent markup.
	priced := setBilling(t, manager, entry.Id, map[string]any{
		"revision": entry.Revision, "billable": true, "markupPercent": 10.0,
	})
	if priced.Billing == nil || priced.Billing.BillAmount != 1100 {
		t.Fatalf("the priced line bills %+v, want 1100.00", priced.Billing)
	}

	// A draft is in draft and is ready for nothing: ready is an approval
	// question, and the pricing door does not answer it.
	draft := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "draft", draft.Draft, 1, "1000.00", "1100.00")
	if draft.ReadyCount != 0 || draft.UnpricedCount != 0 {
		t.Errorf("a priced draft reads ready %d / unpriced %d, want 0 / 0",
			draft.ReadyCount, draft.UnpricedCount)
	}

	approvedBy(t, owner, manager, entry.Id)
	approved := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	wantBucket(t, "approved", approved.Approved, 1, "1000.00", "1100.00")
	wantBucket(t, "draft", approved.Draft, 0, "0.00", "0.00")
	if approved.ReadyCount != 1 || approved.ReadyAmount != "1100.00" {
		t.Errorf("after the approval ready = %d worth %q, want 1 worth \"1100.00\"",
			approved.ReadyCount, approved.ReadyAmount)
	}
	if approved.InvoicedCount != 0 || approved.InvoicedAmount != "0.00" {
		t.Errorf("after the approval invoiced = %d worth %q, want none",
			approved.InvoicedCount, approved.InvoicedAmount)
	}

	markInvoiced(t, manager, entry.Id, map[string]any{"revision": getEntry(t, manager, entry.Id).Revision})
	billed := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	// Invoicing is a stamp, not a status: the line stays in approved, and it
	// moves from ready to invoiced.
	wantBucket(t, "approved", billed.Approved, 1, "1000.00", "1100.00")
	if billed.ReadyCount != 0 || billed.ReadyAmount != "0.00" {
		t.Errorf("after the invoicing ready = %d worth %q, want nothing left to invoice",
			billed.ReadyCount, billed.ReadyAmount)
	}
	if billed.InvoicedCount != 1 || billed.InvoicedAmount != "1100.00" {
		t.Errorf("after the invoicing invoiced = %d worth %q, want 1 worth \"1100.00\"",
			billed.InvoicedCount, billed.InvoicedAmount)
	}
}

// A per diem day is never ready to invoice and never unpriced, even when the
// row says it is billable. No door in this module can write that row — a per
// diem day bills nobody anything (design §4), and the entry doors, the pricing
// door and the invoicing door each refuse it — so this fixture is one the
// module cannot reach, seeded on purpose: the two write-side doors guard the
// rule twice, and a figure called "ready to invoice" must never name a line
// POST /entries/{id}/invoiced would refuse.
func TestProjectExpensesNeverCountsAPerDiemDayAsReady(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, kind: "per_diem",
		gross: "400.00", billable: true, billAmount: "500.00", status: "approved"})
	recordExpense(t, h, user, recordedExpense{project: projectKraftVerket, kind: "per_diem",
		gross: "300.00", billable: true, status: "approved"})

	nok := currencyOf(t, projectExpensesOf(t, p, projectKraftVerket), "NOK")
	// The cost is real — the company paid for those days — and so is the bill
	// amount the row carries, which is why only ready and unpriced name the
	// kind: they are about what may be invoiced, and this never may.
	wantBucket(t, "approved", nok.Approved, 2, "700.00", "500.00")
	if nok.ReadyCount != 0 || nok.ReadyAmount != "0.00" {
		t.Errorf("ready = %d worth %q, want none: a per diem day bills nobody anything",
			nok.ReadyCount, nok.ReadyAmount)
	}
	if nok.UnpricedCount != 0 {
		t.Errorf("unpricedCount = %d, want 0: an unpriced per diem day is not work waiting to be done",
			nok.UnpricedCount)
	}
}
