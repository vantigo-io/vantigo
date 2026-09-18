package projects_test

import (
	"context"
	"net/http"
	"testing"

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
