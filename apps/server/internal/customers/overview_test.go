package customers_test

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /customers/{id}/overview (customer 360 design D1, D2). depguard forbids
// this package from importing projects, time or expenses, even in a test, so
// the three contracts the overview reads are fakes built against
// internal/contracts and handed to Deps through modtest.WithProjects,
// WithActuals and WithExpenses — and leaving one out is the installation
// without that module, which the endpoint must answer as well.

// fakeProjects is contracts.ProjectDirectory over projects and roles a test
// adds after the harness exists (the customer ids come from the database).
// Only the two methods the overview calls are implemented: the embedded nil
// interface answers every other one with a panic, which is the right answer
// to a call the overview must never make.
type fakeProjects struct {
	contracts.ProjectDirectory
	mu       sync.Mutex
	projects []contracts.ProjectEntry
	roles    map[uuid.UUID][]int32
}

var _ contracts.ProjectDirectory = (*fakeProjects)(nil)

func (f *fakeProjects) add(entries ...contracts.ProjectEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects = append(f.projects, entries...)
}

func (f *fakeProjects) grant(userID uuid.UUID, projectIDs ...int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.roles == nil {
		f.roles = map[uuid.UUID][]int32{}
	}
	f.roles[userID] = append(f.roles[userID], projectIDs...)
}

// ProjectsForCustomer is the contract's rule restated: by id, capped.
func (f *fakeProjects) ProjectsForCustomer(_ context.Context, customerID int32) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.ProjectEntry
	for _, p := range f.projects {
		if p.CustomerID != nil && *p.CustomerID == customerID {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b contracts.ProjectEntry) int { return cmp.Compare(a.ID, b.ID) })
	if len(out) > contracts.MaxActualsRequests {
		out = out[:contracts.MaxActualsRequests]
	}
	return out, nil
}

// ProjectsForUser answers every project the user holds a role on, whichever
// customer it bills — the overview's intersection is what narrows it.
func (f *fakeProjects) ProjectsForUser(_ context.Context, userID uuid.UUID) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.ProjectEntry
	for _, p := range f.projects {
		if slices.Contains(f.roles[userID], p.ID) {
			out = append(out, p)
		}
	}
	return out, nil
}

// fakeActuals is contracts.ProjectActuals over totals a test sets, and it
// remembers every request so a test can check which projects were asked
// about, and in which currency.
type fakeActuals struct {
	contracts.ProjectActuals // Actuals (one project) is never the overview's call
	mu                       sync.Mutex
	totals                   map[int32]contracts.ActualsTotals
	asked                    []contracts.ActualsRequest
}

var _ contracts.ProjectActuals = (*fakeActuals)(nil)

func (f *fakeActuals) set(projectID int32, totals contracts.ActualsTotals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.totals == nil {
		f.totals = map[int32]contracts.ActualsTotals{}
	}
	f.totals[projectID] = totals
}

// ActualsForProjects answers every requested project, zero-valued when the
// test set nothing — the contract's own rule.
func (f *fakeActuals) ActualsForProjects(_ context.Context, reqs []contracts.ActualsRequest) (map[int32]contracts.ActualsTotals, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, reqs...)
	out := make(map[int32]contracts.ActualsTotals, len(reqs))
	for _, r := range reqs {
		out[r.ProjectID] = f.totals[r.ProjectID]
	}
	return out, nil
}

// fakeExpenses is contracts.ProjectExpenses over totals a test sets; a
// project with nothing set is absent from the answer, as the contract says.
type fakeExpenses struct {
	mu     sync.Mutex
	totals map[int32]contracts.ProjectExpenseTotals
	asked  []int32
}

var _ contracts.ProjectExpenses = (*fakeExpenses)(nil)

func (f *fakeExpenses) set(projectID int32, totals contracts.ProjectExpenseTotals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.totals == nil {
		f.totals = map[int32]contracts.ProjectExpenseTotals{}
	}
	f.totals[projectID] = totals
}

func (f *fakeExpenses) ExpensesForProjects(_ context.Context, ids []int32) (map[int32]contracts.ProjectExpenseTotals, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, ids...)
	out := map[int32]contracts.ProjectExpenseTotals{}
	for _, id := range ids {
		if totals, ok := f.totals[id]; ok {
			out[id] = totals
		}
	}
	return out, nil
}

