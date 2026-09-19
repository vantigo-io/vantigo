package projects_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /projects/{id}/economy: a project's budget beside what has actually
// been logged on it (design §5, delivery B). The cases here are the four the
// endpoint is really about — who is shown which half of it, that the three
// buckets and the per-line rows say what the provider said, what happens when
// the module that owns the hours is absent or broken, and that this read
// never holds anything while it waits for another module.

// economyProject is one project with everything the economy has to report on:
// a currency, a budget in both hours and money, two billing lines with
// budgets of their own (one of them switched off), a task estimate and an
// invoice plan. Tests override what they are about.
func economyProject(t *testing.T, c *modtest.Client, code string, overrides map[string]any) projectJSON {
	t.Helper()
	body := map[string]any{
		"code":         code,
		"currency":     "NOK",
		"budgetHours":  100,
		"budgetAmount": 200000,
	}
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
			continue
		}
		body[field] = value
	}
	return createProject(t, c, body)
}

// economySetUp creates the standard project with two lines, one of them
// deactivated, and returns the project and the two line ids in the order the
// billing tab lists them (by code).
func economySetUp(t *testing.T, c *modtest.Client, code string) (projectJSON, int32, int32) {
	t.Helper()
	project := economyProject(t, c, code, nil)
	dev := createLine(t, c, project.Id, map[string]any{
		"code": "DEV", "variantId": variantDeveloperHour, "budgetHours": 60, "budgetAmount": 90000,
	})
	pm := createLine(t, c, project.Id, map[string]any{
		"code": "PM", "variantId": variantProjectManagerHour, "budgetHours": 20,
	})
	changeLine(t, c, project.Id, pm.Id, lineBody(map[string]any{
		"code": "PM", "variantId": variantProjectManagerHour, "budgetHours": 20, "active": false,
	}))
	return project, dev.Id, pm.Id
}

// projectOf reads a project back as its own resource, for the assertions
// whose subject is capabilities.canSeeCosts rather than the economy itself.
func projectOf(t *testing.T, c *modtest.Client, id int32) projectJSON {
	t.Helper()
	r := getProject(t, c, id)
	if r.Status != http.StatusOK {
		t.Fatalf("get project: status %d body %s, want 200", r.Status, r.Body)
	}
	var project projectJSON
	r.JSON(&project)
	return project
}

// A member sees the hours and nothing else: no amounts anywhere, no
// milestone totals, no cost, and no currency — a caller who may not see the
// money has no use for the unit it is in, exactly as on the project itself.
func TestGetProjectEconomy_MemberSeesHoursWithoutAmounts(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, dev, _ := economySetUp(t, manager, "ECOMEM1000")
	actuals.set(project.Id,
		loggedTotals(loggedBucket(10, "9000.00", "4000.00"), loggedBucket(4, "3600.00", "1600.00"), loggedBucket(1, "900.00", "400.00")),
		contracts.LineActuals{BillingLineID: &dev, Totals: loggedTotals(
			loggedBucket(10, "9000.00", "4000.00"), loggedBucket(4, "3600.00", "1600.00"), loggedBucket(1, "900.00", "400.00"))})

	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")

	economy := getEconomy(t, member, project.Id)
	if economy.Actuals == nil || economy.Actuals.TotalHours != 15 {
		t.Fatalf("actuals = %+v, want 15 hours: a member sees what has been logged", economy.Actuals)
	}
	if economy.Actuals.TotalAmount != nil || economy.Actuals.Approved.Amount != nil {
		t.Errorf("actuals = %+v, want no amounts for a member", economy.Actuals)
	}
	if economy.Budget.Hours == nil || *economy.Budget.Hours != 100 {
		t.Errorf("budget.hours = %v, want 100: hours are planning data", economy.Budget.Hours)
	}
	if economy.Budget.Amount != nil || economy.Budget.LinesAmount != nil {
		t.Errorf("budget = %+v, want no amounts for a member", economy.Budget)
	}
	if economy.Currency != nil || economy.Milestones != nil || economy.Cost != nil {
		t.Errorf("currency/milestones/cost = %v/%v/%v, want all three absent for a member",
			economy.Currency, economy.Milestones, economy.Cost)
	}
	// budgetUsed falls through to the hours basis rather than being silently
	// computed off an amount budget the member cannot see.
	if economy.BudgetUsed == nil || economy.BudgetUsed.Basis != "hours" || economy.BudgetUsed.Percent != 15 {
		t.Errorf("budgetUsed = %+v, want the hours basis at 15 %%", economy.BudgetUsed)
	}

	// Absent, not null: the keys are not in the body at all.
	raw := rawEconomy(t, member, project.Id)
	for _, key := range []string{"currency", "milestones", "cost"} {
		if _, present := raw[key]; present {
			t.Errorf("%q is present in a member's body, want it absent", key)
		}
	}
}

