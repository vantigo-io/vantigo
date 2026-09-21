package integration_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
)

// The harness these tests share: the real projects, time and expenses modules
// composed together by module.Compose, through modtest, against the real test
// database. No module here is given a fake of another module — the only fake
// is the customer directory, because `customers` is the one thing projects
// needs that this delivery has nothing to say about, and composing it would
// only add a schema nothing reads.
//
// That is the whole point. Each module's own suite hands the other side an
// imitation; here `Deps.Expenses` is the expenses module's own provider and
// `Deps.Projects` is the projects module's own directory, resolved by Compose
// exactly as `cmd/vantigo` resolves them.

// The module names, so a test says which installation it is about rather than
// repeating a list.
const (
	modProjects = "projects"
	modTime     = "time"
	modExpenses = "expenses"
)

// moduleNamed is the real module value for one name. It is the only place this
// package maps a name to a module, so an installation is a list of strings
// everywhere else.
func moduleNamed(t *testing.T, name string) module.Module {
	t.Helper()
	switch name {
	case modProjects:
		return projects.Module()
	case modTime:
		return timetracking.Module()
	case modExpenses:
		return expenses.Module()
	}
	t.Fatalf("integration: no module named %q", name)
	return module.Module{}
}

// newInstallation composes exactly the named modules, in the order given, the
// way an operator's MODULES list would. modtest derives MODULES from the
// modules it is handed, so "expenses is enabled" here means the real thing:
// enabledModules filtered it in, Compose resolved its provider slot, and
// Deps.Expenses is the provider rather than a preset.
func newInstallation(t *testing.T, names ...string) *modtest.Harness {
	t.Helper()
	opts := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithDirectory(fakeCustomers{}),
	}
	for _, name := range names {
		opts = append(opts, modtest.WithModule(moduleNamed(t, name)))
	}
	return modtest.New(t, opts...)
}

// recorder validates every exchange of every test here against the *three*
// contracts at once, so a request to any of the modules is checked against the
// contract that declares it.
//
// It is one recorder over a merged document rather than one per module
// because modtest installs a single transport: openapi.Validate routes an
// exchange by path through the document it is given, and the three modules'
// paths are disjoint (/api/v1/projects, /api/v1/time, /api/v1/expenses), so a
// document holding all of them routes each exchange to its own operation. The
// documents are already fully resolved when Load returns, so a path item
// carries its schemas with it and nothing is lost in the merge.
//
// There is no coverage gate here, deliberately: coverage is each module's own
// TestMain's job over its own contract, and a cross-module test that counted
// towards it would let an operation look exercised without its module's suite
// ever having exercised it.
var recorder = mergedRecorder(modProjects, modTime, modExpenses)

func mergedRecorder(names ...string) *contracttest.Recorder {
	merged := &openapi3.T{
		OpenAPI: "3.0.3",
		Info:    &openapi3.Info{Title: "integration", Version: "1"},
		Paths:   openapi3.NewPaths(),
	}
	for _, name := range names {
		doc, err := openapi.Load(context.Background(), name)
		if err != nil {
			panic("integration: load the " + name + " contract: " + err.Error())
		}
		for path, item := range doc.Paths.Map() {
			merged.Paths.Set(path, item)
		}
	}
	return contracttest.New(merged)
}

// The customer the fixture's project belongs to.
const (
	customerKraftVerket     = 4001
	customerKraftVerketName = "Kraft-Verket AS"
)

// fakeCustomers is contracts.CustomerDirectory over the one customer these
// tests need. It is the only fake in this package: customers is a module this
// delivery does not touch, and projects refuses a customer id it cannot
// resolve, so something has to answer for it.
type fakeCustomers struct{}

var _ contracts.CustomerDirectory = fakeCustomers{}

func (fakeCustomers) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	if id == customerKraftVerket {
		return &contracts.CustomerEntry{ID: id, Name: customerKraftVerketName}, nil
	}
	return nil, nil
}

func (fakeCustomers) Customers(_ context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	entries := []contracts.CustomerEntry{}
	for _, id := range ids {
		if id == customerKraftVerket {
			entries = append(entries, contracts.CustomerEntry{ID: id, Name: customerKraftVerketName})
		}
	}
	return entries, nil
}

func (fakeCustomers) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (fakeCustomers) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return nil, nil
}

func (fakeCustomers) BillingProfile(_ context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	if id == customerKraftVerket {
		return &contracts.CustomerBillingProfile{ID: id, Name: customerKraftVerketName}, nil
	}
	return nil, nil
}

// materialsCategory is the first category 00012 seeds (identity starts at
// 1001, and design §3.3's eight names are seeded in order). Every outlay needs
// one, and which it is does not matter to a single figure here.
const materialsCategory = 1001

// The project roles, as projects names them.
const (
	roleManager = "manager"
	roleMember  = "member"
)

// addRole assigns a role directly, the one thing here that is not done through
// an API: the endpoints that assign project roles do not exist yet (they are a
// later task of the projects module), and the insert is the one
// InsertProjectRole makes.
func addRole(t *testing.T, h *modtest.Harness, projectID int32, userID uuid.UUID, role string) {
	t.Helper()
	h.Exec(t, `INSERT INTO projects.project_roles (project_id, user_id, role, created_at)
	           VALUES ($1, $2, $3, now())`, projectID, userID, role)
}

// The paths these tests read, so a typo is a compile error.
const (
	projectsPath       = "/api/v1/projects"
	projectsEconomy    = "/api/v1/projects/economy"
	expensesEntries    = "/api/v1/expenses/entries"
	expensesClaims     = "/api/v1/expenses/claims"
	expensesSubmit     = "/api/v1/expenses/submit"
	expensesApprove    = "/api/v1/expenses/approve"
	expensesReject     = "/api/v1/expenses/reject"
	expensesProjectSum = "/api/v1/expenses/projects/%d/summary"
	projectEconomyPath = "/api/v1/projects/%d/economy"
)

// okJSON sends one request, fails the test unless it answered 2xx, and decodes
// the body into out (nil to ignore it).
func okJSON(t *testing.T, c *modtest.Client, method, path string, body, out any) {
	t.Helper()
	r := c.Do(method, path, body)
	if r.Status < 200 || r.Status >= 300 {
		t.Fatalf("%s %s: status %d body %s, want 2xx", method, path, r.Status, r.Body)
	}
	if out != nil {
		r.JSON(out)
	}
}

// signInAdmin is one caller who may do everything this fixture needs in both
// modules: create the project, record and price expenses, and read the money.
// Every test here is about figures agreeing, not about who may see them —
// that is each module's own suite's subject — so one caller keeps the fixture
// readable.
func signInAdmin(t *testing.T, h *modtest.Harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t,
		"projects:access", "projects:create", "projects:manage-all", "projects:view-all",
		"projects:view-financials", "projects:view-costs",
		"expenses:access", "expenses:approve", "expenses:manage",
		"time:access",
	)
}

// httpGet is a plain GET returning the whole response, for a test whose
// subject is the status.
func httpGet(t *testing.T, c *modtest.Client, path string) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, path, nil)
}
