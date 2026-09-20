package projects_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /projects/economy: the economy portfolio (design §5, delivery B). The
// cases here are the four things it is really about — who gets a row at all,
// what each filter and each sort does to the set, that the totals are over
// the whole filtered set rather than over the page, and that a page of
// hundreds of projects still costs one call into the module that owns the
// hours.
//
// Every row is a project the caller has financial rights on, so nothing here
// is shaped per caller the way the per-project economy is: a caller who may
// not see a project's money does not get its row.

// portfolioProject creates one project and makes it active — the portfolio's
// default filter is 'active', because a portfolio is about the work being
// done now — and returns it. The creator holds the manager role on it (D6),
// which is what gives them financial rights on it.
func portfolioProject(t *testing.T, c *modtest.Client, code string, overrides map[string]any) projectJSON {
	t.Helper()
	body := map[string]any{"code": code, "currency": "NOK", "budgetAmount": 1000}
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
			continue
		}
		body[field] = value
	}
	status := "active"
	if s, ok := overrides["status"]; ok {
		status, _ = s.(string)
		delete(body, "status")
	}
	project := createProject(t, c, body)
	if status == "" {
		return project
	}
	return setStatus(t, c, project.Id, status)
}

// portfolioPath is the portfolio with a query string.
func portfolioPath(query string) string {
	if query == "" {
		return "/api/v1/projects/economy"
	}
	return "/api/v1/projects/economy?" + query
}

// readPortfolio asks for the portfolio and returns whatever came back: some
// of the cases here are refusals.
func readPortfolio(t *testing.T, c *modtest.Client, query string, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, portfolioPath(query), nil, opts...)
}