// A manager sees the money: the amounts in every bucket, the budget amounts,
// the currency and the invoice plan's totals — but not the cost, which needs
// a permission of its own.
func TestGetProjectEconomy_ManagerSeesAmountsButNotCosts(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, dev, _ := economySetUp(t, manager, "ECOMAN1000")
	actuals.set(project.Id,
		loggedTotals(loggedBucket(10, "9000.00", "4000.00"), loggedBucket(4, "3600.00", "1600.00"), loggedBucket(1, "900.00", "400.00")),
		contracts.LineActuals{BillingLineID: &dev, Totals: loggedTotals(
			loggedBucket(10, "9000.00", "4000.00"), loggedBucket(4, "3600.00", "1600.00"), loggedBucket(1, "900.00", "400.00"))})

	economy := getEconomy(t, manager, project.Id)
	if economy.Currency == nil || *economy.Currency != "NOK" {
		t.Errorf("currency = %v, want NOK", economy.Currency)
	}
	if economy.Actuals.TotalAmount == nil || *economy.Actuals.TotalAmount != 13500 {
		t.Errorf("totalAmount = %v, want 13500", economy.Actuals.TotalAmount)
	}
	if economy.Budget.Amount == nil || *economy.Budget.Amount != 200000 {
		t.Errorf("budget.amount = %v, want 200000", economy.Budget.Amount)
	}
	if economy.Milestones == nil {
		t.Error("milestones is absent, want the plan's totals for a manager")
	}
	if economy.Cost != nil {
		t.Errorf("cost = %+v, want it absent without projects:view-costs", economy.Cost)
	}
	// The amount budget is the first basis, so the percentage is money
	// against money: 13 500 of 200 000.
	if economy.BudgetUsed == nil || economy.BudgetUsed.Basis != "amount" || economy.BudgetUsed.Percent != 6.8 {
		t.Errorf("budgetUsed = %+v, want the amount basis at 6.8 %%", economy.BudgetUsed)
	}
	if economy.BudgetUsed.ApprovedPercent != 4.5 {
		t.Errorf("approvedPercent = %v, want 4.5", economy.BudgetUsed.ApprovedPercent)
	}
}

