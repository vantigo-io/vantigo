package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestPersonalData_TheRealProjectsModuleIsAskedAndKeepsItsProjects is customers
// GDPR design D2 end to end, against the other side itself: the export reads
// the real projects module's section through the slot Compose filled, and the
// worker — found through module.Workers, which collects the same slot in worker
// mode — asks the real projects module to erase and records that it did, while
// the project itself stays, naming the anonymised customer. Only projects is
// composed beside customers, for the merge test's reason: each module proves
// its own SQL in its own package, and what only this package can prove is that
// a real anonymisation reaches a real implementation at all.
func TestPersonalData_TheRealProjectsModuleIsAskedAndKeepsItsProjects(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modCustomers, modProjects)
	admin, _ := h.SignInUser(t,
		"customers:view", "customers:create", "customers:delete", "customers:personal-data", "customers:timeline-view",
		"projects:access", "projects:create", "projects:manage-all",
	)

	type created struct {
		Id int32 `json:"id"`
	}
	var person, project created
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Kari Nordmann", "type": "person"}, &person)
	okJSON(t, admin, http.MethodPost, projectsPath, map[string]any{
		"code": "KARI1000", "name": "Varmepumpe", "customerId": person.Id,
		"billingType": "time-and-materials", "currency": "NOK",
	}, &project)

	var file struct {
		Modules map[string]json.RawMessage `json:"modules"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/personal-data", person.Id), nil, &file)
	var section struct {
		Projects []struct {
			Code string `json:"code"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(file.Modules["projects"], &section); err != nil || len(section.Projects) != 1 || section.Projects[0].Code != "KARI1000" {
		t.Fatalf("modules.projects = %s, want the real module's one project", file.Modules["projects"])
	}

	if r := admin.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/anonymisation", person.Id),
		map[string]any{"anonymiseOn": h.Now().UTC().Format("2006-01-02")}, nil)

	var worker *customers.AnonymisationWorker
	for _, w := range module.Workers(h.Deps(), customers.Module(), moduleNamed(t, modProjects)) {
		if aw, ok := w.(*customers.AnonymisationWorker); ok {
			worker = aw
		}
	}
	if worker == nil {
		t.Fatal("module.Workers built no anonymisation worker")
	}
	if ran, err := worker.RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v", ran, err)
	}

	var timeline struct {
		Data []struct {
			EventType string          `json:"eventType"`
			Payload   json.RawMessage `json:"payload"`
		} `json:"data"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline", person.Id), nil, &timeline)
	var payload struct {
		Erased []struct {
			Kind  string `json:"kind"`
			Count int64  `json:"count"`
		} `json:"erased"`
	}
	if len(timeline.Data) == 0 || timeline.Data[0].EventType != "customer.anonymised" || json.Unmarshal(timeline.Data[0].Payload, &payload) != nil {
		t.Fatalf("newest entry = %+v, want customer.anonymised", timeline.Data)
	}
	asked := false
	for _, e := range payload.Erased {
		asked = asked || (e.Kind == "projects.projects" && e.Count == 0)
	}
	if !asked {
		t.Errorf("erased = %+v, want projects.projects asked and keeping its rows", payload.Erased)
	}

	var got struct {
		CustomerId   *int32  `json:"customerId"`
		CustomerName *string `json:"customerName"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("%s/%d", projectsPath, project.Id), nil, &got)
	if got.CustomerId == nil || *got.CustomerId != person.Id || got.CustomerName == nil || *got.CustomerName != "Anonymised person" {
		t.Errorf("the project names %v %v, want the anonymised customer %d", got.CustomerId, got.CustomerName, person.Id)
	}
}
