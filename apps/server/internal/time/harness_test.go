package timetracking_test

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
)

// harness is one time installation for one test: internal/modtest's shared
// harness composing this module beside identity, with every exchange
// validated against time.yaml through the package recorder, plus the fake
// project directory it was composed with, so a test can give a user a role
// or take a task away.
//
// Deps.Projects, Deps.Products and Deps.Directory are fakes built directly
// against internal/contracts — depguard forbids internal/time/** from
// importing internal/projects, internal/products or internal/customers, even
// in tests. The user directory is not stubbed: identity is always composed,
// and it provides the real one, which names every user signIn seeds.
type harness struct {
	*modtest.Harness
	projects  *fakeProjects
	customers *fakeCustomers
}

// newHarness is a time installation with products enabled: list and
// discount billing lines price through the fake catalog.
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	return newTimeHarness(t, newFakeCatalog(), opts...)
}

// newHarnessWithoutProducts is newHarness with the products module off:
// Deps.Products stays nil, the optional contract's absent case, which the
// rate chain reads as "a list or discount line prices nothing" (D4).
func newHarnessWithoutProducts(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	return newTimeHarness(t, nil, opts...)
}

// newTimeHarness is the one composition every harness here is: the catalog
// is the only thing they differ in, nil for "products disabled".
func newTimeHarness(t *testing.T, catalog *fakeCatalog, opts ...modtest.Option) *harness {
	t.Helper()
	projects := newFakeProjects()
	customers := newFakeCustomers()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(timetracking.Module()),
		modtest.WithProjects(projects),
		modtest.WithDirectory(customers),
	}
	if catalog != nil {
		base = append(base, modtest.WithProducts(catalog))
	}
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), projects: projects, customers: customers}
	t.Cleanup(func() {
		if calls := projects.locked.all(); len(calls) > 0 {
			t.Errorf("project directory called inside a locked transaction: %v", calls)
		}
		if calls := customers.locked.all(); len(calls) > 0 {
			t.Errorf("customer directory called inside a locked transaction: %v", calls)
		}
		if catalog != nil {
			if calls := catalog.locked.all(); len(calls) > 0 {
				t.Errorf("product catalog called inside a locked transaction: %v", calls)
			}
		}
	})
	return h
}

// lockedCalls records every directory call a fake answered from inside one of
// the module's locked transactions (timetracking.InLockedTx). The directories
// read through the same connection pool as the module, so a transaction
// holding row locks while it waits for a directory's connection can starve
// the pool under load; the rule is that none ever does, and every harness
// checks it when its test ends (newTimeHarness). A real pool of a size small
// enough to starve cannot be had here — modtest's pools are a fixed
// testdb.PoolMaxConns, and the fakes use no connection at all — so the call
// itself is what is caught.
type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if !timetracking.InLockedTx(ctx) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, method)
}

func (l *lockedCalls) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

// The projects the fake directory knows (design §4.2's cases):
//   - 1001 is the ordinary customer project: active, time and materials, in
//     NOK, with a default bill rate of 900;
//   - 1002 is active and non-billable, so billable is forced false on it;
//   - 1003 is completed, so nobody may log time on it;
//   - 1004 is active, in EUR, with no default bill rate, so an entry without
//     a line falls through to the person's rate — when the person's card is
//     in EUR too;
//   - 1005 is active and fixed-price, in NOK, which is billable by default;
//   - 1006 is active, time and materials, with no currency at all, so a
//     person-priced entry takes the card's currency.
const (
	projectKraftVerket = 1001
	projectInternal    = 1002
	projectCompleted   = 1003
	projectEuro        = 1004
	projectFixedPrice  = 1005
	projectNoCurrency  = 1006
	projectUnknown     = 9999

	projectKraftVerketCode = "KVEM1000"
	projectKraftVerketName = "Kraft-Verket modernisering"
)

