package projects_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /projects/economy, the expenses half: what each project has ready to
// invoice once its billable expenses are counted beside its milestones. The
// cases here are what the portfolio adds to the per-project read — one batch
// over the same capped set, a row that keeps milestones and expenses apart
// while offering their total, totals per currency, and the sort and the
// filter that now mean "ready" in the wider sense.

// readyExpenses is one project's answer with nothing but ready billable
// lines in it: the shape most cases here need, since the portfolio reports
// only what is ready to invoice.
func readyExpenses(currency string, count int64, amount string) contracts.CurrencyExpenses {
	c := spentInCurrency(currency, spentBucket(count, "0.00", amount),
		spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00"))
	c.ReadyCount, c.ReadyAmount = count, amount
	return c
}

// Without the contract the portfolio is the one it has always been, to the
// byte, plus the one new required boolean.
func TestGetProjectsEconomy_WithoutExpensesTheAnswerIsUnchanged(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "PXGOLD11", map[string]any{"name": "Gyllen"})
	movedMilestone(t, creator, createMilestone(t, creator, project.Id,
		map[string]any{"name": "Oppstart", "amount": 2500}), "ready", nil)
	actuals.set(project.Id, loggedTotals(loggedBucket(4, "800.00", "300.00"),
		loggedBucket(1, "200.00", "75.00"), loggedBucket(0, "0.00", "0.00")))

	r := readPortfolio(t, creator, "sort=code")
	if r.Status != http.StatusOK {
		t.Fatalf("portfolio: status %d body %s, want 200", r.Status, r.Body)
	}
	raw := map[string]any{}
	r.JSON(&raw)
	tracking, present := raw["expenseTracking"]
	if !present || tracking != false {
		t.Fatalf("expenseTracking = %v (present %v), want false", tracking, present)
	}
	delete(raw, "expenseTracking")

	var golden map[string]any
	if err := json.Unmarshal([]byte(goldenPortfolioBody), &golden); err != nil {
		t.Fatalf("the golden body is not JSON: %v", err)
	}
	if !reflect.DeepEqual(raw, golden) {
		got, _ := json.Marshal(raw)
		t.Errorf("the portfolio of an installation without expenses changed.\ngot  %s\nwant %s", got, goldenPortfolioBody)
	}
}

// goldenPortfolioBody is what the fixture above answered before this change,
// captured from the response rather than written by hand, with the one field
// that is allowed to be new removed.
const goldenPortfolioBody = `{
  "data": [
    {
      "project": {"id": 1001, "code": "PXGOLD11", "name": "Gyllen", "status": "active",
                  "customer": {"id": 1001, "name": "Kraft-Verket"}},
      "currency": "NOK",
      "actuals": {
        "approved": {"amount": 800, "hours": 4},
        "submitted": {"amount": 200, "hours": 1},
        "draft": {"amount": 0, "hours": 0},
        "totalAmount": 1000, "totalHours": 5
      },
      "budgetUsed": {"approvedPercent": 80, "basis": "amount", "percent": 100},
      "overBudget": false,
      "pendingHours": 1,
      "nextMilestone": {"id": 1001, "name": "Oppstart", "status": "ready",
                        "effectiveAmount": 2500, "overdue": false},
      "readyAmount": 2500, "readyCount": 1
    }
  ],
  "pagination": {"page": 1, "pageSize": 25, "totalCount": 1, "totalPages": 1,
                 "hasNextPage": false, "hasPreviousPage": false},
  "totals": {"projectCount": 1, "overBudgetCount": 0, "readyCount": 1,
             "readyAmounts": [{"currency": "NOK", "amount": 2500}]},
  "timeTracking": true
}`

