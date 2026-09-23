package expenses_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// harness is one expenses installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against expenses.yaml through the package recorder, plus the fake
// project directory it was composed with (nil for an installation without
// projects) and the object store its receipts go through.
//
// Deps.Projects is a fake built directly against internal/contracts —
// depguard forbids internal/expenses/** from importing internal/projects,
// even in tests. The user directory is not stubbed: identity is always
// composed, and it provides the real one, which names every user signIn
// seeds.
type harness struct {
	*modtest.Harness
	projects *fakeProjects
	objects  *fakeObjectStore
}

// newHarness is an expenses installation with projects enabled: Deps.Projects
// is the fake directory below, which a test gives roles to and takes projects
// away from.
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	return newExpensesHarness(t, newFakeProjects(), opts...)
}

// newHarnessWithoutProjects is newHarness with the projects module off:
// Deps.Projects stays nil, the optional contract's absent case (decisions X1
// and X2), which meta reports as projectsAvailable false and which every
// project-shaped request field is refused against. The projects module is not
// composed and is not named in MODULES either, so this is the installation an
// operator running MODULES=customers,expenses actually gets.
func newHarnessWithoutProjects(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	return newExpensesHarness(t, nil, opts...)
}

// newExpensesHarness is the one composition every harness here is: the
// project directory is the only thing they differ in, nil for "projects
// disabled", so it is the only thing any of them says.
//
// Every harness also carries the module's locking guarantee out of its test:
// nothing this module asks of another module is asked while one of its
// transactions holds locks (see lockedContractCalls). The check is the whole
// suite's, not one path's — whichever write a future change introduces it on,
// the call is reported and the test that made it fails.
func newExpensesHarness(t *testing.T, projects *fakeProjects, opts ...modtest.Option) *harness {
	t.Helper()
	objects := newFakeObjectStore()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(expenses.Module()),
		modtest.WithObjectStore(objects),
	}
	if projects != nil {
		base = append(base, modtest.WithProjects(projects))
	}
	before := lockedContractCalls.count()
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), projects: projects, objects: objects}
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its locked transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return h
}

// lockedContractCalls is every call out of the module — to another module's
// directory, or to the object store its receipts go through — made from inside
// a transaction that holds locks (expenses.InLockedTx). The directories read
// through the same connection pool as the module, so a transaction holding row
// locks while it waits for one of them can starve the pool under load, and a
// slow object store under a row lock is the same hazard by another route; the
// rule is that none ever does, and every harness checks it when its test ends.
//
// It is one recorder for the package rather than one per harness because the
// hook it is installed as is a package-level one (TestMain), and the module's
// own call sites carry no harness with them. Each harness therefore checks only
// what was recorded while it existed, and the stack trace beside each call
// names the path that actually made it — which is what attributes a violation
// when parallel tests overlap.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

// note is the hook itself. A call from outside any locked transaction — which
// is every call the module is supposed to make — costs one context lookup and
// nothing else.
func (l *lockedCalls) note(ctx context.Context, method string) {
	if !expenses.InLockedTx(ctx) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, method+"\n"+string(debug.Stack()))
}

func (l *lockedCalls) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func (l *lockedCalls) since(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n >= len(l.calls) {
		return nil
	}
	return slices.Clone(l.calls[n:])
}

// The projects the fake directory knows:
//   - 1001 is the ordinary customer project: active, time and materials, in
//     NOK, so an expense may be booked and billed on it;
//   - 1002 is active and non-billable, so billable is forced false on it;
//   - 1003 is completed, so nobody may book an expense on it;
//   - 1004 is active and in EUR, the project whose currency is not the
//     installation's default.
const (
	projectKraftVerket = 1001
	projectInternal    = 1002
	projectCompleted   = 1003
	projectEuro        = 1004
	projectFormula     = 1005
	projectUnknown     = 9999

	projectKraftVerketCode = "KVEM1000"
	projectKraftVerketName = "Kraft-Verket modernisering"
)

// The billing lines the fake directory knows: two on 1001 and one on 1004,
// which is "a line on another project" seen from 1001.
const (
	lineFixed    = 3001
	lineInactive = 3002
	lineEuro     = 3003

	variantProjectManagerHour = 2001
)

// The roles a project directory answers, as projects names them.
const (
	roleManager = "manager"
	roleMember  = "member"
	roleViewer  = "viewer"
)

type roleKey struct {
	projectID int32
	userID    uuid.UUID
}

// fakeProjects is contracts.ProjectDirectory over the fixtures above. Roles
// start empty and are given per test (addRole); a project can be taken away
// (removeProject), the way projects losing it looks from here. It is safe for
// concurrent use, because the handlers of one harness read it from many
// requests at once.
type fakeProjects struct {
	mu       sync.Mutex
	projects map[int32]contracts.ProjectEntry
	lines    map[int32]contracts.BillingLineEntry
	tasks    map[int32]contracts.TaskEntry
	roles    map[roleKey]string
	// canLogTime overrides the directory's own rule for one project, so a
	// test can drive "projects says no" without arranging a status for it.
	canLogTime map[int32]bool
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
				Currency: ptr("NOK"),
			},
			projectEuro: {
				ID: projectEuro, Code: "EURO2026", Name: "Euro-prosjektet",
				CustomerID: &customer, Status: "active", OpenForWork: true, BillingType: "time-and-materials",
				Currency: ptr("EUR"),
			},
		},
		lines: map[int32]contracts.BillingLineEntry{
			lineFixed: {
				ID: lineFixed, ProjectID: projectKraftVerket, Code: "PM", VariantID: variantProjectManagerHour,
				PricingMode: "fixed", FixedAmount: ptr(1500.0), Active: true,
			},
			lineInactive: {
				ID: lineInactive, ProjectID: projectKraftVerket, Code: "OLD", VariantID: variantProjectManagerHour,
				PricingMode: "fixed", FixedAmount: ptr(1200.0), Active: false,
			},
			lineEuro: {
				ID: lineEuro, ProjectID: projectEuro, Code: "EU", VariantID: variantProjectManagerHour,
				PricingMode: "fixed", FixedAmount: ptr(100.0), Active: true,
			},
		},
		tasks:      map[int32]contracts.TaskEntry{},
		roles:      map[roleKey]string{},
		canLogTime: map[int32]bool{},
	}
}

// addRole gives userID role on projectID, as assigning it in projects would.
func (f *fakeProjects) addRole(projectID int32, userID uuid.UUID, role string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.roles[roleKey{projectID, userID}] = role
}

// setCanLogTime makes the directory answer allowed for projectID whatever the
// project's own status and the caller's role say, so a test can drive the two
// sides of CanLogTime without building a project for each.
func (f *fakeProjects) setCanLogTime(projectID int32, allowed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canLogTime[projectID] = allowed
}

// clearCanLogTime drops an override set by setCanLogTime, so the directory
// answers its own rule again.
func (f *fakeProjects) clearCanLogTime(projectID int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.canLogTime, projectID)
}

// deactivateLine and activateLine are a billing line going out of use and
// coming back, the way projects changing it looks from here.
func (f *fakeProjects) deactivateLine(lineID int32) { f.setLineActive(lineID, false) }
func (f *fakeProjects) activateLine(lineID int32)   { f.setLineActive(lineID, true) }

func (f *fakeProjects) setLineActive(lineID int32, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line, ok := f.lines[lineID]
	if !ok {
		return
	}
	line.Active = active
	f.lines[lineID] = line
}

// addProject puts one more project in the directory, for a test that needs a
// project this fixture set does not hold — a code a spreadsheet would read as
// a formula, say.
func (f *fakeProjects) addProject(p contracts.ProjectEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[p.ID] = p
}

// removeProject takes a project away, the way projects losing it looks from
// here: the directory answers (nil, nil) for it from then on.
func (f *fakeProjects) removeProject(id int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.projects, id)
}

func (f *fakeProjects) Project(_ context.Context, id int32) (*contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *fakeProjects) Role(_ context.Context, projectID int32, userID uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.roles[roleKey{projectID, userID}], nil
}

func (f *fakeProjects) BillingLine(_ context.Context, projectID, lineID int32) (*contracts.BillingLineEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.lines[lineID]
	if !ok || l.ProjectID != projectID {
		return nil, nil
	}
	return &l, nil
}

