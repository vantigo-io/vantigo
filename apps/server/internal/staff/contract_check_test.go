package staff_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func contractStates(check gen.StaffContractCheck) map[string]string {
	out := map[string]string{}
	for _, i := range check.Items {
		out[i.Item] = string(i.State)
	}
	return out
}

// The § 14-6 check answers the seventeen items from the terms in force:
// for a permanent employee with no probation, special arrangement, tariff or
// hirer, e, f, l, n and o are not applicable and the rest of a bare row is
// missing; filled in, they are present, each with its source. contractSigned
// follows the employment's signing date. The person, a manager of theirs and
// HR read it; a colleague, who reads no more than the dates, does not.
func TestContractCheck_ThroughTheAPI(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	unit := company(t, hr)
	p, e := employed(t, hr, "Kari", "2026-01-01", terms())

	var check gen.StaffContractCheck
	do(t, hr, http.MethodGet, contractCheckPath(e.Id), nil, http.StatusOK, &check)
	if len(check.Items) != 17 || check.ContractSigned || check.EmploymentId != e.Id {
		t.Fatalf("check = %+v, want seventeen items, unsigned", check)
	}
	got := contractStates(check)
	for item, want := range map[string]string{"a": "present", "d": "present", "b": "missing", "i": "missing", "q": "missing",
		"e": "not_applicable", "f": "not_applicable", "l": "not_applicable", "n": "not_applicable", "o": "not_applicable"} {
		if got[item] != want {
			t.Errorf("bare row: item %s = %s, want %s", item, got[item], want)
		}
	}
	for _, i := range check.Items {
		if i.Source == "" {
			t.Errorf("item %s names no source", i.Item)
		}
	}

	loc := createLocation(t, hr, "Oslo office").Id
	addTerms(t, hr, e.Id, terms("validFrom", "2026-02-01", "locationId", loc, "title", "Developer", "probationEndDate", "2026-06-30",
		"payComponents", []any{map[string]any{"name": "Base", "amount": 50000, "currency": "NOK", "period": "monthly"}},
		"payMethod", "Bank transfer", "paydayText", "The 20th"))
	do(t, hr, http.MethodPut, employmentPath(e.Id), map[string]any{"startDate": "2026-01-01", "relationshipType": "ordinary",
		"businessUnitId": unit.Id, "contractSignedAt": "2025-12-15"}, http.StatusOK, nil, ifMatch(getEmployment(t, hr, e.Id).Revision))
	do(t, hr, http.MethodGet, contractCheckPath(e.Id), nil, http.StatusOK, &check)
	got = contractStates(check)
	for item, want := range map[string]string{"b": "present", "c": "present", "f": "present", "i": "present", "e": "not_applicable"} {
		if got[item] != want {
			t.Errorf("filled in: item %s = %s, want %s", item, got[item], want)
		}
	}
	if !check.ContractSigned || check.ContractSignedAt == nil {
		t.Errorf("check = %+v, want signed", check)
	}

	self, user := signIn(t, h)
	link(t, h, p.Id, user)
	do(t, self, http.MethodGet, contractCheckPath(e.Id), nil, http.StatusOK, nil)
	colleague, _ := signIn(t, h, "staff:view")
	unknown := colleague.Do(http.MethodGet, contractCheckPath(e.Id+1000), nil)
	if r := colleague.Do(http.MethodGet, contractCheckPath(e.Id), nil); r.Status != http.StatusNotFound || string(r.Body) != string(unknown.Body) {
		t.Errorf("colleague: %d %q, want the unknown id's 404", r.Status, r.Body)
	}
}

// createLocation adds a location and answers it.
func createLocation(t *testing.T, c *modtest.Client, name string) gen.StaffLocation {
	t.Helper()
	var out gen.StaffLocation
	do(t, c, http.MethodPost, locationsPath, map[string]any{"name": name}, http.StatusCreated, &out)
	return out
}