// The billing lines the fake directory knows. 3001–3004 are on 1001: a fixed
// 1500, a list line and a 10 % discount line on variant 2001 (1600 NOK in the
// catalog), and an inactive fixed line. 3005 is a list line on 1004, which is
// both "a line on another project" for 1001 and a list line priced in EUR.
const (
	lineFixed    = 3001
	lineList     = 3002
	lineInactive = 3003
	lineDiscount = 3004
	lineEuroList = 3005

	variantProjectManagerHour = 2001
)

// The tasks the fake directory knows: two on 1001 and one on 1004.
const (
	taskSpecification = 5001
	taskDelivery      = 5002
	taskForeign       = 5003

	taskSpecificationTitle = "Skriv spesifikasjonen"
)

// The roles a project directory answers, as projects names them.
const (
	roleManager = "manager"
	roleMember  = "member"
	roleViewer  = "viewer"
)

// fakeProjects is contracts.ProjectDirectory over the fixtures above. Roles
// start empty and are given per test (addRole); a task can be taken away
// (removeTask), the way projects deleting it looks from here. It is safe for
// concurrent use, because the handlers of one harness read it from many
// requests at once.
type fakeProjects struct {
	mu        sync.Mutex
	projects  map[int32]contracts.ProjectEntry
	lines     map[int32]contracts.BillingLineEntry
	tasks     map[int32]contracts.TaskEntry
	workTypes map[int32]contracts.WorkTypeEntry
	roles     map[roleKey]string
	locked    lockedCalls
}

type roleKey struct {
	projectID int32
	userID    uuid.UUID
}

var _ contracts.ProjectDirectory = (*fakeProjects)(nil)

func ptr[T any](v T) *T { return &v }

func newFakeProjects() *fakeProjects {
	customer := int32(1001)
	return &fakeProjects{
		projects: map[int32]contracts.ProjectEntry{
			projectKraftVerket: {
				ID: projectKraftVerket, Code: projectKraftVerketCode, Name: projectKraftVerketName,
				CustomerID: &customer, Status: "active", OpenForWork: true, BillingType: "time-and-materials",
				Currency: ptr("NOK"), DefaultBillRate: ptr(900.0),
			},
			projectInternal: {
				ID: projectInternal, Code: "INTERN", Name: "Internt arbeid",
				Status: "active", OpenForWork: true, BillingType: "non-billable",
			},
			projectCompleted: {
				ID: projectCompleted, Code: "FERDIG01", Name: "Avsluttet prosjekt",
				CustomerID: &customer, Status: "completed", OpenForWork: false, BillingType: "time-and-materials",
				Currency: ptr("NOK"), DefaultBillRate: ptr(900.0),
			},
			projectEuro: {
				ID: projectEuro, Code: "EURO2026", Name: "Euro-prosjektet",
				CustomerID: &customer, Status: "active", OpenForWork: true, BillingType: "time-and-materials",
				Currency: ptr("EUR"),
			},
			projectFixedPrice: {
				ID: projectFixedPrice, Code: "FAST2026", Name: "Fastprisprosjektet",
				CustomerID: &customer, Status: "active", OpenForWork: true, BillingType: "fixed-price",
				Currency: ptr("NOK"),
			},
			projectNoCurrency: {
				ID: projectNoCurrency, Code: "UTENVAL", Name: "Prosjekt uten valuta",
				CustomerID: &customer, Status: "active", OpenForWork: true, BillingType: "time-and-materials",
			},
		},
		lines: map[int32]contracts.BillingLineEntry{
			lineFixed: {
				ID: lineFixed, ProjectID: projectKraftVerket, Code: "PM", VariantID: variantProjectManagerHour,
				PricingMode: "fixed", FixedAmount: ptr(1500.0), Active: true,
			},
			lineList: {
				ID: lineList, ProjectID: projectKraftVerket, Code: "DEV", VariantID: variantProjectManagerHour,
				PricingMode: "list", Active: true,
			},
			lineInactive: {
				ID: lineInactive, ProjectID: projectKraftVerket, Code: "OLD", VariantID: variantProjectManagerHour,
				PricingMode: "fixed", FixedAmount: ptr(1200.0), Active: false,
			},
			lineDiscount: {
				ID: lineDiscount, ProjectID: projectKraftVerket, Code: "RAB", VariantID: variantProjectManagerHour,
				PricingMode: "discount", DiscountPercent: ptr(10.0), Active: true,
			},
			lineEuroList: {
				ID: lineEuroList, ProjectID: projectEuro, Code: "EU", VariantID: variantProjectManagerHour,
				PricingMode: "list", Active: true,
			},
		},
		tasks: map[int32]contracts.TaskEntry{
			taskSpecification: {ID: taskSpecification, ProjectID: projectKraftVerket, Title: taskSpecificationTitle, Status: "todo"},
			taskDelivery:      {ID: taskDelivery, ProjectID: projectKraftVerket, Title: "Test leveransen", Status: "in-progress"},
			taskForeign:       {ID: taskForeign, ProjectID: projectEuro, Title: "Oversett rapporten", Status: "todo"},
		},
		workTypes: map[int32]contracts.WorkTypeEntry{},
		roles:     map[roleKey]string{},
	}
}