// overviewHarness is newHarness with the three contracts stubbed as the test
// says. A nil argument leaves that module off — and must stay a nil
// interface, which is why each is checked rather than passed through.
func overviewHarness(t *testing.T, projects *fakeProjects, actuals *fakeActuals, expenses *fakeExpenses) *modtest.Harness {
	t.Helper()
	var opts []modtest.Option
	if projects != nil {
		opts = append(opts, modtest.WithProjects(projects))
	}
	if actuals != nil {
		opts = append(opts, modtest.WithActuals(actuals))
	}
	if expenses != nil {
		opts = append(opts, modtest.WithExpenses(expenses))
	}
	return newHarness(t, opts...)
}

// project is one fake project billed to customerID.
func project(id, customerID int32, code, status string, currency *string) contracts.ProjectEntry {
	return contracts.ProjectEntry{
		ID: id, Code: code, Name: "Prosjekt " + code, CustomerID: &customerID,
		Status: status, OpenForWork: status == "active", BillingType: "time-and-materials", Currency: currency,
	}
}

// bucket is an actuals bucket with hours and a bill amount; cost is never
// read by the overview, and "9999.99" makes sure it is never shown either.
func bucket(hundredths int64, bill string) contracts.ActualsBucket {
	return contracts.ActualsBucket{HoursHundredths: hundredths, BillAmount: bill, CostAmount: "9999.99"}
}

type overviewAmountJSON struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

type overviewJSON struct {
	Projects *struct {
		OpenCount  int  `json:"openCount"`
		TotalCount int  `json:"totalCount"`
		Truncated  bool `json:"truncated"`
		Open       []struct {
			ID         int32   `json:"id"`
			Code       string  `json:"code"`
			Name       string  `json:"name"`
			Status     string  `json:"status"`
			LastWorkOn *string `json:"lastWorkOn"`
		} `json:"open"`
	} `json:"projects"`
	Work *struct {
		UnbilledHoursHundredths  int64                 `json:"unbilledHoursHundredths"`
		UnbilledAmounts          *[]overviewAmountJSON `json:"unbilledAmounts"`
		ApprovedHoursHundredths  int64                 `json:"approvedHoursHundredths"`
		SubmittedHoursHundredths int64                 `json:"submittedHoursHundredths"`
		DraftHoursHundredths     int64                 `json:"draftHoursHundredths"`
		LastWorkOn               *string               `json:"lastWorkOn"`
	} `json:"work"`
	Expenses *struct {
		ReadyCount    int                  `json:"readyCount"`
		ReadyAmounts  []overviewAmountJSON `json:"readyAmounts"`
		LastExpenseOn *string              `json:"lastExpenseOn"`
	} `json:"expenses"`
	// LastActivity is a map so a test can say "exactly these dates, and no
	// others" in one comparison.
	LastActivity map[string]string `json:"lastActivity"`
}

// getOverview asks for customerID's overview, fails the test on anything but
// 200, and answers it twice: decoded, and as its top-level keys — "absent" is
// the claim most of these tests make, and a nil pointer cannot tell absent
// from null.
func getOverview(t *testing.T, c *modtest.Client, customerID int32) (overviewJSON, map[string]json.RawMessage) {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/overview", customerID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("overview: status %d body %s, want 200", r.Status, r.Body)
	}
	var got overviewJSON
	r.JSON(&got)
	var keys map[string]json.RawMessage
	r.JSON(&keys)
	return got, keys
}

// wantSections fails unless the response carries exactly these sections.
func wantSections(t *testing.T, keys map[string]json.RawMessage, want ...string) {
	t.Helper()
	got := slices.Sorted(maps.Keys(keys))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("sections = %v, want exactly %v", got, want)
	}
}

// The permission sets the matrix below signs in with.
var (
	overviewAccess     = []string{"customers:view", "projects:access"}
	overviewAll        = []string{"customers:view", "projects:access", "projects:view-all"}
	overviewFinancials = []string{"customers:view", "projects:access", "projects:view-all", "projects:view-financials"}
	overviewManageAll  = []string{"customers:view", "projects:access", "projects:manage-all"}
)