// getPortfolio is readPortfolio for a test that expects a page.
func getPortfolio(t *testing.T, c *modtest.Client, query string) portfolioJSON {
	t.Helper()
	r := readPortfolio(t, c, query)
	if r.Status != http.StatusOK {
		t.Fatalf("portfolio %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var page portfolioJSON
	r.JSON(&page)
	return page
}

// portfolioCodes is the page's project codes in the order they came, which is
// what every ordering and filtering assertion here compares.
func portfolioCodes(page portfolioJSON) []string {
	out := make([]string, 0, len(page.Data))
	for _, row := range page.Data {
		out = append(out, row.Project.Code)
	}
	return out
}

// portfolioRow is one row of a page by project code, for an assertion about
// one project among several.
func portfolioRow(t *testing.T, page portfolioJSON, code string) portfolioRowJSON {
	t.Helper()
	for _, row := range page.Data {
		if row.Project.Code == code {
			return row
		}
	}
	t.Fatalf("no row for %q in %v", code, portfolioCodes(page))
	return portfolioRowJSON{}
}

// portfolioJSON decodes ProjectEconomyListResponse.
type portfolioJSON struct {
	Data            []portfolioRowJSON  `json:"data"`
	Pagination      paginationJSON      `json:"pagination"`
	Totals          portfolioTotalsJSON `json:"totals"`
	TimeTracking    bool                `json:"timeTracking"`
	ExpenseTracking bool                `json:"expenseTracking"`
}

// portfolioRowJSON decodes ProjectEconomyRow. Every optional field is a
// pointer: absent is what a row says instead of zero.
type portfolioRowJSON struct {
	Project       portfolioProjectJSON    `json:"project"`
	Currency      *string                 `json:"currency"`
	BudgetUsed    *budgetUsedJSON         `json:"budgetUsed"`
	OverBudget    bool                    `json:"overBudget"`
	Actuals       *portfolioActualsJSON   `json:"actuals"`
	PendingHours  *float64                `json:"pendingHours"`
	NextMilestone *portfolioMilestoneJSON `json:"nextMilestone"`
	ReadyCount    int32                   `json:"readyCount"`
	ReadyAmount   *float64                `json:"readyAmount"`
	// The three expense figures are pointers because all three are absent
	// exactly when expenseTracking is false — "this installation cannot say",
	// which is a different answer from a project with nothing ready.
	ReadyExpenseCount  *int32   `json:"readyExpenseCount"`
	ReadyExpenseAmount *float64 `json:"readyExpenseAmount"`
	ReadyTotalAmount   *float64 `json:"readyTotalAmount"`
}

type portfolioProjectJSON struct {
	Id       int32                  `json:"id"`
	Code     string                 `json:"code"`
	Name     string                 `json:"name"`
	Status   string                 `json:"status"`
	Customer *portfolioCustomerJSON `json:"customer"`
}

type portfolioCustomerJSON struct {
	Id   int32   `json:"id"`
	Name *string `json:"name"`
}

type portfolioActualsJSON struct {
	Approved    economyBucketJSON `json:"approved"`
	Submitted   economyBucketJSON `json:"submitted"`
	Draft       economyBucketJSON `json:"draft"`
	TotalHours  float64           `json:"totalHours"`
	TotalAmount *float64          `json:"totalAmount"`
}

type portfolioMilestoneJSON struct {
	Id              int32    `json:"id"`
	Name            string   `json:"name"`
	PlannedDate     *string  `json:"plannedDate"`
	Status          string   `json:"status"`
	EffectiveAmount *float64 `json:"effectiveAmount"`
	Overdue         bool     `json:"overdue"`
}

type portfolioTotalsJSON struct {
	ProjectCount      int32             `json:"projectCount"`
	OverBudgetCount   int32             `json:"overBudgetCount"`
	ReadyCount        int32             `json:"readyCount"`
	ReadyExpenseCount *int32            `json:"readyExpenseCount"`
	ReadyAmounts      []readyAmountJSON `json:"readyAmounts"`
}

type readyAmountJSON struct {
	Currency      string   `json:"currency"`
	Amount        float64  `json:"amount"`
	ExpenseAmount *float64 `json:"expenseAmount"`
	TotalAmount   *float64 `json:"totalAmount"`
}

// Who gets a row. The portfolio lists projects the caller has *financial
// rights* on — the project's manager, projects:manage-all, or
// projects:view-financials on a project they can see — and nothing else. A
// member or a viewer of a project sees the project everywhere else in this
// module and does not see it here, because every column of this table is
// money.
func TestGetProjectsEconomy_ListsOnlyProjectsTheCallerMaySeeTheMoneyOf(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	alpha := portfolioProject(t, creator, "PALPHA10", nil)
	beta := portfolioProject(t, creator, "PBETA100", nil)
	portfolioProject(t, creator, "PGAMMA10", nil)

	// Its manager, by having created all three.
	if got := portfolioCodes(getPortfolio(t, creator, "")); fmt.Sprint(got) != "[PALPHA10 PBETA100 PGAMMA10]" {
		t.Errorf("the manager's rows = %v, want all three", got)
	}

	// A member and a viewer see the project and never its money.
	member, memberID := signIn(t, h)
	addRole(t, h, alpha.Id, memberID, "member")
	viewer, viewerID := signIn(t, h)
	addRole(t, h, beta.Id, viewerID, "viewer")
	for name, c := range map[string]*modtest.Client{"member": member, "viewer": viewer} {
		page := getPortfolio(t, c, "")
		if len(page.Data) != 0 || page.Totals.ProjectCount != 0 {
			t.Errorf("the %s's rows = %v (count %d), want none: a role that grants no financials grants no row",
				name, portfolioCodes(page), page.Totals.ProjectCount)
		}
	}

	// view-financials applies to the projects the caller can see, so a role
	// that only makes a project visible is enough beside it.
	financials, financialsID := signIn(t, h, "projects:view-financials")
	addRole(t, h, alpha.Id, financialsID, "viewer")
	page := getPortfolio(t, financials, "")
	if got := portfolioCodes(page); fmt.Sprint(got) != "[PALPHA10]" {
		t.Errorf("view-financials' rows = %v, want only the project they can see", got)
	}
	if page.Totals.ProjectCount != 1 {
		t.Errorf("totals.projectCount = %d, want 1: the totals cover the rows the caller may see", page.Totals.ProjectCount)
	}

	// The two global permissions that reach every project.
	all, _ := signIn(t, h, "projects:manage-all")
	if got := portfolioCodes(getPortfolio(t, all, "")); fmt.Sprint(got) != "[PALPHA10 PBETA100 PGAMMA10]" {
		t.Errorf("manage-all's rows = %v, want all three", got)
	}
	seeAll, _ := signIn(t, h, "projects:view-all", "projects:view-financials")
	if got := portfolioCodes(getPortfolio(t, seeAll, "")); fmt.Sprint(got) != "[PALPHA10 PBETA100 PGAMMA10]" {
		t.Errorf("view-all + view-financials' rows = %v, want all three", got)
	}

	// Somebody with no role and no permission has no portfolio at all.
	stranger, _ := signIn(t, h)
	if page := getPortfolio(t, stranger, ""); len(page.Data) != 0 {
		t.Errorf("the stranger's rows = %v, want none", portfolioCodes(page))
	}
}

// The three filters that are decided in SQL: the status (defaulting to
// 'active', with 'all' for every status), the customer, and the search over
// code and name whose wildcards are the caller's characters rather than
// patterns.
func TestGetProjectsEconomy_FiltersOnStatusCustomerAndSearch(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	portfolioProject(t, creator, "PFACTIVE", map[string]any{"name": "Aktivt arbeid"})
	portfolioProject(t, creator, "PFHOLD11", map[string]any{"status": "on-hold"})
	portfolioProject(t, creator, "PFACME11", map[string]any{"customerId": customerAcme})
	portfolioProject(t, creator, "PFPCT100", map[string]any{"name": "100% ferdig"})
	portfolioProject(t, creator, "PFPCTXYZ", map[string]any{"name": "100 og noe ferdig"})

	for _, tc := range []struct {
		query string
		want  string
	}{
		{"", "[PFACME11 PFACTIVE PFPCT100 PFPCTXYZ]"},
		{"status=all", "[PFACME11 PFACTIVE PFHOLD11 PFPCT100 PFPCTXYZ]"},
		{"status=on-hold", "[PFHOLD11]"},
		{"status=completed", "[]"},
		{"customerId=" + fmt.Sprint(customerAcme), "[PFACME11]"},
		{"search=acme", "[PFACME11]"},
		{"search=aktivt", "[PFACTIVE]"},
		{url.Values{"search": {"100%"}}.Encode(), "[PFPCT100]"},
	} {
		if got := portfolioCodes(getPortfolio(t, creator, tc.query)); fmt.Sprint(got) != tc.want {
			t.Errorf("%q gave %v, want %s", tc.query, got, tc.want)
		}
	}
}

// The two filters that are decided in Go, from figures no SQL of this module
// may read: over budget, and having something ready to invoice. The totals
// follow the filter — a portfolio filtered to what is over budget says how
// many that is, not how many projects exist.
func TestGetProjectsEconomy_FiltersOnOverBudgetAndReady(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	over := portfolioProject(t, creator, "PXOVER11", nil)
	under := portfolioProject(t, creator, "PXUNDER1", nil)
	ready := portfolioProject(t, creator, "PXREADY1", nil)
	actuals.set(over.Id, loggedTotals(loggedBucket(10, "1500.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	actuals.set(under.Id, loggedTotals(loggedBucket(10, "500.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	movedMilestone(t, creator, createMilestone(t, creator, ready.Id, map[string]any{"amount": 2500}), "ready", nil)

	page := getPortfolio(t, creator, "overBudget=true")
	if got := portfolioCodes(page); fmt.Sprint(got) != "[PXOVER11]" {
		t.Errorf("overBudget=true gave %v, want only the project past its budget", got)
	}
	if page.Totals.ProjectCount != 1 || page.Totals.OverBudgetCount != 1 {
		t.Errorf("totals = %+v, want a count of 1 over the filtered set", page.Totals)
	}

	page = getPortfolio(t, creator, "hasReady=true")
	if got := portfolioCodes(page); fmt.Sprint(got) != "[PXREADY1]" {
		t.Errorf("hasReady=true gave %v, want only the project with something ready", got)
	}
	if page.Totals.ProjectCount != 1 || page.Totals.ReadyCount != 1 {
		t.Errorf("totals = %+v, want one project with one ready milestone", page.Totals)
	}

	// Unfiltered, the same three rows say the same things.
	page = getPortfolio(t, creator, "")
	if page.Totals.ProjectCount != 3 || page.Totals.OverBudgetCount != 1 || page.Totals.ReadyCount != 1 {
		t.Errorf("unfiltered totals = %+v, want 3 projects, 1 over budget, 1 ready", page.Totals)
	}
}

// The default order: most of the budget used first, decided on the exact
// ratio rather than on the rounded percent, with the projects that have no
// basis to measure against last and ties broken by code.
func TestGetProjectsEconomy_SortsByBudgetUsedWithNoBasisLast(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	half := portfolioProject(t, creator, "PSHALF11", nil)
	most := portfolioProject(t, creator, "PSMOST11", nil)
	tieA := portfolioProject(t, creator, "PSTIEA11", nil)
	tieB := portfolioProject(t, creator, "PSTIEB11", nil)
	// No budget of any kind, so nothing to be a share of.
	none := portfolioProject(t, creator, "PSNONE11", map[string]any{"budgetAmount": nil})
	zero := func(h float64, bill string) contracts.ActualsTotals {
		return loggedTotals(loggedBucket(h, bill, "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))
	}
	actuals.set(half.Id, zero(5, "500.00"))
	actuals.set(most.Id, zero(9, "900.00"))
	actuals.set(tieA.Id, zero(7, "700.00"))
	actuals.set(tieB.Id, zero(7, "700.00"))
	actuals.set(none.Id, zero(1, "100.00"))

	page := getPortfolio(t, creator, "")
	if got := portfolioCodes(page); fmt.Sprint(got) != "[PSMOST11 PSTIEA11 PSTIEB11 PSHALF11 PSNONE11]" {
		t.Errorf("default order = %v, want most-used first, ties by code, no basis last", got)
	}
	// Every sort falls through to the code as its tie-break, so the order above
	// is only a claim about budgetUsed if it differs from the alphabetical one.
	if got := portfolioCodes(getPortfolio(t, creator, "sort=code")); fmt.Sprint(got) != "[PSHALF11 PSMOST11 PSNONE11 PSTIEA11 PSTIEB11]" {
		t.Errorf("sort=code gave %v, want the alphabetical order, which differs from the budgetUsed one", got)
	}
	if row := portfolioRow(t, page, "PSNONE11"); row.BudgetUsed != nil {
		t.Errorf("PSNONE11 budgetUsed = %+v, want absent: it has no basis", row.BudgetUsed)
	}
	if row := portfolioRow(t, page, "PSMOST11"); row.BudgetUsed == nil || row.BudgetUsed.Basis != "amount" || row.BudgetUsed.Percent != 90 {
		t.Errorf("PSMOST11 budgetUsed = %+v, want 90 %% of the amount basis", row.BudgetUsed)
	}
	// The default is the same order the sort names explicitly.
	if got := portfolioCodes(getPortfolio(t, creator, "sort=budgetUsed")); fmt.Sprint(got) != "[PSMOST11 PSTIEA11 PSTIEB11 PSHALF11 PSNONE11]" {
		t.Errorf("sort=budgetUsed gave %v, want the default order", got)
	}
}

// readyAmount desc, by currency first: two amounts in different currencies
// cannot be ranked against each other, so the order is the currency code and
// then the largest amount in it. Projects with nothing ready come last.
func TestGetProjectsEconomy_SortsByReadyAmountWithinEachCurrency(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	// The codes are chosen so that the expected order disagrees with the
	// alphabetical one at every position: every sort falls through to the code
	// as its tie-break, so a fixture whose two orders coincide would stay green
	// with the whole comparison deleted.
	nokBig := portfolioProject(t, creator, "PRNOKZ11", nil)
	nokSmall := portfolioProject(t, creator, "PRNOKA11", nil)
	eur := portfolioProject(t, creator, "PRZEUR11", map[string]any{"currency": "EUR"})
	portfolioProject(t, creator, "PRNONE11", nil)
	for _, seed := range []struct {
		project projectJSON
		amount  float64
	}{{nokBig, 5000}, {nokSmall, 1000}, {eur, 100}} {
		movedMilestone(t, creator, createMilestone(t, creator, seed.project.Id, map[string]any{"amount": seed.amount}), "ready", nil)
	}

	page := getPortfolio(t, creator, "sort=readyAmount")
	if got := portfolioCodes(page); fmt.Sprint(got) != "[PRZEUR11 PRNOKZ11 PRNOKA11 PRNONE11]" {
		t.Errorf("sort=readyAmount gave %v, want EUR before NOK, largest first inside each, nothing-ready last", got)
	}
	if got := portfolioCodes(getPortfolio(t, creator, "sort=code")); fmt.Sprint(got) != "[PRNOKA11 PRNOKZ11 PRNONE11 PRZEUR11]" {
		t.Errorf("sort=code gave %v, want the alphabetical order — which this fixture makes differ from the readyAmount one at every position", got)
	}
	if row := portfolioRow(t, page, "PRNOKZ11"); row.ReadyAmount == nil || *row.ReadyAmount != 5000 {
		t.Errorf("PRNOKZ11 readyAmount = %v, want 5000", row.ReadyAmount)
	}
	if row := portfolioRow(t, page, "PRNONE11"); row.ReadyAmount != nil || row.ReadyCount != 0 {
		t.Errorf("PRNONE11 = %+v, want no ready amount at all", row)
	}
	want := []readyAmountJSON{{Currency: "EUR", Amount: 100}, {Currency: "NOK", Amount: 6000}}
	if fmt.Sprint(page.Totals.ReadyAmounts) != fmt.Sprint(want) {
		t.Errorf("totals.readyAmounts = %v, want %v: one sum per currency, by currency code", page.Totals.ReadyAmounts, want)
	}
	if page.Totals.ReadyCount != 3 {
		t.Errorf("totals.readyCount = %d, want 3", page.Totals.ReadyCount)
	}
}

// nextMilestone asc: the soonest planned date first, milestones nobody has
// dated after every dated one, and the projects with no open milestone at all
// last. And the plain alphabetical order, which is the fourth sort.
func TestGetProjectsEconomy_SortsByNextMilestoneAndByCode(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	soon := portfolioProject(t, creator, "PNSOON11", nil)
	later := portfolioProject(t, creator, "PNLATER1", nil)
	undated := portfolioProject(t, creator, "PNUNDTD1", nil)
	portfolioProject(t, creator, "PNNONE11", nil)
	day := func(days int) string {
		return modtest.Start.Add(time.Duration(days) * 24 * time.Hour).Format(time.DateOnly)
	}
	createMilestone(t, creator, soon.Id, map[string]any{"plannedDate": day(3)})
	createMilestone(t, creator, later.Id, map[string]any{"plannedDate": day(30)})
	createMilestone(t, creator, undated.Id, nil)

	if got := portfolioCodes(getPortfolio(t, creator, "sort=nextMilestone")); fmt.Sprint(got) != "[PNSOON11 PNLATER1 PNUNDTD1 PNNONE11]" {
		t.Errorf("sort=nextMilestone gave %v, want soonest first, undated after dated, none last", got)
	}
	if got := portfolioCodes(getPortfolio(t, creator, "sort=code")); fmt.Sprint(got) != "[PNLATER1 PNNONE11 PNSOON11 PNUNDTD1]" {
		t.Errorf("sort=code gave %v, want alphabetical", got)
	}
}

// The next milestone is the earliest-dated *open* one: an invoiced milestone
// dated before it is history, a cancelled one bills nothing, and a percent
// milestone resolves against the project's fixed price exactly as it does on
// the invoice plan.
func TestGetProjectsEconomy_NextMilestoneIsTheEarliestOpenOne(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "PMNEXT11", map[string]any{
		"billingType": "fixed-price", "fixedPriceAmount": 400000, "budgetAmount": nil,
	})
	day := func(days int) string {
		return modtest.Start.Add(time.Duration(days) * 24 * time.Hour).Format(time.DateOnly)
	}
	// Dated first of all, and already billed: not the next thing to do.
	billed := createMilestone(t, creator, project.Id, map[string]any{"name": "Fakturert", "plannedDate": day(-10)})
	movedMilestone(t, creator, movedMilestone(t, creator, billed, "ready", nil), "invoiced",
		map[string]any{"invoiceReference": "F-1", "invoiceDate": day(-9)})
	// Open, overdue, a percentage of the fixed price: the next one.
	createMilestone(t, creator, project.Id, map[string]any{
		"name": "Oppstart", "plannedDate": day(-1), "amount": nil, "percent": 25,
	})
	createMilestone(t, creator, project.Id, map[string]any{"name": "Sluttfase", "plannedDate": day(20)})

	h.Advance(24 * time.Hour)
	later, _ := signIn(t, h, "projects:manage-all")
	row := portfolioRow(t, getPortfolio(t, later, ""), "PMNEXT11")
	if row.NextMilestone == nil {
		t.Fatalf("row = %+v, want the next open milestone", row)
	}
	next := *row.NextMilestone
	if next.Name != "Oppstart" || next.Status != "planned" {
		t.Errorf("next milestone = %+v, want the open 'Oppstart'", next)
	}
	if next.PlannedDate == nil || *next.PlannedDate != day(-1) {
		t.Errorf("next milestone plannedDate = %v, want %s", next.PlannedDate, day(-1))
	}
	if !next.Overdue {
		t.Errorf("next milestone = %+v, want it marked overdue: its day has passed", next)
	}
	if next.EffectiveAmount == nil || *next.EffectiveAmount != 100000 {
		t.Errorf("next milestone effectiveAmount = %v, want 25 %% of 400 000", next.EffectiveAmount)
	}
}

// A page is a page, and the totals are not: they cover every row the filters
// matched, so turning the page never changes the headline figures.
func TestGetProjectsEconomy_PagesWithTotalsOverTheWholeFilteredSet(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	for _, code := range []string{"PPONE111", "PPTWO111", "PPTHREE1"} {
		project := portfolioProject(t, creator, code, nil)
		movedMilestone(t, creator, createMilestone(t, creator, project.Id, map[string]any{"amount": 1000}), "ready", nil)
	}

	first := getPortfolio(t, creator, "sort=code&pageSize=2")
	if got := portfolioCodes(first); fmt.Sprint(got) != "[PPONE111 PPTHREE1]" {
		t.Errorf("page 1 = %v, want the first two by code", got)
	}
	if first.Pagination.TotalCount != 3 || first.Pagination.TotalPages != 2 || !first.Pagination.HasNextPage {
		t.Errorf("page 1 pagination = %+v, want 3 rows over 2 pages", first.Pagination)
	}
	second := getPortfolio(t, creator, "sort=code&pageSize=2&page=2")
	if got := portfolioCodes(second); fmt.Sprint(got) != "[PPTWO111]" {
		t.Errorf("page 2 = %v, want the last row", got)
	}
	if fmt.Sprint(first.Totals) != fmt.Sprint(second.Totals) {
		t.Errorf("totals differ between pages: %v vs %v", first.Totals, second.Totals)
	}
	if first.Totals.ProjectCount != 3 || first.Totals.ReadyCount != 3 {
		t.Errorf("totals = %+v, want the whole filtered set", first.Totals)
	}

	// A page past the end is an empty page of a portfolio that still has three
	// projects in it, not an empty portfolio.
	past := getPortfolio(t, creator, "sort=code&pageSize=2&page=9")
	if len(past.Data) != 0 {
		t.Errorf("page 9 = %v, want no rows", portfolioCodes(past))
	}
	if past.Pagination.TotalCount != 3 || fmt.Sprint(past.Totals) != fmt.Sprint(first.Totals) {
		t.Errorf("page 9 pagination = %+v totals = %v, want the same set described", past.Pagination, past.Totals)
	}
}

// One request, one call into the module that owns the hours — not one per
// project — every project asked for in its own currency, no project asked for
// twice, and nothing locked while the answer is awaited.
func TestGetProjectsEconomy_AsksTheProviderOnceForTheWholeSet(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	nok := portfolioProject(t, creator, "PQNOK111", nil)
	eur := portfolioProject(t, creator, "PQEUR111", map[string]any{"currency": "EUR"})
	bare := portfolioProject(t, creator, "PQBARE11", map[string]any{
		"currency": nil, "budgetAmount": nil, "customerId": nil, "billingType": "non-billable",
	})

	var lockErr error
	actuals.during(func(ctx context.Context, req contracts.ActualsRequest) {
		_, lockErr = h.Pool().Exec(ctx,
			`SELECT id FROM projects.projects WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, req.ProjectID)
	})

	getPortfolio(t, creator, "")
	batches := actuals.batched()
	if len(batches) != 1 {
		t.Fatalf("the provider was handed %d batches, want exactly one call per request", len(batches))
	}
	want := map[int32]string{nok.Id: "NOK", eur.Id: "EUR", bare.Id: ""}
	got := map[int32]string{}
	for _, req := range batches[0] {
		if _, twice := got[req.ProjectID]; twice {
			t.Fatalf("project %d is in the batch twice: the contract refuses that", req.ProjectID)
		}
		got[req.ProjectID] = ""
		if req.Currency != nil {
			got[req.ProjectID] = *req.Currency
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("batch = %v, want each project in its own currency (%v)", got, want)
	}
	if lockErr != nil {
		t.Errorf("a project row could not be locked while the provider was called: %v — the read must hold no lock", lockErr)
	}
}

// Without time tracking the portfolio is still a portfolio: budgets, ready
// amounts and next milestones are this module's own. There are no actuals and
// no percentages at all — absent, not zeroes — and nothing can be over budget.
func TestGetProjectsEconomy_WithoutTimeTracking(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "PTNOTIME", nil)
	portfolioProject(t, creator, "PTALSONO", nil)
	movedMilestone(t, creator, createMilestone(t, creator, project.Id, map[string]any{"amount": 750}), "ready", nil)

	page := getPortfolio(t, creator, "")
	// Every row has a budget and none has a percentage, so the default sort
	// compares nothing and the whole list falls through to its tie-break. A
	// portfolio whose order depended on map iteration would shuffle here.
	if got := portfolioCodes(page); fmt.Sprint(got) != "[PTALSONO PTNOTIME]" {
		t.Errorf("default order without time tracking = %v, want code order: no row has a basis to compare", got)
	}
	if page.TimeTracking {
		t.Errorf("timeTracking = true, want false: no module reports what has been logged")
	}
	row := portfolioRow(t, page, "PTNOTIME")
	if row.Actuals != nil || row.BudgetUsed != nil || row.PendingHours != nil || row.OverBudget {
		t.Errorf("row = %+v, want no actuals, no budgetUsed, no pendingHours and not over budget", row)
	}
	if row.ReadyCount != 1 || row.ReadyAmount == nil || *row.ReadyAmount != 750 {
		t.Errorf("row = %+v, want the invoice plan's own figures, which this module owns", row)
	}
	if page := getPortfolio(t, creator, "overBudget=true"); len(page.Data) != 0 {
		t.Errorf("overBudget=true gave %v, want none: nothing can be over a budget nobody measured", portfolioCodes(page))
	}
}

// The buckets a row carries, and the pending hours that are the two
// undecided ones added up. The row's amounts are never shaped away — every
// row is one the caller may see the money of.
func TestGetProjectsEconomy_RowCarriesTheThreeBucketsAndPendingHours(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "PBUCKET1", nil)
	actuals.set(project.Id, loggedTotals(
		loggedBucket(4, "400.00", "150.00"),
		loggedBucket(2.5, "250.00", "100.00"),
		loggedBucket(1.25, "125.00", "50.00"),
	))

	row := portfolioRow(t, getPortfolio(t, creator, ""), "PBUCKET1")
	if row.Actuals == nil {
		t.Fatalf("row = %+v, want actuals", row)
	}
	a := *row.Actuals
	if a.Approved.Hours != 4 || a.Submitted.Hours != 2.5 || a.Draft.Hours != 1.25 || a.TotalHours != 7.75 {
		t.Errorf("hours = %+v, want the three buckets and their sum", a)
	}
	if a.Approved.Amount == nil || *a.Approved.Amount != 400 || a.TotalAmount == nil || *a.TotalAmount != 775 {
		t.Errorf("amounts = %+v, want the buckets' bill amounts and their sum", a)
	}
	if row.PendingHours == nil || *row.PendingHours != 3.75 {
		t.Errorf("pendingHours = %v, want the submitted and draft hours added up", row.PendingHours)
	}
	if row.Currency == nil || *row.Currency != "NOK" {
		t.Errorf("currency = %v, want the project's own", row.Currency)
	}
	if row.Project.Customer == nil || row.Project.Customer.Id != customerKraftVerket ||
		row.Project.Customer.Name == nil || *row.Project.Customer.Name != customerKraftVerketName {
		t.Errorf("customer = %+v, want the directory's name for it", row.Project.Customer)
	}
}

// More projects than one answer can carry is a 400 asking for a narrower
// filter, not a truncated portfolio: a total computed over part of the set
// would be a wrong number rather than a missing one.
func TestGetProjectsEconomy_RefusesMoreProjectsThanOneAnswerCanCarry(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	c, _ := signIn(t, h, "projects:manage-all")
	seed := func(from, to int) {
		t.Helper()
		h.Exec(t, `INSERT INTO projects.projects (code, name, billing_type, status, created_by_user_id, created_at, updated_at)
			SELECT 'CAP' || lpad(g::text, 6, '0'), 'Capped ' || g, 'non-billable', 'active', $1, now(), now()
			FROM generate_series($2::integer, $3::integer) g`, uuid.Nil, from, to)
	}

	seed(1, contracts.MaxActualsRequests)
	if page := getPortfolio(t, c, ""); page.Totals.ProjectCount != int32(contracts.MaxActualsRequests) {
		t.Fatalf("projectCount = %d, want the cap exactly — %d projects still answer",
			page.Totals.ProjectCount, contracts.MaxActualsRequests)
	}

	seed(contracts.MaxActualsRequests+1, contracts.MaxActualsRequests+1)
	r := readPortfolio(t, c, "")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["status"]) == 0 {
		t.Errorf("problem = %+v, want a message on 'status' asking for a narrower filter", problem)
	}
}

// A query parameter outside its enumeration is a mistake worth naming, not a
// filter that silently matches nothing or an order nobody asked for.
func TestGetProjectsEconomy_RejectsUnknownParameters(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	c, _ := signIn(t, h)

	for _, tc := range []struct{ query, field string }{
		{"sort=bogus", "sort"},
		{"status=bogus", "status"},
		{"page=0", "page"},
		{"pageSize=1000", "pageSize"},
	} {
		r := readPortfolio(t, c, tc.query)
		if r.Status != http.StatusBadRequest {
			t.Errorf("%q: status %d body %s, want 400", tc.query, r.Status, r.Body)
			continue
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if len(problem.Errors[tc.field]) == 0 {
			t.Errorf("%q: problem = %+v, want a message on %q", tc.query, problem, tc.field)
		}
	}
}

// unpriceableWarning is what responses.go logs for a milestone whose amount
// cannot be worked out. The portfolio must not produce it for milestones it
// never reports.
const unpriceableWarning = "a billing milestone cannot be priced"

// A milestone that is neither the row's next one nor ready is never priced at
// all. Pricing it would be exact-decimal arithmetic per milestone across the
// whole filtered set for a number nothing reads — and, worse, an unpriceable
// one logs a warning, so a handful of percent milestones orphaned by a project
// leaving fixed-price billing would warn on every portfolio read, about rows
// that are not even on the page.
func TestGetProjectsEconomy_DoesNotPriceMilestonesItDoesNotReport(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "PWORPHAN", nil)
	createMilestone(t, creator, project.Id, map[string]any{
		"name": "Neste", "plannedDate": modtest.Start.Add(72 * time.Hour).Format(time.DateOnly),
	})
	// A percent milestone on a project with no fixed price has no effective
	// amount. The API refuses to create one — only a project that dropped its
	// fixed price after the fact leaves one behind — so it goes in directly,
	// undated, which puts it after the dated one.
	orphan := insertMilestone(t, h, project.Id, map[string]any{
		"name": "Uprisbar", "percent": 25.00, "position": int32(2),
	})

	row := portfolioRow(t, getPortfolio(t, creator, ""), "PWORPHAN")
	if row.NextMilestone == nil || row.NextMilestone.Name != "Neste" {
		t.Fatalf("nextMilestone = %+v, want the dated one", row.NextMilestone)
	}
	if row.ReadyCount != 0 {
		t.Errorf("readyCount = %d, want 0", row.ReadyCount)
	}
	if strings.Contains(h.Logs(), unpriceableWarning) {
		t.Errorf("the read warned about a milestone it does not report:\n%s", h.Logs())
	}

	// Ready, the same milestone *is* reported — it counts towards readyCount —
	// so it is priced, and the warning is then the honest one. That is what
	// makes the assertion above able to fail.
	h.Exec(t, `UPDATE projects.billing_milestones SET status = 'ready' WHERE id = $1`, orphan)
	row = portfolioRow(t, getPortfolio(t, creator, ""), "PWORPHAN")
	if row.ReadyCount != 1 || row.ReadyAmount != nil {
		t.Errorf("row = %+v, want it counted and left out of the amount", row)
	}
	if !strings.Contains(h.Logs(), unpriceableWarning) {
		t.Errorf("nothing was logged about a milestone the row reports and cannot price:\n%s", h.Logs())
	}
}

// A provider that cannot answer fails the portfolio, exactly as it fails the
// per-project economy: a budget compared against zeroes is a wrong answer,
// not a degraded one. (The dashboard is the one place that degrades, because
// it merges many modules' items.)
func TestGetProjectsEconomy_ProviderFailureIsAnError(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	portfolioProject(t, creator, "PZFAIL11", nil)
	actuals.fail(errors.New("time: the entries could not be read"))

	r := readPortfolio(t, creator, "",
		modtest.SkipContract("a degraded actuals provider is an infrastructure failure, deliberately off-contract"))
	if r.Status != http.StatusInternalServerError {
		t.Errorf("status %d body %s, want 500", r.Status, r.Body)
	}
}
