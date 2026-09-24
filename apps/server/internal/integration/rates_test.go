package integration_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TestRates_TheRealCustomerDirectoryPricesTime is the customer default bill
// rate end to end (customers bill-rate design D2, D3): the real customers
// module stores the rate through its billing profile, the real projects module
// names the customer, and the real time module reads the rate back through
// Compose's customer directory — not Time's own fake of it — and prices an
// entry with it. The project has a currency and no default of its own, and
// the person has no rate card, so the customer step is the only one that can
// price the hours: without it the entry would be "none".
func TestRates_TheRealCustomerDirectoryPricesTime(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modCustomers, modProjects, modTime)
	admin, _ := h.SignInUser(t,
		"customers:view", "customers:create", "customers:billing-manage",
		"projects:access", "projects:create", "projects:manage-all",
		"time:access",
	)

	var customer struct {
		Id int32 `json:"id"`
	}
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": customerKraftVerketName}, &customer)
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/billing-profile", customer.Id),
		map[string]any{"currency": "NOK", "defaultBillRate": 1250}, nil)

	var project projectResponse
	okJSON(t, admin, http.MethodPost, projectsPath, map[string]any{
		"code":        "KVEM1000",
		"name":        "Kraft-Verket modernisering",
		"customerId":  customer.Id,
		"billingType": "time-and-materials",
		"currency":    "NOK",
	}, &project)
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("%s/%d/status", projectsPath, project.Id),
		map[string]any{"status": "active"}, nil)

	var entry struct {
		RateSource string `json:"rateSource"`
		Billing    *struct {
			BillRate *float64 `json:"billRate"`
			Currency *string  `json:"currency"`
		} `json:"billing"`
	}
	okJSON(t, admin, http.MethodPost, "/api/v1/time/entries",
		map[string]any{"projectId": project.Id, "entryDate": "2026-09-14", "hours": 2}, &entry)
	if entry.RateSource != "customer" {
		t.Errorf("rateSource = %q, want \"customer\"", entry.RateSource)
	}
	b := entry.Billing
	if b == nil || b.BillRate == nil || b.Currency == nil {
		t.Fatalf("billing = %+v, want 1250 NOK", b)
	}
	if *b.BillRate != 1250 || *b.Currency != "NOK" {
		t.Errorf("billing = %v %s, want 1250 NOK", *b.BillRate, *b.Currency)
	}
}