func (f *fakeProjects) ProjectsForUser(_ context.Context, userID uuid.UUID) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.ProjectEntry
	for key := range f.roles {
		if key.userID == userID {
			if p, ok := f.projects[key.projectID]; ok {
				out = append(out, p)
			}
		}
	}
	slices.SortFunc(out, func(a, b contracts.ProjectEntry) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeProjects) ProjectsForCustomer(_ context.Context, customerID int32) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.ProjectEntry
	for _, p := range f.projects {
		if p.CustomerID != nil && *p.CustomerID == customerID {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b contracts.ProjectEntry) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeProjects) Projects(_ context.Context, ids []int32) ([]contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.ProjectEntry
	for _, id := range ids {
		if p, ok := f.projects[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeProjects) ProjectByCode(_ context.Context, code string) (*contracts.ProjectEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.projects {
		if strings.EqualFold(p.Code, code) {
			return &p, nil
		}
	}
	return nil, nil
}

func (f *fakeProjects) BillingLines(_ context.Context, projectID int32) ([]contracts.BillingLineEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.BillingLineEntry
	for _, l := range f.lines {
		if l.ProjectID == projectID {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b contracts.BillingLineEntry) int { return strings.Compare(a.Code, b.Code) })
	return out, nil
}

func (f *fakeProjects) Task(_ context.Context, id int32) (*contracts.TaskEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	task, ok := f.tasks[id]
	if !ok {
		return nil, nil
	}
	return &task, nil
}

func (f *fakeProjects) OpenTasksForUser(_ context.Context, userID uuid.UUID) ([]contracts.TaskEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.TaskEntry
	for _, task := range f.tasks {
		if task.AssigneeUserID != nil && *task.AssigneeUserID == userID && task.Status != "done" {
			out = append(out, task)
		}
	}
	return out, nil
}

// CanLogTime is projects' own rule — the project is open for work and the
// user holds the member or manager role on it — unless a test overrode the
// answer for that project (setCanLogTime). Booking an expense on a project
// needs exactly what logging time needs (decision X9).
func (f *fakeProjects) CanLogTime(_ context.Context, projectID int32, userID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if allowed, ok := f.canLogTime[projectID]; ok {
		return allowed, nil
	}
	p, ok := f.projects[projectID]
	if !ok || !p.OpenForWork {
		return false, nil
	}
	role := f.roles[roleKey{projectID, userID}]
	return role == roleMember || role == roleManager, nil
}

// fakeObjectStore is storage.ObjectStore in memory: the receipts this module
// uploads go here rather than to a filesystem, so no test depends on
// filesystem permissions. It is installed by every harness, so the attachment
// paths always have somewhere to write.
//
// Every operation reports itself to lockedContractCalls, so the whole-suite
// check that covers this module's calls into its neighbours covers its calls
// into the object store too: the bytes are written before the locked
// transaction and removed after it, never inside one.
//
// Each of the four operations can be made to fail on demand (failPut and its
// siblings), which is the only way to drive the storage-failure answers — a
// write that fails after an entry was locked, a read of an object that is not
// there any more — deterministically.
type fakeObjectStore struct {
	mu        sync.Mutex
	objects   map[string][]byte
	putErr    error
	getErr    error
	deleteErr error
}

var _ storage.ObjectStore = (*fakeObjectStore)(nil)

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: map[string][]byte{}}
}

func (s *fakeObjectStore) Put(ctx context.Context, key string, r io.Reader, _ string) error {
	lockedContractCalls.note(ctx, "ObjectStore.Put")
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.objects[key] = data
	return nil
}

func (s *fakeObjectStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	lockedContractCalls.note(ctx, "ObjectStore.Get")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("expenses test store: %q: %w", key, storage.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *fakeObjectStore) Exists(ctx context.Context, key string) (bool, error) {
	lockedContractCalls.note(ctx, "ObjectStore.Exists")
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}

func (s *fakeObjectStore) Delete(ctx context.Context, key string) error {
	lockedContractCalls.note(ctx, "ObjectStore.Delete")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.objects, key)
	return nil
}

// failPut, failGet and failDelete make the next and every later call of that
// operation fail with err; nil restores it.
func (s *fakeObjectStore) failPut(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putErr = err
}

func (s *fakeObjectStore) failGet(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getErr = err
}

func (s *fakeObjectStore) failDelete(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteErr = err
}

// keys is every key the store holds, sorted, so a test can say exactly what
// was written and what was cleaned up again.
func (s *fakeObjectStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.objects))
}

// count is how many objects the store holds.
func (s *fakeObjectStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

// signIn seeds a caller holding expenses:access plus whatever else the test
// needs, and returns their client and user id. Every operation requires
// expenses:access, so adding it here keeps each test's permission list to the
// thing it is actually about.
func signIn(t *testing.T, h *harness, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, append([]string{"expenses:access"}, permissions...)...)
}

// signInAs is signIn for a caller who also holds role on projectID.
func signInAs(t *testing.T, h *harness, projectID int32, role string, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, id := signIn(t, h, permissions...)
	h.projects.addRole(projectID, id, role)
	return c, id
}

// The module's paths, so a typo is one compile error rather than a 404 a test
// then explains to itself.
const (
	metaPath           = "/api/v1/expenses/meta"
	settingsPath       = "/api/v1/expenses/settings"
	ratesPath          = "/api/v1/expenses/rates"
	ratesResetPath     = ratesPath + "/reset"
	categoriesPath     = "/api/v1/expenses/categories"
	entriesPath        = "/api/v1/expenses/entries"
	claimsPath         = "/api/v1/expenses/claims"
	projectOptionsPath = "/api/v1/expenses/projects"
	attachmentsPath    = "/api/v1/expenses/attachments"
	submitPath         = "/api/v1/expenses/submit"
	approvePath        = "/api/v1/expenses/approve"
	rejectPath         = "/api/v1/expenses/reject"
	unapprovePath      = "/api/v1/expenses/unapprove"
	approvalsPath      = "/api/v1/expenses/approvals"

	reimbursementsPath       = "/api/v1/expenses/reimbursements"
	reimbursementsExportPath = reimbursementsPath + "/export.csv"
	reimbursedPath           = "/api/v1/expenses/reimbursed"
	reimbursedUndoPath       = reimbursedPath + "/undo"

	statsPath           = "/api/v1/expenses/stats"
	statsSummaryPath    = statsPath + "/summary"
	statsTimeseriesPath = statsPath + "/timeseries"
	statsAttentionPath  = statsPath + "/attention"
)

func ratePath(id int32) string     { return fmt.Sprintf("%s/%d", ratesPath, id) }
func categoryPath(id int32) string { return fmt.Sprintf("%s/%d", categoriesPath, id) }
func entryPath(id int64) string    { return fmt.Sprintf("%s/%d", entriesPath, id) }
func claimPath(id int64) string    { return fmt.Sprintf("%s/%d", claimsPath, id) }

// perDiemSuggestionPath is where a claim's own days are proposed from its
// departure and its return. It writes nothing.
func perDiemSuggestionPath(id int64) string {
	return fmt.Sprintf("%s/%d/per-diem-suggestion", claimsPath, id)
}
func attachmentPath(id int64) string {
	return fmt.Sprintf("%s/%d", attachmentsPath, id)
}

// entryRatePath and entryBillingPath are the two single-expense writes the flow
// adds: an approver's rate override and the project side's pricing.
func entryRatePath(id int64) string    { return fmt.Sprintf("%s/%d/rate", entriesPath, id) }
func entryBillingPath(id int64) string { return fmt.Sprintf("%s/%d/billing", entriesPath, id) }

// entryInvoicedPath and entryInvoicedUndoPath are the second of the two tracks
// after approval: what the customer has been billed for, per line.
func entryInvoicedPath(id int64) string {
	return fmt.Sprintf("%s/%d/invoiced", entriesPath, id)
}

func entryInvoicedUndoPath(id int64) string {
	return fmt.Sprintf("%s/%d/invoiced/undo", entriesPath, id)
}

// entryAttachmentsPath is where a receipt is uploaded: the entry's own
// sub-collection, unlike the read and the delete, which are by attachment id
// alone.
func entryAttachmentsPath(entryID int64) string {
	return fmt.Sprintf("%s/%d/attachments", entriesPath, entryID)
}

// The titles this module's refusals carry, so a test says which refusal it
// expects rather than repeating the string.
const (
	invalidEntryTitle      = "Invalid expense"
	invalidSettingsTitle   = "Invalid expense settings"
	invalidClaimTitle      = "Invalid travel claim"
	invalidQueryTitle      = "Invalid query parameters"
	invalidReceiptTitle    = "Invalid receipt"
	invalidSubmissionTitle = "Invalid submission"
	invalidApprovalTitle   = "Invalid approval"
	invalidSuggestionTitle = "Invalid per diem suggestion"

	// The two titles the tracks after approval carry: a payroll run and a
	// payroll export are their own moment, and neither is an approval.
	invalidReimbursementTitle = "Invalid reimbursement"
	invalidExportTitle        = "Invalid export"
)

