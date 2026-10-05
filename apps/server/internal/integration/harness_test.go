package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
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
// needs that the economy figures have nothing to say about, and composing it
// would only add a schema nothing reads. The one test that is about what
// customers answers — its default bill rate pricing time (rates_test.go) —
// composes the real module instead, and Compose then replaces the fake with
// customers' own directory, exactly as it does in `cmd/vantigo`. So do the
// invoices tests (invoices_test.go), which compose customers and invoices
// and give the installation what a send needs beyond the database: a real
// file-system object store over a temporary directory, where the issue
// stores the PDF the send attaches, and the smtp driver with the SMTP seam
// (modtest.WithSMTPSend) recording each envelope instead of dialing out.
//
// That is the whole point. Each module's own suite hands the other side an
// imitation; here `Deps.Expenses` is the expenses module's own provider and
// `Deps.Projects` is the projects module's own directory, resolved by Compose
// exactly as `cmd/vantigo` resolves them.

// The module names, so a test says which installation it is about rather than
// repeating a list.
const (
	modCustomers = "customers"
	modProjects  = "projects"
	modTime      = "time"
	modExpenses  = "expenses"
	modInvoices  = "invoices"
)

// moduleNamed is the real module value for one name. It is the only place this
// package maps a name to a module, so an installation is a list of strings
// everywhere else.
func moduleNamed(t *testing.T, name string) module.Module {
	t.Helper()
	switch name {
	case modCustomers:
		return customers.Module()
	case modProjects:
		return projects.Module()
	case modTime:
		return timetracking.Module()
	case modExpenses:
		return expenses.Module()
	case modInvoices:
		return invoices.Module()
	}
	t.Fatalf("integration: no module named %q", name)
	return module.Module{}
}

// anonymisationWorker is customers' anonymisation worker as worker mode
// builds it: module.Workers over the named modules, so every module among
// them that holds personal data has its slot in the worker's run.
func anonymisationWorker(t *testing.T, h *modtest.Harness, names ...string) *customers.AnonymisationWorker {
	t.Helper()
	mods := make([]module.Module, 0, len(names))
	for _, name := range names {
		mods = append(mods, moduleNamed(t, name))
	}
	for _, w := range module.Workers(h.Deps(), mods...) {
		if aw, ok := w.(*customers.AnonymisationWorker); ok {
			return aw
		}
	}
	t.Fatal("module.Workers built no anonymisation worker")
	return nil
}

// newInstallation composes exactly the named modules, in the order given, the
// way an operator's MODULES list would. modtest derives MODULES from the
// modules it is handed, so "expenses is enabled" here means the real thing:
// enabledModules filtered it in, Compose resolved its provider slot, and
// Deps.Expenses is the provider rather than a preset. The same holds for
// customers: composed, its directory replaces the fake preset below (Compose
// overwrites Deps.Directory whenever a composed module provides one).
func newInstallation(t *testing.T, names ...string) *modtest.Harness {
	t.Helper()
	return newInstallationWith(t, nil, names...)
}

// newInstallationWith is newInstallation with the options an installation
// needs beyond its modules — an object store, the SMTP seam, an environment
// variable — applied after the shared ones, so one of them may replace a
// shared one.
func newInstallationWith(t *testing.T, opts []modtest.Option, names ...string) *modtest.Harness {
	t.Helper()
	all := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithDirectory(fakeCustomers{}),
	}
	all = append(all, opts...)
	for _, name := range names {
		all = append(all, modtest.WithModule(moduleNamed(t, name)))
	}
	return modtest.New(t, all...)
}

// withStorecove starts a test Storecove for t and answers it with the
// options that point an installation's access point at it, as the invoices
// suite's own harness does: INVOICES_STORECOVE_BASE_URL set to its URL and
// Deps.HTTPTransport trusting its TLS listener, so the invoices module's own
// Storecove adapter — built by the EHF workers from the stored credentials —
// reaches it and never the real provider. The transport is the
// installation's only one: customers' Brreg client would reach the test
// server too, which the tests that take these options never ask it to.
func withStorecove(t *testing.T) (*storecovetest.Server, []modtest.Option) {
	t.Helper()
	storecove := storecovetest.New(t)
	return storecove, []modtest.Option{
		modtest.WithEnv("INVOICES_STORECOVE_BASE_URL", storecove.URL()),
		modtest.WithTransport(storecove.Transport()),
	}
}

