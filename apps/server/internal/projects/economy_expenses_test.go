package projects_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /projects/{id}/economy, the expenses half: what a project's expenses
// cost, what they will bill, and what of them is ready to invoice, read
// through contracts.ProjectExpenses. The cases here are the five things that
// half is really about — that an installation without it answers exactly as
// it did before, that expenses reach the Costs section, the margin and
// "ready to invoice" and *nothing else* (X12), that a line in another
// currency is reported rather than converted or dropped, who is shown the
// block, and that this read still asks once and holds nothing while it waits.

// expenseSetUp is economySetUp's fixture with both providers composed: the
// standard project with two billing lines, the hours already logged against
// it, and a handle on each provider.
func expenseSetUp(t *testing.T, code string) (*modtest.Harness, *modtest.Client, *fakeActuals, *fakeExpenses, projectJSON) {
	t.Helper()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, code)
	return h, manager, actuals, expenses, project
}

// spentNOK is one project's expenses in the project's own currency, in the
// shape most cases here want: three buckets and the four invoicing figures.
func spentNOK(c contracts.CurrencyExpenses, readyCount int64, readyAmount string, invoicedCount int64, invoicedAmount string, unpriced int64) contracts.CurrencyExpenses {
	c.ReadyCount, c.ReadyAmount = readyCount, readyAmount
	c.InvoicedCount, c.InvoicedAmount = invoicedCount, invoicedAmount
	c.UnpricedCount = unpriced
	return c
}

// Without the contract the answer is the one it has always been, to the byte,
// plus the one new required boolean. The golden body is the response this
// fixture produced before expenses existed at all: not a paraphrase of it, so
// a field that quietly changed shape — an amount rounded differently, a key
// that appeared, a zero standing in for an absence — fails here rather than
// in somebody's spreadsheet.
func TestGetProjectEconomy_WithoutExpensesTheAnswerIsUnchanged(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create", "projects:view-costs")
	project := goldenEconomyFixture(t, manager, actuals)

	raw := rawEconomy(t, manager, project.Id)
	tracking, present := raw["expenseTracking"]
	if !present || tracking != false {
		t.Fatalf("expenseTracking = %v (present %v), want false: the contract is not composed", tracking, present)
	}
	delete(raw, "expenseTracking")

	var golden map[string]any
	if err := json.Unmarshal([]byte(goldenEconomyBody), &golden); err != nil {
		t.Fatalf("the golden body is not JSON: %v", err)
	}
	if !reflect.DeepEqual(raw, golden) {
		got, _ := json.Marshal(raw)
		t.Errorf("the economy of an installation without expenses changed.\ngot  %s\nwant %s", got, goldenEconomyBody)
	}
}

// goldenEconomyBody is what goldenEconomyFixture's project answered before
// this change, captured from the response itself rather than written by hand,
// with `expenseTracking` — the one field that is allowed to be new — removed.
//
// Every harness here runs against its own database, created from this
// binary's template, so the two billing line ids in it are the same on every
// run and belong in the golden like any other figure.
const goldenEconomyBody = `{
  "actuals": {
    "approved": {"amount": 9450, "hours": 10.5},
    "submitted": {"amount": 3825, "hours": 4.25},
    "draft": {"amount": 1125, "hours": 1.25},
    "totalAmount": 14400, "totalHours": 16,
    "billableHours": 14.75, "nonBillableHours": 1.25, "unpricedHours": 1.25,
    "lastEntryDate": "2026-09-18"
  },
  "budget": {"amount": 200000, "hours": 100, "linesAmount": 90000, "linesHours": 80},
  "budgetUsed": {"approvedPercent": 4.7, "basis": "amount", "percent": 7.2},
  "cost": {"approved": 4200, "submitted": 1700, "draft": 500, "total": 6400, "margin": 8000, "uncostedHours": 2},
  "currency": "NOK",
  "lines": [
    {
      "active": true, "billingLineId": 1001, "code": "DEV",
      "budgetAmount": 90000, "budgetHours": 60,
      "actuals": {
        "approved": {"amount": 7200, "hours": 8},
        "submitted": {"amount": 0, "hours": 0},
        "draft": {"amount": 0, "hours": 0},
        "totalAmount": 7200, "totalHours": 8,
        "billableHours": 8, "nonBillableHours": 0, "unpricedHours": 0
      },
      "overBudget": false, "remainingHours": 52, "usedPercent": 8
    },
    {
      "active": false, "billingLineId": 1002, "code": "PM", "budgetHours": 20,
      "actuals": {
        "approved": {"amount": 0, "hours": 0},
        "submitted": {"amount": 0, "hours": 0},
        "draft": {"amount": 0, "hours": 0},
        "totalAmount": 0, "totalHours": 0,
        "billableHours": 0, "nonBillableHours": 0, "unpricedHours": 0
      },
      "overBudget": false, "remainingHours": 20, "usedPercent": 0
    }
  ],
  "milestones": {"currency": "NOK", "invoiced": 0, "planned": 0, "ready": 40000},
  "overBudget": false,
  "taskEstimateHours": 12,
  "timeTracking": true
}`

