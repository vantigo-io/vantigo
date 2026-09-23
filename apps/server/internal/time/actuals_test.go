package timetracking_test

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	gotime "time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
)

// contracts.ProjectActuals as this module provides it (design §4): what has
// been logged against a project, in the three buckets of E2, folded by the
// currency the caller passes in.
//
// Every test here builds the provider the way Compose does, but with a
// project directory that fails the test the moment anything asks it a
// question: serving actuals must not call back into projects, which would be
// a module cycle at request time. The provider is given nothing but the pool.

// forbiddenProjects is a contracts.ProjectDirectory no one may call. It is
// what the actuals provider is composed with here, so a single directory
// lookup while serving fails the test that made it — the rule the fakes in
// harness_test.go enforce for locked transactions, tightened to "never" for
// this one collaborator.
type forbiddenProjects struct{ t *testing.T }

var _ contracts.ProjectDirectory = (*forbiddenProjects)(nil)

func (f *forbiddenProjects) deny(method string) {
	f.t.Helper()
	f.t.Errorf("the actuals provider called the project directory (%s): it must serve from time's own tables alone", method)
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

func (f *forbiddenProjects) BillingLines(context.Context, int32) ([]contracts.BillingLineEntry, error) {
	f.deny("BillingLines")
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

// forbiddenCatalog is the same for contracts.ProductCatalog: pricing is
// snapshotted on the entry, so serving actuals never asks products anything
// either.
type forbiddenCatalog struct{ t *testing.T }

var _ contracts.ProductCatalog = (*forbiddenCatalog)(nil)

func (f *forbiddenCatalog) deny(method string) {
	f.t.Helper()
	f.t.Errorf("the actuals provider called the product catalog (%s): it must serve from time's own tables alone", method)
}

func (f *forbiddenCatalog) Variant(context.Context, int32) (*contracts.VariantEntry, error) {
	f.deny("Variant")
	return nil, nil
}

func (f *forbiddenCatalog) Variants(context.Context, []int32) ([]contracts.VariantEntry, error) {
	f.deny("Variants")
	return nil, nil
}

func (f *forbiddenCatalog) ListPrice(context.Context, int32, string, gotime.Time) (*contracts.Money, error) {
	f.deny("ListPrice")
	return nil, nil
}

// actualsProvider builds the provider the way module.Compose does — from the
// harness's own Deps, so the pool is the test database's — with every
// directory replaced by one that may not be called.
func actualsProvider(t *testing.T, h *harness) contracts.ProjectActuals {
	t.Helper()
	build := timetracking.Module().Actuals
	if build == nil {
		t.Fatal("the time module declares no Actuals provider")
	}
	deps := h.Deps()
	deps.Projects = &forbiddenProjects{t: t}
	deps.Products = &forbiddenCatalog{t: t}
	return build(deps)
}

// loggedEntry is one row seeded straight into time.entries: the provider
// reads whatever is there, whatever path put it there, and a test about
// currencies and statuses needs combinations no create call can reach
// (invoiced, a cost in another currency than the bill).
type loggedEntry struct {
	project      int32
	line         *int32
	date         string
	hours        string
	billable     bool
	billRate     any
	billCurrency any
	costRate     any
	costCurrency any
	status       string
}

// logEntry seeds one row, so a test reads as the list of what was logged.
func logEntry(t *testing.T, h *harness, userID uuid.UUID, e loggedEntry) {
	t.Helper()
	source := "none"
	if e.billRate != nil {
		source = "project"
	}
	h.Exec(t, `INSERT INTO time.entries
	    (user_id, project_id, billing_line_id, entry_date, hours, billable,
	     bill_rate, bill_currency, cost_rate, cost_currency, rate_source, status, created_at, updated_at)
	    VALUES ($1, $2, $3, $4::date, $5::numeric, $6, $7::numeric, $8, $9::numeric, $10, $11, $12, now(), now())`,
		userID, e.project, e.line, e.date, e.hours, e.billable,
		e.billRate, e.billCurrency, e.costRate, e.costCurrency, source, e.status)
}

// wantBucket fails the test unless b is exactly hours, bill and cost.
func wantBucket(t *testing.T, name string, b contracts.ActualsBucket, hours int64, bill, cost string) {
	t.Helper()
	if b.HoursHundredths != hours {
		t.Errorf("%s hours = %d hundredths, want %d", name, b.HoursHundredths, hours)
	}
	if b.BillAmount != bill {
		t.Errorf("%s bill amount = %q, want %q", name, b.BillAmount, bill)
	}
	if b.CostAmount != cost {
		t.Errorf("%s cost amount = %q, want %q", name, b.CostAmount, cost)
	}
}

// TestTimeModuleProvidesActuals: the module value carries the provider, so
// Compose has one to resolve at all.
func TestTimeModuleProvidesActuals(t *testing.T) {
	t.Parallel()
	if timetracking.Module().Actuals == nil {
		t.Fatal("timetracking.Module().Actuals is nil: nothing would provide contracts.ProjectActuals")
	}
}

// Every status lands in its bucket (E2): approved carries invoiced too, and
// draft carries rejected.
func TestActualsBucketsEveryStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	for _, e := range []loggedEntry{
		{project: projectKraftVerket, date: workDay, hours: "1.00", billable: true, status: "draft"},
		{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true, status: "rejected"},
		{project: projectKraftVerket, date: workDay, hours: "4.00", billable: true, status: "submitted"},
		{project: projectKraftVerket, date: workDay, hours: "8.00", billable: true, status: "approved"},
		{project: projectKraftVerket, date: workDay, hours: "16.00", billable: true, status: "invoiced"},
	} {
		logEntry(t, h, user, e)
	}

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 2400, "0.00", "0.00")
	wantBucket(t, "submitted", got.Totals.Submitted, 400, "0.00", "0.00")
	wantBucket(t, "draft", got.Totals.Draft, 300, "0.00", "0.00")
	// Invoiced is the part of approved already billed: the one invoiced entry,
	// and only it, while approved still carries both.
	wantBucket(t, "invoiced", got.Totals.Invoiced, 1600, "0.00", "0.00")
}

