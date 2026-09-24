package integration_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TestMerge_RepointsTheRealProjectsAndTheSurvivorsOverviewShowsThem is
// customers merge design D1 end to end, against the other side itself: the
// real customers module merges, Compose has put the real projects module's
// CustomerReferenceHolder on its Deps, and projects' own statement moves the
// duplicate's project in the merge's transaction. The project then names the
// survivor — through projects' own read of the real customer directory — and
// the survivor's overview, which reads projects through the project directory,
// counts it, while the duplicate's counts nothing.
//
// Only projects is composed beside customers, deliberately: energy's and
// communications' holders prove their SQL in their own packages, and
// internal/module proves Compose collects every enabled holder. What only this
// package can prove is that a real merge reaches a real holder at all.
func TestMerge_RepointsTheRealProjectsAndTheSurvivorsOverviewShowsThem(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modCustomers, modProjects)
	admin, _ := h.SignInUser(t,
		"customers:view", "customers:create", "customers:merge",
		"projects:access", "projects:create", "projects:manage-all",
	)

	type created struct {
		Id int32 `json:"id"`
	}
	var survivor, absorbed created
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Acme AS"}, &survivor)
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Acme Norge AS"}, &absorbed)
	var project created
	okJSON(t, admin, http.MethodPost, projectsPath, map[string]any{
		"code":        "ACME1000",
		"name":        "Acme-migrering",
		"customerId":  absorbed.Id,
		"billingType": "time-and-materials",
		"currency":    "NOK",
	}, &project)
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("%s/%d/status", projectsPath, project.Id), map[string]any{"status": "active"}, nil)

	var merged struct {
		Moved []struct {
			Kind  string `json:"kind"`
			Count int64  `json:"count"`
		} `json:"moved"`
	}
	okJSON(t, admin, http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/merge", survivor.Id), map[string]any{"sourceId": absorbed.Id}, &merged)
	projectsMoved := int64(-1)
	for _, m := range merged.Moved {
		if m.Kind == "projects.projects" {
			projectsMoved = m.Count
		}
	}
	if projectsMoved != 1 {
		t.Errorf("moved = %+v, want projects.projects 1 from the real holder", merged.Moved)
	}

	var got struct {
		CustomerId   *int32  `json:"customerId"`
		CustomerName *string `json:"customerName"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("%s/%d", projectsPath, project.Id), nil, &got)
	if got.CustomerId == nil || *got.CustomerId != survivor.Id || got.CustomerName == nil || *got.CustomerName != "Acme AS" {
		t.Errorf("the project names customer %v %v, want the survivor %d Acme AS", got.CustomerId, got.CustomerName, survivor.Id)
	}

	type overview struct {
		Projects *struct {
			TotalCount int32 `json:"totalCount"`
			OpenCount  int32 `json:"openCount"`
			Open       []struct {
				Id int32 `json:"id"`
			} `json:"open"`
		} `json:"projects"`
	}
	var ofSurvivor, ofAbsorbed overview
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/overview", survivor.Id), nil, &ofSurvivor)
	if p := ofSurvivor.Projects; p == nil || p.TotalCount != 1 || p.OpenCount != 1 || len(p.Open) != 1 || p.Open[0].Id != project.Id {
		t.Errorf("the survivor's overview projects = %+v, want the one moved project, open", p)
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/overview", absorbed.Id), nil, &ofAbsorbed)
	if p := ofAbsorbed.Projects; p == nil || p.TotalCount != 0 {
		t.Errorf("the duplicate's overview projects = %+v, want none left", p)
	}
}
