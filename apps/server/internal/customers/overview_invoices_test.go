package customers_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The overview's invoices section (customers receivables design D2). depguard
// forbids this package from importing invoices, even in a test, so the
// receivables contract is a fake built against internal/contracts and handed
// to Deps through modtest.WithReceivables — and leaving it out is the
// installation without invoices, which the endpoint must answer as well.

// fakeReceivables is contracts.CustomerReceivables over totals a test sets
// per customer; a customer with nothing set answers the zero value with
// today in it, as the contract says. It remembers who was asked about.
type fakeReceivables struct {
	mu     sync.Mutex
	totals map[int32]contracts.CustomerReceivableTotals
	asked  []int32
	err    error // when set, what ReceivablesForCustomer fails with
}

var _ contracts.CustomerReceivables = (*fakeReceivables)(nil)

func (f *fakeReceivables) set(customerID int32, totals contracts.CustomerReceivableTotals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.totals == nil {
		f.totals = map[int32]contracts.CustomerReceivableTotals{}
	}
	f.totals[customerID] = totals
}

func (f *fakeReceivables) ReceivablesForCustomer(_ context.Context, customerID int32) (contracts.CustomerReceivableTotals, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, customerID)
	if f.err != nil {
		return contracts.CustomerReceivableTotals{}, f.err
	}
	if totals, ok := f.totals[customerID]; ok {
		return totals, nil
	}
	return contracts.CustomerReceivableTotals{AsOf: "2026-09-12"}, nil
}

// receivablesHarness is newHarness with only the receivables contract
// stubbed: the projects, time and expenses modules are off.
func receivablesHarness(t *testing.T, receivables *fakeReceivables) *modtest.Harness {
	t.Helper()
	if receivables == nil {
		return newHarness(t)
	}
	return newHarness(t, modtest.WithReceivables(receivables))
}

func amounts(pairs ...any) []overviewAmountJSON {
	out := make([]overviewAmountJSON, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, overviewAmountJSON{Currency: pairs[i].(string), Amount: pairs[i+1].(float64)})
	}
	return out
}

// invoices:access is the door: without it nothing about invoices is
// answered — and the contract is not even asked.
func TestOverview_InvoicesNeedInvoicesAccess(t *testing.T) {
	t.Parallel()
	receivables := &fakeReceivables{}
	h := receivablesHarness(t, receivables)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")

	_, keys := getOverview(t, h.SignIn(t, overviewFinancials...), id)
	wantSections(t, keys, "lastActivity")
	if len(receivables.asked) != 0 {
		t.Errorf("asked receivables for %v, want nothing asked", receivables.asked)
	}
}

// Without the invoices module the section is absent for everybody, the key
// notwithstanding: Deps.Receivables nil is "invoicing is off".
func TestOverview_InvoicesAreAbsentWithoutTheModule(t *testing.T) {
	t.Parallel()
	h := receivablesHarness(t, nil)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")

	_, keys := getOverview(t, h.SignIn(t, "customers:view", "invoices:access"), id)
	wantSections(t, keys, "lastActivity")
}

