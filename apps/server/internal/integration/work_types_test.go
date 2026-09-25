package integration_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TestWorkTypes_APickedTypeMultipliesTheProjectsEconomy is the work types
// design end to end (D1–D4): the real projects module stores a work type, the
// real time module reads it through Compose's project directory — not Time's
// own fake of it — and snapshots it onto an entry, and the real economy reads
// the multiplied value back through the real actuals provider, naming the row
// from projects' own work types (a rename reads at once). The project's
// default bill rate prices the hours and the person's card costs them, so the
// arithmetic is one line per figure:
//
//	value: 2 h × 1000 × 150 % + 1 h × 1000 = 4000
//	cost:  2 h ×  600 × 120 % + 1 h ×  600 = 2040
func TestWorkTypes_APickedTypeMultipliesTheProjectsEconomy(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modProjects, modTime)
	admin, adminID := signInAdmin(t, h)
	rates, _ := h.SignInUser(t, "time:access", "time:manage")

	var project projectResponse
	okJSON(t, admin, http.MethodPost, projectsPath, map[string]any{
		"code":            "KVEM2000",
		"name":            "Kraft-Verket overtid",
		"customerId":      customerKraftVerket,
		"billingType":     "time-and-materials",
		"currency":        "NOK",
		"defaultBillRate": 1000,
	}, &project)
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("%s/%d/status", projectsPath, project.Id),
		map[string]any{"status": "active"}, nil)
	okJSON(t, rates, http.MethodPost, "/api/v1/time/rates",
		map[string]any{"userId": adminID, "validFrom": "2026-01-01", "costRate": 600, "currency": "NOK"}, nil)

	var workType struct {
		Id   int32  `json:"id"`
		Name string `json:"name"`
	}
	okJSON(t, admin, http.MethodPost, fmt.Sprintf("%s/%d/work-types", projectsPath, project.Id),
		map[string]any{"name": "Overtid 50 %", "billMultiplierPercent": 150, "costMultiplierPercent": 120}, &workType)

	var entry struct {
		WorkType *struct {
			Id   int32  `json:"id"`
			Name string `json:"name"`
		} `json:"workType"`
		Billing *struct {
			BillRate          *float64 `json:"billRate"`
			MultiplierPercent *float64 `json:"multiplierPercent"`
			EffectiveRate     *float64 `json:"effectiveRate"`
		} `json:"billing"`
	}
	okJSON(t, admin, http.MethodPost, "/api/v1/time/entries", map[string]any{
		"projectId": project.Id, "entryDate": "2026-09-14", "hours": 2, "workTypeId": workType.Id,
	}, &entry)
	if entry.WorkType == nil || entry.WorkType.Id != workType.Id || entry.WorkType.Name != "Overtid 50 %" {
		t.Errorf("entry's work type = %+v, want the one projects stored", entry.WorkType)
	}
	b := entry.Billing
	if b == nil || b.BillRate == nil || *b.BillRate != 1000 || b.MultiplierPercent == nil || *b.MultiplierPercent != 150 ||
		b.EffectiveRate == nil || *b.EffectiveRate != 1500 {
		t.Errorf("billing = %+v, want the project's 1000 at 150 %%, 1500 an hour", b)
	}
	okJSON(t, admin, http.MethodPost, "/api/v1/time/entries",
		map[string]any{"projectId": project.Id, "entryDate": "2026-09-15", "hours": 1}, nil)

	var economy struct {
		Actuals *struct {
			TotalAmount *float64 `json:"totalAmount"`
		} `json:"actuals"`
		Cost *struct {
			Total float64 `json:"total"`
		} `json:"cost"`
		WorkTypes []struct {
			Id         int32    `json:"id"`
			Name       string   `json:"name"`
			Hours      float64  `json:"hours"`
			BillAmount *float64 `json:"billAmount"`
			CostAmount *float64 `json:"costAmount"`
		} `json:"workTypes"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf(projectEconomyPath, project.Id), nil, &economy)
	if economy.Actuals == nil || economy.Actuals.TotalAmount == nil || *economy.Actuals.TotalAmount != 4000 {
		t.Errorf("actuals = %+v, want the value of work 4000", economy.Actuals)
	}
	if economy.Cost == nil || economy.Cost.Total != 2040 {
		t.Errorf("cost = %+v, want 2040", economy.Cost)
	}
	if len(economy.WorkTypes) != 1 {
		t.Fatalf("work types = %+v, want the one type, ordinary hours in none", economy.WorkTypes)
	}
	got := economy.WorkTypes[0]
	if got.Id != workType.Id || got.Name != "Overtid 50 %" || got.Hours != 2 ||
		got.BillAmount == nil || *got.BillAmount != 3000 || got.CostAmount == nil || *got.CostAmount != 1440 {
		t.Errorf("work type row = %+v, want 2 h worth 3000 costing 1440", got)
	}

	// The row is named from projects' own work types (work types design D4,
	// as ruled on the plan's review): a rename reads at once, though the
	// entry snapshotted the old name.
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("%s/%d/work-types/%d", projectsPath, project.Id, workType.Id),
		map[string]any{"name": "Overtid 100 %", "billMultiplierPercent": 150, "costMultiplierPercent": 120}, nil)
	okJSON(t, admin, http.MethodGet, fmt.Sprintf(projectEconomyPath, project.Id), nil, &economy)
	if len(economy.WorkTypes) != 1 || economy.WorkTypes[0].Name != "Overtid 100 %" {
		t.Errorf("work types after a rename = %+v, want the one row under its new name", economy.WorkTypes)
	}
}
