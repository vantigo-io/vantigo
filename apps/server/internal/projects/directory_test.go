package projects_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// newDirectory builds the module's project directory over the harness's
// dependencies, exactly as module.Compose builds it before any module mounts.
func newDirectory(t *testing.T, h *modtest.Harness) contracts.ProjectDirectory {
	t.Helper()
	d := projects.Module().Projects
	if d == nil {
		t.Fatal("the module declares no project directory")
	}
	return d(h.Deps())
}

// TestModule_DeclaresProjectDirectory proves the module publishes a
// contracts.ProjectDirectory provider, the thing Compose wires onto every
// module's Deps.Projects (design §6).
func TestModule_DeclaresProjectDirectory(t *testing.T) {
	t.Parallel()
	if projects.Module().Projects == nil {
		t.Error("Module().Projects = nil, want a contracts.ProjectDirectory provider")
	}
}

// TestModule_ComposesProjectDirectoryOntoDeps proves what module.Compose
// actually does with that provider: once projects is one of the composed
// modules, every module's Deps.Projects — including a module that provides
// nothing of its own — carries projects' directory, non-nil. capture is
// mounted beside projects the way modtest/users_test.go's identity check
// mounts beside identity, under the "customers" name so openapi.Load has a
// real embedded contract to load for it; customers itself is never composed
// here, so nothing of its collides with projects' own routes or permissions.
func TestModule_ComposesProjectDirectoryOntoDeps(t *testing.T) {
	t.Parallel()
	var got contracts.ProjectDirectory
	capture := module.Module{
		Name: "customers",
		Mount: func(d module.Deps) (http.Handler, error) {
			got = d.Projects
			return http.NotFoundHandler(), nil
		},
	}

	newHarness(t, modtest.WithModule(capture))

	if got == nil {
		t.Error("Deps.Projects = nil, want projects' own directory once projects is composed")
	}
}

// TestDirectory_ResolvesAProject proves a project resolves with the fields
// the directory publishes, OpenForWork false for a freshly created ('planned')
// project.
func TestDirectory_ResolvesAProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR1000"})

	got, err := newDirectory(t, h).Project(context.Background(), project.Id)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got == nil || got.ID != project.Id || got.Code != "DIR1000" || got.Name != project.Name ||
		got.CustomerID == nil || project.CustomerId == nil || *got.CustomerID != *project.CustomerId ||
		got.Status != "planned" || got.OpenForWork || got.BillingType != "time-and-materials" {
		t.Errorf("Project = %+v, want the project just created (planned, not open for work, customer %v)", got, project.CustomerId)
	}
}

// TestDirectory_ActiveProjectIsOpenForWork proves OpenForWork tracks the
// status column exactly: true only for 'active', the one question time
// tracking will ask.
func TestDirectory_ActiveProjectIsOpenForWork(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR1001"})
	setStatus(t, c, project.Id, "active")

	got, err := newDirectory(t, h).Project(context.Background(), project.Id)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got == nil || got.Status != "active" || !got.OpenForWork {
		t.Errorf("Project = %+v, want status active and OpenForWork true", got)
	}
}

// TestDirectory_CancelledAndCompletedProjectsStillResolve proves the
// directory never hides a project behind its status: old hours logged
// against a project that has since wrapped up must still be able to name it.
func TestDirectory_CancelledAndCompletedProjectsStillResolve(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	dir := newDirectory(t, h)
	ctx := context.Background()

	cancelled := createProject(t, c, map[string]any{"code": "DIR1002"})
	setStatus(t, c, cancelled.Id, "cancelled")
	got, err := dir.Project(ctx, cancelled.Id)
	if err != nil {
		t.Fatalf("Project(cancelled): %v", err)
	}
	if got == nil || got.Status != "cancelled" || got.OpenForWork {
		t.Errorf("Project(cancelled) = %+v, want status cancelled and OpenForWork false", got)
	}

	completed := createProject(t, c, map[string]any{"code": "DIR1003"})
	setStatus(t, c, completed.Id, "completed")
	got, err = dir.Project(ctx, completed.Id)
	if err != nil {
		t.Fatalf("Project(completed): %v", err)
	}
	if got == nil || got.Status != "completed" || got.OpenForWork {
		t.Errorf("Project(completed) = %+v, want status completed and OpenForWork false", got)
	}
}

