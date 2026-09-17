package projects_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// POST /api/v1/projects: the create path, design §4.1's rules and D6's
// "whoever creates a project becomes its manager".

func TestPostProjects_MinimalBody_CreatesWithTheCallerAsManager(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signIn(t, h, "projects:create")

	// The code arrives padded and lower-cased; D2 says it is trimmed and
	// upper-cased before it is validated or stored.
	project := createProject(t, c, map[string]any{"code": " kvem1000 "})

	if project.Code != "KVEM1000" {
		t.Errorf("Code = %q, want %q (trimmed and upper-cased)", project.Code, "KVEM1000")
	}
	if project.Id < 1001 {
		t.Errorf("Id = %d, want the identity sequence's first value or later", project.Id)
	}
	if project.Status != "planned" {
		t.Errorf("Status = %q, want the column default %q", project.Status, "planned")
	}
	if project.Revision != 1 {
		t.Errorf("Revision = %d, want 1", project.Revision)
	}
	if project.Internal {
		t.Error("Internal = true, want false for a project with a customer")
	}
	if project.CustomerId == nil || *project.CustomerId != customerKraftVerket {
		t.Errorf("CustomerId = %v, want %d", project.CustomerId, customerKraftVerket)
	}
	if project.CustomerName == nil || *project.CustomerName != customerKraftVerketName {
		t.Errorf("CustomerName = %v, want %q from the directory", project.CustomerName, customerKraftVerketName)
	}
	if len(project.Managers) != 1 || project.Managers[0].UserId != userID {
		t.Fatalf("Managers = %+v, want exactly the creator %s", project.Managers, userID)
	}
	if name := project.Managers[0].DisplayName; name == "" || name == "Unknown user" {
		t.Errorf("Managers[0].DisplayName = %q, want the name the user directory resolved", name)
	}
	if !project.Capabilities.CanManage || !project.Capabilities.CanSeeFinancials {
		t.Errorf("Capabilities = %+v, want both true for the creator, who is a manager", project.Capabilities)
	}
	if project.Financials == nil {
		t.Error("Financials is absent, want it present (possibly empty) for a caller who may see it")
	}
	if !project.BillingLinesAvailable {
		t.Error("BillingLinesAvailable = false, want true with the products catalog composed")
	}
}

// Every §4.1 rule, one body override each, each reported on its own field.
// A case is built so only the rule under test can fire: the assertion is
// that exactly one field failed, and that it is the expected one, so a rule
// that leaked a second message would fail here rather than quietly.
func TestPostProjects_ValidationRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	cases := []struct {
		name      string
		overrides map[string]any
		wantField string
	}{
		{"blank code", map[string]any{"code": "   "}, "code"},
		{"code with a hyphen", map[string]any{"code": "KV-1000"}, "code"},
		{"code shorter than two characters", map[string]any{"code": "K"}, "code"},
		{"code longer than twenty characters", map[string]any{"code": strings.Repeat("A", 21)}, "code"},
		{"blank name", map[string]any{"name": "  "}, "name"},
		{"name longer than 200 characters", map[string]any{"name": strings.Repeat("n", 201)}, "name"},
		{"description longer than 4000 characters", map[string]any{"description": strings.Repeat("d", 4001)}, "description"},
		{"customer that does not exist", map[string]any{"code": "UNKNOWNCUST", "customerId": customerUnknown}, "customerId"},
		{"blank billing type", map[string]any{"billingType": ""}, "billingType"},
		{"billing type outside the set", map[string]any{"billingType": "hourly"}, "billingType"},
		{"internal project that is not non-billable", map[string]any{"customerId": nil, "billingType": "time-and-materials"}, "billingType"},
		{"fixed price project without an amount", map[string]any{"billingType": "fixed-price"}, "fixedPriceAmount"},
		{"fixed price amount of zero", map[string]any{"billingType": "fixed-price", "fixedPriceAmount": 0, "currency": "NOK"}, "fixedPriceAmount"},
		{"fixed price amount on a time-and-materials project", map[string]any{"fixedPriceAmount": 1000, "currency": "NOK"}, "fixedPriceAmount"},
		{"budget hours of zero", map[string]any{"budgetHours": 0}, "budgetHours"},
		{"budget amount of zero", map[string]any{"budgetAmount": 0, "currency": "NOK"}, "budgetAmount"},
		{"currency that is not three letters", map[string]any{"currency": "kroner", "budgetAmount": 1000}, "currency"},
		{"an amount with no currency", map[string]any{"budgetAmount": 1000}, "currency"},
		{"end date before start date", map[string]any{"startDate": "2026-03-01", "endDate": "2026-02-01"}, "endDate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := c.Do(http.MethodPost, "/api/v1/projects", createBody(tc.overrides))
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if problem.Title != "Invalid project" {
				t.Errorf("Title = %q, want %q", problem.Title, "Invalid project")
			}
			if len(problem.Errors) != 1 || len(problem.Errors[tc.wantField]) == 0 {
				t.Errorf("Errors = %v, want exactly one field %q", problem.Errors, tc.wantField)
			}
		})
	}
}