// A project nothing was logged on answers zero totals and no lines, never an
// error: "nothing logged" is an answer, not a failure.
func TestActualsProjectWithoutEntries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := actualsProvider(t, h)

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 0, "0.00", "0.00")
	wantBucket(t, "submitted", got.Totals.Submitted, 0, "0.00", "0.00")
	wantBucket(t, "draft", got.Totals.Draft, 0, "0.00", "0.00")
	if got.Totals.LastEntryDate != nil {
		t.Errorf("last entry date = %q, want none", *got.Totals.LastEntryDate)
	}
	if len(got.Lines) != 0 {
		t.Errorf("lines = %v, want none", got.Lines)
	}
}

// The per-line split: a line each, by id, and what was logged on no line at
// all last. The project's own totals are all of them together.
func TestActualsSplitsPerLineWithTheNoLineOneLast(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	fixed, discount := int32(lineFixed), int32(lineDiscount)
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, line: &discount, date: workDay, hours: "3.00", billable: true, status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, line: &fixed, date: workDay, hours: "2.00", billable: true, status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "1.00", billable: true, status: "approved"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "project approved", got.Totals.Approved, 600, "0.00", "0.00")
	if len(got.Lines) != 3 {
		t.Fatalf("lines = %v, want three: %d, %d and the one without a line", got.Lines, lineFixed, lineDiscount)
	}
	if got.Lines[0].BillingLineID == nil || *got.Lines[0].BillingLineID != lineFixed {
		t.Errorf("first line = %v, want %d (lines come by id)", got.Lines[0].BillingLineID, lineFixed)
	}
	wantBucket(t, "line 3001 approved", got.Lines[0].Totals.Approved, 200, "0.00", "0.00")
	if got.Lines[1].BillingLineID == nil || *got.Lines[1].BillingLineID != lineDiscount {
		t.Errorf("second line = %v, want %d", got.Lines[1].BillingLineID, lineDiscount)
	}
	wantBucket(t, "line 3004 approved", got.Lines[1].Totals.Approved, 300, "0.00", "0.00")
	if got.Lines[2].BillingLineID != nil {
		t.Errorf("last line = %v, want the hours logged on no line", *got.Lines[2].BillingLineID)
	}
	wantBucket(t, "no line approved", got.Lines[2].Totals.Approved, 100, "0.00", "0.00")
}