func TestOverview_UnknownCustomerIs404(t *testing.T) {
	t.Parallel()
	h := overviewHarness(t, &fakeProjects{}, nil, nil)
	c := h.SignIn(t, overviewFinancials...)

	r := c.Do(http.MethodGet, "/api/v1/customers/999999/overview", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// With no module beside it the overview is the customer's own last timeline
// date and nothing else — even for a caller who holds every projects key,
// because the key is not the module.
func TestOverview_WithoutTheOtherModulesAnswersOnlyLastActivity(t *testing.T) {
	t.Parallel()
	h := overviewHarness(t, nil, nil, nil)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")
	createManual(t, authenticatedClient(t, h), id, "2026-09-10", "Møte om ny rammeavtale")

	got, keys := getOverview(t, h.SignIn(t, overviewFinancials...), id)
	wantSections(t, keys, "lastActivity")
	if !maps.Equal(got.LastActivity, map[string]string{"timelineOn": "2026-09-10"}) {
		t.Errorf("lastActivity = %v, want only timelineOn 2026-09-10", got.LastActivity)
	}
}

// lastActivity is always present, and each date in it is absent when nothing
// is known — a customer inserted behind the API has no timeline at all. The
// customer is archived, too: it is still a customer, and its page still shows
// it, so it answers 200 like any other.
func TestOverview_LastActivityIsEmptyWhenNothingIsKnown(t *testing.T) {
	t.Parallel()
	h := overviewHarness(t, nil, nil, nil)
	id := insertCustomer(t, h, "Nedlagt Handel AS", "archived")

	got, keys := getOverview(t, h.SignIn(t, "customers:view"), id)
	wantSections(t, keys, "lastActivity")
	if string(keys["lastActivity"]) != "{}" || len(got.LastActivity) != 0 {
		t.Errorf("lastActivity = %s, want {}", keys["lastActivity"])
	}
}

// projects:access is the door (D2): without it nothing about projects is
// answered — and nothing is even asked of time or expenses.
func TestOverview_ProjectsNeedProjectsAccess(t *testing.T) {
	t.Parallel()
	projects, actuals, expenses := &fakeProjects{}, &fakeActuals{}, &fakeExpenses{}
	h := overviewHarness(t, projects, actuals, expenses)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")
	projects.add(project(1001, id, "KVEM1000", "active", ptr("NOK")))

	_, keys := getOverview(t, h.SignIn(t, "customers:view", "projects:view-all", "projects:view-financials"), id)
	wantSections(t, keys, "lastActivity")
	if len(actuals.asked) != 0 || len(expenses.asked) != 0 {
		t.Errorf("asked actuals %v and expenses %v, want neither asked anything", actuals.asked, expenses.asked)
	}
}

// Without view-all or manage-all the caller sees the projects it holds a role
// on — the list endpoint's own rule — and every count is over that set. A
// role on another customer's project must not leak in.
func TestOverview_ProjectsAccessAloneSeesOnlyTheCallersRoleProjects(t *testing.T) {
	t.Parallel()
	projects := &fakeProjects{}
	h := overviewHarness(t, projects, nil, nil)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")
	other := insertCustomer(t, h, "Acme Industrier AS", "active")
	projects.add(
		project(1001, id, "KVEM1000", "active", nil),
		project(1002, id, "KVEM1001", "active", nil),
		project(1003, id, "KVEM1002", "completed", nil),
		project(1004, other, "ACME1000", "active", nil),
	)
	c, userID := h.SignInUser(t, overviewAccess...)
	projects.grant(userID, 1002, 1003, 1004)

	got, keys := getOverview(t, c, id)
	wantSections(t, keys, "lastActivity", "projects")
	if got.Projects.TotalCount != 2 || got.Projects.OpenCount != 1 || len(got.Projects.Open) != 1 ||
		got.Projects.Open[0].ID != 1002 || got.Projects.Open[0].Status != "active" {
		t.Errorf("projects = %+v, want total 2, open 1 (1002) — the caller's own role projects on this customer", got.Projects)
	}
}

func TestOverview_ViewAllAndManageAllSeeEveryProject(t *testing.T) {
	t.Parallel()
	for name, permissions := range map[string][]string{"view-all": overviewAll, "manage-all": overviewManageAll} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			projects := &fakeProjects{}
			h := overviewHarness(t, projects, nil, nil)
			id := insertCustomer(t, h, "Kraft-Verket AS", "active")
			projects.add(
				project(1001, id, "KVEM1000", "active", nil),
				project(1002, id, "KVEM1001", "planned", nil),
				project(1003, id, "KVEM1002", "cancelled", nil),
			)

			got, _ := getOverview(t, h.SignIn(t, permissions...), id)
			if got.Projects == nil || got.Projects.TotalCount != 3 || got.Projects.OpenCount != 1 {
				t.Errorf("projects = %+v, want total 3 and open 1 without any role", got.Projects)
			}
		})
	}
}