// noteLocked records a directory call made from inside a locked transaction
// (see lockedCalls). f.mu is held.
func (f *fakeProjects) noteLocked(ctx context.Context, method string) { f.locked.note(ctx, method) }

// addRole gives userID role on projectID, as assigning it in projects would.
func (f *fakeProjects) addRole(projectID int32, userID uuid.UUID, role string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.roles[roleKey{projectID, userID}] = role
}

// removeTask takes a task away, the way projects deleting it looks from
// here: the directory answers (nil, nil) for it from then on (D5).
func (f *fakeProjects) removeTask(id int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.tasks, id)
}

func (f *fakeProjects) Project(ctx context.Context, id int32) (*contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "Project")
	p, ok := f.projects[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *fakeProjects) Role(ctx context.Context, projectID int32, userID uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "Role")
	return f.roles[roleKey{projectID, userID}], nil
}

func (f *fakeProjects) BillingLine(ctx context.Context, projectID, lineID int32) (*contracts.BillingLineEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "BillingLine")
	l, ok := f.lines[lineID]
	if !ok || l.ProjectID != projectID {
		return nil, nil
	}
	return &l, nil
}

func (f *fakeProjects) ProjectsForUser(ctx context.Context, userID uuid.UUID) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "ProjectsForUser")
	var out []contracts.ProjectEntry
	for key := range f.roles {
		if key.userID == userID {
			out = append(out, f.projects[key.projectID])
		}
	}
	slices.SortFunc(out, func(a, b contracts.ProjectEntry) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeProjects) ProjectsForCustomer(ctx context.Context, customerID int32) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "ProjectsForCustomer")
	var out []contracts.ProjectEntry
	for _, p := range f.projects {
		if p.CustomerID != nil && *p.CustomerID == customerID {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b contracts.ProjectEntry) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeProjects) Projects(ctx context.Context, ids []int32) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "Projects")
	var out []contracts.ProjectEntry
	for _, id := range ids {
		if p, ok := f.projects[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeProjects) ProjectByCode(ctx context.Context, code string) (*contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "ProjectByCode")
	for _, p := range f.projects {
		if p.Code == code {
			return &p, nil
		}
	}
	return nil, nil
}

func (f *fakeProjects) BillingLines(ctx context.Context, projectID int32) ([]contracts.BillingLineEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "BillingLines")
	var out []contracts.BillingLineEntry
	for _, l := range f.lines {
		if l.ProjectID == projectID {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b contracts.BillingLineEntry) int {
		if a.Code < b.Code {
			return -1
		}
		if a.Code > b.Code {
			return 1
		}
		return 0
	})
	return out, nil
}