// The currency rule for the bill amount: an entry's amount counts when its
// own currency is the one asked for, and its hours are unpriced when it is
// not. Hours count either way.
func TestActualsCountsBillAmountsOnlyInTheRequestedCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "3.00", billable: true,
		billRate: "100.00", billCurrency: "SEK", status: "approved"})

	nok, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved in NOK", nok.Totals.Approved, 500, "1800.00", "0.00")
	if nok.Totals.UnpricedHoursHundredths != 300 {
		t.Errorf("unpriced = %d hundredths, want 300: the SEK hours are priced in no currency we asked for", nok.Totals.UnpricedHoursHundredths)
	}

	sek, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("SEK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved in SEK", sek.Totals.Approved, 500, "300.00", "0.00")
	if sek.Totals.UnpricedHoursHundredths != 200 {
		t.Errorf("unpriced = %d hundredths, want 200", sek.Totals.UnpricedHoursHundredths)
	}
}

// With no currency asked for, no amount is summed at all and only the hours
// without a rate are unpriced — a project that carries no amounts still gets
// its hours.
func TestActualsWithoutACurrencySumsNoAmounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", costRate: "400.00", costCurrency: "NOK", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "1.00", billable: true, status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "0.50", billable: false, status: "approved"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 350, "0.00", "0.00")
	if got.Totals.UnpricedHoursHundredths != 100 {
		t.Errorf("unpriced = %d hundredths, want 100: the billable hour without a rate, and no inference of a currency from what happens to be logged",
			got.Totals.UnpricedHoursHundredths)
	}
	if got.Totals.UncostedHoursHundredths != 150 {
		t.Errorf("uncosted = %d hundredths, want 150: the hours without a cost rate, billable or not",
			got.Totals.UncostedHoursHundredths)
	}
}

// Non-billable hours are never unpriced (review I1): they were never meant to
// carry a price, and calling them unpriced sends someone hunting for a rate
// that should not exist. They are reported as non-billable instead.
func TestActualsDoesNotCallNonBillableHoursUnpriced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "10.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "5.00", billable: false, status: "approved"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 1500, "9000.00", "0.00")
	if got.Totals.UnpricedHoursHundredths != 0 {
		t.Errorf("unpriced = %d hundredths, want 0: every billable hour is priced and the rest is not billable",
			got.Totals.UnpricedHoursHundredths)
	}
	if got.Totals.NonBillableHoursHundredths != 500 {
		t.Errorf("non-billable = %d hundredths, want 500", got.Totals.NonBillableHoursHundredths)
	}
}

// A non-billable entry that somehow carries a bill rate is left out of the
// bill amount and out of the priced/unpriced split alike — the same hours
// either way, and the same set the project summary bills on. Nothing writes
// this row today (the rate chain never prices non-billable work), which is
// why it is seeded straight into the table.
func TestActualsIgnoresARateOnNonBillableWork(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	client, user := signInAs(t, h, projectKraftVerket, roleManager)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "10.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "5.00", billable: false,
		billRate: "900.00", billCurrency: "NOK", status: "approved"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 1500, "9000.00", "0.00")
	if got.Totals.UnpricedHoursHundredths != 0 {
		t.Errorf("unpriced = %d hundredths, want 0: the non-billable hours are neither priced nor unpriced",
			got.Totals.UnpricedHoursHundredths)
	}

	summary, _ := readProjectSummary(t, client, projectKraftVerket)
	if summary.Billing == nil {
		t.Fatal("billing absent from the summary, want it for the project's manager")
	}
	if summary.Billing.Amount != 9000 {
		t.Errorf("the project summary bills %v, the actuals say 9000.00: the two surfaces must agree", summary.Billing.Amount)
	}
}