// Open rows are newest work first, then id, and there are at most ten of
// them; the counts are not cut. A closed project's work still counts towards
// when work last happened — and it is the latest date on a project in the
// middle of the id order, so only a true maximum finds it: taking the first
// or the last date seen does not.
func TestOverview_OpenProjectsAreNewestWorkFirstAndCutAtTen(t *testing.T) {
	t.Parallel()
	projects, actuals := &fakeProjects{}, &fakeActuals{}
	h := overviewHarness(t, projects, actuals, nil)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")
	for pid := int32(1001); pid <= 1013; pid++ {
		status := "active"
		if pid == 1005 {
			status = "completed"
		}
		projects.add(project(pid, id, fmt.Sprintf("KVEM%d", pid), status, nil))
	}
	actuals.set(1003, contracts.ActualsTotals{LastEntryDate: ptr("2026-09-11")})
	actuals.set(1005, contracts.ActualsTotals{LastEntryDate: ptr("2026-09-12")})
	actuals.set(1007, contracts.ActualsTotals{LastEntryDate: ptr("2026-09-11")})
	actuals.set(1010, contracts.ActualsTotals{LastEntryDate: ptr("2026-09-01")})

	got, _ := getOverview(t, h.SignIn(t, overviewAll...), id)
	if got.Projects.OpenCount != 12 || got.Projects.TotalCount != 13 || got.Projects.Truncated {
		t.Errorf("counts = open %d, total %d, truncated %v; want 12, 13, false",
			got.Projects.OpenCount, got.Projects.TotalCount, got.Projects.Truncated)
	}
	var ids []int32
	for _, row := range got.Projects.Open {
		ids = append(ids, row.ID)
	}
	want := []int32{1003, 1007, 1010, 1001, 1002, 1004, 1006, 1008, 1009, 1011}
	if !slices.Equal(ids, want) {
		t.Errorf("open rows = %v, want %v (newest work first, then id, ten at most)", ids, want)
	}
	if got.Projects.Open[0].LastWorkOn == nil || *got.Projects.Open[0].LastWorkOn != "2026-09-11" || got.Projects.Open[3].LastWorkOn != nil {
		t.Errorf("lastWorkOn = %v / %v, want 2026-09-11 on the first row and none on the fourth",
			got.Projects.Open[0].LastWorkOn, got.Projects.Open[3].LastWorkOn)
	}
	if got.LastActivity["workOn"] != "2026-09-12" {
		t.Errorf("lastActivity.workOn = %q, want 2026-09-12 (the completed project's work counts too)", got.LastActivity["workOn"])
	}
}

// truncated says the customer's list reached the directory's cap, and only then.
func TestOverview_TruncatedOnlyWhenTheCustomerReachesTheCap(t *testing.T) {
	t.Parallel()
	for _, count := range []int{contracts.MaxActualsRequests - 1, contracts.MaxActualsRequests} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			t.Parallel()
			projects := &fakeProjects{}
			h := overviewHarness(t, projects, nil, nil)
			id := insertCustomer(t, h, "Kraft-Verket AS", "active")
			for i := range count {
				projects.add(project(int32(10000+i), id, fmt.Sprintf("CAP%d", i), "planned", nil))
			}

			got, _ := getOverview(t, h.SignIn(t, overviewAll...), id)
			if got.Projects.Truncated != (count == contracts.MaxActualsRequests) || got.Projects.TotalCount != count {
				t.Errorf("truncated = %v, total %d; want truncated only at the cap (%d)",
					got.Projects.Truncated, got.Projects.TotalCount, contracts.MaxActualsRequests)
			}
		})
	}
}