// D2: codes are upper-cased before they are stored, so the plain unique
// index makes uniqueness case-insensitive. The loser of the race gets an
// ordinary field error, not a conflict (D3).
func TestPostProjects_DuplicateCodeDifferingOnlyInCase_IsRejectedOnTheCodeField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	createProject(t, c, map[string]any{"code": "KVEM1000"})

	r := c.Do(http.MethodPost, "/api/v1/projects", createBody(map[string]any{
		"code": "kvem1000", "name": "A different project entirely",
	}))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["code"]) == 0 {
		t.Errorf("Errors = %v, want the failure on the 'code' field", problem.Errors)
	}
}

// D4: a project with no customer is internal, and an internal project must
// be non-billable — there is nobody to invoice.
func TestPostProjects_InternalProjectMustBeNonBillable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	billable := c.Do(http.MethodPost, "/api/v1/projects", createBody(map[string]any{
		"code": "INT1000", "customerId": nil, "billingType": "time-and-materials",
	}))
	if billable.Status != http.StatusBadRequest {
		t.Fatalf("billable internal project: status %d body %s, want 400", billable.Status, billable.Body)
	}
	var problem validationProblemJSON
	billable.JSON(&problem)
	if len(problem.Errors["billingType"]) == 0 {
		t.Errorf("Errors = %v, want the failure on the 'billingType' field", problem.Errors)
	}

	project := createProject(t, c, map[string]any{
		"code": "INT1001", "customerId": nil, "billingType": "non-billable",
	})
	if !project.Internal {
		t.Error("Internal = false, want true for a project with no customer")
	}
	if project.CustomerId != nil || project.CustomerName != nil {
		t.Errorf("CustomerId = %v, CustomerName = %v, want both absent", project.CustomerId, project.CustomerName)
	}
}

// §4.1: an archived customer resolves and is allowed. A project can outlive
// the relationship that started it.
func TestPostProjects_ArchivedCustomer_IsAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	project := createProject(t, c, map[string]any{"code": "ARCH1000", "customerId": customerArchived})
	if project.CustomerName == nil || *project.CustomerName != customerArchivedName {
		t.Errorf("CustomerName = %v, want %q", project.CustomerName, customerArchivedName)
	}
}

// The financial fields survive the round trip through numeric(12,2), and
// currency is part of financials rather than the project's own fields (D12).
func TestPostProjects_FixedPriceProject_CarriesItsFinancials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	project := createProject(t, c, map[string]any{
		"code": "FIX1000", "billingType": "fixed-price",
		"fixedPriceAmount": 125000.5, "budgetAmount": 130000, "budgetHours": 400, "currency": "nok",
	})
	if project.Financials == nil {
		t.Fatal("Financials is absent, want it present for the creator")
	}
	if project.Financials.Currency == nil || *project.Financials.Currency != "NOK" {
		t.Errorf("Financials.Currency = %v, want %q (upper-cased)", project.Financials.Currency, "NOK")
	}
	if project.Financials.FixedPriceAmount == nil || *project.Financials.FixedPriceAmount != 125000.5 {
		t.Errorf("Financials.FixedPriceAmount = %v, want 125000.5", project.Financials.FixedPriceAmount)
	}
	if project.Financials.BudgetAmount == nil || *project.Financials.BudgetAmount != 130000 {
		t.Errorf("Financials.BudgetAmount = %v, want 130000", project.Financials.BudgetAmount)
	}
	// Budget hours are planning data, outside the financial shaping.
	if project.BudgetHours == nil || *project.BudgetHours != 400 {
		t.Errorf("BudgetHours = %v, want 400", project.BudgetHours)
	}
}