// The figure is the one Time already reports on its own project summary:
// two surfaces saying different numbers of unpriced hours for the same
// project on the same day is a bug report waiting to happen (review I1).
func TestActualsUnpricedHoursAgreeWithTheProjectSummary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	client, user := signInAs(t, h, projectKraftVerket, roleManager)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "10.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "5.00", billable: false, status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "3.00", billable: true, status: "submitted"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true,
		billRate: "100.00", billCurrency: "SEK", status: "approved"})

	summary, _ := readProjectSummary(t, client, projectKraftVerket)
	if summary.Billing == nil {
		t.Fatal("billing absent from the summary, want it for the project's manager")
	}
	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	if got.Totals.UnpricedHoursHundredths != 500 {
		t.Errorf("unpriced = %d hundredths, want 500: three hours without a rate and two priced in SEK",
			got.Totals.UnpricedHoursHundredths)
	}
	summaryHundredths := int64(math.Round(summary.Billing.UnpricedHours * 100))
	if got.Totals.UnpricedHoursHundredths != summaryHundredths {
		t.Errorf("unpriced = %d hundredths, the project summary says %d (%v hours): the two surfaces must agree",
			got.Totals.UnpricedHoursHundredths, summaryHundredths, summary.Billing.UnpricedHours)
	}

	// And where they legitimately part company. The summary covers only work
	// waiting for or past a decision, so a billable draft with no rate is
	// unpriced here and outside the summary entirely. Neither figure is wrong;
	// the contract says which hours each one is about, and this pins the
	// boundary rather than leaving the fixture's silence to imply there is none.
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "4.00", billable: true, status: "draft"})

	summary, _ = readProjectSummary(t, client, projectKraftVerket)
	got, err = p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	if got.Totals.UnpricedHoursHundredths != 900 {
		t.Errorf("unpriced = %d hundredths, want 900: the unpriced draft counts in all three buckets",
			got.Totals.UnpricedHoursHundredths)
	}
	if got := int64(math.Round(summary.Billing.UnpricedHours * 100)); got != summaryHundredths {
		t.Errorf("the project summary's unpriced hours moved to %d, want them unchanged at %d: a draft is outside it",
			got, summaryHundredths)
	}
}

// Work costed in another currency is not in CostAmount, and the hours say so
// (review I2): a person on a EUR rate card working on a NOK project bills in
// NOK and costs in EUR, and a margin taken from the cost alone would make the
// project look better than it is.
func TestActualsUncostedHoursCountWorkCostedInAnotherCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", costRate: "50.00", costCurrency: "EUR", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "1.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", costRate: "400.00", costCurrency: "NOK", status: "approved"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 300, "2700.00", "400.00")
	if got.Totals.UncostedHoursHundredths != 200 {
		t.Errorf("uncosted = %d hundredths, want 200: the hours whose cost is in EUR are not in the cost amount",
			got.Totals.UncostedHoursHundredths)
	}
}

// Hours with no cost rate at all are uncosted too, non-billable ones
// included: work nobody is billed for still costs the company.
func TestActualsUncostedHoursCountWorkWithoutACostRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", costRate: "400.00", costCurrency: "NOK", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "1.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", status: "submitted"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "4.00", billable: false, status: "draft"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	if got.Totals.UncostedHoursHundredths != 500 {
		t.Errorf("uncosted = %d hundredths, want 500: every bucket, billable or not", got.Totals.UncostedHoursHundredths)
	}
	if got.Totals.UnpricedHoursHundredths != 0 {
		t.Errorf("unpriced = %d hundredths, want 0: the only unbilled hours are the non-billable ones",
			got.Totals.UnpricedHoursHundredths)
	}
}

// The cost currency is the entry's own column and folds on its own: an entry
// billed in the currency asked for whose cost is in another contributes its
// bill amount and no cost.
func TestActualsFoldsTheCostCurrencyOnItsOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", costRate: "500.00", costCurrency: "EUR", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "1.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", costRate: "400.00", costCurrency: "NOK", status: "approved"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 300, "2700.00", "400.00")
	if got.Totals.UnpricedHoursHundredths != 0 {
		t.Errorf("unpriced = %d hundredths, want 0: unpriced is about the bill rate, not the cost", got.Totals.UnpricedHoursHundredths)
	}
	if got.Totals.UncostedHoursHundredths != 200 {
		t.Errorf("uncosted = %d hundredths, want 200: the EUR-costed hours are not in the cost amount",
			got.Totals.UncostedHoursHundredths)
	}
}

