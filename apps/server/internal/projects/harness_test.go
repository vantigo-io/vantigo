package projects_test

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// newHarness is one projects installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against projects.yaml through the package recorder. It stubs
// Deps.Directory and Deps.Products with fakes — depguard forbids
// internal/projects/** from importing internal/customers or
// internal/products, even in tests, so both are built directly against
// internal/contracts, the same seam energy uses for customers. The user
// directory is not stubbed: identity is always composed, and it provides the
// real one.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, newFakeCatalog(), opts...)
}

// newHarnessWithoutProducts is newHarness with the products module off:
// Deps.Products stays nil, which is the optional contract's absent case
// (D10) and what billingLinesAvailable answers false for.
func newHarnessWithoutProducts(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, nil, opts...)
}

// newHarnessWithCatalog is newHarness over a catalog the test keeps a handle
// on, for a case whose subject is the catalog changing under a stored line —
// a variant products drops after a line was pinned to it.
func newHarnessWithCatalog(t *testing.T, catalog *fakeCatalog, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, catalog, opts...)
}

// newProjectsHarness is the one composition every harness here is: the
// catalog is the only thing they differ in, nil for "products disabled", so
// it is the only thing any of them says. A second copy of the option list
// would drift the moment this module grows another dependency.
func newProjectsHarness(t *testing.T, catalog *fakeCatalog, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(projects.Module()),
		modtest.WithDirectory(fakeDirectory{}),
	}
	if catalog != nil {
		base = append(base, modtest.WithProducts(catalog))
	}
	return modtest.New(t, append(base, opts...)...)
}

// The customers the fake directory knows. 1003 is archived, which resolves
// and is allowed: a project can outlive the relationship that started it
// (design §4.1).
const (
	customerKraftVerket     = 1001
	customerAcme            = 1002
	customerArchived        = 1003
	customerUnknown         = 9999
	customerKraftVerketName = "Kraft-Verket"
	customerAcmeName        = "Acme Industrier AS"
	customerArchivedName    = "Nedlagt Handel AS"
)

// fakeDirectory is contracts.CustomerDirectory over three customers.
// Everything else does not exist, which is what makes the unknown-customer
// validation rule testable without composing customers beside this module.
// Contact and ContactsByEmail satisfy the interface and are never exercised
// here: projects never resolves a contact.
type fakeDirectory struct{}

var _ contracts.CustomerDirectory = fakeDirectory{}

func (fakeDirectory) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	switch id {
	case customerKraftVerket:
		return &contracts.CustomerEntry{ID: id, Name: customerKraftVerketName}, nil
	case customerAcme:
		return &contracts.CustomerEntry{ID: id, Name: customerAcmeName}, nil
	case customerArchived:
		return &contracts.CustomerEntry{ID: id, Name: customerArchivedName, Archived: true}, nil
	default:
		return nil, nil
	}
}

func (fakeDirectory) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (fakeDirectory) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return nil, nil
}

// fakeCatalog is contracts.ProductCatalog over two service variants, the two
// a project bills hours against. It is what makes "products enabled" a real
// composition rather than a flag: the billing lines read it for the product
// name, SKU and unit they embed (D15) and for their list price.
//
// A variant it does not know is (nil, nil) and absent from Variants, never an
// error, which is what a line whose variant products has since dropped is
// rendered from. Its list prices are in NOK only, so a project in another
// currency exercises the "priced variant, no price in this currency" case
// without a second fixture.
type fakeCatalog struct {
	variants map[int32]contracts.VariantEntry
	prices   map[int32]float64
}

var _ contracts.ProductCatalog = (*fakeCatalog)(nil)