// recorder validates every exchange of every test here against every
// composed module's contract at once, so a request to any of the modules is checked against the
// contract that declares it.
//
// It is one recorder over a merged document rather than one per module
// because modtest installs a single transport: openapi.Validate routes an
// exchange by path through the document it is given, and the modules' paths
// are disjoint (/api/v1/customers, /api/v1/projects, /api/v1/time,
// /api/v1/expenses, /api/v1/invoices), so a
// document holding all of them routes each exchange to its own operation. The
// documents are already fully resolved when Load returns, so a path item
// carries its schemas with it and nothing is lost in the merge.
//
// There is no coverage gate here, deliberately: coverage is each module's own
// TestMain's job over its own contract, and a cross-module test that counted
// towards it would let an operation look exercised without its module's suite
// ever having exercised it.
var recorder = mergedRecorder(modCustomers, modProjects, modTime, modExpenses, modInvoices)

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
// tests need, for the harnesses that do not compose the real customers module
// (rates_test.go does, to price a time entry from a real billing profile):
// projects refuses a customer id it cannot resolve, so something has to
// answer for it.
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

// The invoicing-work installation (invoices work design D1, D3): customers,
// projects, time, expenses and invoices — every one real — so the holders an
// issue stamps through and the billable reads its view and wizard read are
// the source modules' own, resolved by Compose as cmd/vantigo resolves them,
// and the merge reaches every module's CustomerReferenceHolder in Compose's
// order. It is invoicesInstallation's installation (a real object store, the
// seller saved complete and VAT-registered) over all five modules.

// workPermissions are what the one caller of a work installation holds
// beyond customers' and invoices': it creates and manages the projects (their
// manager), approves the hours, records, prices, approves and reimburses the
// expenses, and reads every figure.
var workPermissions = []string{
	"projects:access", "projects:create", "projects:manage-all", "projects:view-all", "projects:view-financials",
	"time:access", "time:approve", "time:manage",
	"expenses:access", "expenses:approve", "expenses:manage",
}

// workInstallation is the five modules composed in the binary's order, the
// caller answered as Kari Nordmann, the seller complete. opts are applied
// after the installation's own, so one of them — modtest.WithPoolMaxConns,
// the races' — may size what these build.
func workInstallation(t *testing.T, opts ...modtest.Option) (*modtest.Harness, *modtest.Client, uuid.UUID) {
	t.Helper()
	h, admin, adminID, _ := installInvoices(t,
		[]string{modCustomers, modProjects, modTime, modExpenses, modInvoices}, workPermissions, opts...)
	h.Exec(t, `UPDATE identity.users SET display_name = 'Kari Nordmann' WHERE id = $1`, adminID)
	return h, admin, adminID
}

// workProject is the part of ProjectResponse the work tests read.
type workProject struct {
	Id       int32  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Revision int32  `json:"revision"`
}

// newWorkProject creates an active project for customer through the real
// POST /api/v1/projects, body's fields over a NOK time-and-materials project
// billing 1 200 an hour by default, the caller its manager.
func newWorkProject(t *testing.T, c *modtest.Client, customer int32, code string, body map[string]any) workProject {
	t.Helper()
	req := map[string]any{
		"code": code, "name": "Prosjekt " + code, "customerId": customer,
		"billingType": "time-and-materials", "currency": "NOK", "defaultBillRate": 1200,
	}
	for k, v := range body {
		req[k] = v
	}
	var p workProject
	okJSON(t, c, http.MethodPost, projectsPath, req, &p)
	okJSON(t, c, http.MethodPut, fmt.Sprintf("%s/%d/status", projectsPath, p.Id), map[string]any{"status": "active"}, nil)
	return p
}

// workHour is the part of TimeEntryResponse the work tests read.
type workHour struct {
	Id         int64   `json:"id"`
	Status     string  `json:"status"`
	Revision   int32   `json:"revision"`
	Hours      float64 `json:"hours"`
	InvoicedAt *string `json:"invoicedAt"`
	InvoicedBy *struct {
		InvoiceId int64 `json:"invoiceId"`
		Number    int64 `json:"number"`
	} `json:"invoicedBy"`
}

const timeEntries = "/api/v1/time/entries"

// approvedHours logs hours on project as who on date, submits them as who
// and approves them as approver — the project's manager — answering the
// entry as approved.
func approvedHours(t *testing.T, who, approver *modtest.Client, project int32, date string, hours float64) workHour {
	t.Helper()
	var e workHour
	okJSON(t, who, http.MethodPost, timeEntries, map[string]any{"projectId": project, "entryDate": date, "hours": hours}, &e)
	okJSON(t, who, http.MethodPost, timeEntries+"/submit", map[string]any{"ids": []int64{e.Id}}, nil)
	var approved []workHour
	okJSON(t, approver, http.MethodPost, timeEntries+"/approve", map[string]any{"ids": []int64{e.Id}}, &approved)
	if len(approved) != 1 || approved[0].Status != "approved" {
		t.Fatalf("approve entry %d = %+v, want it approved", e.Id, approved)
	}
	return approved[0]
}