// The billable split covers all three buckets at once, and the two halves add
// up to everything logged.
func TestActualsSplitsBillableHoursAcrossEveryBucket(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "1.00", billable: true, status: "draft"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "2.00", billable: false, status: "submitted"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "4.00", billable: true, status: "invoiced"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "8.00", billable: false, status: "rejected"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	if got.Totals.BillableHoursHundredths != 500 {
		t.Errorf("billable = %d hundredths, want 500", got.Totals.BillableHoursHundredths)
	}
	if got.Totals.NonBillableHoursHundredths != 1000 {
		t.Errorf("non-billable = %d hundredths, want 1000", got.Totals.NonBillableHoursHundredths)
	}
}

// The last entry date is the latest date in any bucket, as YYYY-MM-DD.
func TestActualsLastEntryDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: "2026-09-14", hours: "1.00", billable: true, status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: "2026-09-16", hours: "1.00", billable: true, status: "draft"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: "2026-09-15", hours: "1.00", billable: true, status: "submitted"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	if got.Totals.LastEntryDate == nil || *got.Totals.LastEntryDate != "2026-09-16" {
		t.Errorf("last entry date = %v, want 2026-09-16", got.Totals.LastEntryDate)
	}
}

// Amounts are exact: the products are summed unrounded and the bucket is
// rounded once, half-up. Three entries of 0.33 h at 333.33 are 329.9967, not
// three times a rounded 110.00.
func TestActualsSumsAmountsExactly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	for range 3 {
		logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "0.33", billable: true,
			billRate: "333.33", billCurrency: "NOK", status: "approved"})
	}

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 99, "330.00", "0.00")
}

// Rounding is the bucket's, not the group's: two lines worth 0.005 each are
// 0.01 together, though each line on its own rounds to 0.01. Rounding every
// group and adding those would say 0.02.
func TestActualsRoundsOncePerBucketNotPerGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	fixed, discount := int32(lineFixed), int32(lineDiscount)
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, line: &fixed, date: workDay, hours: "0.05", billable: true,
		billRate: "0.10", billCurrency: "NOK", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, line: &discount, date: workDay, hours: "0.05", billable: true,
		billRate: "0.10", billCurrency: "NOK", status: "approved"})

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 10, "0.01", "0.00")
	if len(got.Lines) != 2 {
		t.Fatalf("lines = %v, want two", got.Lines)
	}
	wantBucket(t, "line 3001 approved", got.Lines[0].Totals.Approved, 5, "0.01", "0.00")
	wantBucket(t, "line 3004 approved", got.Lines[1].Totals.Approved, 5, "0.01", "0.00")
}

// Total is rounded once from the unrounded whole, which is not the same
// number as adding the three published bucket amounts: each of those was
// rounded on its own first. Three buckets of 0.005 publish "0.01" apiece — a
// consumer adding them reports 0.03 for work worth 0.02 (review M1).
func TestActualsTotalIsRoundedOnceAndNotTheSumOfTheBuckets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	for _, status := range []string{"approved", "submitted", "draft"} {
		logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "0.05", billable: true,
			billRate: "0.10", billCurrency: "NOK", costRate: "0.10", costCurrency: "NOK", status: status})
	}

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	for name, bucket := range map[string]contracts.ActualsBucket{
		"approved": got.Totals.Approved, "submitted": got.Totals.Submitted, "draft": got.Totals.Draft,
	} {
		wantBucket(t, name, bucket, 5, "0.01", "0.01")
	}
	// 3 × 0.005 is 0.015, which rounds half away from zero to 0.02 — while the
	// three published buckets add up to 0.03.
	wantBucket(t, "total", got.Totals.Total, 15, "0.02", "0.02")
}