// materialsCategory is the first seeded category's id. The migration gives
// expenses.categories an identity starting at 1001 and seeds design §3.3's
// eight names in order, so Materials is 1001;
// TestExpensesEntries_TheDefaultBodyNamesASeededCategory pins it here rather
// than leaving every entry body to look it up.
const materialsCategory = 1001

// metaJSON decodes ExpensesMetaResponse.
type metaJSON struct {
	ProjectsAvailable    bool           `json:"projectsAvailable"`
	DefaultCurrency      string         `json:"defaultCurrency"`
	DefaultMarkupPercent *float64       `json:"defaultMarkupPercent"`
	LockedBefore         *string        `json:"lockedBefore"`
	ReceiptRequiredOver  *float64       `json:"receiptRequiredOver"`
	TimeZone             string         `json:"timeZone"`
	Categories           []categoryJSON `json:"categories"`
	Capabilities         struct {
		CanApprove bool `json:"canApprove"`
		CanViewAll bool `json:"canViewAll"`
		CanManage  bool `json:"canManage"`
	} `json:"capabilities"`
}

// categoryJSON decodes ExpensesCategoryResponse.
type categoryJSON struct {
	Id       int32  `json:"id"`
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	Position int32  `json:"position"`
}

// rateJSON decodes ExpensesRateResponse.
type rateJSON struct {
	Id        int32   `json:"id"`
	Kind      string  `json:"kind"`
	ValidFrom string  `json:"validFrom"`
	Value     float64 `json:"value"`
	Currency  *string `json:"currency"`
	Source    *string `json:"source"`
}

// settingsJSON decodes ExpensesSettingsResponse.
type settingsJSON struct {
	DefaultCurrency      string   `json:"defaultCurrency"`
	DefaultMarkupPercent *float64 `json:"defaultMarkupPercent"`
	LockedBefore         *string  `json:"lockedBefore"`
	ReceiptRequiredOver  *float64 `json:"receiptRequiredOver"`
	TimeZone             string   `json:"timeZone"`
}

// validationProblemJSON decodes the field-error body every refusal of a body
// answers with.
type validationProblemJSON struct {
	Title  string              `json:"title"`
	Errors map[string][]string `json:"errors"`
}

// getMeta reads the module metadata and fails the test unless it answered
// 200.
func getMeta(t *testing.T, c *modtest.Client) metaJSON {
	t.Helper()
	r := c.Do(http.MethodGet, metaPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get meta: status %d body %s, want 200", r.Status, r.Body)
	}
	var meta metaJSON
	r.JSON(&meta)
	return meta
}

// rawMeta reads the module metadata as a bare JSON object, for a test whose
// subject is whether a key is there at all.
func rawMeta(t *testing.T, c *modtest.Client) map[string]any {
	t.Helper()
	r := c.Do(http.MethodGet, metaPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get meta: status %d body %s, want 200", r.Status, r.Body)
	}
	var raw map[string]any
	r.JSON(&raw)
	return raw
}

// getSettings reads the settings and fails the test unless it answered 200.
func getSettings(t *testing.T, c *modtest.Client) settingsJSON {
	t.Helper()
	r := c.Do(http.MethodGet, settingsPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get settings: status %d body %s, want 200", r.Status, r.Body)
	}
	var settings settingsJSON
	r.JSON(&settings)
	return settings
}

// rawSettings reads the settings as a bare JSON object, for a test whose
// subject is whether a key is there at all — a nil pointer cannot tell
// "absent" from "null".
func rawSettings(t *testing.T, c *modtest.Client) map[string]any {
	t.Helper()
	r := c.Do(http.MethodGet, settingsPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get settings: status %d body %s, want 200", r.Status, r.Body)
	}
	var raw map[string]any
	r.JSON(&raw)
	return raw
}

// settingsBody is a valid full-replace settings body which tests override one
// field of at a time. A nil override value removes that field.
func settingsBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"defaultCurrency":      "NOK",
		"defaultMarkupPercent": 0,
	}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

// putSettings replaces the settings and fails the test unless it answered
// 200.
func putSettings(t *testing.T, c *modtest.Client, body map[string]any) settingsJSON {
	t.Helper()
	r := c.Do(http.MethodPut, settingsPath, body)
	if r.Status != http.StatusOK {
		t.Fatalf("put settings %v: status %d body %s, want 200", body, r.Status, r.Body)
	}
	var settings settingsJSON
	r.JSON(&settings)
	return settings
}

// refused sends body to path and fails the test unless it was refused with a
// validation problem carrying title, answering the field errors. body is `any`
// rather than a map so a DELETE, which declares no request body, can pass a
// real nil — a nil map inside an interface is not nil, and the contract
// recorder rightly refuses a body on an operation that declares none.
func refused(t *testing.T, c *modtest.Client, method, path string, body any, title string) map[string][]string {
	t.Helper()
	r := c.Do(method, path, body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("%s %s %v: status %d body %s, want 400", method, path, body, r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if problem.Title != title {
		t.Errorf("problem title = %q, want %q", problem.Title, title)
	}
	return problem.Errors
}

// listRates reads every rate and fails the test unless it answered 200.
func listRates(t *testing.T, c *modtest.Client) []rateJSON {
	t.Helper()
	r := c.Do(http.MethodGet, ratesPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list rates: status %d body %s, want 200", r.Status, r.Body)
	}
	var rates []rateJSON
	r.JSON(&rates)
	return rates
}

// rateBody is a valid minimal create body — a company's own customer rate per
// kilometre — which tests override one field of at a time.
func rateBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"kind":      "mileage_customer",
		"validFrom": "2026-01-01",
		"value":     7.5,
		"currency":  "NOK",
	}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

// createRate adds a rate and fails the test unless it was created.
func createRate(t *testing.T, c *modtest.Client, overrides map[string]any) rateJSON {
	t.Helper()
	r := c.Do(http.MethodPost, ratesPath, rateBody(overrides))
	if r.Status != http.StatusCreated {
		t.Fatalf("create rate %v: status %d body %s, want 201", rateBody(overrides), r.Status, r.Body)
	}
	var rate rateJSON
	r.JSON(&rate)
	return rate
}

// resetRates restores one kind's seeded rows and fails the test unless it
// answered 200, answering every rate as it now stands.
func resetRates(t *testing.T, c *modtest.Client, kind string) []rateJSON {
	t.Helper()
	r := c.Do(http.MethodPost, ratesResetPath, map[string]any{"kind": kind})
	if r.Status != http.StatusOK {
		t.Fatalf("reset rates %q: status %d body %s, want 200", kind, r.Status, r.Body)
	}
	var rates []rateJSON
	r.JSON(&rates)
	return rates
}

// ratesOfKind is the rates of one kind, in the order they were listed.
func ratesOfKind(rates []rateJSON, kind string) []rateJSON {
	var out []rateJSON
	for _, rate := range rates {
		if rate.Kind == kind {
			out = append(out, rate)
		}
	}
	return out
}

// listCategories reads every category and fails the test unless it answered
// 200.
func listCategories(t *testing.T, c *modtest.Client) []categoryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, categoriesPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list categories: status %d body %s, want 200", r.Status, r.Body)
	}
	var categories []categoryJSON
	r.JSON(&categories)
	return categories
}

// createCategory adds a category and fails the test unless it was created.
func createCategory(t *testing.T, c *modtest.Client, body map[string]any) categoryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, categoriesPath, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create category %v: status %d body %s, want 201", body, r.Status, r.Body)
	}
	var category categoryJSON
	r.JSON(&category)
	return category
}

// updateCategory replaces a category and fails the test unless it answered
// 200.
func updateCategory(t *testing.T, c *modtest.Client, id int32, body map[string]any) categoryJSON {
	t.Helper()
	r := c.Do(http.MethodPut, categoryPath(id), body)
	if r.Status != http.StatusOK {
		t.Fatalf("update category %d with %v: status %d body %s, want 200", id, body, r.Status, r.Body)
	}
	var category categoryJSON
	r.JSON(&category)
	return category
}

// categoryNames is the names of categories, in order.
func categoryNames(categories []categoryJSON) []string {
	names := make([]string, 0, len(categories))
	for _, c := range categories {
		names = append(names, c.Name)
	}
	return names
}