// One request, one call into the module that owns the expenses — over the
// same set of project ids the actuals batch covers, each named once, and
// nothing locked while the answer is awaited.
func TestGetProjectsEconomy_AsksTheExpensesProviderOnceForTheWholeSet(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	creator, _ := signIn(t, h, "projects:create")
	first := portfolioProject(t, creator, "PXONCE11", nil)
	second := portfolioProject(t, creator, "PXONCE22", map[string]any{"currency": "EUR"})
	bare := portfolioProject(t, creator, "PXONCE33", map[string]any{
		"currency": nil, "budgetAmount": nil, "customerId": nil, "billingType": "non-billable",
	})
	var lockErr error
	expenses.during(func(ctx context.Context, ids []int32) {
		_, lockErr = h.Pool().Exec(ctx,
			`SELECT id FROM projects.projects WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, ids[0])
	})

	page := getPortfolio(t, creator, "sort=code")
	if !page.ExpenseTracking {
		t.Fatal("expenseTracking = false, want true with the contract composed")
	}
	batches := expenses.batched()
	if len(batches) != 1 {
		t.Fatalf("the provider was handed %d batches, want exactly one per request", len(batches))
	}
	want := []int32{first.Id, second.Id, bare.Id}
	got := append([]int32(nil), batches[0]...)
	if len(got) != len(want) {
		t.Fatalf("the batch named %v, want every row's project once: %v", got, want)
	}
	seen := map[int32]bool{}
	for _, id := range got {
		if seen[id] {
			t.Errorf("project %d is in the batch twice: the contract refuses that", id)
		}
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("project %d is missing from the batch %v", id, got)
		}
	}
	if lockErr != nil {
		t.Errorf("a project row could not be locked while the provider was called: %v — the read must hold no lock", lockErr)
	}
}

// A portfolio with no rows asks nothing: a caller with nothing to see costs
// the module that owns the expenses a request neither of them needs.
func TestGetProjectsEconomy_NoRowsAsksTheExpensesProviderNothing(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	stranger, _ := signIn(t, h)

	page := getPortfolio(t, stranger, "")
	if len(page.Data) != 0 {
		t.Fatalf("rows = %v, want none for a caller with no financial rights anywhere", portfolioCodes(page))
	}
	if batches := expenses.batched(); len(batches) != 0 {
		t.Errorf("the provider was asked %v, want not asked at all", batches)
	}
	if !page.ExpenseTracking {
		t.Error("expenseTracking = false, want true: it is about the installation, not the rows")
	}
	if page.Totals.ReadyExpenseCount == nil || *page.Totals.ReadyExpenseCount != 0 {
		t.Errorf("totals.readyExpenseCount = %v, want 0", page.Totals.ReadyExpenseCount)
	}
}

// A row keeps the two halves of "ready to invoice" apart and offers their
// total beside them: readyAmount and readyCount still mean milestones, the
// two new expense figures mean expenses, and readyTotalAmount is the sum in
// the row's own currency.
func TestGetProjectsEconomy_RowReportsReadyMilestonesAndExpensesApart(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "PXROW111", nil)
	movedMilestone(t, creator, createMilestone(t, creator, project.Id,
		map[string]any{"amount": 2500}), "ready", nil)
	expenses.set(project.Id, recordedExpenses("2026-09-19", readyExpenses("NOK", 3, "1250.50")))

	row := portfolioRow(t, getPortfolio(t, creator, "sort=code"), "PXROW111")
	if row.ReadyCount != 1 || row.ReadyAmount == nil || *row.ReadyAmount != 2500 {
		t.Errorf("row ready = %d/%v, want the milestone figures unchanged", row.ReadyCount, row.ReadyAmount)
	}
	if row.ReadyExpenseCount == nil || *row.ReadyExpenseCount != 3 {
		t.Errorf("row.readyExpenseCount = %v, want 3", row.ReadyExpenseCount)
	}
	if row.ReadyExpenseAmount == nil || *row.ReadyExpenseAmount != 1250.5 {
		t.Errorf("row.readyExpenseAmount = %v, want 1250.50", row.ReadyExpenseAmount)
	}
	if row.ReadyTotalAmount == nil || *row.ReadyTotalAmount != 3750.5 {
		t.Errorf("row.readyTotalAmount = %v, want 3750.50 — 2 500 of milestones and 1 250.50 of expenses", row.ReadyTotalAmount)
	}
}

// A row reports its *own* currency and folds in no other: a receipt in EUR on
// a NOK project is the per-project read's business, not a portfolio column —
// adding it would produce a number in neither currency.
func TestGetProjectsEconomy_RowCountsOnlyItsOwnCurrency(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	creator, _ := signIn(t, h, "projects:create")
	mixed := portfolioProject(t, creator, "PXCUR111", nil)
	bare := portfolioProject(t, creator, "PXCUR222", map[string]any{
		"currency": nil, "budgetAmount": nil, "customerId": nil, "billingType": "non-billable",
	})
	expenses.set(mixed.Id, recordedExpenses("2026-09-19",
		readyExpenses("EUR", 2, "400.00"), readyExpenses("NOK", 1, "900.00")))
	expenses.set(bare.Id, recordedExpenses("2026-09-19", readyExpenses("NOK", 5, "5000.00")))

	page := getPortfolio(t, creator, "sort=code")
	row := portfolioRow(t, page, "PXCUR111")
	if row.ReadyExpenseCount == nil || *row.ReadyExpenseCount != 1 {
		t.Errorf("row.readyExpenseCount = %v, want 1: only the NOK line is in the project's currency", row.ReadyExpenseCount)
	}
	if row.ReadyExpenseAmount == nil || *row.ReadyExpenseAmount != 900 {
		t.Errorf("row.readyExpenseAmount = %v, want 900", row.ReadyExpenseAmount)
	}
	if row.ReadyTotalAmount == nil || *row.ReadyTotalAmount != 900 {
		t.Errorf("row.readyTotalAmount = %v, want 900 with no milestone ready", row.ReadyTotalAmount)
	}

	// A project with no currency has no currency of its own for anything to
	// be in, so it reports no ready expenses at all.
	none := portfolioRow(t, page, "PXCUR222")
	if none.ReadyExpenseCount == nil || *none.ReadyExpenseCount != 0 {
		t.Errorf("a currencyless row's readyExpenseCount = %v, want 0", none.ReadyExpenseCount)
	}
	if none.ReadyExpenseAmount != nil || none.ReadyTotalAmount != nil {
		t.Errorf("a currencyless row's amounts = %v/%v, want both absent", none.ReadyExpenseAmount, none.ReadyTotalAmount)
	}
	if page.Totals.ReadyExpenseCount == nil || *page.Totals.ReadyExpenseCount != 1 {
		t.Errorf("totals.readyExpenseCount = %v, want 1: only what a row could count", page.Totals.ReadyExpenseCount)
	}
}

// The totals stay one entry per currency and gain the same split: what the
// milestones come to, what the expenses come to, and their total. A currency
// nothing but expenses is ready in still gets an entry.
func TestGetProjectsEconomy_TotalsSplitReadyByCurrency(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	creator, _ := signIn(t, h, "projects:create")
	nok := portfolioProject(t, creator, "PXTOT111", nil)
	alsoNok := portfolioProject(t, creator, "PXTOT222", nil)
	eur := portfolioProject(t, creator, "PXTOT333", map[string]any{"currency": "EUR"})
	movedMilestone(t, creator, createMilestone(t, creator, nok.Id, map[string]any{"amount": 1000}), "ready", nil)
	movedMilestone(t, creator, createMilestone(t, creator, alsoNok.Id, map[string]any{"amount": 500}), "ready", nil)
	expenses.set(nok.Id, recordedExpenses("2026-09-19", readyExpenses("NOK", 2, "250.25")))
	expenses.set(eur.Id, recordedExpenses("2026-09-19", readyExpenses("EUR", 1, "80.00")))

	totals := getPortfolio(t, creator, "sort=code").Totals
	if totals.ReadyCount != 2 {
		t.Errorf("totals.readyCount = %d, want 2: it still counts milestones", totals.ReadyCount)
	}
	if totals.ReadyExpenseCount == nil || *totals.ReadyExpenseCount != 3 {
		t.Errorf("totals.readyExpenseCount = %v, want 3", totals.ReadyExpenseCount)
	}
	if len(totals.ReadyAmounts) != 2 {
		t.Fatalf("totals.readyAmounts = %+v, want one entry per currency", totals.ReadyAmounts)
	}
	first := totals.ReadyAmounts[0]
	if first.Currency != "EUR" || first.Amount != 0 {
		t.Errorf("readyAmounts[0] = %+v, want EUR with no milestone amount", first)
	}
	if first.ExpenseAmount == nil || *first.ExpenseAmount != 80 || first.TotalAmount == nil || *first.TotalAmount != 80 {
		t.Errorf("readyAmounts[0] = %+v, want 80 of expenses and 80 in total", first)
	}
	second := totals.ReadyAmounts[1]
	if second.Currency != "NOK" || second.Amount != 1500 {
		t.Errorf("readyAmounts[1] = %+v, want NOK with 1 500 of milestones", second)
	}
	if second.ExpenseAmount == nil || *second.ExpenseAmount != 250.25 {
		t.Errorf("readyAmounts[1].expenseAmount = %v, want 250.25", second.ExpenseAmount)
	}
	if second.TotalAmount == nil || *second.TotalAmount != 1750.25 {
		t.Errorf("readyAmounts[1].totalAmount = %v, want 1750.25", second.TotalAmount)
	}
}

// Each currency's figures are rounded once from the exact sum across its
// rows, never from the rows' own published figures: three rows worth half a
// cent each are 0.01 apiece on screen and 0.02 in the total.
func TestGetProjectsEconomy_TotalsAreRoundedOnceAcrossRows(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	creator, _ := signIn(t, h, "projects:create")
	for _, code := range []string{"PXRND111", "PXRND222", "PXRND333"} {
		project := portfolioProject(t, creator, code, nil)
		expenses.set(project.Id, recordedExpenses("2026-09-19", readyExpenses("NOK", 1, "0.005")))
	}

	page := getPortfolio(t, creator, "sort=code")
	row := portfolioRow(t, page, "PXRND111")
	if row.ReadyExpenseAmount == nil || *row.ReadyExpenseAmount != 0.01 {
		t.Errorf("row.readyExpenseAmount = %v, want 0.01: a row is rounded on its own", row.ReadyExpenseAmount)
	}
	amounts := page.Totals.ReadyAmounts
	if len(amounts) != 1 || amounts[0].ExpenseAmount == nil {
		t.Fatalf("totals.readyAmounts = %+v, want the one NOK entry", amounts)
	}
	if *amounts[0].ExpenseAmount != 0.02 {
		t.Errorf("totals expenseAmount = %v, want 0.02 — the exact sum rounded once, not 0.03 from three rounded rows",
			*amounts[0].ExpenseAmount)
	}
}

// sort=readyAmount orders by what is ready *altogether*: a project whose
// milestones are smaller but whose expenses take it past another comes first.
// Rows with nothing ready at all still come last, and currencies are still
// grouped rather than interleaved.
func TestGetProjectsEconomy_ReadySortOrdersByTheTotal(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	creator, _ := signIn(t, h, "projects:create")
	small := portfolioProject(t, creator, "PXSORT11", nil)
	big := portfolioProject(t, creator, "PXSORT22", nil)
	portfolioProject(t, creator, "PXSORT33", nil)
	movedMilestone(t, creator, createMilestone(t, creator, small.Id, map[string]any{"amount": 1000}), "ready", nil)
	movedMilestone(t, creator, createMilestone(t, creator, big.Id, map[string]any{"amount": 1500}), "ready", nil)
	// 1 000 + 900 beats 1 500 + 0, which milestones alone would order the
	// other way round.
	expenses.set(small.Id, recordedExpenses("2026-09-19", readyExpenses("NOK", 2, "900.00")))

	got := portfolioCodes(getPortfolio(t, creator, "sort=readyAmount"))
	if fmt.Sprint(got) != "[PXSORT11 PXSORT22 PXSORT33]" {
		t.Errorf("sort=readyAmount gave %v, want the largest total first and the empty row last", got)
	}
}

// hasReady=true keeps a project whose only ready thing is an expense: the
// filter asks "is there anything to invoice here", and a billable receipt is
// something to invoice.
func TestGetProjectsEconomy_HasReadyKeepsAProjectWithOnlyReadyExpenses(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	creator, _ := signIn(t, h, "projects:create")
	milestones := portfolioProject(t, creator, "PXFLT111", nil)
	receipts := portfolioProject(t, creator, "PXFLT222", nil)
	portfolioProject(t, creator, "PXFLT333", nil)
	movedMilestone(t, creator, createMilestone(t, creator, milestones.Id, map[string]any{"amount": 1000}), "ready", nil)
	expenses.set(receipts.Id, recordedExpenses("2026-09-19", readyExpenses("NOK", 1, "300.00")))

	got := portfolioCodes(getPortfolio(t, creator, "hasReady=true&sort=code"))
	if fmt.Sprint(got) != "[PXFLT111 PXFLT222]" {
		t.Errorf("hasReady=true gave %v, want the milestone project and the expenses one", got)
	}
}

// A provider that cannot answer fails the portfolio, exactly as the actuals
// provider's failure does: a "ready to invoice" column short by everything
// one module knows is a wrong number, not a missing one.
func TestGetProjectsEconomy_ExpensesProviderFailureIsA500(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	creator, _ := signIn(t, h, "projects:create")
	portfolioProject(t, creator, "PXERR111", nil)
	expenses.fail(errors.New("the expenses module is not answering"))

	r := readPortfolio(t, creator, "",
		modtest.SkipContract("a degraded expenses provider is an infrastructure failure, deliberately off-contract"))
	if r.Status != http.StatusInternalServerError {
		t.Errorf("status %d body %s, want 500", r.Status, r.Body)
	}
}

// The dashboard's budget alerts never ask the expenses provider anything:
// X12 says expenses do not reach a budget, and the cheapest proof that they
// cannot is that the figures are never even fetched.
func TestGetProjectsStatsAttention_NeverAsksTheExpensesProvider(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "PXSTAT11", map[string]any{"name": "Over budsjett"})
	actuals.set(project.Id, loggedTotals(loggedBucket(1, "1500.00", "0.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	expenses.set(project.Id, recordedExpenses("2026-09-19", readyExpenses("NOK", 4, "4000.00")))

	items := readAttention(t, creator)
	if len(attentionOfType(items, "budgetExceeded")) != 1 {
		t.Fatalf("attention = %+v, want the one budgetExceeded item the hours alone raise", items)
	}
	if batches := expenses.batched(); len(batches) != 0 {
		t.Errorf("the dashboard asked the expenses provider %v, want it not asked at all", batches)
	}
}