// TestDirectory_InternalProjectHasNoCustomer proves an internal project (no
// customerId) resolves with a nil CustomerID, never a zero one.
func TestDirectory_InternalProjectHasNoCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "DIR1004", "customerId": nil, "billingType": "non-billable",
	})

	got, err := newDirectory(t, h).Project(context.Background(), project.Id)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got == nil || got.CustomerID != nil {
		t.Errorf("Project.CustomerID = %v, want nil for an internal project", got)
	}
}

// TestDirectory_UnknownProjectIsNilWithoutAnError proves a missing project is
// (nil, nil): a caller tells "does not exist" from "the lookup failed" by
// checking err, never by reading nil as failure.
func TestDirectory_UnknownProjectIsNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := newDirectory(t, h).Project(context.Background(), 999_999)
	if got != nil || err != nil {
		t.Errorf("Project(unknown) = %+v, %v, want nil, nil", got, err)
	}
}

// TestDirectory_RoleReportsTheAssignment proves Role answers exactly the role
// a user holds, and "" — never an error — for a user who holds none. "" is
// not itself a valid role name, so it cannot be confused with one.
func TestDirectory_RoleReportsTheAssignment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, ownerID := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR1005"})
	memberID := uuid.New()
	addRole(t, h, project.Id, memberID, "member")
	dir := newDirectory(t, h)
	ctx := context.Background()

	if got, err := dir.Role(ctx, project.Id, ownerID); err != nil || got != "manager" {
		t.Errorf("Role(owner) = %q, %v, want manager, nil", got, err)
	}
	if got, err := dir.Role(ctx, project.Id, memberID); err != nil || got != "member" {
		t.Errorf("Role(member) = %q, %v, want member, nil", got, err)
	}
	if got, err := dir.Role(ctx, project.Id, uuid.New()); err != nil || got != "" {
		t.Errorf("Role(nobody) = %q, %v, want \"\", nil", got, err)
	}
}