// The pieces of ExpensesEntryResponse a test reads. Every optional one is a
// pointer, so "absent" and "zero" are told apart; a test whose subject is
// whether a key is there at all reads the entry as a bare object instead
// (rawEntry).
type (
	entryCategoryJSON struct {
		Id   int32  `json:"id"`
		Name string `json:"name"`
	}
	entryProjectJSON struct {
		Id   int32  `json:"id"`
		Code string `json:"code"`
		Name string `json:"name"`
	}
	entryLineJSON struct {
		Id   int32  `json:"id"`
		Code string `json:"code"`
	}
	entryOwnerJSON struct {
		UserId      uuid.UUID `json:"userId"`
		DisplayName string    `json:"displayName"`
		Active      bool      `json:"active"`
	}
	entryBillingJSON struct {
		MarkupPercent *float64          `json:"markupPercent"`
		BillRatePerKm *float64          `json:"billRatePerKm"`
		BillAmount    float64           `json:"billAmount"`
		Invoice       *entryInvoiceJSON `json:"invoice"`
	}
	// entryInvoiceJSON decodes ExpensesEntryInvoice — what the customer has
	// been billed for. It lives inside billing, so only a caller with
	// financial rights on the project ever sees it.
	entryInvoiceJSON struct {
		At        string         `json:"at"`
		By        entryOwnerJSON `json:"by"`
		Reference *string        `json:"reference"`
	}
	// entryReimbursementJSON decodes ExpensesEntryReimbursement — what the
	// employee has been paid back, which whoever sees the expense sees.
	entryReimbursementJSON struct {
		At        string         `json:"at"`
		By        entryOwnerJSON `json:"by"`
		Date      string         `json:"date"`
		Reference *string        `json:"reference"`
	}
	entryCapabilitiesJSON struct {
		CanEdit           bool `json:"canEdit"`
		CanDelete         bool `json:"canDelete"`
		CanSubmit         bool `json:"canSubmit"`
		CanApprove        bool `json:"canApprove"`
		CanUnapprove      bool `json:"canUnapprove"`
		CanOverrideRate   bool `json:"canOverrideRate"`
		CanMarkInvoiced   bool `json:"canMarkInvoiced"`
		CanUndoInvoiced   bool `json:"canUndoInvoiced"`
		CanMarkReimbursed bool `json:"canMarkReimbursed"`
		CanUndoReimbursed bool `json:"canUndoReimbursed"`
		CanSeeBilling     bool `json:"canSeeBilling"`
		CanSetBilling     bool `json:"canSetBilling"`
	}
	// entryRateOverrideJSON decodes ExpensesEntryRateOverride — who overrode a
	// mileage line's rate and what the rate table had said.
	entryRateOverrideJSON struct {
		ByUser              entryOwnerJSON `json:"byUser"`
		TableValue          *float64       `json:"tableValue"`
		PassengerTableValue *float64       `json:"passengerTableValue"`
	}
	// entryDecisionJSON decodes ExpensesEntryDecision — what was decided about
	// the expense, when, by whom and why.
	entryDecisionJSON struct {
		Status string         `json:"status"`
		At     string         `json:"at"`
		By     entryOwnerJSON `json:"by"`
		Reason *string        `json:"reason"`
	}
	// mealPercentsJSON decodes ExpensesEntryMealPercents — what each covered
	// meal deducted, as the table stood when the day was priced.
	mealPercentsJSON struct {
		Breakfast *float64 `json:"breakfast"`
		Lunch     *float64 `json:"lunch"`
		Dinner    *float64 `json:"dinner"`
	}
	// entryPerDiemJSON decodes ExpensesEntryPerDiem — the per diem day one
	// line is.
	entryPerDiemJSON struct {
		Type             string           `json:"type"`
		BreakfastCovered bool             `json:"breakfastCovered"`
		LunchCovered     bool             `json:"lunchCovered"`
		DinnerCovered    bool             `json:"dinnerCovered"`
		DayRate          float64          `json:"dayRate"`
		MealPercents     mealPercentsJSON `json:"mealPercents"`
	}
)

// entryJSON decodes ExpensesEntryResponse.
type entryJSON struct {
	Id              int64                   `json:"id"`
	ClaimId         *int64                  `json:"claimId"`
	Kind            string                  `json:"kind"`
	EntryDate       string                  `json:"entryDate"`
	Description     string                  `json:"description"`
	Category        *entryCategoryJSON      `json:"category"`
	Supplier        *string                 `json:"supplier"`
	PaidBy          *string                 `json:"paidBy"`
	Currency        string                  `json:"currency"`
	GrossAmount     float64                 `json:"grossAmount"`
	VatAmount       *float64                `json:"vatAmount"`
	NetAmount       float64                 `json:"netAmount"`
	DistanceKm      *float64                `json:"distanceKm"`
	FromPlace       *string                 `json:"fromPlace"`
	ToPlace         *string                 `json:"toPlace"`
	Passengers      *int32                  `json:"passengers"`
	Rate            *float64                `json:"rate"`
	PassengerRate   *float64                `json:"passengerRate"`
	PerDiem         *entryPerDiemJSON       `json:"perDiem"`
	OwedToEmployee  float64                 `json:"owedToEmployee"`
	Status          string                  `json:"status"`
	SubmittedAt     *string                 `json:"submittedAt"`
	Decision        *entryDecisionJSON      `json:"decision"`
	RateOverride    *entryRateOverrideJSON  `json:"rateOverride"`
	Project         *entryProjectJSON       `json:"project"`
	BillingLine     *entryLineJSON          `json:"billingLine"`
	Billable        bool                    `json:"billable"`
	Billing         *entryBillingJSON       `json:"billing"`
	Reimbursement   *entryReimbursementJSON `json:"reimbursement"`
	AttachmentCount int32                   `json:"attachmentCount"`
	Attachments     []attachmentJSON        `json:"attachments"`
	Owner           entryOwnerJSON          `json:"owner"`
	Revision        int32                   `json:"revision"`
	Capabilities    entryCapabilitiesJSON   `json:"capabilities"`
}

// entryPageJSON decodes PaginatedResponseOfExpensesEntryResponse.
type entryPageJSON struct {
	Data       []entryJSON `json:"data"`
	Pagination struct {
		Page       int32 `json:"page"`
		PageSize   int32 `json:"pageSize"`
		TotalCount int32 `json:"totalCount"`
		TotalPages int32 `json:"totalPages"`
	} `json:"pagination"`
}

// projectOptionJSON decodes ExpensesProjectOption.
type projectOptionJSON struct {
	Id           int32           `json:"id"`
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	Currency     *string         `json:"currency"`
	BillingLines []entryLineJSON `json:"billingLines"`
}

// The pieces of ExpensesProjectSummaryResponse a test reads — what one
// project's expenses cost and bill, per currency.
type (
	summaryBucketJSON struct {
		Count      int32   `json:"count"`
		Cost       float64 `json:"cost"`
		BillAmount float64 `json:"billAmount"`
	}
	summaryCurrencyJSON struct {
		Currency       string            `json:"currency"`
		Approved       summaryBucketJSON `json:"approved"`
		Submitted      summaryBucketJSON `json:"submitted"`
		Draft          summaryBucketJSON `json:"draft"`
		Total          summaryBucketJSON `json:"total"`
		ReadyCount     int32             `json:"readyCount"`
		ReadyAmount    float64           `json:"readyAmount"`
		InvoicedCount  int32             `json:"invoicedCount"`
		InvoicedAmount float64           `json:"invoicedAmount"`
		UnpricedCount  int32             `json:"unpricedCount"`
	}
)

// projectSummaryJSON decodes ExpensesProjectSummaryResponse.
type projectSummaryJSON struct {
	Currencies    []summaryCurrencyJSON `json:"currencies"`
	LastEntryDate *string               `json:"lastEntryDate"`
	// ProjectCurrency is the project's own — the entry of Currencies with this
	// code is the project's, every other one is in another currency. A pointer,
	// because a project may carry none.
	ProjectCurrency *string `json:"projectCurrency"`
	Capabilities    struct {
		CanRecord bool `json:"canRecord"`
	} `json:"capabilities"`
}

// projectSummaryPath is where one project's expenses are summed up.
func projectSummaryPath(id int32) string {
	return fmt.Sprintf("%s/%d/summary", projectOptionsPath, id)
}