// Every state change writes its timeline entry in the same transaction as
// the change (design §3), so a created project always has its first entry.
func TestPostProjects_WritesTheProjectCreatedTimelineEntry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signIn(t, h, "projects:create")

	project := createProject(t, c, map[string]any{"code": "TL1000"})

	entries := h.Count(t, `SELECT count(*) FROM projects.timeline_entries
	                       WHERE project_id = $1 AND event_type = 'project-created' AND actor_user_id = $2`,
		project.Id, userID)
	if entries != 1 {
		t.Errorf("project-created entries = %d, want exactly 1", entries)
	}
	display := modtest.One[string](t, h, `SELECT actor_display FROM projects.timeline_entries WHERE project_id = $1`, project.Id)
	if display == "" || display == "Unknown user" {
		t.Errorf("actor_display = %q, want the name the user directory resolved", display)
	}
}

// §4.2: the counter advances by one on every create, whatever code that
// create used, and starts at 1000 so the first suggestion is KVEM1000.
// next_value is what the suggestion reads without allocating, so it always
// holds the next number nobody has taken: 1001 once 1000 has been handed
// out, 1002 once 1001 has.
func TestPostProjects_AdvancesTheProjectCodeCounter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	createProject(t, c, map[string]any{"code": "CNT1000"})
	if got := h.Count(t, `SELECT next_value FROM projects.counters WHERE counter_name = 'project_code'`); got != 1001 {
		t.Errorf("counter after one create = %d, want 1001 (1000 was allocated)", got)
	}
	createProject(t, c, map[string]any{"code": "CNT1001"})
	if got := h.Count(t, `SELECT next_value FROM projects.counters WHERE counter_name = 'project_code'`); got != 1002 {
		t.Errorf("counter after two creates = %d, want 1002", got)
	}
}

// The optional descriptive fields are stored, not merely accepted: the 201
// carries them back and so does the GET that follows it. Without this, a
// create that dropped description or the dates on the floor would pass every
// other test in this file.
func TestPostProjects_OptionalFields_RoundTripOnCreateAndGet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	const description = "Oppgradering av styringssystemet på Kraft-Verket"
	created := createProject(t, c, map[string]any{
		"code": "RT1000", "description": description,
		"startDate": "2026-03-01", "endDate": "2026-09-30",
	})

	assertRoundTrip := func(label string, project projectJSON) {
		t.Helper()
		if project.Description == nil || *project.Description != description {
			t.Errorf("%s: Description = %v, want %q", label, project.Description, description)
		}
		if project.StartDate == nil || *project.StartDate != "2026-03-01" {
			t.Errorf("%s: StartDate = %v, want %q", label, project.StartDate, "2026-03-01")
		}
		if project.EndDate == nil || *project.EndDate != "2026-09-30" {
			t.Errorf("%s: EndDate = %v, want %q", label, project.EndDate, "2026-09-30")
		}
	}
	assertRoundTrip("create", created)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", created.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get: status %d body %s, want 200", r.Status, r.Body)
	}
	var fetched projectJSON
	r.JSON(&fetched)
	assertRoundTrip("get", fetched)
}

// D10: products is an optional dependency. With it composed the project can
// carry priced billing lines; without it the same project says so, rather
// than the frontend having to ask separately.
func TestPostProjects_BillingLinesAvailable_FollowsTheProductsModule(t *testing.T) {
	t.Parallel()

	t.Run("products enabled", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		c, _ := signIn(t, h, "projects:create")
		if project := createProject(t, c, nil); !project.BillingLinesAvailable {
			t.Error("BillingLinesAvailable = false, want true with a product catalog on Deps")
		}
	})

	t.Run("products disabled", func(t *testing.T) {
		t.Parallel()
		h := newHarnessWithoutProducts(t)
		c, _ := signIn(t, h, "projects:create")
		if project := createProject(t, c, nil); project.BillingLinesAvailable {
			t.Error("BillingLinesAvailable = true, want false with no product catalog on Deps")
		}
	})
}

// The contract's own access rule, enforced by the router before the handler
// runs: create needs projects:create on top of projects:access (D8).
func TestPostProjects_WithoutCreatePermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	r := c.Do(http.MethodPost, "/api/v1/projects", createBody(nil))
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

func TestPostProjects_WithoutAccessPermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "projects:create")

	r := c.Do(http.MethodPost, "/api/v1/projects", createBody(nil))
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}