// insertBillingLine inserts one billing_lines row directly, because the
// endpoints that manage lines are Task 10's: this task only publishes the
// read side of contracts.ProjectDirectory.BillingLine, and it must be
// testable before the write side exists.
func insertBillingLine(t *testing.T, h *modtest.Harness, projectID int32, code string, active bool) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO projects.billing_lines
			(project_id, code, variant_id, pricing_mode, fixed_amount, discount_percent, active, created_at, updated_at)
		VALUES ($1, $2, $3, 'fixed', 500.00, NULL, $4, now(), now())
		RETURNING id`,
		projectID, code, variantProjectManagerHour, active)
}

// TestDirectory_BillingLineResolvesActiveAndInactive proves both an active
// and an inactive line resolve with their pricing fields, since old hours
// must stay readable and priced after a line is deactivated.
func TestDirectory_BillingLineResolvesActiveAndInactive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR1006"})
	activeID := insertBillingLine(t, h, project.Id, "PM", true)
	inactiveID := insertBillingLine(t, h, project.Id, "PM2", false)
	dir := newDirectory(t, h)
	ctx := context.Background()

	fixed := 500.0
	wantActive := contracts.BillingLineEntry{
		ID: activeID, ProjectID: project.Id, Code: "PM", VariantID: variantProjectManagerHour,
		PricingMode: "fixed", FixedAmount: &fixed, DiscountPercent: nil, Active: true,
	}
	got, err := dir.BillingLine(ctx, project.Id, activeID)
	if err != nil {
		t.Fatalf("BillingLine(active): %v", err)
	}
	if got == nil || got.ID != wantActive.ID || got.ProjectID != wantActive.ProjectID || got.Code != wantActive.Code ||
		got.VariantID != wantActive.VariantID || got.PricingMode != wantActive.PricingMode ||
		got.FixedAmount == nil || *got.FixedAmount != fixed || got.DiscountPercent != nil || !got.Active {
		t.Errorf("BillingLine(active) = %+v, want %+v", got, wantActive)
	}

	got, err = dir.BillingLine(ctx, project.Id, inactiveID)
	if err != nil {
		t.Fatalf("BillingLine(inactive): %v", err)
	}
	if got == nil || got.Active {
		t.Errorf("BillingLine(inactive) = %+v, want a resolved, inactive line", got)
	}
}

// TestDirectory_BillingLineOfAnotherProjectIsNilWithoutAnError proves
// BillingLine is scoped to the project it is asked about: a line id that
// exists but belongs to a different project answers (nil, nil), exactly like
// an id nobody has, rather than leaking another project's line.
func TestDirectory_BillingLineOfAnotherProjectIsNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	first := createProject(t, c, map[string]any{"code": "DIR1007"})
	second := createProject(t, c, map[string]any{"code": "DIR1008"})
	lineID := insertBillingLine(t, h, first.Id, "PM", true)

	got, err := newDirectory(t, h).BillingLine(context.Background(), second.Id, lineID)
	if got != nil || err != nil {
		t.Errorf("BillingLine(wrong project) = %+v, %v, want nil, nil", got, err)
	}
}

// TestDirectory_BillingLineUnknownIsNilWithoutAnError proves an unknown line
// id is (nil, nil) too.
func TestDirectory_BillingLineUnknownIsNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR1009"})

	got, err := newDirectory(t, h).BillingLine(context.Background(), project.Id, 999_999)
	if got != nil || err != nil {
		t.Errorf("BillingLine(unknown) = %+v, %v, want nil, nil", got, err)
	}
}

// TestDirectory_ProjectsForUserListsEveryRoleOrderedByCode proves
// ProjectsForUser finds every project a user holds any role on, whatever its
// status, ordered by code rather than by creation or assignment order.
func TestDirectory_ProjectsForUserListsEveryRoleOrderedByCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	userID := uuid.New()

	zed := createProject(t, c, map[string]any{"code": "DIRZ1000"})
	alpha := createProject(t, c, map[string]any{"code": "DIRA1000"})
	mid := createProject(t, c, map[string]any{"code": "DIRM1000"})
	// other holds no role for userID and must be absent from the result.
	createProject(t, c, map[string]any{"code": "DIRX1000"})
	addRole(t, h, zed.Id, userID, "viewer")
	addRole(t, h, alpha.Id, userID, "manager")
	addRole(t, h, mid.Id, userID, "member")
	setStatus(t, c, mid.Id, "cancelled")

	got, err := newDirectory(t, h).ProjectsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ProjectsForUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ProjectsForUser = %+v, want 3 projects", got)
	}
	wantCodes := []string{"DIRA1000", "DIRM1000", "DIRZ1000"}
	for i, want := range wantCodes {
		if got[i].Code != want {
			t.Errorf("ProjectsForUser[%d].Code = %q, want %q (order by code)", i, got[i].Code, want)
		}
	}
	if got[1].Status != "cancelled" {
		t.Errorf("ProjectsForUser[1].Status = %q, want cancelled: any status must be included", got[1].Status)
	}
}

// TestDirectory_ProjectsForUserWithoutAnyRoleIsEmpty proves a user who holds
// no role anywhere gets an empty result rather than an error.
func TestDirectory_ProjectsForUserWithoutAnyRoleIsEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := newDirectory(t, h).ProjectsForUser(context.Background(), uuid.New())
	if err != nil || len(got) != 0 {
		t.Errorf("ProjectsForUser = %+v, %v, want an empty result and no error", got, err)
	}
}

// TestDirectory_ProjectsForCustomerListsEveryStatusByID proves
// ProjectsForCustomer finds every project billed to one customer, whatever its
// status, by id rather than by code — the cap cuts in id order (customer 360
// design D1), so the order is part of the contract — and nobody else's.
func TestDirectory_ProjectsForCustomerListsEveryStatusByID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	// Created in reverse code order, so id order and code order disagree and
	// the assertion below can tell which one the query used.
	zed := createProject(t, c, map[string]any{"code": "CUSZ1000"})
	mid := createProject(t, c, map[string]any{"code": "CUSM1000"})
	alpha := createProject(t, c, map[string]any{"code": "CUSA1000"})
	setStatus(t, c, mid.Id, "cancelled")
	// Neither of these bills to Kraft-Verket, and both must be absent.
	createProject(t, c, map[string]any{"code": "CUSX1000", "customerId": customerAcme})
	createProject(t, c, map[string]any{"code": "CUSI1000", "customerId": nil, "billingType": "non-billable"})

	got, err := newDirectory(t, h).ProjectsForCustomer(context.Background(), customerKraftVerket)
	if err != nil {
		t.Fatalf("ProjectsForCustomer: %v", err)
	}
	wantIDs := []int32{zed.Id, mid.Id, alpha.Id}
	if len(got) != len(wantIDs) {
		t.Fatalf("ProjectsForCustomer = %+v, want %d projects", got, len(wantIDs))
	}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Errorf("ProjectsForCustomer[%d].ID = %d, want %d (order by id, not by code)", i, got[i].ID, want)
		}
		if got[i].CustomerID == nil || *got[i].CustomerID != customerKraftVerket {
			t.Errorf("ProjectsForCustomer[%d].CustomerID = %v, want %d", i, got[i].CustomerID, customerKraftVerket)
		}
	}
	if got[1].Status != "cancelled" || got[1].OpenForWork {
		t.Errorf("ProjectsForCustomer[1] = %+v, want the cancelled project, not open for work: any status is included", got[1])
	}
}

// TestDirectory_ProjectsForCustomerUnknownIsEmpty proves a customer with no
// projects — here one nobody has — is an empty result rather than an error:
// the directory does not know which customers exist, only which projects do.
func TestDirectory_ProjectsForCustomerUnknownIsEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := newDirectory(t, h).ProjectsForCustomer(context.Background(), customerUnknown)
	if err != nil || len(got) != 0 {
		t.Errorf("ProjectsForCustomer(unknown) = %+v, %v, want an empty result and no error", got, err)
	}
}

// TestDirectory_ProjectsForCustomerStopsAtTheActualsCap proves the answer is
// never longer than contracts.MaxActualsRequests — the batch a consumer asks
// the time and expenses providers next — and that what it keeps is the lowest
// ids. The rows are inserted behind the API because two thousand and one
// creates over HTTP would be the whole test's runtime.
func TestDirectory_ProjectsForCustomerStopsAtTheActualsCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Exec(t, `INSERT INTO projects.projects (code, name, customer_id, billing_type, created_by_user_id, created_at, updated_at)
	           SELECT 'CAP' || g, 'Cap ' || g, $1::integer, 'time-and-materials', $2::uuid, now(), now()
	           FROM generate_series(1, $3::integer) AS g`,
		customerAcme, uuid.New(), contracts.MaxActualsRequests+1)
	highest := modtest.One[int32](t, h, `SELECT max(id) FROM projects.projects`)

	got, err := newDirectory(t, h).ProjectsForCustomer(context.Background(), customerAcme)
	if err != nil {
		t.Fatalf("ProjectsForCustomer: %v", err)
	}
	if len(got) != contracts.MaxActualsRequests {
		t.Fatalf("ProjectsForCustomer = %d projects, want exactly %d (the cap)", len(got), contracts.MaxActualsRequests)
	}
	if got[len(got)-1].ID == highest || got[0].ID >= got[len(got)-1].ID {
		t.Errorf("ProjectsForCustomer kept ids %d..%d, want the lowest ids with the highest (%d) cut",
			got[0].ID, got[len(got)-1].ID, highest)
	}
}

// TestDirectory_ProjectsReturnsKnownIdsAndOmitsUnknown proves Projects
// resolves every id that exists and simply omits one that does not, rather
// than erroring or leaving a hole in the slice.
func TestDirectory_ProjectsReturnsKnownIdsAndOmitsUnknown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	first := createProject(t, c, map[string]any{"code": "DIR2000"})
	second := createProject(t, c, map[string]any{"code": "DIR2001"})

	got, err := newDirectory(t, h).Projects(context.Background(), []int32{first.Id, 999_999, second.Id})
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Projects = %+v, want exactly the 2 known ids", got)
	}
	gotCodes := map[string]bool{got[0].Code: true, got[1].Code: true}
	if !gotCodes["DIR2000"] || !gotCodes["DIR2001"] {
		t.Errorf("Projects codes = %v, want DIR2000 and DIR2001", gotCodes)
	}
}

// TestDirectory_ProjectsOfNoIdsIsEmpty proves an empty id slice answers an
// empty result rather than every project (a bare `= ANY('{}')` would still
// be well-defined SQL, but the guard is what keeps a caller's empty slice
// cheap and unsurprising).
func TestDirectory_ProjectsOfNoIdsIsEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := newDirectory(t, h).Projects(context.Background(), nil)
	if err != nil || len(got) != 0 {
		t.Errorf("Projects(nil) = %+v, %v, want an empty result and no error", got, err)
	}
}

// TestDirectory_ProjectByCodeResolvesCaseInsensitively proves ProjectByCode
// finds a project by its code regardless of the case it is asked in, since
// codes are always stored upper-cased.
func TestDirectory_ProjectByCodeResolvesCaseInsensitively(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "KVEM1000"})

	got, err := newDirectory(t, h).ProjectByCode(context.Background(), "kvem1000")
	if err != nil {
		t.Fatalf("ProjectByCode: %v", err)
	}
	if got == nil || got.ID != project.Id || got.Code != "KVEM1000" {
		t.Errorf("ProjectByCode(lower-case) = %+v, want the project created as KVEM1000", got)
	}
}

// TestDirectory_ProjectByCodeUnknownIsNilWithoutAnError proves a code nobody
// has is (nil, nil).
func TestDirectory_ProjectByCodeUnknownIsNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := newDirectory(t, h).ProjectByCode(context.Background(), "NOSUCH1")
	if got != nil || err != nil {
		t.Errorf("ProjectByCode(unknown) = %+v, %v, want nil, nil", got, err)
	}
}

// TestDirectory_BillingLinesReturnsActiveAndInactiveOrderedByCode proves
// BillingLines lists every line on a project, active and inactive, ordered
// by code — the read side a caller filters itself, rather than the
// directory deciding which lines matter to them.
func TestDirectory_BillingLinesReturnsActiveAndInactiveOrderedByCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR2002"})
	insertBillingLine(t, h, project.Id, "ZZ", true)
	insertBillingLine(t, h, project.Id, "AA", false)

	got, err := newDirectory(t, h).BillingLines(context.Background(), project.Id)
	if err != nil {
		t.Fatalf("BillingLines: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("BillingLines = %+v, want 2 lines", got)
	}
	if got[0].Code != "AA" || got[1].Code != "ZZ" {
		t.Errorf("BillingLines codes = [%s %s], want [AA ZZ] (ordered by code)", got[0].Code, got[1].Code)
	}
	if got[0].Active {
		t.Errorf("BillingLines[0] (AA) Active = true, want false")
	}
	if !got[1].Active {
		t.Errorf("BillingLines[1] (ZZ) Active = false, want true")
	}
}

// insertTask inserts one projects.tasks row directly, because the endpoints
// that manage tasks are Task 2's: this task only publishes the read side of
// contracts.ProjectDirectory.Task/OpenTasksForUser, and it must be testable
// before the write side exists.
func insertTask(t *testing.T, h *modtest.Harness, projectID int32, title, status string, assigneeUserID *uuid.UUID, dueDate *time.Time, position int32) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO projects.tasks
			(project_id, title, status, assignee_user_id, due_date, position, created_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
		RETURNING id`,
		projectID, title, status, assigneeUserID, dueDate, position, uuid.New())
}