// getProjectSummary reads a project's expense summary and fails the test
// unless it answered 200.
func getProjectSummary(t *testing.T, c *modtest.Client, id int32) projectSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, projectSummaryPath(id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get the summary of project %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	var summary projectSummaryJSON
	r.JSON(&summary)
	return summary
}

// rawProjectSummary reads the summary as a bare JSON object, for a test whose
// subject is whether a key is there at all — a nil pointer cannot tell
// "absent" from "null".
func rawProjectSummary(t *testing.T, c *modtest.Client, id int32) map[string]any {
	t.Helper()
	r := c.Do(http.MethodGet, projectSummaryPath(id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get the summary of project %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	var raw map[string]any
	r.JSON(&raw)
	return raw
}

// summaryCurrency picks one currency out of a summary, or fails the test.
func summaryCurrency(t *testing.T, summary projectSummaryJSON, code string) summaryCurrencyJSON {
	t.Helper()
	for _, c := range summary.Currencies {
		if c.Currency == code {
			return c
		}
	}
	t.Fatalf("no %s among %+v", code, summary.Currencies)
	return summaryCurrencyJSON{}
}

// summaryDenied asserts that the summary of a project answers the one bare
// 404 it refuses everybody with: no projects module, no such project, and no
// financial rights on it are byte for byte the same answer.
func summaryDenied(t *testing.T, c *modtest.Client, id int32, who string) {
	t.Helper()
	r := c.Do(http.MethodGet, projectSummaryPath(id), nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("%s reading the summary of project %d: status %d body %s, want 404", who, id, r.Status, r.Body)
	}
	if len(r.Body) > 0 {
		t.Errorf("%s reading the summary of project %d: body %s, want it empty", who, id, r.Body)
	}
}

// outlayBody is a valid minimal outlay — a receipt the employee paid — which
// tests override one field of at a time. A nil override value removes that
// field.
func outlayBody(overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{
		"kind":        "outlay",
		"entryDate":   "2026-03-10",
		"description": "Kabel og kontakter",
		"categoryId":  materialsCategory,
		"paidBy":      "employee",
		"currency":    "NOK",
		"grossAmount": 1250.00,
	}, overrides)
}

// mileageBody is a valid minimal mileage line, which tests override one field
// of at a time. The distance is the one the seeded rates price.
func mileageBody(overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{
		"kind":        "mileage",
		"entryDate":   "2026-03-10",
		"description": "Til anlegget og tilbake",
		"distanceKm":  120.0,
	}, overrides)
}

// perDiemBody is a valid minimal per diem day: a 6–12 hour day inside the trip
// claimBody describes, with no meal covered. It carries no claimId of its own —
// addLine puts it there — because a per diem day standing alone is one of the
// things the tests are about.
func perDiemBody(overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{
		"kind":        "per_diem",
		"entryDate":   "2026-03-10",
		"perDiemType": "day_6_12",
	}, overrides)
}

// bodyWith is base with overrides applied; a nil value removes the field, so a
// test can say "without a currency" as well as "with this currency".
func bodyWith(base, overrides map[string]any) map[string]any {
	maps.Copy(base, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(base, field)
		}
	}
	return base
}

// createEntry records an entry and fails the test unless it was created.
func createEntry(t *testing.T, c *modtest.Client, body map[string]any) entryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, entriesPath, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create entry %v: status %d body %s, want 201", body, r.Status, r.Body)
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

// rawEntry reads one entry as a bare JSON object, for a test whose subject is
// whether a key is there at all — a nil pointer cannot tell "absent" from
// "null".
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

// updateEntry replaces an entry and fails the test unless it answered 200. The
// body is a create body plus the revision it was read at.
func updateEntry(t *testing.T, c *modtest.Client, id int64, body map[string]any) entryJSON {
	t.Helper()
	r := c.Do(http.MethodPut, entryPath(id), body)
	if r.Status != http.StatusOK {
		t.Fatalf("update entry %d with %v: status %d body %s, want 200", id, body, r.Status, r.Body)
	}
	var entry entryJSON
	r.JSON(&entry)
	return entry
}

// listEntries reads a page of entries and fails the test unless it answered
// 200. query is appended as it stands ("?userId=…"), "" for none.
func listEntries(t *testing.T, c *modtest.Client, query string) entryPageJSON {
	t.Helper()
	r := c.Do(http.MethodGet, entriesPath+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list entries %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var page entryPageJSON
	r.JSON(&page)
	return page
}

// entryIDs is the ids of a page's entries, in the order they were listed.
func entryIDs(page entryPageJSON) []int64 {
	ids := make([]int64, 0, len(page.Data))
	for _, e := range page.Data {
		ids = append(ids, e.Id)
	}
	return ids
}

// listProjectOptions reads the projects the caller may book on and fails the
// test unless it answered 200.
func listProjectOptions(t *testing.T, c *modtest.Client) []projectOptionJSON {
	t.Helper()
	r := c.Do(http.MethodGet, projectOptionsPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list project options: status %d body %s, want 200", r.Status, r.Body)
	}
	var options []projectOptionJSON
	r.JSON(&options)
	return options
}

// refusedEntry is refused for an entry body: the field errors of a create or
// an update that did not pass. body is `any` so the same helper serves a
// DELETE, which carries none.
func refusedEntry(t *testing.T, c *modtest.Client, method, path string, body any) map[string][]string {
	t.Helper()
	return refused(t, c, method, path, body, invalidEntryTitle)
}

// The pieces of ExpensesClaimResponse and ExpensesClaimListResponse a test
// reads. Every optional one is a pointer, so "absent" and "zero" are told
// apart.
type (
	claimCapabilitiesJSON struct {
		CanEdit           bool `json:"canEdit"`
		CanDelete         bool `json:"canDelete"`
		CanSubmit         bool `json:"canSubmit"`
		CanApprove        bool `json:"canApprove"`
		CanUnapprove      bool `json:"canUnapprove"`
		CanMarkReimbursed bool `json:"canMarkReimbursed"`
		CanUndoReimbursed bool `json:"canUndoReimbursed"`
	}
	currencyTotalJSON struct {
		Currency       string  `json:"currency"`
		Gross          float64 `json:"gross"`
		OwedToEmployee float64 `json:"owedToEmployee"`
	}
)

// claimJSON decodes ExpensesClaimResponse.
type claimJSON struct {
	Id             int64                   `json:"id"`
	Purpose        string                  `json:"purpose"`
	Destination    *string                 `json:"destination"`
	Abroad         bool                    `json:"abroad"`
	AbroadDayRate  *float64                `json:"abroadDayRate"`
	AbroadCurrency *string                 `json:"abroadCurrency"`
	DepartureAt    string                  `json:"departureAt"`
	ReturnAt       string                  `json:"returnAt"`
	Project        *entryProjectJSON       `json:"project"`
	Owner          entryOwnerJSON          `json:"owner"`
	Status         string                  `json:"status"`
	SubmittedAt    *string                 `json:"submittedAt"`
	Decision       *entryDecisionJSON      `json:"decision"`
	Reimbursement  *entryReimbursementJSON `json:"reimbursement"`
	Lines          []entryJSON             `json:"lines"`
	Totals         []currencyTotalJSON     `json:"totals"`
	BillableTotals *[]currencyAmountJSON   `json:"billableTotals"`
	Revision       int32                   `json:"revision"`
	Capabilities   claimCapabilitiesJSON   `json:"capabilities"`
}

// claimListJSON decodes ExpensesClaimListResponse — the same header with a
// count in place of the lines.
type claimListJSON struct {
	Id           int64                 `json:"id"`
	Purpose      string                `json:"purpose"`
	Status       string                `json:"status"`
	DepartureAt  string                `json:"departureAt"`
	Owner        entryOwnerJSON        `json:"owner"`
	Project      *entryProjectJSON     `json:"project"`
	LineCount    int32                 `json:"lineCount"`
	Totals       []currencyTotalJSON   `json:"totals"`
	Revision     int32                 `json:"revision"`
	Capabilities claimCapabilitiesJSON `json:"capabilities"`
}

// claimPageJSON decodes PaginatedResponseOfExpensesClaimListResponse.
type claimPageJSON struct {
	Data       []claimListJSON `json:"data"`
	Pagination struct {
		Page       int32 `json:"page"`
		PageSize   int32 `json:"pageSize"`
		TotalCount int32 `json:"totalCount"`
		TotalPages int32 `json:"totalPages"`
	} `json:"pagination"`
}

// claimBody is a valid minimal travel claim — a domestic two-day trip — which
// tests override one field of at a time. A nil override value removes that
// field. The dates sit in the same month the entry bodies do, so a line's own
// date falls inside the trip without every test saying so.
func claimBody(overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{
		"purpose":     "Montasje hos kunden",
		"destination": "Bergen",
		"departureAt": "2026-03-09T07:00:00Z",
		"returnAt":    "2026-03-11T16:00:00Z",
	}, overrides)
}