func (f *fakeProjects) Task(ctx context.Context, id int32) (*contracts.TaskEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "Task")
	task, ok := f.tasks[id]
	if !ok {
		return nil, nil
	}
	return &task, nil
}

func (f *fakeProjects) OpenTasksForUser(ctx context.Context, userID uuid.UUID) ([]contracts.TaskEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "OpenTasksForUser")
	var out []contracts.TaskEntry
	for _, task := range f.tasks {
		if task.AssigneeUserID != nil && *task.AssigneeUserID == userID && task.Status != "done" {
			out = append(out, task)
		}
	}
	return out, nil
}

// CanLogTime is projects' own rule: the project is open for work and the
// user holds the member or manager role on it.
func (f *fakeProjects) CanLogTime(ctx context.Context, projectID int32, userID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "CanLogTime")
	p, ok := f.projects[projectID]
	if !ok || !p.OpenForWork {
		return false, nil
	}
	role := f.roles[roleKey{projectID, userID}]
	return role == roleMember || role == roleManager, nil
}

// WorkType is projects' own answer: any type by id, active or not, (nil, nil)
// for one nobody has (work types design D2).
func (f *fakeProjects) WorkType(ctx context.Context, id int32) (*contracts.WorkTypeEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "WorkType")
	wt, ok := f.workTypes[id]
	if !ok {
		return nil, nil
	}
	return &wt, nil
}