// readHour is one time entry as it now stands.
func readHour(t *testing.T, c *modtest.Client, id int64) workHour {
	t.Helper()
	var e workHour
	okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/%d", timeEntries, id), nil, &e)
	return e
}

// workExpense is the part of ExpensesEntryResponse the work tests read: the
// invoice stamp under billing, which only a financial viewer sees.
type workExpense struct {
	Id            int64  `json:"id"`
	Revision      int32  `json:"revision"`
	Status        string `json:"status"`
	Reimbursement *struct {
		Date string `json:"date"`
	} `json:"reimbursement"`
	Billing *struct {
		BillAmount float64 `json:"billAmount"`
		Invoice    *struct {
			Reference  *string `json:"reference"`
			InvoicedBy *struct {
				InvoiceId int64 `json:"invoiceId"`
				Number    int64 `json:"number"`
			} `json:"invoicedBy"`
		} `json:"invoice"`
	} `json:"billing"`
}

// rebillableExpense records an employee-paid outlay of gross on project,
// billable with a 10 % markup, and has it submitted and approved — ready to
// invoice, and owed to the employee.
func rebillableExpense(t *testing.T, c *modtest.Client, project int32, date string, gross float64) workExpense {
	t.Helper()
	var e workExpense
	okJSON(t, c, http.MethodPost, expensesEntries, map[string]any{
		"kind": "outlay", "categoryId": materialsCategory, "entryDate": date, "description": "Hotell Bergen",
		"paidBy": "employee", "currency": "NOK", "grossAmount": gross, "vatAmount": gross / 5,
		"projectId": project, "billable": true, "markupPercent": 10.0,
	}, &e)
	move(t, c, expensesSubmit, map[string]any{"entryIds": []int64{e.Id}})
	move(t, c, expensesApprove, map[string]any{"entryIds": []int64{e.Id}})
	return readExpense(t, c, e.Id)
}

// readExpense is one expense as it now stands.
func readExpense(t *testing.T, c *modtest.Client, id int64) workExpense {
	t.Helper()
	var e workExpense
	okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/%d", expensesEntries, id), nil, &e)
	return e
}

// workMilestone is the part of BillingMilestoneResponse the work tests read.
type workMilestone struct {
	Id                int64    `json:"id"`
	Status            string   `json:"status"`
	Revision          int32    `json:"revision"`
	Amount            *float64 `json:"amount"`
	Percent           *float64 `json:"percent"`
	EffectiveAmount   *float64 `json:"effectiveAmount"`
	InvoicedByInvoice *struct {
		InvoiceId int64 `json:"invoiceId"`
		Number    int64 `json:"number"`
	} `json:"invoicedByInvoice"`
}

// newMilestone adds a billing milestone to project — body is its amount or
// percent — and moves it to ready.
func newMilestone(t *testing.T, c *modtest.Client, project int32, name string, body map[string]any) workMilestone {
	t.Helper()
	req := map[string]any{"name": name}
	for k, v := range body {
		req[k] = v
	}
	var m workMilestone
	okJSON(t, c, http.MethodPost, fmt.Sprintf("%s/%d/milestones", projectsPath, project), req, &m)
	return moveMilestone(t, c, m, "ready")
}

// moveMilestone moves m to status at its revision.
func moveMilestone(t *testing.T, c *modtest.Client, m workMilestone, status string) workMilestone {
	t.Helper()
	var moved workMilestone
	okJSON(t, c, http.MethodPost, fmt.Sprintf("%s/milestones/%d/status", projectsPath, m.Id),
		map[string]any{"status": status, "revision": m.Revision}, &moved)
	return moved
}

// readMilestone is one of project's milestones as it now stands.
func readMilestone(t *testing.T, c *modtest.Client, project int32, id int64) workMilestone {
	t.Helper()
	var plan struct {
		Milestones []workMilestone `json:"milestones"`
	}
	okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/%d/milestones", projectsPath, project), nil, &plan)
	for _, m := range plan.Milestones {
		if m.Id == id {
			return m
		}
	}
	t.Fatalf("project %d has no milestone %d", project, id)
	return workMilestone{}
}