// goldenEconomyFixture is the one project the golden body is of: a currency,
// both budgets, two billing lines (one switched off), a task estimate, an
// invoice plan with a ready milestone, and hours in all three buckets with
// costs and a last entry date. It is deliberately the widest fixture here —
// a golden over a thin one would pin nothing.
func goldenEconomyFixture(t *testing.T, manager *modtest.Client, actuals *fakeActuals) projectJSON {
	t.Helper()
	project, dev, _ := economySetUp(t, manager, "ECOGOLD1000")
	createTask(t, manager, project.Id, map[string]any{"title": "Analyse", "estimateHours": 12})
	milestone := createMilestone(t, manager, project.Id, map[string]any{"name": "Oppstart", "amount": 40000})
	movedMilestone(t, manager, milestone, "ready", nil)

	totals := loggedTotals(
		loggedBucket(10.5, "9450.00", "4200.00"),
		loggedBucket(4.25, "3825.00", "1700.00"),
		loggedBucket(1.25, "1125.00", "500.00"))
	totals.UnpricedHoursHundredths = loggedHours(1.25)
	totals.UncostedHoursHundredths = loggedHours(2)
	totals.BillableHoursHundredths = loggedHours(14.75)
	totals.NonBillableHoursHundredths = loggedHours(1.25)
	day := "2026-09-18"
	totals.LastEntryDate = &day
	actuals.set(project.Id, totals, contracts.LineActuals{BillingLineID: &dev, Totals: loggedTotals(
		loggedBucket(8, "7200.00", "3200.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))})
	return project
}

// With the contract composed the block is there, in the project's own
// currency: the three buckets as the provider split them, the totals rounded
// once from the unrounded whole rather than from the three published figures,
// and the four invoicing figures beside them.
func TestGetProjectEconomy_ExpensesInTheProjectsOwnCurrency(t *testing.T) {
	t.Parallel()
	_, manager, _, expenses, project := expenseSetUp(t, "ECOEXP1000")
	expenses.set(project.Id, recordedExpenses("2026-09-19", spentNOK(
		spentInCurrency("NOK",
			spentBucket(3, "1200.00", "1500.00"),
			spentBucket(2, "800.00", "900.00"),
			spentBucket(1, "300.00", "0.00")),
		2, "1100.00", 1, "400.00", 1)))

	economy := getEconomy(t, manager, project.Id)
	if !economy.ExpenseTracking {
		t.Fatal("expenseTracking = false, want true with the contract composed")
	}
	e := economy.Expenses
	if e == nil || e.Approved == nil {
		t.Fatalf("expenses = %+v, want the project's own currency's figures", e)
	}
	if *e.Approved != (expenseBucketJSON{Count: 3, Cost: 1200, Amount: 1500}) {
		t.Errorf("expenses.approved = %+v, want 3 lines costing 1200 and billing 1500", *e.Approved)
	}
	if *e.Submitted != (expenseBucketJSON{Count: 2, Cost: 800, Amount: 900}) {
		t.Errorf("expenses.submitted = %+v, want 2/800/900", *e.Submitted)
	}
	if *e.Draft != (expenseBucketJSON{Count: 1, Cost: 300, Amount: 0}) {
		t.Errorf("expenses.draft = %+v, want 1/300/0", *e.Draft)
	}
	if e.TotalCost == nil || *e.TotalCost != 2300 || e.TotalAmount == nil || *e.TotalAmount != 2400 {
		t.Errorf("expenses totals = %v/%v, want 2300 cost and 2400 billed", e.TotalCost, e.TotalAmount)
	}
	if e.ReadyCount == nil || *e.ReadyCount != 2 || e.ReadyAmount == nil || *e.ReadyAmount != 1100 {
		t.Errorf("expenses ready = %v/%v, want 2 lines worth 1100", e.ReadyCount, e.ReadyAmount)
	}
	if e.InvoicedCount == nil || *e.InvoicedCount != 1 || e.InvoicedAmount == nil || *e.InvoicedAmount != 400 {
		t.Errorf("expenses invoiced = %v/%v, want 1 line worth 400", e.InvoicedCount, e.InvoicedAmount)
	}
	if e.UnpricedCount == nil || *e.UnpricedCount != 1 {
		t.Errorf("expenses.unpricedCount = %v, want 1: a billable line nobody has priced is not a bill of nothing", e.UnpricedCount)
	}
	if e.LastEntryDate == nil || *e.LastEntryDate != "2026-09-19" {
		t.Errorf("expenses.lastEntryDate = %v, want 2026-09-19", e.LastEntryDate)
	}
	if e.OtherCurrencies != nil {
		t.Errorf("expenses.otherCurrencies = %+v, want it absent when everything is in the project's currency", e.OtherCurrencies)
	}
}

// The totals are the provider's own across-bucket figures, rounded once from
// the unrounded whole — never the three published buckets added up, which is
// a different number by a cent whenever anything lands on a boundary.
func TestGetProjectEconomy_ExpenseTotalsAreRoundedOnceAndNotTheBucketsAddedUp(t *testing.T) {
	t.Parallel()
	_, manager, _, expenses, project := expenseSetUp(t, "ECOEXPRND1000")
	nok := spentInCurrency("NOK",
		spentBucket(1, "0.005", "0.005"), spentBucket(1, "0.005", "0.005"), spentBucket(1, "0.005", "0.005"))
	nok.Total = contracts.ExpenseBucket{Count: 3, CostAmount: "0.015", BillAmount: "0.015"}
	expenses.set(project.Id, recordedExpenses("2026-09-19", nok))

	e := getEconomy(t, manager, project.Id).Expenses
	if e == nil || e.TotalCost == nil {
		t.Fatalf("expenses = %+v, want the totals", e)
	}
	if *e.Approved != (expenseBucketJSON{Count: 1, Cost: 0.01, Amount: 0.01}) {
		t.Errorf("expenses.approved = %+v, want each bucket rounded on its own to 0.01", *e.Approved)
	}
	if *e.TotalCost != 0.02 || *e.TotalAmount != 0.02 {
		t.Errorf("expenses totals = %v/%v, want 0.02 — the unrounded whole rounded once, not 0.03 from three rounded buckets",
			*e.TotalCost, *e.TotalAmount)
	}
}

// A project whose expenses are tracked and has none recorded says so with
// zeroes, exactly as actuals does for a project nobody has logged against:
// the provider leaves such a project out of its map entirely, and "no key"
// is the same answer as an empty one — not the absence that says this
// installation cannot tell.
func TestGetProjectEconomy_TrackedWithNothingRecordedIsZeroes(t *testing.T) {
	t.Parallel()
	_, manager, _, _, project := expenseSetUp(t, "ECOEXPNIL1000")

	economy := getEconomy(t, manager, project.Id)
	if !economy.ExpenseTracking {
		t.Fatal("expenseTracking = false, want true")
	}
	e := economy.Expenses
	if e == nil || e.Approved == nil || e.TotalCost == nil {
		t.Fatalf("expenses = %+v, want the block with zeroes for a project with nothing recorded", e)
	}
	if *e.Approved != (expenseBucketJSON{}) || *e.Submitted != (expenseBucketJSON{}) || *e.Draft != (expenseBucketJSON{}) {
		t.Errorf("expenses buckets = %+v/%+v/%+v, want three empty ones", *e.Approved, *e.Submitted, *e.Draft)
	}
	if *e.TotalCost != 0 || *e.TotalAmount != 0 || *e.ReadyCount != 0 || *e.ReadyAmount != 0 ||
		*e.InvoicedCount != 0 || *e.InvoicedAmount != 0 || *e.UnpricedCount != 0 {
		t.Errorf("expenses = %+v, want every figure at zero", e)
	}
	if e.LastEntryDate != nil {
		t.Errorf("expenses.lastEntryDate = %v, want it absent: nothing has been recorded", *e.LastEntryDate)
	}
	if e.OtherCurrencies != nil {
		t.Errorf("expenses.otherCurrencies = %+v, want it absent", e.OtherCurrencies)
	}
}

// The two modules are independent slots, and expenses without time tracking
// is a real installation: the budgets and the invoice plan are there, there
// is nothing logged to compare them with, and the expenses are reported all
// the same. The cost block stays a Time tracking block — it is the labour
// cost, and what the receipts cost is in expenses.totalCost.
func TestGetProjectEconomy_ExpensesWithoutTimeTracking(t *testing.T) {
	t.Parallel()
	expenses := newFakeExpenses()
	h := newHarnessWithExpenses(t, expenses)
	manager, _ := signIn(t, h, "projects:create", "projects:view-costs")
	project, _, _ := economySetUp(t, manager, "ECOEXPNOT1000")
	expenses.set(project.Id, recordedExpenses("2026-09-19", spentNOK(
		spentInCurrency("NOK", spentBucket(2, "1000.00", "1200.00"),
			spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00")), 2, "1200.00", 0, "0.00", 0)))

	economy := getEconomy(t, manager, project.Id)
	if economy.TimeTracking || !economy.ExpenseTracking {
		t.Fatalf("timeTracking/expenseTracking = %v/%v, want false/true", economy.TimeTracking, economy.ExpenseTracking)
	}
	if economy.Expenses == nil || economy.Expenses.TotalCost == nil || *economy.Expenses.TotalCost != 1000 {
		t.Fatalf("expenses = %+v, want what the receipts cost", economy.Expenses)
	}
	if economy.Actuals != nil || economy.BudgetUsed != nil || economy.Cost != nil {
		t.Errorf("actuals/budgetUsed/cost = %v/%v/%v, want all three absent without time tracking",
			economy.Actuals, economy.BudgetUsed, economy.Cost)
	}
	if economy.Budget.Amount == nil || *economy.Budget.Amount != 200000 {
		t.Errorf("budget.amount = %v, want the budget still there", economy.Budget.Amount)
	}
}

// A receipt in another currency is neither converted nor dropped: it is
// reported as what it is, with its own count and amounts, and it stays out of
// every figure the project's own currency carries.
func TestGetProjectEconomy_AnotherCurrencyIsReportedNotConverted(t *testing.T) {
	t.Parallel()
	_, manager, _, expenses, project := expenseSetUp(t, "ECOEXPCUR1000")
	eur := spentInCurrency("EUR", spentBucket(1, "90.00", "100.00"),
		spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00"))
	eur.ReadyCount, eur.ReadyAmount = 1, "100.00"
	expenses.set(project.Id, recordedExpenses("2026-09-19",
		eur,
		spentNOK(spentInCurrency("NOK", spentBucket(2, "1000.00", "1200.00"),
			spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00")), 2, "1200.00", 0, "0.00", 0)))

	e := getEconomy(t, manager, project.Id).Expenses
	if e == nil || e.TotalCost == nil {
		t.Fatalf("expenses = %+v, want the project's own figures", e)
	}
	if *e.TotalCost != 1000 || *e.TotalAmount != 1200 || *e.ReadyAmount != 1200 {
		t.Errorf("expenses = %+v, want only the NOK lines: 1000 cost, 1200 billed, 1200 ready", e)
	}
	if len(e.OtherCurrencies) != 1 {
		t.Fatalf("expenses.otherCurrencies = %+v, want the one EUR entry", e.OtherCurrencies)
	}
	want := expenseCurrencyJSON{Currency: "EUR", Count: 1, Cost: 90, Amount: 100, ReadyAmount: 100}
	if e.OtherCurrencies[0] != want {
		t.Errorf("expenses.otherCurrencies[0] = %+v, want %+v", e.OtherCurrencies[0], want)
	}
}

// A project with no currency of its own has no figures of its own: every
// currency anything was recorded in is "in another currency", and there is
// nothing for a margin to be computed from.
func TestGetProjectEconomy_ProjectWithoutACurrencyReportsEveryCurrencyAsAnother(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	manager, _ := signIn(t, h, "projects:create", "projects:view-costs")
	project := economyProject(t, manager, "ECOEXPNOC1000", map[string]any{"currency": nil, "budgetAmount": nil})
	actuals.set(project.Id, loggedTotals(loggedBucket(4, "0.00", "0.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	expenses.set(project.Id, recordedExpenses("2026-09-19",
		spentInCurrency("EUR", spentBucket(1, "90.00", "100.00"), spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00")),
		spentInCurrency("NOK", spentBucket(2, "1000.00", "1200.00"), spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00"))))

	economy := getEconomy(t, manager, project.Id)
	e := economy.Expenses
	if e == nil {
		t.Fatal("expenses is absent, want the block: the caller has financial rights and the contract is composed")
	}
	if e.Approved != nil || e.TotalCost != nil || e.TotalAmount != nil || e.ReadyCount != nil || e.UnpricedCount != nil {
		t.Errorf("expenses = %+v, want no own-currency figures on a project that carries no currency", e)
	}
	if len(e.OtherCurrencies) != 2 || e.OtherCurrencies[0].Currency != "EUR" || e.OtherCurrencies[1].Currency != "NOK" {
		t.Errorf("expenses.otherCurrencies = %+v, want both currencies, by code", e.OtherCurrencies)
	}
	if economy.Cost != nil {
		t.Errorf("cost = %+v, want it absent on a project with no currency", economy.Cost)
	}
}

// X12, the rule this whole delivery is shaped around: nothing an expense
// costs or bills reaches the budget. The project here would be well past its
// budget if its expenses were counted against it, and it is not — not in
// budgetUsed, not in overBudget, not in a line's usedPercent, and not in the
// logged work the comparison is made from.
func TestGetProjectEconomy_ExpensesNeverReachTheBudget(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	manager, _ := signIn(t, h, "projects:create")
	project := economyProject(t, manager, "ECOEXPX12", map[string]any{"budgetAmount": 10000, "budgetHours": nil})
	line := createLine(t, manager, project.Id, map[string]any{
		"code": "DEV", "variantId": variantDeveloperHour, "budgetAmount": 10000,
	})
	work := loggedTotals(loggedBucket(9, "9000.00", "4000.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))
	actuals.set(project.Id, work, contracts.LineActuals{BillingLineID: &line.Id, Totals: work})
	// 9 000 of a 10 000 budget is 90 %. The expenses would take it to 110 %
	// if anything folded them in — which is the mutation this test exists for.
	expenses.set(project.Id, recordedExpenses("2026-09-19", spentNOK(
		spentInCurrency("NOK", spentBucket(4, "1800.00", "2000.00"),
			spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00")), 4, "2000.00", 0, "0.00", 0)))

	economy := getEconomy(t, manager, project.Id)
	if economy.BudgetUsed == nil || economy.BudgetUsed.Basis != "amount" || economy.BudgetUsed.Percent != 90 {
		t.Fatalf("budgetUsed = %+v, want the amount basis at 90 %%: expenses are not work against a budget", economy.BudgetUsed)
	}
	if economy.OverBudget {
		t.Error("overBudget = true, want false: 9 000 of 10 000 is under budget, expenses or no expenses")
	}
	if economy.Actuals == nil || economy.Actuals.TotalAmount == nil || *economy.Actuals.TotalAmount != 9000 {
		t.Errorf("actuals.totalAmount = %v, want 9000: loggedWork is hours and only hours", economy.Actuals)
	}
	if len(economy.Lines) != 1 {
		t.Fatalf("lines = %+v, want the one billing line and no expense row", economy.Lines)
	}
	row := economy.Lines[0]
	if row.UsedPercent == nil || *row.UsedPercent != 90 || row.OverBudget {
		t.Errorf("line = %+v, want 90 %% and not over budget: the per-line table is work, never expenses", row)
	}
	if row.Actuals == nil || row.Actuals.TotalAmount == nil || *row.Actuals.TotalAmount != 9000 {
		t.Errorf("line actuals = %+v, want 9000", row.Actuals)
	}
	// And the same numbers raise no dashboard alert, because the dashboard is
	// the same comparison.
	items := readAttention(t, manager)
	if len(attentionOfType(items, "budgetWarning"))+len(attentionOfType(items, "budgetExceeded")) != 0 {
		t.Errorf("attention = %+v, want no budget alert for a project at 90 %%", items)
	}
	if asked := expenses.asked(); len(asked) != 1 || asked[0] != project.Id {
		t.Errorf("the expenses provider was asked %v, want only the economy read's own single call — the dashboard never asks it", asked)
	}
}

// The margin spans both halves: what the work bills plus what the expenses
// will bill, less what the work cost plus what the expenses cost. It is
// computed from the exact decimals and rounded *once*, so it is not the two
// published halves subtracted — those were each rounded on their own first.
func TestGetProjectEconomy_MarginSpansLabourAndExpensesAndIsRoundedOnce(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOEXPMAR1000")
	work := loggedTotals(loggedBucket(10, "10000.00", "4000.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))
	work.Total.CostAmount = "4000.005"
	actuals.set(project.Id, work)
	nok := spentInCurrency("NOK", spentBucket(2, "2000.00", "2000.00"),
		spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00"))
	nok.Total = contracts.ExpenseBucket{Count: 2, CostAmount: "2000.005", BillAmount: "2000.00"}
	expenses.set(project.Id, recordedExpenses("2026-09-19", nok))

	costs, _ := signIn(t, h, "projects:view-all", "projects:view-financials", "projects:view-costs")
	cost := getEconomy(t, costs, project.Id).Cost
	if cost == nil || cost.ExpenseCost == nil {
		t.Fatalf("cost = %+v, want the block with expenseCost", cost)
	}
	if cost.Total != 4000.01 {
		t.Errorf("cost.total = %v, want 4000.01: the labour half, rounded on its own", cost.Total)
	}
	if *cost.ExpenseCost != 2000.01 {
		t.Errorf("cost.expenseCost = %v, want 2000.01: the expense half, rounded on its own", *cost.ExpenseCost)
	}
	// (10 000 + 2 000) − (4 000.005 + 2 000.005) = 5 999.99. Subtracting the
	// two published halves instead would answer 5 999.98.
	if cost.Margin != 5999.99 {
		t.Errorf("cost.margin = %v, want 5999.99: one rounding at the end, not two halves added up", cost.Margin)
	}
}

// Without the contract the margin is exactly the one it always was, and the
// cost block carries no expense half at all — an installation with no
// expenses module says nothing about expenses rather than saying zero.
func TestGetProjectEconomy_WithoutExpensesTheMarginIsTodays(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOEXPOFF1000")
	actuals.set(project.Id, loggedTotals(loggedBucket(10, "9000.00", "4000.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))

	costs, _ := signIn(t, h, "projects:view-all", "projects:view-financials", "projects:view-costs")
	economy := getEconomy(t, costs, project.Id)
	if economy.Cost == nil || economy.Cost.Margin != 5000 {
		t.Fatalf("cost = %+v, want the margin 9 000 − 4 000 = 5 000", economy.Cost)
	}
	if economy.Cost.ExpenseCost != nil {
		t.Errorf("cost.expenseCost = %v, want it absent without the contract", *economy.Cost.ExpenseCost)
	}
	raw := rawEconomy(t, costs, project.Id)
	if cost, ok := raw["cost"].(map[string]any); ok {
		if _, present := cost["expenseCost"]; present {
			t.Error("cost.expenseCost is in the body, want the key absent without expense tracking")
		}
	}
	if _, present := raw["expenses"]; present {
		t.Error("expenses is in the body, want the key absent without the contract")
	}
}

// Expense cost is not labour cost: a caller with financial rights on the
// project sees what the expenses cost without projects:view-costs, because
// nothing in it can be divided by somebody's hours. The margin is the other
// way round — it contains the labour cost, so it stays behind the permission.
func TestGetProjectEconomy_ExpenseCostNeedsFinancialRightsAndNotViewCosts(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOEXPGATE1000")
	actuals.set(project.Id, loggedTotals(loggedBucket(10, "9000.00", "4000.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	expenses.set(project.Id, recordedExpenses("2026-09-19", spentNOK(
		spentInCurrency("NOK", spentBucket(2, "1000.00", "1200.00"),
			spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00")), 2, "1200.00", 0, "0.00", 0)))

	financials, _ := signIn(t, h, "projects:view-all", "projects:view-financials")
	economy := getEconomy(t, financials, project.Id)
	if economy.Expenses == nil || economy.Expenses.TotalCost == nil || *economy.Expenses.TotalCost != 1000 {
		t.Fatalf("expenses = %+v, want what the expenses cost without projects:view-costs", economy.Expenses)
	}
	if economy.Cost != nil {
		t.Errorf("cost = %+v, want it absent without projects:view-costs — it carries the margin", economy.Cost)
	}
}

// A project member has no financial rights, so there is no expenses block —
// and the provider is never asked at all, exactly as the invoice plan is not
// read for a caller whose answer cannot carry it.
func TestGetProjectEconomy_MemberSeesNoExpensesAndCostsTheProviderNothing(t *testing.T) {
	t.Parallel()
	actuals, expenses := newFakeActuals(), newFakeExpenses()
	h := newHarnessWithActualsAndExpenses(t, actuals, expenses)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOEXPMEM1000")
	actuals.set(project.Id, loggedTotals(loggedBucket(10, "9000.00", "4000.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	expenses.set(project.Id, recordedExpenses("2026-09-19", spentNOK(
		spentInCurrency("NOK", spentBucket(2, "1000.00", "1200.00"),
			spentBucket(0, "0.00", "0.00"), spentBucket(0, "0.00", "0.00")), 2, "1200.00", 0, "0.00", 0)))

	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")

	raw := rawEconomy(t, member, project.Id)
	if tracking, ok := raw["expenseTracking"].(bool); !ok || !tracking {
		t.Errorf("expenseTracking = %v, want true even for a member: it is about the installation, not the caller", raw["expenseTracking"])
	}
	if _, present := raw["expenses"]; present {
		t.Error("expenses is in a member's body, want it absent: every figure in it is money")
	}
	if asked := expenses.asked(); len(asked) != 0 {
		t.Errorf("the expenses provider was asked %v for a member's read, want it not asked at all", asked)
	}
}

// A provider that cannot answer is a 500, never zeroes: "the expenses could
// not be read" and "this project has no expenses" are different claims, and
// a margin computed from the second when the first is true is a wrong number
// somebody would invoice from.
func TestGetProjectEconomy_ExpensesProviderFailureIsA500(t *testing.T) {
	t.Parallel()
	_, manager, _, expenses, project := expenseSetUp(t, "ECOEXPERR1000")
	expenses.fail(errors.New("the expenses module is not answering"))

	// Off-contract on purpose, exactly as the actuals provider's failure is:
	// a 500 is not a declared response.
	r := readEconomy(t, manager, project.Id,
		modtest.SkipContract("a degraded expenses provider is an infrastructure failure, deliberately off-contract"))
	if r.Status != http.StatusInternalServerError {
		t.Errorf("status %d body %s, want 500", r.Status, r.Body)
	}
}

// One call per request, with the project's own id and nothing else, and made
// while this module holds no lock at all: the hook takes the project's row
// lock on another connection while the provider is answering, which a
// request holding one would be refused.
func TestGetProjectEconomy_AsksTheExpensesProviderOnceAndHoldsNoLock(t *testing.T) {
	t.Parallel()
	h, manager, _, expenses, project := expenseSetUp(t, "ECOEXPLOCK1000")
	var lockErr error
	expenses.during(func(ctx context.Context, _ []int32) {
		_, lockErr = h.Pool().Exec(ctx,
			`SELECT id FROM projects.projects WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, project.Id)
	})

	getEconomy(t, manager, project.Id)
	batches := expenses.batched()
	if len(batches) != 1 || len(batches[0]) != 1 || batches[0][0] != project.Id {
		t.Fatalf("the provider was handed %v, want exactly one batch naming this project once", batches)
	}
	if lockErr != nil {
		t.Errorf("the project row could not be locked while the provider was called: %v — the read must hold no lock", lockErr)
	}
}