// WorkTypes is the project's types in the directory's order: active first,
// each half by name without regard to case, then by id.
func (f *fakeProjects) WorkTypes(ctx context.Context, projectID int32) ([]contracts.WorkTypeEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLocked(ctx, "WorkTypes")
	var out []contracts.WorkTypeEntry
	for _, wt := range f.workTypes {
		if wt.ProjectID == projectID {
			out = append(out, wt)
		}
	}
	slices.SortFunc(out, func(a, b contracts.WorkTypeEntry) int {
		if a.Active != b.Active {
			if a.Active {
				return -1
			}
			return 1
		}
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// customerKraftVerket is the customer every fixture project but the internal
// one bills to (newFakeProjects' customer) — the id a test gives a default
// bill rate through h.customers.setBillRate.
const customerKraftVerket = 1001

// fakeCustomers is contracts.CustomerDirectory as the rate chain reads it
// (customers bill-rate design D3): a billing profile for each customer a test
// gave a default bill rate, (nil, nil) for every other id — so a harness
// nobody set a rate on prices exactly as it did before the customer step
// existed. The other four methods answer nothing; time never asks them. It
// records calls made inside a locked transaction, like the other fakes.
type fakeCustomers struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
	locked   lockedCalls
}

var _ contracts.CustomerDirectory = (*fakeCustomers)(nil)

func newFakeCustomers() *fakeCustomers {
	return &fakeCustomers{profiles: map[int32]contracts.CustomerBillingProfile{}}
}

// setBillRate gives customerID a default bill rate quoted in currency, as a
// PUT of its billing profile would.
func (f *fakeCustomers) setBillRate(customerID int32, rate float64, currency string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles[customerID] = contracts.CustomerBillingProfile{
		ID: customerID, Name: "Kraft-Verket AS", Type: "business", Currency: currency, DefaultBillRate: &rate,
	}
}

func (f *fakeCustomers) Customer(ctx context.Context, _ int32) (*contracts.CustomerEntry, error) {
	f.locked.note(ctx, "Customer")
	return nil, nil
}

func (f *fakeCustomers) Customers(ctx context.Context, _ []int32) ([]contracts.CustomerEntry, error) {
	f.locked.note(ctx, "Customers")
	return []contracts.CustomerEntry{}, nil
}

func (f *fakeCustomers) Contact(ctx context.Context, _ int32) (*contracts.ContactEntry, error) {
	f.locked.note(ctx, "Contact")
	return nil, nil
}

func (f *fakeCustomers) ContactsByEmail(ctx context.Context, _ string) ([]contracts.ContactMatch, error) {
	f.locked.note(ctx, "ContactsByEmail")
	return []contracts.ContactMatch{}, nil
}

func (f *fakeCustomers) BillingProfile(ctx context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.locked.note(ctx, "BillingProfile")
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// fakeCatalog is contracts.ProductCatalog over the one variant the billing
// lines are pinned to, priced 1600 in NOK and 1400 in EUR. A currency it has
// no price in is (nil, nil), never an error, which is the "no list price"
// case of the rate chain.
type fakeCatalog struct {
	prices map[string]float64
	locked lockedCalls
}

var _ contracts.ProductCatalog = (*fakeCatalog)(nil)

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{prices: map[string]float64{"NOK": 1600, "EUR": 1400}}
}

func (c *fakeCatalog) Variant(ctx context.Context, id int32) (*contracts.VariantEntry, error) {
	c.locked.note(ctx, "Variant")
	if id != variantProjectManagerHour {
		return nil, nil
	}
	return &contracts.VariantEntry{ID: id, ProductID: 4001, ProductName: "Prosjektledertime", SKU: "PM-H", Unit: "hour", ProductType: "Service", ProductStatus: "Active"}, nil
}

func (c *fakeCatalog) Variants(ctx context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	c.locked.note(ctx, "Variants")
	var out []contracts.VariantEntry
	for _, id := range ids {
		if v, _ := c.Variant(ctx, id); v != nil {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (c *fakeCatalog) ListPrice(ctx context.Context, variantID int32, currency string, _ time.Time) (*contracts.Money, error) {
	c.locked.note(ctx, "ListPrice")
	price, ok := c.prices[currency]
	if variantID != variantProjectManagerHour || !ok {
		return nil, nil
	}
	return &contracts.Money{Amount: price, Currency: currency}, nil
}

// signIn seeds a caller holding time:access plus whatever else the test
// needs, and returns their client and user id. Every operation requires
// time:access, so adding it here keeps each test's permission list to the
// thing it is actually about.
func signIn(t *testing.T, h *harness, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, append([]string{"time:access"}, permissions...)...)
}

// signInAs is signIn for a caller who also holds role on projectID.
func signInAs(t *testing.T, h *harness, projectID int32, role string, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, id := signIn(t, h, permissions...)
	h.projects.addRole(projectID, id, role)
	return c, id
}

// workDay is the Monday every entry is dated on unless a test says
// otherwise: the week of modtest.Start (Saturday 12 September 2026) is over,
// so the Monday after it is a plain working day.
const workDay = "2026-09-14"

// entryBody is a valid minimal create body — two hours on 1001, no line, no
// task — which tests override one field of at a time. A nil override value
// removes that field.
func entryBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"projectId": projectKraftVerket,
		"entryDate": workDay,
		"hours":     2,
	}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

const entriesPath = "/api/v1/time/entries"

func entryPath(id int64) string { return fmt.Sprintf("%s/%d", entriesPath, id) }

// createEntry creates an entry with entryBody(overrides) and fails the test
// unless it was created.
func createEntry(t *testing.T, c *modtest.Client, overrides map[string]any) entryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, entriesPath, entryBody(overrides))
	if r.Status != http.StatusCreated {
		t.Fatalf("create entry: status %d body %s, want 201", r.Status, r.Body)
	}
	var entry entryJSON
	r.JSON(&entry)
	return entry
}

// getEntry reads one entry and fails the test unless it answered 200.
func getEntry(t *testing.T, c *modtest.Client, id int64) entryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, entryPath(id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get entry %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	var entry entryJSON
	r.JSON(&entry)
	return entry
}

// seedRate writes one person rate card row directly, for a test whose subject
// is what a rate does rather than how one is kept (ratecards_test.go). bill
// and cost are a float64 or nil (no rate).
func seedRate(t *testing.T, h *harness, userID uuid.UUID, validFrom string, bill, cost any, currency string) {
	t.Helper()
	h.Exec(t, `INSERT INTO time.person_rates (user_id, valid_from, bill_rate, cost_rate, currency, created_at, updated_at)
	           VALUES ($1, $2::date, $3::numeric, $4::numeric, $5, now(), now())`,
		userID, validFrom, bill, cost, currency)
}

// setLock sets the period lock (D9) directly, for a test whose subject is
// what the lock holds back rather than how it is set (settings_test.go):
// entries dated before date are closed to everyone but time:manage.
func setLock(t *testing.T, h *harness, date string) {
	t.Helper()
	h.Exec(t, `INSERT INTO time.settings (key, value) VALUES ('locked_before', $1)
	           ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, date)
}

// setStatus moves an entry to status directly, for a test whose subject is
// what may be done with an entry in that status — invoiced above all, which
// no operation of this module leads to.
func setStatus(t *testing.T, h *harness, id int64, status string) {
	t.Helper()
	h.Exec(t, `UPDATE time.entries SET status = $2 WHERE id = $1`, id, status)
}

// entryJSON decodes TimeEntryResponse. billing and cost are pointers because
// their absence is the whole of D8's shaping: a test that must prove a key
// is not there decodes into a map instead (rawEntry), since a nil pointer
// cannot tell "absent" from "null".
type entryJSON struct {
	Id              int64            `json:"id"`
	UserId          uuid.UUID        `json:"userId"`
	UserDisplayName string           `json:"userDisplayName"`
	ProjectId       int32            `json:"projectId"`
	ProjectCode     string           `json:"projectCode"`
	ProjectName     string           `json:"projectName"`
	BillingLineId   *int32           `json:"billingLineId"`
	BillingLineCode *string          `json:"billingLineCode"`
	TrackableCode   *string          `json:"trackableCode"`
	TaskId          *int32           `json:"taskId"`
	TaskTitle       *string          `json:"taskTitle"`
	EntryDate       string           `json:"entryDate"`
	Hours           float64          `json:"hours"`
	StartTime       *string          `json:"startTime"`
	EndTime         *string          `json:"endTime"`
	Note            *string          `json:"note"`
	Billable        bool             `json:"billable"`
	RateSource      string           `json:"rateSource"`
	Status          string           `json:"status"`
	RejectionReason *string          `json:"rejectionReason"`
	SubmittedAt     *time.Time       `json:"submittedAt"`
	ApprovedAt      *time.Time       `json:"approvedAt"`
	ApprovedBy      *approverJSON    `json:"approvedBy"`
	Revision        int32            `json:"revision"`
	CreatedAt       time.Time        `json:"createdAt"`
	UpdatedAt       time.Time        `json:"updatedAt"`
	Capabilities    capabilitiesJSON `json:"capabilities"`
	Billing         *billingJSON     `json:"billing"`
	Cost            *costJSON        `json:"cost"`
}

type approverJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
}

type capabilitiesJSON struct {
	CanEdit      bool `json:"canEdit"`
	CanSubmit    bool `json:"canSubmit"`
	CanApprove   bool `json:"canApprove"`
	CanUnapprove bool `json:"canUnapprove"`
}

type billingJSON struct {
	BillRate *float64 `json:"billRate"`
	Currency *string  `json:"currency"`
}

type costJSON struct {
	CostRate *float64 `json:"costRate"`
	Currency *string  `json:"currency"`
}

// rawEntry reads one entry as a bare JSON object, for a test whose subject is
// whether a key is there at all.
func rawEntry(t *testing.T, c *modtest.Client, id int64) map[string]any {
	t.Helper()
	r := c.Do(http.MethodGet, entryPath(id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get entry %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	var raw map[string]any
	r.JSON(&raw)
	return raw
}

// validationProblemJSON decodes the field-error body every §4.2 refusal
// answers with.
type validationProblemJSON struct {
	Title  string              `json:"title"`
	Errors map[string][]string `json:"errors"`
}

// fieldErrors posts body and fails the test unless it was refused with a
// validation problem, answering the field errors.
func fieldErrors(t *testing.T, c *modtest.Client, body map[string]any) map[string][]string {
	t.Helper()
	r := c.Do(http.MethodPost, entriesPath, body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("create entry %v: status %d body %s, want 400", body, r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid time entry" {
		t.Errorf("problem title = %q, want %q", problem.Title, "Invalid time entry")
	}
	return problem.Errors
}

// updateBody is a full-replace body for e: every field e stands with now, at
// the revision it was read at, with overrides applied the way entryBody
// applies them (a nil value removes the field).
func updateBody(e entryJSON, overrides map[string]any) map[string]any {
	body := map[string]any{
		"projectId": e.ProjectId,
		"entryDate": e.EntryDate,
		"hours":     e.Hours,
		"billable":  e.Billable,
		"revision":  e.Revision,
	}
	for field, value := range map[string]any{
		"billingLineId": e.BillingLineId, "taskId": e.TaskId, "startTime": e.StartTime, "endTime": e.EndTime, "note": e.Note,
	} {
		switch v := value.(type) {
		case *int32:
			if v != nil {
				body[field] = *v
			}
		case *string:
			if v != nil {
				body[field] = *v
			}
		}
	}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

// updateEntry replaces e with updateBody(e, overrides) and fails the test
// unless it answered 200.
func updateEntry(t *testing.T, c *modtest.Client, e entryJSON, overrides map[string]any) entryJSON {
	t.Helper()
	r := c.Do(http.MethodPut, entryPath(e.Id), updateBody(e, overrides))
	if r.Status != http.StatusOK {
		t.Fatalf("update entry %d: status %d body %s, want 200", e.Id, r.Status, r.Body)
	}
	var updated entryJSON
	r.JSON(&updated)
	return updated
}

// entryPageJSON decodes PaginatedResponseOfTimeEntryResponse.
type entryPageJSON struct {
	Data       []entryJSON `json:"data"`
	Pagination struct {
		Page            int32 `json:"page"`
		PageSize        int32 `json:"pageSize"`
		TotalCount      int32 `json:"totalCount"`
		TotalPages      int32 `json:"totalPages"`
		HasNextPage     bool  `json:"hasNextPage"`
		HasPreviousPage bool  `json:"hasPreviousPage"`
	} `json:"pagination"`
}

// listEntries lists entries with query ("" or "userId=…&status=…") and fails
// the test unless it answered 200.
func listEntries(t *testing.T, c *modtest.Client, query string) entryPageJSON {
	t.Helper()
	r := c.Do(http.MethodGet, entriesPath+"?"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list entries ?%s: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var page entryPageJSON
	r.JSON(&page)
	return page
}

// entryIDs is the ids of entries, in order.
func entryIDs(entries ...entryJSON) []int64 {
	ids := make([]int64, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.Id)
	}
	return ids
}

// submitPath is the single-entry submit operation's path.
const submitPath = entriesPath + "/submit"

// submitEntries submits ids and fails the test unless it answered 200.
func submitEntries(t *testing.T, c *modtest.Client, ids ...int64) []entryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, submitPath, map[string]any{"ids": ids})
	if r.Status != http.StatusOK {
		t.Fatalf("submit entries %v: status %d body %s, want 200", ids, r.Status, r.Body)
	}
	var entries []entryJSON
	r.JSON(&entries)
	return entries
}

// weekPath is the week operation's path for the Monday weekStart.
func weekPath(weekStart string) string { return "/api/v1/time/weeks/" + weekStart }

// getWeek reads the caller's week and fails the test unless it answered 200.
func getWeek(t *testing.T, c *modtest.Client, weekStart string) weekJSON {
	t.Helper()
	r := c.Do(http.MethodGet, weekPath(weekStart), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get week %s: status %d body %s, want 200", weekStart, r.Status, r.Body)
	}
	var week weekJSON
	r.JSON(&week)
	return week
}

// submitWeek submits the caller's week and fails the test unless it answered
// 200.
func submitWeek(t *testing.T, c *modtest.Client, weekStart string) weekJSON {
	t.Helper()
	r := c.Do(http.MethodPost, weekPath(weekStart)+"/submit", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("submit week %s: status %d body %s, want 200", weekStart, r.Status, r.Body)
	}
	var week weekJSON
	r.JSON(&week)
	return week
}

// weekJSON decodes TimeWeekResponse.
type weekJSON struct {
	WeekStart             string     `json:"weekStart"`
	SubmittedAt           *time.Time `json:"submittedAt"`
	HasUnsubmittedChanges bool       `json:"hasUnsubmittedChanges"`
	Rows                  []struct {
		ProjectId       int32   `json:"projectId"`
		ProjectCode     string  `json:"projectCode"`
		ProjectName     string  `json:"projectName"`
		BillingLineId   *int32  `json:"billingLineId"`
		BillingLineCode *string `json:"billingLineCode"`
		TrackableCode   *string `json:"trackableCode"`
		TaskId          *int32  `json:"taskId"`
		TaskTitle       *string `json:"taskTitle"`
		Days            []struct {
			Date    string      `json:"date"`
			Entries []entryJSON `json:"entries"`
		} `json:"days"`
	} `json:"rows"`
	Totals struct {
		PerDay []float64 `json:"perDay"`
		Week   float64   `json:"week"`
	} `json:"totals"`
}

// The approval operations' paths.
const (
	approvePath   = entriesPath + "/approve"
	rejectPath    = entriesPath + "/reject"
	unapprovePath = entriesPath + "/unapprove"
	approvalsPath = "/api/v1/time/approvals"
)

// batch posts ids (and body's other fields) to path and fails the test
// unless it answered 200, answering the entries.
func batch(t *testing.T, c *modtest.Client, path string, body map[string]any, ids ...int64) []entryJSON {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["ids"] = ids
	r := c.Do(http.MethodPost, path, body)
	if r.Status != http.StatusOK {
		t.Fatalf("POST %s %v: status %d body %s, want 200", path, ids, r.Status, r.Body)
	}
	var entries []entryJSON
	r.JSON(&entries)
	return entries
}

// approveEntries approves ids and fails the test unless it answered 200.
func approveEntries(t *testing.T, c *modtest.Client, ids ...int64) []entryJSON {
	t.Helper()
	return batch(t, c, approvePath, nil, ids...)
}

// rejectEntries rejects ids with reason and fails the test unless it
// answered 200.
func rejectEntries(t *testing.T, c *modtest.Client, reason string, ids ...int64) []entryJSON {
	t.Helper()
	return batch(t, c, rejectPath, map[string]any{"reason": reason}, ids...)
}

// unapproveEntries unapproves ids and fails the test unless it answered 200.
func unapproveEntries(t *testing.T, c *modtest.Client, ids ...int64) []entryJSON {
	t.Helper()
	return batch(t, c, unapprovePath, nil, ids...)
}

// submittedEntry creates an entry with entryBody(overrides) and submits it.
func submittedEntry(t *testing.T, c *modtest.Client, overrides map[string]any) entryJSON {
	t.Helper()
	e := createEntry(t, c, overrides)
	return submitEntries(t, c, e.Id)[0]
}

// setDisplayName renames a seeded user in identity, for a test about the
// order people are listed in: seeded users are named by their generated
// e-mail address.
func setDisplayName(t *testing.T, h *harness, userID uuid.UUID, name string) {
	t.Helper()
	h.Exec(t, `UPDATE identity.users SET display_name = $2 WHERE id = $1`, userID, name)
}