// TestDirectory_TaskResolvesARow proves Task resolves a task with the fields
// the directory publishes.
func TestDirectory_TaskResolvesARow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR2003"})
	assignee := uuid.New()
	dueDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	taskID := insertTask(t, h, project.Id, "Write the spec", "in-progress", &assignee, &dueDate, 0)

	got, err := newDirectory(t, h).Task(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if got == nil || got.ID != taskID || got.ProjectID != project.Id || got.Title != "Write the spec" ||
		got.Status != "in-progress" || got.AssigneeUserID == nil || *got.AssigneeUserID != assignee ||
		got.DueDate == nil || !got.DueDate.Equal(dueDate) {
		t.Errorf("Task = %+v, want the task just inserted", got)
	}
}

// TestDirectory_TaskUnknownIsNilWithoutAnError proves an unknown (or
// deleted — there is no soft-delete flag) task id is (nil, nil).
func TestDirectory_TaskUnknownIsNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := newDirectory(t, h).Task(context.Background(), 999_999)
	if got != nil || err != nil {
		t.Errorf("Task(unknown) = %+v, %v, want nil, nil", got, err)
	}
}

// TestDirectory_OpenTasksForUserExcludesDoneAndOrdersByDueDate proves
// OpenTasksForUser lists only a user's non-done tasks, earliest due date
// first, nulls last.
func TestDirectory_OpenTasksForUserExcludesDoneAndOrdersByDueDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR2004"})
	userID := uuid.New()
	later := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	sooner := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	noDueDate := insertTask(t, h, project.Id, "No due date", "todo", &userID, nil, 0)
	laterID := insertTask(t, h, project.Id, "Later", "todo", &userID, &later, 1)
	soonerID := insertTask(t, h, project.Id, "Sooner", "in-progress", &userID, &sooner, 2)
	insertTask(t, h, project.Id, "Already done", "done", &userID, &sooner, 3)
	otherUser := uuid.New()
	insertTask(t, h, project.Id, "Someone else's", "todo", &otherUser, nil, 4)

	got, err := newDirectory(t, h).OpenTasksForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("OpenTasksForUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("OpenTasksForUser = %+v, want 3 open tasks", got)
	}
	wantOrder := []int32{soonerID, laterID, noDueDate}
	for i, wantID := range wantOrder {
		if got[i].ID != wantID {
			t.Errorf("OpenTasksForUser[%d].ID = %d, want %d (due date ascending, nulls last)", i, got[i].ID, wantID)
		}
	}
}