// Total is the ordinary case too: the three buckets' hours and their money,
// on a project where nothing is on a rounding boundary.
func TestActualsTotalAddsUpTheThreeBuckets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	for _, seed := range []struct{ hours, status string }{
		{"4.00", "approved"}, {"2.50", "submitted"}, {"1.25", "draft"}, {"1.00", "invoiced"},
	} {
		logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: seed.hours, billable: true,
			billRate: "100.00", billCurrency: "NOK", costRate: "40.00", costCurrency: "NOK", status: seed.status})
	}

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 500, "500.00", "200.00")
	wantBucket(t, "total", got.Totals.Total, 875, "875.00", "350.00")
	// A project with nothing logged answers a zero-valued Total rather than an
	// empty one, so a consumer never has to check.
	empty, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectInternal, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "empty total", empty.Totals.Total, 0, "0.00", "0.00")
}

// Invoiced is a part of Approved, not a bucket beside it (customer 360 design
// D1): an invoiced entry lands in Approved exactly as before and in Invoiced
// as well, an approved one only in Approved, and Total counts the invoiced
// work once. The per-line totals and the batch carry the same split, because
// a consumer reads whichever of the three it was built on.
func TestActualsInvoicedIsThePartOfApprovedAlreadyBilled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	for _, seed := range []struct{ hours, status string }{
		{"8.00", "approved"}, {"16.00", "invoiced"}, {"4.00", "submitted"}, {"1.00", "draft"},
	} {
		logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: seed.hours, billable: true,
			billRate: "100.00", billCurrency: "NOK", costRate: "40.00", costCurrency: "NOK", status: seed.status})
	}

	got, err := p.Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "approved", got.Totals.Approved, 2400, "2400.00", "960.00")
	wantBucket(t, "invoiced", got.Totals.Invoiced, 1600, "1600.00", "640.00")
	wantBucket(t, "total", got.Totals.Total, 2900, "2900.00", "1160.00")
	if len(got.Lines) != 1 {
		t.Fatalf("lines = %d, want the one no-line entry", len(got.Lines))
	}
	wantBucket(t, "no-line invoiced", got.Lines[0].Totals.Invoiced, 1600, "1600.00", "640.00")

	batch, err := p.ActualsForProjects(t.Context(), []contracts.ActualsRequest{
		{ProjectID: projectKraftVerket, Currency: ptr("NOK")},
		{ProjectID: projectInternal, Currency: ptr("NOK")},
	})
	if err != nil {
		t.Fatalf("actuals for projects: %v", err)
	}
	wantBucket(t, "batch invoiced", batch[projectKraftVerket].Invoiced, 1600, "1600.00", "640.00")
	wantBucket(t, "batch approved", batch[projectKraftVerket].Approved, 2400, "2400.00", "960.00")
	wantBucket(t, "nothing invoiced", batch[projectInternal].Invoiced, 0, "0.00", "0.00")
}

// The batch: every requested project is in the result, one without entries
// included, and each is folded in the currency it was asked in.
func TestActualsForProjectsAnswersEveryRequestedProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectEuro, date: workDay, hours: "3.00", billable: true,
		billRate: "100.00", billCurrency: "EUR", status: "submitted"})

	got, err := p.ActualsForProjects(t.Context(), []contracts.ActualsRequest{
		{ProjectID: projectKraftVerket, Currency: ptr("NOK")},
		{ProjectID: projectEuro, Currency: ptr("EUR")},
		{ProjectID: projectInternal, Currency: ptr("NOK")},
	})
	if err != nil {
		t.Fatalf("actuals for projects: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("result has %d projects, want 3 — every requested project is in it", len(got))
	}
	wantBucket(t, "1001 approved", got[projectKraftVerket].Approved, 200, "1800.00", "0.00")
	wantBucket(t, "1004 submitted", got[projectEuro].Submitted, 300, "300.00", "0.00")
	wantBucket(t, "1002 approved", got[projectInternal].Approved, 0, "0.00", "0.00")
	if got[projectInternal].LastEntryDate != nil {
		t.Errorf("1002 last entry date = %v, want none", got[projectInternal].LastEntryDate)
	}
}