// createClaim records a travel claim and fails the test unless it was created.
func createClaim(t *testing.T, c *modtest.Client, overrides map[string]any) claimJSON {
	t.Helper()
	body := claimBody(overrides)
	r := c.Do(http.MethodPost, claimsPath, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create claim %v: status %d body %s, want 201", body, r.Status, r.Body)
	}
	var claim claimJSON
	r.JSON(&claim)
	return claim
}

// getClaim reads one claim with its lines and fails the test unless it
// answered 200.
func getClaim(t *testing.T, c *modtest.Client, id int64) claimJSON {
	t.Helper()
	r := c.Do(http.MethodGet, claimPath(id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get claim %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	var claim claimJSON
	r.JSON(&claim)
	return claim
}

// rawClaim reads one claim as a bare JSON object, for a test whose subject is
// whether a key is there at all.
func rawClaim(t *testing.T, c *modtest.Client, id int64) map[string]any {
	t.Helper()
	r := c.Do(http.MethodGet, claimPath(id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get claim %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	var raw map[string]any
	r.JSON(&raw)
	return raw
}

// updateClaim replaces a claim's header and fails the test unless it answered
// 200. The body is a create body plus the revision it was read at.
func updateClaim(t *testing.T, c *modtest.Client, id int64, overrides map[string]any) claimJSON {
	t.Helper()
	body := claimBody(overrides)
	r := c.Do(http.MethodPut, claimPath(id), body)
	if r.Status != http.StatusOK {
		t.Fatalf("update claim %d with %v: status %d body %s, want 200", id, body, r.Status, r.Body)
	}
	var claim claimJSON
	r.JSON(&claim)
	return claim
}

// deleteClaim removes a claim and fails the test unless it answered 204.
func deleteClaim(t *testing.T, c *modtest.Client, id int64) {
	t.Helper()
	if r := c.Do(http.MethodDelete, claimPath(id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete claim %d: status %d body %s, want 204", id, r.Status, r.Body)
	}
}

// listClaims reads a page of claims and fails the test unless it answered 200.
func listClaims(t *testing.T, c *modtest.Client, query string) claimPageJSON {
	t.Helper()
	r := c.Do(http.MethodGet, claimsPath+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list claims %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var page claimPageJSON
	r.JSON(&page)
	return page
}

// claimIDsOf is the ids of a page's claims, in the order they were listed.
func claimIDsOf(page claimPageJSON) []int64 {
	ids := make([]int64, 0, len(page.Data))
	for _, claim := range page.Data {
		ids = append(ids, claim.Id)
	}
	return ids
}

// refusedClaim is refused for a claim body: the field errors of a create, a
// replace or a delete that did not pass.
func refusedClaim(t *testing.T, c *modtest.Client, method, path string, body any) map[string][]string {
	t.Helper()
	return refused(t, c, method, path, body, invalidClaimTitle)
}

// addLine records one expense inside a claim and fails the test unless it was
// created.
func addLine(t *testing.T, c *modtest.Client, claimID int64, body map[string]any) entryJSON {
	t.Helper()
	return createEntry(t, c, bodyWith(body, map[string]any{"claimId": claimID}))
}

// suggestedDayJSON decodes ExpensesPerDiemSuggestedDay.
type suggestedDayJSON struct {
	EntryDate   string   `json:"entryDate"`
	PerDiemType string   `json:"perDiemType"`
	DayRate     *float64 `json:"dayRate"`
	Amount      *float64 `json:"amount"`
	Exists      bool     `json:"exists"`
}

// suggestDays asks a claim for the per diem days its times imply and fails the
// test unless it answered 200.
func suggestDays(t *testing.T, c *modtest.Client, claimID int64, overnight bool) []suggestedDayJSON {
	t.Helper()
	r := c.Do(http.MethodPost, perDiemSuggestionPath(claimID), map[string]any{"overnight": overnight})
	if r.Status != http.StatusOK {
		t.Fatalf("suggest days for claim %d: status %d body %s, want 200", claimID, r.Status, r.Body)
	}
	var days []suggestedDayJSON
	r.JSON(&days)
	return days
}

// seedClaimStatus moves a claim straight to a status the flow does not reach
// yet, in SQL. The claim flow is a later task's; every rule this one writes is
// written against the status column, so this is how those rules are proved —
// with the row in a state the API will only be able to produce later.
func seedClaimStatus(t *testing.T, h *harness, claimID int64, status string) {
	t.Helper()
	h.Exec(t, `
		UPDATE expenses.claims
		SET status = $2::text,
		    submitted_at = CASE WHEN $2::text = 'draft' THEN NULL ELSE now() END,
		    decided_at = CASE WHEN $2::text IN ('approved', 'rejected') THEN now() ELSE NULL END,
		    revision = revision + 1
		WHERE id = $1`, claimID, status)
}

// attachmentJSON decodes ExpensesAttachmentResponse — the receipt itself, as
// the upload answers it and as every entry carries it.
type attachmentJSON struct {
	Id          int64  `json:"id"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
}

// The receipt bytes every test uploads. No binary fixture is committed: the
// images come from the standard library's own encoders, and the PDF and the
// HEIC file are the magic bytes each format is sniffed by.
func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height)), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return buf.Bytes()
}

// testPDF is the smallest thing http.DetectContentType calls a PDF: the
// "%PDF-" header, then enough of a document body to look like one.
func testPDF(padding int) []byte {
	return append([]byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"), bytes.Repeat([]byte("x"), padding)...)
}

// testHEIC is an ISO base media file whose ftyp brand is "heic" — the format
// an iPhone photographs a receipt in, and the one net/http's sniffer does not
// know, so this module reads the brand itself.
func testHEIC(brand string) []byte {
	box := []byte{0, 0, 0, 24}
	box = append(box, []byte("ftyp")...)
	box = append(box, []byte(brand)...)
	box = append(box, 0, 0, 0, 0)
	box = append(box, []byte("mif1")...)
	return append(box, []byte("meta and the picture itself")...)
}

// receiptForm is one multipart/form-data body carrying a single part named
// field, with the file name and declared content type a client would send.
func receiptForm(t *testing.T, field, fileName, contentType string, data []byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, fileName))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatalf("create the %q part: %v", field, err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write the %q part: %v", field, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

// postReceipt uploads one receipt and answers the whole response, for a test
// whose subject is a refusal.
func postReceipt(t *testing.T, c *modtest.Client, entryID int64, fileName, contentType string, data []byte) *modtest.Response {
	t.Helper()
	ct, body := receiptForm(t, "file", fileName, contentType, data)
	return c.Do(http.MethodPost, entryAttachmentsPath(entryID), nil, modtest.RawBody(ct, body))
}

// uploadReceipt uploads one receipt and fails the test unless it was created.
func uploadReceipt(t *testing.T, c *modtest.Client, entryID int64, fileName, contentType string, data []byte) attachmentJSON {
	t.Helper()
	r := postReceipt(t, c, entryID, fileName, contentType, data)
	if r.Status != http.StatusCreated {
		t.Fatalf("upload %s to entry %d: status %d body %s, want 201", fileName, entryID, r.Status, r.Body)
	}
	var attachment attachmentJSON
	r.JSON(&attachment)
	return attachment
}

// refusedReceipt is an upload that did not pass: the field errors of the
// validation problem it was refused with.
// refusedReceiptDelete is the field errors of a receipt delete refused by what
// the expense is right now — its status, or the period lock — which is a 400
// naming the reason rather than a bare 403.
func refusedReceiptDelete(t *testing.T, c *modtest.Client, attachmentID int64) map[string][]string {
	t.Helper()
	return refused(t, c, http.MethodDelete, attachmentPath(attachmentID), nil, invalidReceiptTitle)
}

func refusedReceipt(t *testing.T, c *modtest.Client, entryID int64, fileName, contentType string, data []byte) map[string][]string {
	t.Helper()
	r := postReceipt(t, c, entryID, fileName, contentType, data)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("upload %s to entry %d: status %d body %s, want 400", fileName, entryID, r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if problem.Title != invalidReceiptTitle {
		t.Errorf("problem title = %q, want %q", problem.Title, invalidReceiptTitle)
	}
	return problem.Errors
}

// approvalTotalJSON decodes ExpensesApprovalTotal — one currency's figures in
// one person's group of the approval queue.
type approvalTotalJSON struct {
	Currency       string  `json:"currency"`
	Gross          float64 `json:"gross"`
	OwedToEmployee float64 `json:"owedToEmployee"`
}

// claimSummaryJSON decodes ExpensesClaimSummary — one travel claim as a unit
// in the approval queue or the reimbursement list.
type claimSummaryJSON struct {
	Id              int64                 `json:"id"`
	Purpose         string                `json:"purpose"`
	Destination     *string               `json:"destination"`
	DepartureAt     string                `json:"departureAt"`
	ReturnAt        string                `json:"returnAt"`
	Project         *entryProjectJSON     `json:"project"`
	LineCount       int32                 `json:"lineCount"`
	Totals          []currencyTotalJSON   `json:"totals"`
	ReceiptsMissing int32                 `json:"receiptsMissing"`
	OverriddenRates int32                 `json:"overriddenRates"`
	Capabilities    claimCapabilitiesJSON `json:"capabilities"`

	// The two a queue row shows about the trip itself: what it is, and the
	// payroll run that paid it back once one has.
	Status        string                  `json:"status"`
	Reimbursement *entryReimbursementJSON `json:"reimbursement"`
}

// claimSummaryByID is one trip out of a group's claims, or a failed test.
func claimSummaryByID(t *testing.T, claims []claimSummaryJSON, id int64) claimSummaryJSON {
	t.Helper()
	for _, claim := range claims {
		if claim.Id == id {
			return claim
		}
	}
	t.Fatalf("travel claim %d is not among %v", id, claims)
	return claimSummaryJSON{}
}

// approvalGroupJSON decodes ExpensesApprovalGroup.
type approvalGroupJSON struct {
	User            entryOwnerJSON      `json:"user"`
	Entries         []entryJSON         `json:"entries"`
	Claims          []claimSummaryJSON  `json:"claims"`
	Totals          []approvalTotalJSON `json:"totals"`
	ReceiptsMissing int32               `json:"receiptsMissing"`
	OverriddenRates int32               `json:"overriddenRates"`
}

// approvalPageJSON decodes PaginatedResponseOfExpensesApprovalGroup.
type approvalPageJSON struct {
	Data       []approvalGroupJSON `json:"data"`
	Pagination struct {
		Page       int32 `json:"page"`
		PageSize   int32 `json:"pageSize"`
		TotalCount int32 `json:"totalCount"`
		TotalPages int32 `json:"totalPages"`
	} `json:"pagination"`
}

// flowBody is the body the four batch operations take. A nil override removes
// the field, so a test can say "with no entryIds at all" as well as "with
// these".
func flowBody(entryIDs []int64, overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{"entryIds": entryIDs}, overrides)
}

// flowResultJSON decodes ExpensesFlowResponse — what one batch moved, each
// unit in its own list.
type flowResultJSON struct {
	Entries []entryJSON     `json:"entries"`
	Claims  []claimListJSON `json:"claims"`
}

// moveUnits posts one of the six batch operations and fails the test unless it
// answered 200, returning both lists as they came back.
func moveUnits(t *testing.T, c *modtest.Client, path string, body map[string]any) flowResultJSON {
	t.Helper()
	r := c.Do(http.MethodPost, path, body)
	if r.Status != http.StatusOK {
		t.Fatalf("post %s %v: status %d body %s, want 200", path, body, r.Status, r.Body)
	}
	var moved flowResultJSON
	r.JSON(&moved)
	return moved
}

// moveEntries is moveUnits for a batch of standalone expenses alone, which is
// what most tests are about.
func moveEntries(t *testing.T, c *modtest.Client, path string, body map[string]any) []entryJSON {
	t.Helper()
	return moveUnits(t, c, path, body).Entries
}

// claimFlowBody is the body a batch of travel claims takes.
func claimFlowBody(claimIDs []int64, overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{"claimIds": claimIDs}, overrides)
}

// The four flow moves and the two payroll ones, over travel claims.
func submitClaims(t *testing.T, c *modtest.Client, ids ...int64) []claimListJSON {
	t.Helper()
	return moveUnits(t, c, submitPath, claimFlowBody(ids, nil)).Claims
}

func approveClaims(t *testing.T, c *modtest.Client, ids ...int64) []claimListJSON {
	t.Helper()
	return moveUnits(t, c, approvePath, claimFlowBody(ids, nil)).Claims
}

func rejectClaims(t *testing.T, c *modtest.Client, reason string, ids ...int64) []claimListJSON {
	t.Helper()
	return moveUnits(t, c, rejectPath, claimFlowBody(ids, map[string]any{"reason": reason})).Claims
}

func unapproveClaims(t *testing.T, c *modtest.Client, ids ...int64) []claimListJSON {
	t.Helper()
	return moveUnits(t, c, unapprovePath, claimFlowBody(ids, nil)).Claims
}

// approvedClaimBy submits as the owner and approves as the approver, the
// shortest road to an approved trip.
func approvedClaimBy(t *testing.T, owner, approver *modtest.Client, ids ...int64) {
	t.Helper()
	submitClaims(t, owner, ids...)
	approveClaims(t, approver, ids...)
}

func submitEntries(t *testing.T, c *modtest.Client, ids ...int64) []entryJSON {
	t.Helper()
	return moveEntries(t, c, submitPath, flowBody(ids, nil))
}

func approveEntries(t *testing.T, c *modtest.Client, ids ...int64) []entryJSON {
	t.Helper()
	return moveEntries(t, c, approvePath, flowBody(ids, nil))
}

func rejectEntries(t *testing.T, c *modtest.Client, reason string, ids ...int64) []entryJSON {
	t.Helper()
	return moveEntries(t, c, rejectPath, flowBody(ids, map[string]any{"reason": reason}))
}

func unapproveEntries(t *testing.T, c *modtest.Client, ids ...int64) []entryJSON {
	t.Helper()
	return moveEntries(t, c, unapprovePath, flowBody(ids, nil))
}

// refusedFlow is a batch operation that did not pass: the field errors of the
// validation problem it was refused with, under the title that operation
// carries.
func refusedFlow(t *testing.T, c *modtest.Client, path string, body map[string]any) map[string][]string {
	t.Helper()
	title := invalidApprovalTitle
	if path == submitPath {
		title = invalidSubmissionTitle
	}
	return refused(t, c, http.MethodPost, path, body, title)
}

// approvedBy submits as the owner and then approves as the approver, the
// shortest road to an approved expense.
func approvedBy(t *testing.T, owner, approver *modtest.Client, ids ...int64) {
	t.Helper()
	submitEntries(t, owner, ids...)
	approveEntries(t, approver, ids...)
}

// getApprovals reads a page of the approval queue and fails the test unless it
// answered 200. query is appended as it stands ("?page=2"), "" for none.
func getApprovals(t *testing.T, c *modtest.Client, query string) approvalPageJSON {
	t.Helper()
	r := c.Do(http.MethodGet, approvalsPath+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get approvals %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var page approvalPageJSON
	r.JSON(&page)
	return page
}

// overrideRate replaces a submitted mileage line's rate and fails the test
// unless it answered 200.
func overrideRate(t *testing.T, c *modtest.Client, id int64, body map[string]any) entryJSON {
	t.Helper()
	r := c.Do(http.MethodPut, entryRatePath(id), body)
	if r.Status != http.StatusOK {
		t.Fatalf("override the rate on %d with %v: status %d body %s, want 200", id, body, r.Status, r.Body)
	}
	var entry entryJSON
	r.JSON(&entry)
	return entry
}

// setBilling prices one expense from the project side and fails the test
// unless it answered 200.
func setBilling(t *testing.T, c *modtest.Client, id int64, body map[string]any) entryJSON {
	t.Helper()
	r := c.Do(http.MethodPut, entryBillingPath(id), body)
	if r.Status != http.StatusOK {
		t.Fatalf("price %d with %v: status %d body %s, want 200", id, body, r.Status, r.Body)
	}
	var entry entryJSON
	r.JSON(&entry)
	return entry
}

// mentions reports whether any of the messages holds the given text, so a test
// can say which refusal it expects without repeating the whole sentence.
func mentions(messages []string, text string) bool {
	for _, msg := range messages {
		if strings.Contains(msg, text) {
			return true
		}
	}
	return false
}

// downloadReceipt reads one receipt's bytes and fails the test unless it
// answered 200.
func downloadReceipt(t *testing.T, c *modtest.Client, id int64) *modtest.Response {
	t.Helper()
	r := c.Do(http.MethodGet, attachmentPath(id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("download attachment %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	return r
}

// reimbursementGroupJSON decodes ExpensesReimbursementGroup — one person's
// approved expenses that are owed back to them, with their totals.
type reimbursementGroupJSON struct {
	User    entryOwnerJSON      `json:"user"`
	Entries []entryJSON         `json:"entries"`
	Claims  []claimSummaryJSON  `json:"claims"`
	Totals  []approvalTotalJSON `json:"totals"`
}

// reimbursementPageJSON decodes PaginatedResponseOfExpensesReimbursementGroup.
type reimbursementPageJSON struct {
	Data       []reimbursementGroupJSON `json:"data"`
	Pagination struct {
		Page       int32 `json:"page"`
		PageSize   int32 `json:"pageSize"`
		TotalCount int32 `json:"totalCount"`
		TotalPages int32 `json:"totalPages"`
	} `json:"pagination"`
}

// getReimbursements reads a page of the reimbursement list and fails the test
// unless it answered 200. query is appended as it stands ("?state=reimbursed"),
// "" for none.
func getReimbursements(t *testing.T, c *modtest.Client, query string) reimbursementPageJSON {
	t.Helper()
	r := c.Do(http.MethodGet, reimbursementsPath+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get reimbursements %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var page reimbursementPageJSON
	r.JSON(&page)
	return page
}

// reimbursedBody is the body POST /reimbursed takes. A nil override removes
// the field, so a test can say "with no date at all" as well as "with this
// one".
func reimbursedBody(entryIDs []int64, overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{"entryIds": entryIDs, "date": "2026-04-02"}, overrides)
}

// markReimbursed marks expenses reimbursed and fails the test unless it
// answered 200.
func markReimbursed(t *testing.T, c *modtest.Client, body map[string]any) []entryJSON {
	t.Helper()
	return moveEntries(t, c, reimbursedPath, body)
}

// reimbursedClaimBody is the payroll run's body over travel claims.
func reimbursedClaimBody(claimIDs []int64, overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{"claimIds": claimIDs, "date": "2026-04-02"}, overrides)
}

// markClaimsReimbursed records a payroll run over trips and fails the test
// unless it answered 200.
func markClaimsReimbursed(t *testing.T, c *modtest.Client, body map[string]any) []claimListJSON {
	t.Helper()
	return moveUnits(t, c, reimbursedPath, body).Claims
}

// undoClaimsReimbursed takes the stamp back off trips.
func undoClaimsReimbursed(t *testing.T, c *modtest.Client, ids ...int64) []claimListJSON {
	t.Helper()
	return moveUnits(t, c, reimbursedUndoPath, claimFlowBody(ids, nil)).Claims
}

// undoReimbursed takes the reimbursement stamp back off and fails the test
// unless it answered 200.
func undoReimbursed(t *testing.T, c *modtest.Client, ids ...int64) []entryJSON {
	t.Helper()
	return moveEntries(t, c, reimbursedUndoPath, flowBody(ids, nil))
}

// refusedReimbursement is a reimbursement batch that did not pass: the field
// errors of the validation problem it was refused with.
func refusedReimbursement(t *testing.T, c *modtest.Client, path string, body map[string]any) map[string][]string {
	t.Helper()
	return refused(t, c, http.MethodPost, path, body, invalidReimbursementTitle)
}

// markInvoiced marks one billable line invoiced and fails the test unless it
// answered 200.
func markInvoiced(t *testing.T, c *modtest.Client, id int64, body map[string]any) entryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, entryInvoicedPath(id), body)
	if r.Status != http.StatusOK {
		t.Fatalf("mark %d invoiced with %v: status %d body %s, want 200", id, body, r.Status, r.Body)
	}
	var entry entryJSON
	r.JSON(&entry)
	return entry
}

// undoInvoiced takes the invoicing stamp back off one line and fails the test
// unless it answered 200.
func undoInvoiced(t *testing.T, c *modtest.Client, id int64, body map[string]any) entryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, entryInvoicedUndoPath(id), body)
	if r.Status != http.StatusOK {
		t.Fatalf("undo the invoicing of %d with %v: status %d body %s, want 200", id, body, r.Status, r.Body)
	}
	var entry entryJSON
	r.JSON(&entry)
	return entry
}

// currencyAmountJSON decodes ExpensesCurrencyAmount — one currency's figure in
// a dashboard reading.
type currencyAmountJSON struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

// statsJSON decodes ExpensesStatsResponse.
type statsJSON struct {
	Draft              int32                `json:"draft"`
	Submitted          int32                `json:"submitted"`
	Approved           int32                `json:"approved"`
	Rejected           int32                `json:"rejected"`
	Unreimbursed       []currencyAmountJSON `json:"unreimbursed"`
	AwaitingMyApproval int32                `json:"awaitingMyApproval"`
}

// statsSummaryJSON decodes ExpensesStatsSummaryResponse.
type statsSummaryJSON struct {
	From                    string               `json:"from"`
	To                      string               `json:"to"`
	AwaitingMyApproval      int32                `json:"awaitingMyApproval"`
	AwaitingMyApprovalDelta int32                `json:"awaitingMyApprovalDelta"`
	MyDrafts                int32                `json:"myDrafts"`
	MyUnreimbursed          []currencyAmountJSON `json:"myUnreimbursed"`
}

// bucketJSON decodes ExpensesStatsDailyBucket.
type bucketJSON struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// attentionJSON decodes ExpensesStatsAttentionItem.
type attentionJSON struct {
	Id         string `json:"id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	OccurredAt string `json:"occurredAt"`
	EntityId   string `json:"entityId"`
	Count      *int32 `json:"count"`
}

// getStats reads the module's own key figures and fails the test unless it
// answered 200.
func getStats(t *testing.T, c *modtest.Client) statsJSON {
	t.Helper()
	r := c.Do(http.MethodGet, statsPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get stats: status %d body %s, want 200", r.Status, r.Body)
	}
	var stats statsJSON
	r.JSON(&stats)
	return stats
}

// getStatsSummary reads the dashboard card and fails the test unless it
// answered 200.
func getStatsSummary(t *testing.T, c *modtest.Client, query string) statsSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, statsSummaryPath+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get stats summary %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var summary statsSummaryJSON
	r.JSON(&summary)
	return summary
}

// getTimeseries reads the dashboard's sparkline and fails the test unless it
// answered 200.
func getTimeseries(t *testing.T, c *modtest.Client, query string) []bucketJSON {
	t.Helper()
	r := c.Do(http.MethodGet, statsTimeseriesPath+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get stats timeseries %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var buckets []bucketJSON
	r.JSON(&buckets)
	return buckets
}

// getAttention reads the dashboard's attention items and fails the test unless
// it answered 200.
func getAttention(t *testing.T, c *modtest.Client) []attentionJSON {
	t.Helper()
	r := c.Do(http.MethodGet, statsAttentionPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get stats attention: status %d body %s, want 200", r.Status, r.Body)
	}
	var items []attentionJSON
	r.JSON(&items)
	return items
}

// attentionOfType is every item of one type, in the order the endpoint gave
// them.
func attentionOfType(items []attentionJSON, kind string) []attentionJSON {
	var out []attentionJSON
	for _, item := range items {
		if item.Type == kind {
			out = append(out, item)
		}
	}
	return out
}

// exportCSV reads the payroll export and fails the test unless it answered
// 200, returning the whole response so a test can read its headers too.
func exportCSV(t *testing.T, c *modtest.Client, query string) *modtest.Response {
	t.Helper()
	r := c.Do(http.MethodGet, reimbursementsExportPath+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("export %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	return r
}

// refusedExport is an export that did not pass: the field errors of the
// validation problem it was refused with.
func refusedExport(t *testing.T, c *modtest.Client, query string) map[string][]string {
	t.Helper()
	return refused(t, c, http.MethodGet, reimbursementsExportPath+query, nil, invalidExportTitle)
}

// forbidden asserts that one request was refused with the access layer's 403.
func forbidden(t *testing.T, c *modtest.Client, method, path string, body any) {
	t.Helper()
	r := c.Do(method, path, body)
	if r.Status != http.StatusForbidden {
		t.Errorf("%s %s: status %d body %s, want 403", method, path, r.Status, r.Body)
	}
}