const (
	variantProjectManagerHour = 2001
	variantDeveloperHour      = 2002
	variantUnknown            = 2999
	catalogCurrency           = "NOK"
)

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{
		variants: map[int32]contracts.VariantEntry{
			variantProjectManagerHour: {
				ID: variantProjectManagerHour, ProductID: 3001, ProductName: "Project manager hour",
				SKU: "PM-H", Unit: "hour", ProductType: "Service", ProductStatus: "Active",
			},
			variantDeveloperHour: {
				ID: variantDeveloperHour, ProductID: 3002, ProductName: "Developer hour",
				SKU: "DEV-H", Unit: "hour", ProductType: "Service", ProductStatus: "Active",
			},
		},
		prices: map[int32]float64{
			variantProjectManagerHour: 1600,
			variantDeveloperHour:      1250,
		},
	}
}

// forget drops a variant the way products dropping it would look from here:
// the catalog stops knowing it, while the lines already pinned to it stay in
// the table. It is not concurrency-safe, so a test that calls it drives its
// own harness (newHarnessWithCatalog) rather than sharing one.
func (c *fakeCatalog) forget(id int32) {
	delete(c.variants, id)
	delete(c.prices, id)
}

func (c *fakeCatalog) Variant(_ context.Context, id int32) (*contracts.VariantEntry, error) {
	v, ok := c.variants[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (c *fakeCatalog) Variants(_ context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	out := make([]contracts.VariantEntry, 0, len(ids))
	for _, id := range ids {
		if v, ok := c.variants[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (c *fakeCatalog) ListPrice(_ context.Context, variantID int32, currency string, _ time.Time) (*contracts.Money, error) {
	price, ok := c.prices[variantID]
	if !ok || currency != catalogCurrency {
		return nil, nil
	}
	return &contracts.Money{Amount: price, Currency: currency}, nil
}

// signIn seeds a caller holding projects:access plus whatever else the test
// needs, and returns their client and user id. Every operation requires
// projects:access (D8), so adding it here keeps each test's permission list
// to the thing it is actually about.
func signIn(t *testing.T, h *modtest.Harness, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, append([]string{"projects:access"}, permissions...)...)
}

// createBody is a valid minimal create body, which tests override one field
// of at a time. Kraft-Verket is the customer, so the default project is a
// customer project rather than an internal one.
func createBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"code":        "KVEM1000",
		"name":        "Kraft-Verket modernisering",
		"customerId":  customerKraftVerket,
		"billingType": "time-and-materials",
	}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

// addRole assigns one user one role on one project directly, because the
// endpoints that assign roles are Task 7's: every test whose subject is what
// a member or a viewer may do needs the assignment to exist first, and the
// insert is the same one InsertProjectRole makes.
func addRole(t *testing.T, h *modtest.Harness, projectID int32, userID uuid.UUID, role string) {
	t.Helper()
	h.Exec(t, `INSERT INTO projects.project_roles (project_id, user_id, role, created_at) VALUES ($1, $2, $3, now())`,
		projectID, userID, role)
}

// milestoneNumericColumns is the subset of insertMilestone's columns that
// are numeric, cast explicitly in the built INSERT: a raw Go float64 or int
// argument otherwise arrives typed float8/int4 over the wire, and Postgres
// only casts that to a numeric(_,_) column on an explicit cast, not an
// assignment one.
var milestoneNumericColumns = map[string]bool{"amount": true, "percent": true, "invoiced_amount": true}

// insertMilestone inserts one projects.billing_milestones row directly, at
// the SQL level: milestones.go, the CRUD that would otherwise create one, is
// Task 2's, and the guards Task 1 adds (design §3.3's currency guard extended
// to milestones, and the fixed-price guard) need real milestone rows to
// decide against before that API exists. Task 2 reuses it for the same
// reason billing lines' own tests keep insertLineRow around after the POST
// exists: the two rows that operation cannot produce.
//
// overrides is a column name to value map, layered over a valid minimal
// default (one open, amount-priced, 'planned' milestone at position 1); a nil
// value in overrides removes that column from the insert, leaving the
// column's own default (or SQL NULL) to stand — overrides set exactly what
// they name, and nothing else. The one exception is deliberate, not
// incidental: setting "percent" to an actual value, without also naming
// "amount", drops the default amount, since a real milestone never carries
// both (design §3.2) — the DB itself does not enforce that; only Go's rules
// do (there are no CHECK constraints here, house style). Setting "percent"
// to nil (asking for a milestone with neither) does not trigger that — nil
// always means "leave this column out", never "and drop something else too".
func insertMilestone(t *testing.T, h *modtest.Harness, projectID int32, overrides map[string]any) int32 {
	t.Helper()
	now := time.Now().UTC()
	row := map[string]any{
		"project_id":         projectID,
		"name":               "Milestone",
		"amount":             1000.00,
		"status":             "planned",
		"position":           int32(1),
		"created_by_user_id": uuid.New(),
		"created_at":         now,
		"updated_at":         now,
	}
	if v, ok := overrides["percent"]; ok && v != nil {
		if _, keepingAmount := overrides["amount"]; !keepingAmount {
			delete(row, "amount")
		}
	}
	for col, v := range overrides {
		if v == nil {
			delete(row, col)
			continue
		}
		row[col] = v
	}

	cols := make([]string, 0, len(row))
	for col := range row {
		cols = append(cols, col)
	}
	sort.Strings(cols)

	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, col := range cols {
		placeholder := fmt.Sprintf("$%d", i+1)
		if milestoneNumericColumns[col] {
			placeholder += "::numeric"
		}
		placeholders[i] = placeholder
		args[i] = row[col]
	}
	query := fmt.Sprintf("INSERT INTO projects.billing_milestones (%s) VALUES (%s) RETURNING id",
		strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	return modtest.One[int32](t, h, query, args...)
}

// createProject creates a project with createBody(overrides) and fails the
// test if it is not created. A nil override value removes that field, which
// is how a test builds an internal project (no customerId).
func createProject(t *testing.T, c *modtest.Client, overrides map[string]any) projectJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/projects", createBody(overrides))
	if r.Status != http.StatusCreated {
		t.Fatalf("create project: status %d body %s, want 201", r.Status, r.Body)
	}
	var project projectJSON
	r.JSON(&project)
	return project
}

// projectJSON decodes ProjectResponse. financials is a pointer because its
// absence is the whole of D12: a test that must prove the key is not there
// decodes into a map instead (rawJSON), since a nil pointer cannot tell
// "absent" from "null".
type projectJSON struct {
	Id                    int32            `json:"id"`
	Code                  string           `json:"code"`
	Name                  string           `json:"name"`
	Description           *string          `json:"description"`
	CustomerId            *int32           `json:"customerId"`
	CustomerName          *string          `json:"customerName"`
	Internal              bool             `json:"internal"`
	Status                string           `json:"status"`
	StartDate             *string          `json:"startDate"`
	EndDate               *string          `json:"endDate"`
	BillingType           string           `json:"billingType"`
	BudgetHours           *float64         `json:"budgetHours"`
	Revision              int32            `json:"revision"`
	CreatedAt             time.Time        `json:"createdAt"`
	UpdatedAt             time.Time        `json:"updatedAt"`
	Managers              []personJSON     `json:"managers"`
	Capabilities          capabilitiesJSON `json:"capabilities"`
	BillingLinesAvailable bool             `json:"billingLinesAvailable"`
	Financials            *financialsJSON  `json:"financials"`
}

type personJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
}

type capabilitiesJSON struct {
	CanManage        bool `json:"canManage"`
	CanContribute    bool `json:"canContribute"`
	CanSeeFinancials bool `json:"canSeeFinancials"`
}

type financialsJSON struct {
	Currency         *string  `json:"currency"`
	FixedPriceAmount *float64 `json:"fixedPriceAmount"`
	BudgetAmount     *float64 `json:"budgetAmount"`
	DefaultBillRate  *float64 `json:"defaultBillRate"`
}

// taskJSON decodes TaskResponse and, since it is that shape plus the two
// project fields, MyTaskResponse as well: one decoder, so a test that reads a
// task through the tree and through my-tasks compares like with like.
// assignee is a pointer because an unassigned task has no key at all.
type taskJSON struct {
	Id            int32         `json:"id"`
	ProjectId     int32         `json:"projectId"`
	ParentTaskId  *int32        `json:"parentTaskId"`
	Title         string        `json:"title"`
	Description   *string       `json:"description"`
	Status        string        `json:"status"`
	Assignee      *assigneeJSON `json:"assignee"`
	StartDate     *string       `json:"startDate"`
	DueDate       *string       `json:"dueDate"`
	EstimateHours *float64      `json:"estimateHours"`
	Position      int32         `json:"position"`
	CompletedAt   *time.Time    `json:"completedAt"`
	Revision      int32         `json:"revision"`
	CreatedAt     time.Time     `json:"createdAt"`
	UpdatedAt     time.Time     `json:"updatedAt"`
	Checklist     checklistJSON `json:"checklist"`
	CommentCount  int32         `json:"commentCount"`
	Subtasks      []taskJSON    `json:"subtasks"`
	ProjectCode   string        `json:"projectCode"`
	ProjectName   string        `json:"projectName"`
}

// assigneeJSON decodes TaskAssignee. active is the user directory's answer,
// exactly as a role assignment's is: an account disabled after the task was
// assigned keeps the assignment and renders inactive.
type assigneeJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	Active      bool      `json:"active"`
}

// checklistJSON decodes a task's checklist progress, which is a pair of
// counts rather than the items themselves: the tree renders progress, the
// drawer reads the items (Task 3).
type checklistJSON struct {
	Total int32 `json:"total"`
	Done  int32 `json:"done"`
}

// tasksPath is a project's task collection; taskPath is one task, which is
// addressed without its project because a task id already names one.
func tasksPath(projectID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/tasks", projectID)
}

func taskPath(taskID int32) string {
	return fmt.Sprintf("/api/v1/projects/tasks/%d", taskID)
}

// taskBody is a valid minimal create body — nothing but a title, since that
// is the only required field — which tests override one field of at a time. A
// nil override value removes that field, the same convention createBody uses.
func taskBody(overrides map[string]any) map[string]any {
	body := map[string]any{"title": "Skriv spesifikasjonen"}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

// createTask creates one task and fails the test unless it was created.
func createTask(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) taskJSON {
	t.Helper()
	r := c.Do(http.MethodPost, tasksPath(projectID), taskBody(overrides))
	if r.Status != http.StatusCreated {
		t.Fatalf("create task: status %d body %s, want 201", r.Status, r.Body)
	}
	var task taskJSON
	r.JSON(&task)
	return task
}

// getTasks reads a project's task tree, failing the test unless it answered
// 200. The order is the endpoint's: top-level tasks by position, each with
// its subtasks by position.
func getTasks(t *testing.T, c *modtest.Client, projectID int32) []taskJSON {
	t.Helper()
	r := c.Do(http.MethodGet, tasksPath(projectID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list tasks: status %d body %s, want 200", r.Status, r.Body)
	}
	var tasks []taskJSON
	r.JSON(&tasks)
	return tasks
}

// validationProblemJSON decodes the field-error body every §4.1 refusal
// answers with.
type validationProblemJSON struct {
	Title  string              `json:"title"`
	Errors map[string][]string `json:"errors"`
}

// problemJSON decodes the bare RFC 7807 body the two refusals that carry no
// field map answer with: a list's invalid query parameters and an update's
// stale revision.
type problemJSON struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int32  `json:"status"`
}

// paginationJSON decodes common.yaml's PaginationMetadata, shared by the
// project list and the timeline.
type paginationJSON struct {
	Page            int32 `json:"page"`
	PageSize        int32 `json:"pageSize"`
	TotalCount      int32 `json:"totalCount"`
	TotalPages      int32 `json:"totalPages"`
	HasNextPage     bool  `json:"hasNextPage"`
	HasPreviousPage bool  `json:"hasPreviousPage"`
}