// Unbilled is approved less invoiced (D1), hours summed across every visible
// project and money per currency with no total across them. Each project is
// asked about in its own currency; one with none contributes hours only.
func TestOverview_UnbilledIsApprovedLessInvoicedPerCurrency(t *testing.T) {
	t.Parallel()
	projects, actuals := &fakeProjects{}, &fakeActuals{}
	h := overviewHarness(t, projects, actuals, nil)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")
	projects.add(
		project(1001, id, "KVEM1000", "active", ptr("NOK")),
		project(1002, id, "KVEM1001", "active", ptr("EUR")),
		project(1003, id, "KVEM1002", "active", nil),
		project(1004, id, "KVEM1003", "completed", ptr("NOK")),
	)
	actuals.set(1001, contracts.ActualsTotals{
		Approved: bucket(1000, "9000.00"), Invoiced: bucket(400, "3600.00"),
		Submitted: bucket(200, "1800.00"), Draft: bucket(100, "900.00"), LastEntryDate: ptr("2026-09-10"),
	})
	actuals.set(1002, contracts.ActualsTotals{
		Approved: bucket(300, "300.00"), Invoiced: bucket(0, "0.00"), Draft: bucket(50, "50.00"), LastEntryDate: ptr("2026-09-11"),
	})
	actuals.set(1003, contracts.ActualsTotals{Approved: bucket(200, "0.00"), Invoiced: bucket(50, "0.00"), LastEntryDate: ptr("2026-09-09")})
	actuals.set(1004, contracts.ActualsTotals{Approved: bucket(100, "900.00"), Invoiced: bucket(100, "900.00")})

	got, _ := getOverview(t, h.SignIn(t, overviewFinancials...), id)
	w := got.Work
	if w == nil {
		t.Fatal("work absent, want it: time is on and projects are visible")
	}
	if w.UnbilledHoursHundredths != 1050 || w.ApprovedHoursHundredths != 1600 ||
		w.SubmittedHoursHundredths != 200 || w.DraftHoursHundredths != 150 {
		t.Errorf("hours = unbilled %d, approved %d, submitted %d, draft %d; want 1050, 1600, 200, 150",
			w.UnbilledHoursHundredths, w.ApprovedHoursHundredths, w.SubmittedHoursHundredths, w.DraftHoursHundredths)
	}
	want := []overviewAmountJSON{{Currency: "EUR", Amount: 300}, {Currency: "NOK", Amount: 5400}}
	if w.UnbilledAmounts == nil || !slices.Equal(*w.UnbilledAmounts, want) {
		t.Errorf("unbilledAmounts = %v, want %v (per currency, by code, the fully invoiced project adding nothing)", w.UnbilledAmounts, want)
	}
	if w.LastWorkOn == nil || *w.LastWorkOn != "2026-09-11" || got.LastActivity["workOn"] != "2026-09-11" {
		t.Errorf("lastWorkOn = %v, lastActivity.workOn = %q; want 2026-09-11 for both", w.LastWorkOn, got.LastActivity["workOn"])
	}
	currencies := map[int32]string{}
	for _, r := range actuals.asked {
		currencies[r.ProjectID] = "none"
		if r.Currency != nil {
			currencies[r.ProjectID] = *r.Currency
		}
	}
	if !maps.Equal(currencies, map[int32]string{1001: "NOK", 1002: "EUR", 1003: "none", 1004: "NOK"}) {
		t.Errorf("asked = %v, want each project in its own currency", currencies)
	}
}

// Money needs financial rights (D2): unbilledAmounts and the whole expenses
// section are there for view-financials or manage-all and for nobody else.
func TestOverview_AmountsAndExpensesNeedFinancialRights(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		permissions []string
		money       bool
	}{
		{"view-all alone", overviewAll, false},
		{"view-financials", overviewFinancials, true},
		{"manage-all", overviewManageAll, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			projects, actuals, expenses := &fakeProjects{}, &fakeActuals{}, &fakeExpenses{}
			h := overviewHarness(t, projects, actuals, expenses)
			id := insertCustomer(t, h, "Kraft-Verket AS", "active")
			projects.add(project(1001, id, "KVEM1000", "active", ptr("NOK")))
			actuals.set(1001, contracts.ActualsTotals{Approved: bucket(100, "900.00"), Invoiced: bucket(0, "0.00")})
			expenses.set(1001, contracts.ProjectExpenseTotals{
				Currencies:    []contracts.CurrencyExpenses{{Currency: "NOK", ReadyCount: 1, ReadyAmount: "250.00"}},
				LastEntryDate: ptr("2026-09-05"),
			})

			got, keys := getOverview(t, h.SignIn(t, tc.permissions...), id)
			if tc.money {
				wantSections(t, keys, "lastActivity", "projects", "work", "expenses")
			} else {
				wantSections(t, keys, "lastActivity", "projects", "work")
			}
			if (got.Work.UnbilledAmounts != nil) != tc.money {
				t.Errorf("unbilledAmounts = %v, want present = %v", got.Work.UnbilledAmounts, tc.money)
			}
			_, expenseOn := got.LastActivity["expenseOn"]
			if expenseOn != tc.money || (len(expenses.asked) > 0) != tc.money {
				t.Errorf("expenseOn present = %v, expenses asked %v; want both only with financial rights", expenseOn, expenses.asked)
			}
		})
	}
}