// The contract's figures pass through as they stand: the counts and dates
// the provider's, the money per currency with a zero currency left out and
// nothing added across currencies, this year's and last year's invoiced net
// of the credit notes issued in that year. The section stands on its own
// gate — no projects:access here — and the two dates reach lastActivity.
func TestOverview_InvoicesPassThroughPerCurrencyNetOfCredits(t *testing.T) {
	t.Parallel()
	receivables := &fakeReceivables{}
	h := receivablesHarness(t, receivables)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")
	createManual(t, authenticatedClient(t, h), id, "2026-09-10", "Møte om ny rammeavtale")
	receivables.set(id, contracts.CustomerReceivableTotals{
		Currencies: []contracts.CurrencyReceivables{
			{Currency: "EUR", IssuedThisYear: "300.00", CreditedThisYear: "300.00", IssuedLastYear: "0.00", CreditedLastYear: "0.00", Outstanding: "0.00", Overdue: "0.00"},
			{Currency: "NOK", IssuedThisYear: "1850.00", CreditedThisYear: "200.00", IssuedLastYear: "2000.00", CreditedLastYear: "500.50", Outstanding: "2850.00", Overdue: "2350.00"},
		},
		OutstandingCount: 4, OverdueCount: 3, OnHoldCount: 1, AtCollectionCount: 2,
		OldestOverdueDueOn: ptr("2026-01-14"), LastIssuedOn: ptr("2026-09-05"), LastPaymentOn: ptr("2026-08-20"),
		AsOf: "2026-09-12",
	})

	got, keys := getOverview(t, h.SignIn(t, "customers:view", "invoices:access"), id)
	wantSections(t, keys, "lastActivity", "invoices")
	inv := got.Invoices
	if inv.OutstandingCount != 4 || inv.OverdueCount != 3 || inv.OnHoldCount != 1 || inv.AtCollectionCount != 2 {
		t.Errorf("counts = %d/%d/%d/%d, want 4/3/1/2", inv.OutstandingCount, inv.OverdueCount, inv.OnHoldCount, inv.AtCollectionCount)
	}
	for _, tc := range []struct {
		name string
		got  []overviewAmountJSON
		want []overviewAmountJSON
	}{
		{"outstanding", inv.OutstandingAmounts, amounts("NOK", 2850.0)},
		{"overdue", inv.OverdueAmounts, amounts("NOK", 2350.0)},
		{"invoiced this year", inv.InvoicedThisYearAmounts, amounts("NOK", 1650.0)}, // EUR nets to zero and is left out
		{"invoiced last year", inv.InvoicedLastYearAmounts, amounts("NOK", 1499.5)},
	} {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if inv.OldestOverdueDueOn == nil || *inv.OldestOverdueDueOn != "2026-01-14" || inv.LastIssuedOn == nil || *inv.LastIssuedOn != "2026-09-05" ||
		inv.LastPaymentOn == nil || *inv.LastPaymentOn != "2026-08-20" || inv.AsOf != "2026-09-12" {
		t.Errorf("dates = %v %v %v %s, want 2026-01-14, 2026-09-05, 2026-08-20, 2026-09-12", inv.OldestOverdueDueOn, inv.LastIssuedOn, inv.LastPaymentOn, inv.AsOf)
	}
	if want := map[string]string{"timelineOn": "2026-09-10", "invoicedOn": "2026-09-05", "paymentOn": "2026-08-20"}; !maps.Equal(got.LastActivity, want) {
		t.Errorf("lastActivity = %v, want %v", got.LastActivity, want)
	}
	if !reflect.DeepEqual(receivables.asked, []int32{id}) {
		t.Errorf("asked %v, want exactly this customer once", receivables.asked)
	}
}

// A customer with no issued document answers the section with zero counts
// and empty lists — absent means "not for you or not installed", never
// "nothing owed". An archived customer answers it too: its page still shows
// it. The empty lists are arrays, not null.
func TestOverview_InvoicesWithNothingAnswerZerosNotAbsence(t *testing.T) {
	t.Parallel()
	h := receivablesHarness(t, &fakeReceivables{})
	id := insertCustomer(t, h, "Nedlagt Handel AS", "archived")

	got, keys := getOverview(t, h.SignIn(t, "customers:view", "invoices:access"), id)
	wantSections(t, keys, "lastActivity", "invoices")
	inv := got.Invoices
	if inv.OutstandingCount != 0 || len(inv.OutstandingAmounts) != 0 || len(inv.InvoicedThisYearAmounts) != 0 || inv.LastIssuedOn != nil {
		t.Errorf("invoices = %+v, want zeros and empty lists", inv)
	}
	if bytes.Contains(keys["invoices"], []byte("null")) {
		t.Errorf("invoices = %s, want arrays rather than null", keys["invoices"])
	}
	if len(got.LastActivity) != 0 {
		t.Errorf("lastActivity = %v, want empty", got.LastActivity)
	}
}

// With every module on, the invoices section sits beside the project
// sections unchanged, and lastActivity carries all five dates — the invoices
// block and the project sections share it without one overwriting the other.
// The customer is an anonymised private person: its documents stay for
// bookkeeping, and so does the section.
func TestOverview_InvoicesSitBesideTheProjectSections(t *testing.T) {
	t.Parallel()
	projects, actuals, expenses, receivables := &fakeProjects{}, &fakeActuals{}, &fakeExpenses{}, &fakeReceivables{}
	h := newHarness(t, modtest.WithProjects(projects), modtest.WithActuals(actuals), modtest.WithExpenses(expenses), modtest.WithReceivables(receivables))
	id := insertCustomer(t, h, "Kari Nordmann", "active")
	createManual(t, authenticatedClient(t, h), id, "2026-09-01", "Avtalt oppfølging")
	markAnonymised(t, h, id)
	projects.add(project(1001, id, "KN1000", "active", ptr("NOK")))
	actuals.set(1001, contracts.ActualsTotals{Approved: bucket(1000, "800.00"), LastEntryDate: ptr("2026-09-03")})
	expenses.set(1001, contracts.ProjectExpenseTotals{
		Currencies:    []contracts.CurrencyExpenses{{Currency: "NOK", ReadyCount: 1, ReadyAmount: "120.00"}},
		LastEntryDate: ptr("2026-09-02"),
	})
	receivables.set(id, contracts.CustomerReceivableTotals{
		Currencies:       []contracts.CurrencyReceivables{{Currency: "NOK", IssuedThisYear: "500.00", CreditedThisYear: "0.00", IssuedLastYear: "0.00", CreditedLastYear: "0.00", Outstanding: "500.00", Overdue: "0.00"}},
		OutstandingCount: 1, LastIssuedOn: ptr("2026-09-04"), LastPaymentOn: ptr("2026-09-05"), AsOf: "2026-09-12",
	})

	got, keys := getOverview(t, h.SignIn(t, append(overviewFinancials, "invoices:access")...), id)
	wantSections(t, keys, "lastActivity", "projects", "work", "expenses", "invoices")
	if got.Work.UnbilledHoursHundredths != 1000 || got.Expenses.ReadyCount != 1 || got.Invoices.OutstandingCount != 1 {
		t.Errorf("work %d, expenses %d, invoices %d, want 1000, 1 and 1", got.Work.UnbilledHoursHundredths, got.Expenses.ReadyCount, got.Invoices.OutstandingCount)
	}
	want := map[string]string{"timelineOn": "2026-09-01", "workOn": "2026-09-03", "expenseOn": "2026-09-02", "invoicedOn": "2026-09-04", "paymentOn": "2026-09-05"}
	if !maps.Equal(got.LastActivity, want) {
		t.Errorf("lastActivity = %v, want %v", got.LastActivity, want)
	}
}

// A failing contract fails the request: absent must never also mean "could
// not be read".
func TestOverview_AFailingReceivablesContractFailsTheRequest(t *testing.T) {
	t.Parallel()
	receivables := &fakeReceivables{err: errors.New("boom")}
	h := receivablesHarness(t, receivables)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")

	r := h.SignIn(t, "customers:view", "invoices:access").Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/overview", id), nil,
		modtest.SkipContract("a contract failing is an infrastructure failure, deliberately off-contract"))
	if r.Status != http.StatusInternalServerError || bytes.Contains(r.Body, []byte("lastActivity")) {
		t.Errorf("status %d body %s, want 500 and no part of an overview", r.Status, r.Body)
	}
}
