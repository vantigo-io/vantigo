package projects_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The economy's work-type split (work types design D4): what the provider
// reports per type, named from this module's own work types and shaped by the
// rules every other figure here follows — hours for everyone who sees the
// project, the value with financial rights and a currency, the cost with
// projects:view-costs on top, absent without time tracking.

// economyWorkTypeJSON decodes ProjectEconomyWorkType. The amounts are
// pointers because absence is the shaping; the assertions whose subject is
// the absence read the raw map instead.
type economyWorkTypeJSON struct {
	Id         int32    `json:"id"`
	Name       string   `json:"name"`
	Hours      float64  `json:"hours"`
	BillAmount *float64 `json:"billAmount"`
	CostAmount *float64 `json:"costAmount"`
}

// workTypeRows is the raw workTypes array, failing the test when there is none.
func workTypeRows(t *testing.T, raw map[string]any) []map[string]any {
	t.Helper()
	list, ok := raw["workTypes"].([]any)
	if !ok {
		t.Fatalf("workTypes = %v, want an array", raw["workTypes"])
	}
	rows := make([]map[string]any, 0, len(list))
	for _, item := range list {
		row, _ := item.(map[string]any)
		rows = append(rows, row)
	}
	return rows
}

// twoTypesLogged gives the project two work types through the real API — so
// the economy has names to read — and has the fake provider report work on
// both, by id as the contract orders them: overtime first, then the weekend.
func twoTypesLogged(t *testing.T, c *modtest.Client, actuals *fakeActuals, projectID int32) (overtime, weekend workTypeJSON) {
	t.Helper()
	overtime = createWorkType(t, c, projectID, nil)
	weekend = createWorkType(t, c, projectID, map[string]any{"name": "Helg", "billMultiplierPercent": 200})
	actuals.set(projectID, loggedTotals(loggedBucket(5.5, "8775.00", "3200.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	actuals.setWorkTypes(projectID,
		contracts.WorkTypeActuals{WorkTypeID: overtime.Id, HoursHundredths: 250, BillAmount: "3375.00", CostAmount: "1400.00"},
		contracts.WorkTypeActuals{WorkTypeID: weekend.Id, HoursHundredths: 300, BillAmount: "5400.00", CostAmount: "1800.00"},
	)
	return overtime, weekend
}

// A member sees each type's name and hours and no amount; the manager
// (financial rights, a currency) the value too; a manager with
// projects:view-costs the cost as well. Rows come by name.
func TestGetProjectsByIdEconomy_WorkTypes_AreShapedPerCaller(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "WTE1000", "currency": "NOK"})
	overtime, weekend := twoTypesLogged(t, manager, actuals, project.Id)
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	costs, costsID := signIn(t, h, "projects:view-costs")
	addRole(t, h, project.Id, costsID, "manager")
	// projects:view-costs is the cost half only: a member holding it has no
	// financial rights on the project, so no value, so no cost either.
	costsMember, costsMemberID := signIn(t, h, "projects:view-costs")
	addRole(t, h, project.Id, costsMemberID, "member")

	for name, c := range map[string]*modtest.Client{"member": member, "member with view-costs": costsMember} {
		rows := workTypeRows(t, rawEconomy(t, c, project.Id))
		if len(rows) != 2 {
			t.Fatalf("%s's rows = %v, want both types", name, rows)
		}
		for _, row := range rows {
			for _, key := range []string{"billAmount", "costAmount"} {
				if _, ok := row[key]; ok {
					t.Errorf("%s's row %v carries %s, want hours alone", name, row, key)
				}
			}
		}
	}
	memberView := getEconomy(t, member, project.Id)
	if len(memberView.WorkTypes) != 2 {
		t.Fatalf("member's work types = %+v, want two rows", memberView.WorkTypes)
	}
	if memberView.WorkTypes[0].Id != weekend.Id || memberView.WorkTypes[0].Name != "Helg" || memberView.WorkTypes[0].Hours != 3 ||
		memberView.WorkTypes[1].Id != overtime.Id || memberView.WorkTypes[1].Name != "Overtid 50 %" || memberView.WorkTypes[1].Hours != 2.5 {
		t.Errorf("member's work types = %+v, want Helg 3 h then Overtid 50 %% 2.5 h", memberView.WorkTypes)
	}

	managerView := getEconomy(t, manager, project.Id)
	if len(managerView.WorkTypes) != 2 {
		t.Fatalf("manager's work types = %+v, want two rows", managerView.WorkTypes)
	}
	if managerView.WorkTypes[0].BillAmount == nil || *managerView.WorkTypes[0].BillAmount != 5400 || managerView.WorkTypes[0].CostAmount != nil {
		t.Errorf("manager's Helg = %+v, want the value and no cost", managerView.WorkTypes[0])
	}

	costView := getEconomy(t, costs, project.Id)
	if len(costView.WorkTypes) != 2 {
		t.Fatalf("view-costs' work types = %+v, want two rows", costView.WorkTypes)
	}
	if costView.WorkTypes[1].BillAmount == nil || *costView.WorkTypes[1].BillAmount != 3375 ||
		costView.WorkTypes[1].CostAmount == nil || *costView.WorkTypes[1].CostAmount != 1400 {
		t.Errorf("view-costs' Overtid = %+v, want 3375 and 1400", costView.WorkTypes[1])
	}
}