// projects:view-financials on a project the caller can see is the same
// financial right being the project's manager is.
func TestGetProjectEconomy_ViewFinancialsSeesAmounts(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOFIN1000")
	actuals.set(project.Id, loggedTotals(loggedBucket(10, "9000.00", "4000.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))

	viewer, _ := signIn(t, h, "projects:view-all", "projects:view-financials")
	economy := getEconomy(t, viewer, project.Id)
	if economy.Currency == nil || economy.Actuals.TotalAmount == nil || economy.Milestones == nil {
		t.Errorf("economy = %+v, want the money for a projects:view-financials holder", economy)
	}
	if economy.Cost != nil {
		t.Errorf("cost = %+v, want it absent without projects:view-costs", economy.Cost)
	}
}

// projects:view-costs on top of financial rights is what the cost block
// needs (design §2 E7). The margin is the bill total minus the cost total,
// and uncostedHours says how much of the work the cost leaves out.
func TestGetProjectEconomy_ViewCostsSeesCostAndMargin(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOCOST1000")
	totals := loggedTotals(loggedBucket(10, "9000.00", "4000.00"), loggedBucket(4, "3600.00", "1600.00"), loggedBucket(1, "900.00", "400.00"))
	totals.UncostedHoursHundredths = loggedHours(2)
	actuals.set(project.Id, totals)

	costs, _ := signIn(t, h, "projects:view-all", "projects:view-financials", "projects:view-costs")
	economy := getEconomy(t, costs, project.Id)
	if economy.Cost == nil {
		t.Fatal("cost is absent, want it for a projects:view-costs holder with financial rights")
	}
	if economy.Cost.Approved != 4000 || economy.Cost.Submitted != 1600 || economy.Cost.Draft != 400 {
		t.Errorf("cost buckets = %+v, want 4000/1600/400", economy.Cost)
	}
	if economy.Cost.Total != 6000 {
		t.Errorf("cost.total = %v, want 6000", economy.Cost.Total)
	}
	if economy.Cost.Margin != 7500 {
		t.Errorf("cost.margin = %v, want 7500 (13 500 billed − 6 000 cost)", economy.Cost.Margin)
	}
	if economy.Cost.UncostedHours != 2 {
		t.Errorf("cost.uncostedHours = %v, want 2", economy.Cost.UncostedHours)
	}
	if !projectOf(t, costs, project.Id).Capabilities.CanSeeCosts {
		t.Error("capabilities.canSeeCosts = false, want true")
	}
}

// The permission is not a right of its own: it only ever adds the cost to a
// project whose money the caller can already see. A holder who sees the
// project but not its financials sees no cost — and their project response
// says so through canSeeCosts, so the frontend never offers the view.
func TestGetProjectEconomy_ViewCostsWithoutFinancialRightsSeesNoCost(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECONOFIN1000")
	actuals.set(project.Id, loggedTotals(loggedBucket(10, "9000.00", "4000.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))

	member, memberID := signIn(t, h, "projects:view-costs")
	addRole(t, h, project.Id, memberID, "member")

	raw := rawEconomy(t, member, project.Id)
	if _, present := raw["cost"]; present {
		t.Error("cost is present for a view-costs holder without financial rights, want it absent")
	}
	if projectOf(t, member, project.Id).Capabilities.CanSeeCosts {
		t.Error("capabilities.canSeeCosts = true, want false without financial rights on the project")
	}
}

// An outsider gets the bare 404 an unknown id gets (D7): the economy is not a
// separate secret, the project's existence is.
func TestGetProjectEconomy_OutsiderGets404(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOOUT1000")

	outsider, _ := signIn(t, h, "projects:view-financials", "projects:view-costs")
	if r := readEconomy(t, outsider, project.Id); r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
	if r := readEconomy(t, outsider, 999999); r.Status != http.StatusNotFound {
		t.Errorf("unknown project: status %d body %s, want 404", r.Status, r.Body)
	}
}

// The three buckets are reported as the provider split them, and the totals
// are the provider's own totals — never the lines added up, which is a
// different number because each bucket is rounded once on its own.
func TestGetProjectEconomy_ThreeBucketsAndTheirTotals(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, dev, pm := economySetUp(t, manager, "ECOBUCK1000")
	totals := loggedTotals(loggedBucket(10.5, "9450.00", "0.00"), loggedBucket(4.25, "3825.00", "0.00"), loggedBucket(1.25, "1125.00", "0.00"))
	totals.UnpricedHoursHundredths = loggedHours(1.25)
	totals.BillableHoursHundredths = loggedHours(14.75)
	totals.NonBillableHoursHundredths = loggedHours(1.25)
	day := "2026-09-18"
	totals.LastEntryDate = &day
	// The lines deliberately do not add up to the totals: a test that summed
	// them would get a different answer, which is exactly the mistake the
	// contract warns about.
	actuals.set(project.Id, totals,
		contracts.LineActuals{BillingLineID: &dev, Totals: loggedTotals(loggedBucket(8, "7200.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))},
		contracts.LineActuals{BillingLineID: &pm, Totals: loggedTotals(loggedBucket(2, "1800.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))})

	economy := getEconomy(t, manager, project.Id)
	got := economy.Actuals
	if got.Approved.Hours != 10.5 || got.Submitted.Hours != 4.25 || got.Draft.Hours != 1.25 {
		t.Errorf("bucket hours = %v/%v/%v, want 10.5/4.25/1.25", got.Approved.Hours, got.Submitted.Hours, got.Draft.Hours)
	}
	if *got.Approved.Amount != 9450 || *got.Submitted.Amount != 3825 || *got.Draft.Amount != 1125 {
		t.Errorf("bucket amounts = %+v, want 9450/3825/1125", got)
	}
	if got.TotalHours != 16 || *got.TotalAmount != 14400 {
		t.Errorf("totals = %v h / %v, want 16 h / 14400 — the provider's totals, not the lines added up", got.TotalHours, got.TotalAmount)
	}
	if got.UnpricedHours != 1.25 || got.BillableHours != 14.75 || got.NonBillableHours != 1.25 {
		t.Errorf("spanning figures = %+v, want 1.25 unpriced, 14.75 billable, 1.25 non-billable", got)
	}
	if got.LastEntryDate == nil || *got.LastEntryDate != day {
		t.Errorf("lastEntryDate = %v, want %s", got.LastEntryDate, day)
	}
}

// Every billing line gets a row, deactivated ones included and in the billing
// tab's order, whether or not anything was logged against it; work logged
// against no line gets one row of its own, last.
func TestGetProjectEconomy_LineRowsIncludeInactiveOnesAndTheNoLineRow(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, dev, pm := economySetUp(t, manager, "ECOLINE1000")
	actuals.set(project.Id,
		loggedTotals(loggedBucket(33, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")),
		contracts.LineActuals{BillingLineID: &dev, Totals: loggedTotals(
			loggedBucket(30, "45000.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))},
		contracts.LineActuals{Totals: loggedTotals(
			loggedBucket(3, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))})

	economy := getEconomy(t, manager, project.Id)
	if len(economy.Lines) != 3 {
		t.Fatalf("lines = %+v, want three rows: DEV, the deactivated PM and the no-line row", economy.Lines)
	}
	devRow, pmRow, noLine := economy.Lines[0], economy.Lines[1], economy.Lines[2]
	if devRow.Code == nil || *devRow.Code != "DEV" || devRow.BillingLineId == nil || *devRow.BillingLineId != dev {
		t.Errorf("first row = %+v, want the DEV line", devRow)
	}
	if pmRow.BillingLineId == nil || *pmRow.BillingLineId != pm || pmRow.Active == nil || *pmRow.Active {
		t.Errorf("second row = %+v, want the deactivated PM line", pmRow)
	}
	// A line nothing was logged against still reports zeroes rather than
	// nothing: the provider only knows what was logged, the project knows
	// which lines exist.
	if pmRow.Actuals == nil || pmRow.Actuals.TotalHours != 0 {
		t.Errorf("the deactivated line's actuals = %+v, want a zeroed block", pmRow.Actuals)
	}
	if pmRow.RemainingHours == nil || *pmRow.RemainingHours != 20 {
		t.Errorf("the deactivated line's remainingHours = %v, want its whole 20 h budget", pmRow.RemainingHours)
	}
	if noLine.BillingLineId != nil || noLine.Code != nil || noLine.Active != nil {
		t.Errorf("last row = %+v, want the no-line row", noLine)
	}
	if noLine.Actuals == nil || noLine.Actuals.TotalHours != 3 {
		t.Errorf("no-line row's actuals = %+v, want 3 hours", noLine.Actuals)
	}
	// 30 h of a 60 h budget, but the caller sees amounts, so the line's own
	// budget amount is the basis: 45 000 of 90 000.
	if devRow.UsedPercent == nil || *devRow.UsedPercent != 50 || devRow.OverBudget {
		t.Errorf("DEV row = %+v, want 50 %% used and not over budget", devRow)
	}
	if devRow.RemainingHours == nil || *devRow.RemainingHours != 30 {
		t.Errorf("DEV remainingHours = %v, want 30", devRow.RemainingHours)
	}
}

// Hours the provider attributes to a line this project does not have are
// folded into the no-line row rather than dropped: whatever the reason,
// somebody logged those hours and they have to appear somewhere.
func TestGetProjectEconomy_HoursOnAnUnknownLineJoinTheNoLineRow(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, dev, _ := economySetUp(t, manager, "ECOSTRAY1000")
	stray := dev + 9999
	actuals.set(project.Id,
		loggedTotals(loggedBucket(9, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")),
		contracts.LineActuals{BillingLineID: &stray, Totals: loggedTotals(
			loggedBucket(4, "3600.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))},
		contracts.LineActuals{Totals: loggedTotals(
			loggedBucket(5, "4500.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))})

	economy := getEconomy(t, manager, project.Id)
	last := economy.Lines[len(economy.Lines)-1]
	if last.BillingLineId != nil {
		t.Fatalf("last row = %+v, want the no-line row", last)
	}
	if last.Actuals.TotalHours != 9 {
		t.Errorf("no-line hours = %v, want 9: the stray line's 4 h folded in with the 5 h logged on no line",
			last.Actuals.TotalHours)
	}
	if *last.Actuals.TotalAmount != 8100 {
		t.Errorf("no-line amount = %v, want 8100", *last.Actuals.TotalAmount)
	}
}

// The lines' budgets are reported beside the project's own, so a surface can
// say "the lines add up to 80 h of the project's 100 h" without adding
// anything up itself. Deactivated lines count: an hour logged against one was
// still measured against its budget.
func TestGetProjectEconomy_LineBudgetsAddUpBesideTheProjects(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOSUM1000")

	economy := getEconomy(t, manager, project.Id)
	if economy.Budget.LinesHours == nil || *economy.Budget.LinesHours != 80 {
		t.Errorf("budget.linesHours = %v, want 80 (60 + the deactivated line's 20)", economy.Budget.LinesHours)
	}
	if economy.Budget.LinesAmount == nil || *economy.Budget.LinesAmount != 90000 {
		t.Errorf("budget.linesAmount = %v, want 90000", economy.Budget.LinesAmount)
	}

	// A project whose lines carry no budget at all reports no sum, not zero.
	bare := economyProject(t, manager, "ECOSUM2000", nil)
	createLine(t, manager, bare.Id, map[string]any{"code": "DEV", "variantId": variantDeveloperHour})
	raw := rawEconomy(t, manager, bare.Id)
	budget, _ := raw["budget"].(map[string]any)
	for _, key := range []string{"linesHours", "linesAmount"} {
		if _, present := budget[key]; present {
			t.Errorf("budget.%s is present though no line has one, want it absent", key)
		}
	}
}

// Without a module that reports what has been logged, the budgets and the
// invoice plan are still there and there is nothing to compare them with:
// timeTracking is false, there are no actuals anywhere and no budgetUsed —
// never zeroes, which would read as a project nobody has worked on.
func TestGetProjectEconomy_WithoutTimeTracking(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOOFF1000")
	createTask(t, manager, project.Id, map[string]any{"title": "Analyse", "estimateHours": 12})
	createMilestone(t, manager, project.Id, map[string]any{"name": "Oppstart", "amount": 50000})

	economy := getEconomy(t, manager, project.Id)
	if economy.TimeTracking {
		t.Error("timeTracking = true, want false with no provider composed")
	}
	if economy.Actuals != nil || economy.BudgetUsed != nil || economy.Cost != nil || economy.OverBudget {
		t.Errorf("economy = %+v, want no actuals, no budgetUsed, no cost and not over budget", economy)
	}
	if economy.Budget.Hours == nil || *economy.Budget.Hours != 100 || economy.Budget.Amount == nil {
		t.Errorf("budget = %+v, want the budgets present all the same", economy.Budget)
	}
	if economy.Milestones == nil || economy.Milestones.Planned != 50000 {
		t.Errorf("milestones = %+v, want the plan's totals present all the same", economy.Milestones)
	}
	if economy.TaskEstimateHours == nil || *economy.TaskEstimateHours != 12 {
		t.Errorf("taskEstimateHours = %v, want 12", economy.TaskEstimateHours)
	}
	if len(economy.Lines) != 2 {
		t.Fatalf("lines = %+v, want the two lines with their budgets", economy.Lines)
	}
	for _, line := range economy.Lines {
		if line.Actuals != nil || line.UsedPercent != nil || line.RemainingHours != nil {
			t.Errorf("line %+v carries a comparison, want none without time tracking", line)
		}
	}

	raw := rawEconomy(t, manager, project.Id)
	for _, key := range []string{"actuals", "budgetUsed", "cost"} {
		if _, present := raw[key]; present {
			t.Errorf("%q is present with time tracking off, want it absent", key)
		}
	}
}

// A provider that cannot answer fails the request. "Could not read" is not
// "there is nothing": a budget compared against zeroes is a wrong answer, and
// a wrong number behind a financial decision is worse than an error.
func TestGetProjectEconomy_ProviderFailureIsAnError(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOFAIL1000")
	actuals.fail(errors.New("time: the entries could not be read"))

	// Off-contract on purpose — a 500 is not a declared response — so the
	// exchange opts out of the recorder rather than being validated against a
	// status the contract rightly does not promise.
	r := readEconomy(t, manager, project.Id,
		modtest.SkipContract("a degraded actuals provider is an infrastructure failure, deliberately off-contract"))
	if r.Status != http.StatusInternalServerError {
		t.Errorf("status %d body %s, want 500", r.Status, r.Body)
	}
}

// The provider is asked exactly once, for this project, in the project's own
// currency — and nothing is held while it answers. The hook proves the last
// part from the outside: another connection takes the project's row lock with
// NOWAIT while the call is in flight, which Postgres refuses outright if the
// request is inside a transaction holding it.
func TestGetProjectEconomy_AsksOnceWithTheProjectsCurrencyAndHoldsNoLock(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOONCE1000")

	var lockErr error
	actuals.during(func(ctx context.Context, req contracts.ActualsRequest) {
		_, lockErr = h.Pool().Exec(ctx,
			`SELECT id FROM projects.projects WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, req.ProjectID)
	})

	getEconomy(t, manager, project.Id)
	asked := actuals.asked()
	if len(asked) != 1 {
		t.Fatalf("the provider was asked %d times, want exactly one call per request", len(asked))
	}
	if asked[0].ProjectID != project.Id {
		t.Errorf("asked about project %d, want %d", asked[0].ProjectID, project.Id)
	}
	if asked[0].Currency == nil || *asked[0].Currency != "NOK" {
		t.Errorf("asked in currency %v, want the project's own NOK", asked[0].Currency)
	}
	if lockErr != nil {
		t.Errorf("the project row could not be locked while the provider was called: %v — the read must hold no lock", lockErr)
	}

	// A project with no currency asks for none at all, and then no amounts
	// are summed anywhere.
	internal := createProject(t, manager, map[string]any{
		"code": "ECOONCE2000", "customerId": nil, "billingType": "non-billable",
	})
	getEconomy(t, manager, internal.Id)
	asked = actuals.asked()
	last := asked[len(asked)-1]
	if last.ProjectID != internal.Id || last.Currency != nil {
		t.Errorf("asked %+v, want the currencyless project asked for with no currency", last)
	}
}

// The economy's milestone totals are the invoice plan's, answered from the
// same function: the two endpoints cannot disagree about what a project has
// planned, ready and invoiced.
func TestGetProjectEconomy_MilestoneTotalsMatchThePlan(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project := economyProject(t, manager, "ECOMILE1000", map[string]any{
		"billingType": "fixed-price", "fixedPriceAmount": 500000, "budgetAmount": nil,
	})
	first := createMilestone(t, manager, project.Id, map[string]any{"name": "Oppstart", "amount": nil, "percent": 30})
	createMilestone(t, manager, project.Id, map[string]any{"name": "Levering", "amount": 100000})
	movedMilestone(t, manager, first, "ready", nil)

	plan := getMilestones(t, manager, project.Id)
	economy := getEconomy(t, manager, project.Id)
	if economy.Milestones == nil {
		t.Fatal("milestones is absent, want the plan's totals")
	}
	if !reflect.DeepEqual(*economy.Milestones, plan.Totals) {
		t.Errorf("the economy's milestone totals %+v differ from the plan's %+v", *economy.Milestones, plan.Totals)
	}
	if plan.Totals.Ready != 150000 || plan.Totals.Planned != 100000 {
		t.Errorf("totals = %+v, want 150000 ready and 100000 planned", plan.Totals)
	}
}

// The task estimate is every task's, subtasks and finished ones included, and
// absent when no task carries one — "nobody estimated anything" is not "the
// estimates add up to nothing".
func TestGetProjectEconomy_TaskEstimateSumsEveryTask(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project, _, _ := economySetUp(t, manager, "ECOEST1000")

	raw := rawEconomy(t, manager, project.Id)
	if _, present := raw["taskEstimateHours"]; present {
		t.Error("taskEstimateHours is present with no estimated task, want it absent")
	}

	parent := createTask(t, manager, project.Id, map[string]any{"title": "Analyse", "estimateHours": 12.5})
	createTask(t, manager, project.Id, map[string]any{
		"title": "Delsteg", "parentTaskId": parent.Id, "estimateHours": 3.25,
	})
	createTask(t, manager, project.Id, map[string]any{"title": "Ferdig", "estimateHours": 4.25, "status": "done"})
	createTask(t, manager, project.Id, map[string]any{"title": "Uten estimat"})

	economy := getEconomy(t, manager, project.Id)
	if economy.TaskEstimateHours == nil || *economy.TaskEstimateHours != 20 {
		t.Errorf("taskEstimateHours = %v, want 20 (12.5 + 3.25 + 4.25)", economy.TaskEstimateHours)
	}
}

// A project past its budget says so, and says it on the exact ratio: 100.04 %
// prints 100.0 and is over budget all the same. The line's own flag works the
// same way.
func TestGetProjectEconomy_OverBudgetOnTheExactRatio(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project := economyProject(t, manager, "ECOOVER1000", map[string]any{"budgetAmount": 10000})
	line := createLine(t, manager, project.Id, map[string]any{
		"code": "DEV", "variantId": variantDeveloperHour, "budgetHours": 10,
	})
	actuals.set(project.Id,
		loggedTotals(loggedBucket(12, "10004.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")),
		contracts.LineActuals{BillingLineID: &line.Id, Totals: loggedTotals(
			loggedBucket(12, "10004.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))})

	economy := getEconomy(t, manager, project.Id)
	if economy.BudgetUsed == nil || economy.BudgetUsed.Percent != 100 {
		t.Fatalf("budgetUsed = %+v, want a printed 100.0 %%", economy.BudgetUsed)
	}
	if !economy.OverBudget {
		t.Error("overBudget = false, want true: 10 004 of 10 000 is over it however it prints")
	}
	row := economy.Lines[0]
	if !row.OverBudget || row.UsedPercent == nil || *row.UsedPercent != 120 {
		t.Errorf("line = %+v, want 120 %% and over budget", row)
	}
	if row.RemainingHours == nil || *row.RemainingHours != 0 {
		t.Errorf("line remainingHours = %v, want 0 rather than a negative remainder", row.RemainingHours)
	}
}

// A project with nothing to measure against gets no percentage at all, which
// is a different answer from 0 %.
func TestGetProjectEconomy_WithoutABudgetThereIsNoPercentage(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "ECONOBUD1000"})
	actuals.set(project.Id, loggedTotals(loggedBucket(40, "0.00", "0.00"),
		loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))

	raw := rawEconomy(t, manager, project.Id)
	if _, present := raw["budgetUsed"]; present {
		t.Error("budgetUsed is present on a project with no budget, want it absent")
	}
	economy := getEconomy(t, manager, project.Id)
	if economy.OverBudget {
		t.Error("overBudget = true, want false: there is nothing to be over")
	}
	if economy.Actuals == nil || economy.Actuals.TotalHours != 40 {
		t.Errorf("actuals = %+v, want the 40 hours reported all the same", economy.Actuals)
	}
}