// TestDirectory_CanLogTime proves CanLogTime is true only for a member or
// manager of an active project, and false for a viewer, for every other
// project status, and for an outsider who holds no role at all.
func TestDirectory_CanLogTime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, ownerID := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR2005"})
	memberID := uuid.New()
	viewerID := uuid.New()
	outsiderID := uuid.New()
	addRole(t, h, project.Id, memberID, "member")
	addRole(t, h, project.Id, viewerID, "viewer")
	setStatus(t, c, project.Id, "active")
	dir := newDirectory(t, h)
	ctx := context.Background()

	if ok, err := dir.CanLogTime(ctx, project.Id, ownerID); err != nil || !ok {
		t.Errorf("CanLogTime(manager, active) = %v, %v, want true, nil", ok, err)
	}
	if ok, err := dir.CanLogTime(ctx, project.Id, memberID); err != nil || !ok {
		t.Errorf("CanLogTime(member, active) = %v, %v, want true, nil", ok, err)
	}
	if ok, err := dir.CanLogTime(ctx, project.Id, viewerID); err != nil || ok {
		t.Errorf("CanLogTime(viewer, active) = %v, %v, want false, nil", ok, err)
	}
	if ok, err := dir.CanLogTime(ctx, project.Id, outsiderID); err != nil || ok {
		t.Errorf("CanLogTime(outsider, active) = %v, %v, want false, nil", ok, err)
	}

	for _, status := range []string{"planned", "on-hold", "completed", "cancelled"} {
		setStatus(t, c, project.Id, status)
		if ok, err := dir.CanLogTime(ctx, project.Id, memberID); err != nil || ok {
			t.Errorf("CanLogTime(member, %s) = %v, %v, want false, nil", status, ok, err)
		}
	}
}

// TestDirectory_ProjectEntryCarriesCurrencyAndDefaultBillRate proves
// Currency and DefaultBillRate round-trip onto ProjectEntry: nil when
// unset, and the stored value once set. default_bill_rate has no write
// endpoint yet (Task 2), so it is set directly.
func TestDirectory_ProjectEntryCarriesCurrencyAndDefaultBillRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "DIR2006", "currency": "NOK"})
	ctx := context.Background()
	dir := newDirectory(t, h)

	got, err := dir.Project(ctx, project.Id)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got == nil || got.Currency == nil || *got.Currency != "NOK" || got.DefaultBillRate != nil {
		t.Errorf("Project = %+v, want Currency NOK and DefaultBillRate nil (unset)", got)
	}

	h.Exec(t, `UPDATE projects.projects SET default_bill_rate = 950.00 WHERE id = $1`, project.Id)
	got, err = dir.Project(ctx, project.Id)
	if err != nil {
		t.Fatalf("Project (after setting default_bill_rate): %v", err)
	}
	if got == nil || got.DefaultBillRate == nil || *got.DefaultBillRate != 950.0 {
		t.Errorf("Project.DefaultBillRate = %v, want 950", got)
	}
}
