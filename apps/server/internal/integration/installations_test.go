package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The five installations of this delivery, composed for real.
//
// `timeTracking` and `expenseTracking` are facts about the installation, not
// about the caller, and each module's own suite proves the *handling* of them
// with a preset dependency (modtest.WithExpenses, modtest.WithActuals). What
// no module's suite can prove is that the flag follows real **enablement** —
// that "expenses is in MODULES" and "Deps.Expenses is a provider" are the same
// statement, through config.Load, enabledModules and Compose's soleProvider.
// That is what this file composes the real modules to say.

// economyFlags is the pair of booleans both economy reads answer.
type economyFlags struct {
	TimeTracking    bool `json:"timeTracking"`
	ExpenseTracking bool `json:"expenseTracking"`
}

// TestInstallations_TheTrackingFlagsFollowRealEnablement loops the five
// installations that exist for this delivery and asserts the flags, the
// presence of the expenses block, and whether the Expenses tab's own read is
// mounted at all.
func TestInstallations_TheTrackingFlagsFollowRealEnablement(t *testing.T) {
	t.Parallel()
	for _, inst := range []struct {
		modules []string
		time    bool
		expense bool
	}{
		{modules: []string{modProjects, modTime, modExpenses}, time: true, expense: true},
		{modules: []string{modProjects, modExpenses}, time: false, expense: true},
		{modules: []string{modProjects, modTime}, time: true, expense: false},
		{modules: []string{modProjects}, time: false, expense: false},
	} {
		name := fmt.Sprint(inst.modules)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newInstallation(t, inst.modules...)
			// Creating a project makes the creator its manager, which is the
			// role every figure here is read with — no role assignment needed.
			admin, _ := signInAdmin(t, h)
			project := createProject(t, admin)

			var one struct {
				economyFlags
				Expenses *struct {
					TotalCost float64 `json:"totalCost"`
				} `json:"expenses"`
			}
			okJSON(t, admin, http.MethodGet, fmt.Sprintf(projectEconomyPath, project.Id), nil, &one)
			if one.TimeTracking != inst.time || one.ExpenseTracking != inst.expense {
				t.Errorf("%s: /projects/{id}/economy says time=%v expense=%v, want %v/%v",
					name, one.TimeTracking, one.ExpenseTracking, inst.time, inst.expense)
			}
			// The block follows the flag: present (with zeroes) when expenses
			// are tracked, absent when they are not — never zeroes standing in
			// for "not enabled".
			if (one.Expenses != nil) != inst.expense {
				t.Errorf("%s: the expenses block is %v, want present=%v",
					name, one.Expenses != nil, inst.expense)
			}

			var portfolio struct {
				economyFlags
			}
			okJSON(t, admin, http.MethodGet, projectsEconomy, nil, &portfolio)
			if portfolio.TimeTracking != inst.time || portfolio.ExpenseTracking != inst.expense {
				t.Errorf("%s: /projects/economy says time=%v expense=%v, want %v/%v",
					name, portfolio.TimeTracking, portfolio.ExpenseTracking, inst.time, inst.expense)
			}

			// And the Expenses tab's own read exists exactly when the expenses
			// module does. Without it the path is not mounted at all, so the
			// answer is the platform's own 404 — body and all, which the
			// expenses contract rightly documents no body for. That probe goes
			// past the validator rather than the contract being loosened to
			// admit an answer no installation serving the operation can give.
			path := fmt.Sprintf(expensesProjectSum, project.Id)
			var summary *modtest.Response
			wantSummary := http.StatusOK
			if inst.expense {
				summary = httpGet(t, admin, path)
			} else {
				wantSummary = http.StatusNotFound
				summary = admin.Do(http.MethodGet, path, nil,
					modtest.SkipContract("the expenses module is not mounted in this installation"))
			}
			if summary.Status != wantSummary {
				t.Errorf("%s: the project summary answered %d, want %d", name, summary.Status, wantSummary)
			}
		})
	}
}

// Expenses alone is a valid installation, and the one where the optional
// contract points the other way: nothing provides a project directory, so the
// module's own project-shaped reads answer that they are not there — while
// everything else about it works.
func TestInstallations_ExpensesWithoutProjects(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modExpenses)
	admin, _ := h.SignInUser(t, "expenses:access", "expenses:manage", "expenses:approve",
		"projects:view-financials", "projects:manage-all")

	var meta struct {
		ProjectsAvailable bool `json:"projectsAvailable"`
	}
	okJSON(t, admin, http.MethodGet, "/api/v1/expenses/meta", nil, &meta)
	if meta.ProjectsAvailable {
		t.Error("projectsAvailable is true in an installation composed without the projects module")
	}
	// The summary is the bare 404 of a caller who may not ask, not the
	// platform's unmounted-path one: the route exists, the answer does not
	// say why.
	if r := httpGet(t, admin, fmt.Sprintf(expensesProjectSum, 1001)); r.Status != http.StatusNotFound {
		t.Errorf("the project summary answered %d body %s, want a bare 404", r.Status, r.Body)
	}
	// And the projects module is not mounted, so its economy is not a route at
	// all. The probe is off-contract on purpose — the merged document holds
	// that path because another installation serves it — so it is sent past
	// the validator rather than loosening the contract to admit a 404 no
	// installation that mounts the operation can produce.
	r := admin.Do(http.MethodGet, projectsEconomy, nil,
		modtest.SkipContract("the projects module is not mounted in this installation"))
	if r.Status != http.StatusNotFound {
		t.Errorf("the portfolio answered %d, want 404 in an installation with no projects module", r.Status)
	}
}

// projectResponse is the part of ProjectResponse these tests read. The
// currency lives under financials, which is itself absent without
// projects:view-financials — the admin here holds it.
type projectResponse struct {
	Id         int32  `json:"id"`
	Code       string `json:"code"`
	Financials *struct {
		Currency *string `json:"currency"`
	} `json:"financials"`
}

// currency is the project's own, or a failed test.
func (p projectResponse) currency(t *testing.T) string {
	t.Helper()
	if p.Financials == nil || p.Financials.Currency == nil {
		t.Fatalf("project %d carries no currency: every figure here is in it", p.Id)
	}
	return *p.Financials.Currency
}

// createProject makes the one project every test here works on: a NOK,
// time-and-materials project for the fake directory's customer, through the
// real POST /api/v1/projects.
func createProject(t *testing.T, c *modtest.Client) projectResponse {
	t.Helper()
	var project projectResponse
	okJSON(t, c, http.MethodPost, projectsPath, map[string]any{
		"code":        "KVEM1000",
		"name":        "Kraft-Verket modernisering",
		"customerId":  customerKraftVerket,
		"billingType": "time-and-materials",
		"currency":    "NOK",
	}, &project)
	if got := project.currency(t); got != "NOK" {
		t.Fatalf("the project's currency is %q, want NOK", got)
	}
	// A new project starts 'planned', and CanLogTime — the rule booking an
	// expense on one is judged by — admits only an active project. So the
	// fixture opens it, through the door that does that.
	okJSON(t, c, http.MethodPut, fmt.Sprintf("%s/%d/status", projectsPath, project.Id),
		map[string]any{"status": "active"}, nil)
	return project
}