// The names are this module's own (the controller's ruling on D4): a type
// renamed on the Billing tab reads by its new name at once, a deactivated one
// keeps its row — after the active ones, where the work types list puts it —
// and an id the project does not know is left out rather than shown nameless.
func TestGetProjectsByIdEconomy_WorkTypes_AreNamedFromTheProjectsOwnTypes(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "WTE1004", "currency": "NOK"})
	overtime, weekend := twoTypesLogged(t, manager, actuals, project.Id)
	changeWorkType(t, manager, project.Id, overtime.Id, workTypeBody(map[string]any{"name": "Overtid 100 %"}))
	changeWorkType(t, manager, project.Id, weekend.Id, workTypeBody(map[string]any{"name": "Helg", "billMultiplierPercent": 200, "active": false}))
	actuals.setWorkTypes(project.Id,
		contracts.WorkTypeActuals{WorkTypeID: overtime.Id, HoursHundredths: 250, BillAmount: "3375.00", CostAmount: "1400.00"},
		contracts.WorkTypeActuals{WorkTypeID: weekend.Id, HoursHundredths: 300, BillAmount: "5400.00", CostAmount: "1800.00"},
		contracts.WorkTypeActuals{WorkTypeID: 999999, HoursHundredths: 100, BillAmount: "900.00", CostAmount: "0.00"},
	)

	got := getEconomy(t, manager, project.Id).WorkTypes
	if len(got) != 2 || got[0].Name != "Overtid 100 %" || got[1].Name != "Helg" {
		t.Errorf("work types = %+v, want the new name Overtid 100 %% then Helg (deactivated, still named, listed last), and no unknown id", got)
	}
}

// A project with no currency is an hours-only answer for everybody, cost
// rights or not — an amount in no currency is a number nobody can read.
func TestGetProjectsByIdEconomy_WorkTypes_HoursOnlyWithoutACurrency(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	manager, _ := signIn(t, h, "projects:create", "projects:view-costs")
	project := createProject(t, manager, map[string]any{"code": "WTE1001"})
	twoTypesLogged(t, manager, actuals, project.Id)

	rows := workTypeRows(t, rawEconomy(t, manager, project.Id))
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want both types", rows)
	}
	for _, row := range rows {
		if _, ok := row["billAmount"]; ok {
			t.Errorf("row %v carries billAmount on a project with no currency", row)
		}
		if _, ok := row["costAmount"]; ok {
			t.Errorf("row %v carries costAmount on a project with no currency", row)
		}
	}
}

// Without time tracking there is no split to report — absent, not empty; with
// it and no entry logged as a type, the split is an empty list.
func TestGetProjectsByIdEconomy_WorkTypes_AbsentWithoutTimeTrackingEmptyWithNone(t *testing.T) {
	t.Parallel()
	off := newHarness(t)
	offManager, _ := signIn(t, off, "projects:create")
	offProject := createProject(t, offManager, map[string]any{"code": "WTE1002", "currency": "NOK"})
	if raw := rawEconomy(t, offManager, offProject.Id); raw["workTypes"] != nil {
		t.Errorf("workTypes = %v without time tracking, want the key absent", raw["workTypes"])
	}

	actuals := newFakeActuals()
	on := newHarnessWithActuals(t, actuals)
	onManager, _ := signIn(t, on, "projects:create")
	onProject := createProject(t, onManager, map[string]any{"code": "WTE1003", "currency": "NOK"})
	actuals.set(onProject.Id, loggedTotals(loggedBucket(2, "1800.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))
	raw := rawEconomy(t, onManager, onProject.Id)
	if list, ok := raw["workTypes"].([]any); !ok || len(list) != 0 {
		t.Errorf("workTypes = %v, want an empty list when no entry picked a type", raw["workTypes"])
	}
}
