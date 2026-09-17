package projects_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /api/v1/projects/{id}: design §5's authorization matrix, D7's "an
// outsider gets not-found, not forbidden" and D12's financial shaping. Every
// case reads the *same* project through a differently-permissioned caller,
// so what changes in the answer is only what the caller may see.

// seedProject creates one project through a caller who holds
// projects:create, and returns the harness, that project and its creator's
// client, for a test whose subject is some *other* caller reading it.
func seedProject(t *testing.T, h *modtest.Harness, code string) (projectJSON, *modtest.Client) {
	t.Helper()
	creator, _ := signIn(t, h, "projects:create")
	return createProject(t, creator, map[string]any{"code": code}), creator
}

// getProject reads one project and returns the response, so each case can
// assert on the status before decoding.
func getProject(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", id), nil)
}

// The creator is the project's manager (D6), so they see everything.
func TestGetProjectsById_Creator_SeesTheProjectAndItsFinancials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	created, creator := seedProject(t, h, "SEE1000")

	r := getProject(t, creator, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var project projectJSON
	r.JSON(&project)
	if project.Id != created.Id || project.Code != created.Code {
		t.Errorf("read back %+v, want the project that was created (%d, %q)", project, created.Id, created.Code)
	}
	if !project.Capabilities.CanManage || !project.Capabilities.CanSeeFinancials {
		t.Errorf("Capabilities = %+v, want both true for the project's manager", project.Capabilities)
	}
	if project.Financials == nil {
		t.Error("Financials is absent, want it present for the project's manager")
	}
	if len(project.Managers) != 1 {
		t.Errorf("Managers = %+v, want exactly the creator", project.Managers)
	}
}

// D7: a caller who holds no role on the project, and neither view-all nor
// manage-all, must not be able to tell the project apart from one that does
// not exist.
func TestGetProjectsById_Outsider_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	created, _ := seedProject(t, h, "OUT1000")
	outsider, _ := signIn(t, h)

	existing := getProject(t, outsider, created.Id)
	if existing.Status != http.StatusNotFound {
		t.Fatalf("existing project: status %d body %s, want 404", existing.Status, existing.Body)
	}
	unknown := getProject(t, outsider, 999999)
	if unknown.Status != http.StatusNotFound {
		t.Fatalf("unknown id: status %d body %s, want 404", unknown.Status, unknown.Body)
	}
	// Indistinguishable, byte for byte: a different body would leak that the
	// first project exists.
	if string(existing.Body) != string(unknown.Body) {
		t.Errorf("outsider's 404 body %q differs from an unknown id's %q", existing.Body, unknown.Body)
	}
}

func TestGetProjectsById_UnknownId_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:view-all")

	if r := getProject(t, c, 999999); r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// D12: view-all sees the project; the money is simply not in its copy of the
// resource. The assertion is on the raw JSON object, not the decoded struct:
// a nil pointer cannot tell an absent key from a null one, and absent is the
// contract.
func TestGetProjectsById_ViewAll_SeesTheProjectWithoutItsFinancials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	created, _ := seedProject(t, h, "VIEW1000")
	viewer, _ := signIn(t, h, "projects:view-all")

	r := getProject(t, viewer, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var raw map[string]any
	if err := json.Unmarshal(r.Body, &raw); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
	if _, present := raw["financials"]; present {
		t.Errorf("body %s carries a 'financials' key, want it absent (not null) for a caller who may not see it", r.Body)
	}

	var project projectJSON
	r.JSON(&project)
	if project.Capabilities.CanManage {
		t.Error("Capabilities.CanManage = true, want false: view-all sees, it does not manage")
	}
	if project.Capabilities.CanSeeFinancials {
		t.Error("Capabilities.CanSeeFinancials = true, want false")
	}
	// What everyone who sees the project sees stays visible.
	if project.BillingType != "time-and-materials" || project.CustomerName == nil {
		t.Errorf("read back %+v, want the non-financial fields intact", project)
	}
}

// view-financials widens what a caller sees on the projects they can already
// see; it is view-all that decides whether they can see this one at all.
func TestGetProjectsById_ViewAllWithViewFinancials_SeesFinancials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	created, _ := seedProject(t, h, "FIN1000")
	viewer, _ := signIn(t, h, "projects:view-all", "projects:view-financials")

	r := getProject(t, viewer, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var project projectJSON
	r.JSON(&project)
	if project.Financials == nil {
		t.Error("Financials is absent, want it present for a caller holding projects:view-financials")
	}
	if !project.Capabilities.CanSeeFinancials {
		t.Error("Capabilities.CanSeeFinancials = false, want true")
	}
	if project.Capabilities.CanManage {
		t.Error("Capabilities.CanManage = true, want false: seeing the money is not managing the project")
	}
}

// view-financials on its own grants nothing: it applies only to projects the
// caller can already see, so a project they cannot see stays a 404.
func TestGetProjectsById_ViewFinancialsWithoutVisibility_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	created, _ := seedProject(t, h, "FINONLY1000")
	outsider, _ := signIn(t, h, "projects:view-financials")

	if r := getProject(t, outsider, created.Id); r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// manage-all implies seeing the project and its financials (design §5).
func TestGetProjectsById_ManageAll_CanManageAndSeeFinancials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	created, _ := seedProject(t, h, "MAN1000")
	admin, _ := signIn(t, h, "projects:manage-all")

	r := getProject(t, admin, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var project projectJSON
	r.JSON(&project)
	if !project.Capabilities.CanManage || !project.Capabilities.CanSeeFinancials {
		t.Errorf("Capabilities = %+v, want both true for projects:manage-all", project.Capabilities)
	}
	if project.Financials == nil {
		t.Error("Financials is absent, want it present for projects:manage-all")
	}
}

// Every operation requires projects:access (D8), the read included.
func TestGetProjectsById_WithoutAccessPermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	created, _ := seedProject(t, h, "ACC1000")
	c := h.SignIn(t, "projects:view-all")

	if r := getProject(t, c, created.Id); r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}