// Work needs time: without it there is no work section and no row carries a
// last work date — while expenses, which never needed time, still answer.
func TestOverview_WorkNeedsTime(t *testing.T) {
	t.Parallel()
	projects, expenses := &fakeProjects{}, &fakeExpenses{}
	h := overviewHarness(t, projects, nil, expenses)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")
	projects.add(project(1001, id, "KVEM1000", "active", ptr("NOK")))

	got, keys := getOverview(t, h.SignIn(t, overviewFinancials...), id)
	wantSections(t, keys, "lastActivity", "projects", "expenses")
	if got.Projects.Open[0].LastWorkOn != nil {
		t.Errorf("open row lastWorkOn = %q, want absent with time off", *got.Projects.Open[0].LastWorkOn)
	}
	if _, ok := got.LastActivity["workOn"]; ok {
		t.Errorf("lastActivity = %v, want no workOn with time off", got.LastActivity)
	}
}

// Ready to invoice is the expenses contract's own Ready* figures, per
// currency, over the visible projects only; a currency with nothing ready is
// left out rather than shown as zero. The latest expense date is on the middle
// of three projects, so neither the first nor the last date seen is it.
func TestOverview_ExpensesAreWhatIsReadyToInvoice(t *testing.T) {
	t.Parallel()
	projects, expenses := &fakeProjects{}, &fakeExpenses{}
	h := overviewHarness(t, projects, nil, expenses)
	id := insertCustomer(t, h, "Kraft-Verket AS", "active")
	other := insertCustomer(t, h, "Acme Industrier AS", "active")
	projects.add(
		project(1001, id, "KVEM1000", "active", ptr("NOK")),
		project(1002, id, "KVEM1001", "completed", ptr("NOK")),
		project(1003, id, "KVEM1002", "active", ptr("NOK")),
		project(1099, other, "ACME1000", "active", ptr("NOK")),
	)
	expenses.set(1001, contracts.ProjectExpenseTotals{
		Currencies: []contracts.CurrencyExpenses{
			{Currency: "NOK", ReadyCount: 2, ReadyAmount: "1250.00"},
			{Currency: "SEK", ReadyCount: 0, ReadyAmount: "0.00"},
		},
		LastEntryDate: ptr("2026-09-05"),
	})
	expenses.set(1002, contracts.ProjectExpenseTotals{
		Currencies: []contracts.CurrencyExpenses{
			{Currency: "EUR", ReadyCount: 1, ReadyAmount: "300.00"},
			{Currency: "NOK", ReadyCount: 1, ReadyAmount: "250.50"},
		},
		LastEntryDate: ptr("2026-09-08"),
	})
	expenses.set(1003, contracts.ProjectExpenseTotals{
		Currencies:    []contracts.CurrencyExpenses{{Currency: "NOK", ReadyCount: 0, ReadyAmount: "0.00"}},
		LastEntryDate: ptr("2026-09-06"),
	})
	expenses.set(1099, contracts.ProjectExpenseTotals{
		Currencies:    []contracts.CurrencyExpenses{{Currency: "NOK", ReadyCount: 9, ReadyAmount: "9000.00"}},
		LastEntryDate: ptr("2026-09-12"),
	})

	got, _ := getOverview(t, h.SignIn(t, overviewFinancials...), id)
	e := got.Expenses
	want := []overviewAmountJSON{{Currency: "EUR", Amount: 300}, {Currency: "NOK", Amount: 1500.5}}
	if e == nil || e.ReadyCount != 4 || !slices.Equal(e.ReadyAmounts, want) {
		t.Fatalf("expenses = %+v, want 4 ready and %v", e, want)
	}
	if e.LastExpenseOn == nil || *e.LastExpenseOn != "2026-09-08" || got.LastActivity["expenseOn"] != "2026-09-08" {
		t.Errorf("lastExpenseOn = %v, lastActivity.expenseOn = %q; want 2026-09-08", e.LastExpenseOn, got.LastActivity["expenseOn"])
	}
	slices.Sort(expenses.asked)
	if !slices.Equal(expenses.asked, []int32{1001, 1002, 1003}) {
		t.Errorf("expenses asked about %v, want only this customer's projects", expenses.asked)
	}
}