// One project asked in a currency its entries are not in comes back with
// unpriced hours while its neighbour in the same call is priced: the currency
// is per request, not per call.
func TestActualsForProjectsFoldsEachRequestInItsOwnCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := actualsProvider(t, h)

	logEntry(t, h, user, loggedEntry{project: projectKraftVerket, date: workDay, hours: "2.00", billable: true,
		billRate: "900.00", billCurrency: "NOK", status: "approved"})
	logEntry(t, h, user, loggedEntry{project: projectEuro, date: workDay, hours: "3.00", billable: true,
		billRate: "100.00", billCurrency: "EUR", status: "approved"})

	got, err := p.ActualsForProjects(t.Context(), []contracts.ActualsRequest{
		{ProjectID: projectKraftVerket, Currency: ptr("NOK")},
		{ProjectID: projectEuro, Currency: ptr("NOK")},
	})
	if err != nil {
		t.Fatalf("actuals for projects: %v", err)
	}
	wantBucket(t, "1001 approved", got[projectKraftVerket].Approved, 200, "1800.00", "0.00")
	wantBucket(t, "1004 approved", got[projectEuro].Approved, 300, "0.00", "0.00")
	if got[projectEuro].UnpricedHoursHundredths != 300 {
		t.Errorf("1004 unpriced = %d hundredths, want 300: its entries are in EUR, and NOK was asked for",
			got[projectEuro].UnpricedHoursHundredths)
	}
}

// No requests is an empty map and no query at all: the provider is built on a
// nil pool here, so any query would panic rather than answer.
func TestActualsForProjectsWithoutRequestsQueriesNothing(t *testing.T) {
	t.Parallel()
	p := timetracking.Module().Actuals(module.Deps{})

	got, err := p.ActualsForProjects(t.Context(), nil)
	if err != nil {
		t.Fatalf("actuals for projects: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("result = %v, want an empty map", got)
	}
}

// Beyond the cap the batch is refused rather than turned into a query of
// unbounded size; the caller pages.
func TestActualsForProjectsRefusesTooLargeABatch(t *testing.T) {
	t.Parallel()
	p := timetracking.Module().Actuals(module.Deps{})

	reqs := make([]contracts.ActualsRequest, contracts.MaxActualsRequests+1)
	for i := range reqs {
		reqs[i] = contracts.ActualsRequest{ProjectID: int32(i + 1)}
	}
	_, err := p.ActualsForProjects(t.Context(), reqs)
	if err == nil {
		t.Fatal("actuals for projects: want an error beyond the cap")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(contracts.MaxActualsRequests)) {
		t.Errorf("error %q does not name the cap %d", err, contracts.MaxActualsRequests)
	}
}

// One project named twice in one batch is refused: it has one answer, and
// two requests for it are the caller's bug however they agree (review M2).
func TestActualsForProjectsRefusesADuplicateProject(t *testing.T) {
	t.Parallel()
	p := timetracking.Module().Actuals(module.Deps{})

	_, err := p.ActualsForProjects(t.Context(), []contracts.ActualsRequest{
		{ProjectID: projectKraftVerket, Currency: ptr("NOK")},
		{ProjectID: projectEuro, Currency: ptr("EUR")},
		{ProjectID: projectKraftVerket, Currency: ptr("EUR")},
	})
	if err == nil {
		t.Fatal("actuals for projects: want an error when one project is named twice")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(projectKraftVerket)) {
		t.Errorf("error %q does not name the repeated project %d", err, projectKraftVerket)
	}
}

// The cap itself is allowed: a full page of projects answers.
func TestActualsForProjectsAcceptsTheCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := actualsProvider(t, h)

	reqs := make([]contracts.ActualsRequest, contracts.MaxActualsRequests)
	for i := range reqs {
		reqs[i] = contracts.ActualsRequest{ProjectID: int32(i + 1)}
	}
	got, err := p.ActualsForProjects(t.Context(), reqs)
	if err != nil {
		t.Fatalf("actuals for projects: %v", err)
	}
	if len(got) != contracts.MaxActualsRequests {
		t.Errorf("result has %d projects, want %d", len(got), contracts.MaxActualsRequests)
	}
}
