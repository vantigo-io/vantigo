package customers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/customers"
)

// TestNationalIdentityNumber_IsRefusedByEveryWriteOfAnIdentity is customers
// GDPR design D1 through the three doors an identity comes in by: the create
// body (keyed identity.id, as every nested identity error is), the
// legal-identity PUT (keyed id) and a CSV row (on its legalId column) — one
// validator behind all three, so none of them stores the number, and none of
// them says it back.
func TestNationalIdentityNumber_IsRefusedByEveryWriteOfAnIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	fnr := customers.NationalIDForTest("018190", 100, false)
	const refusal = "A Norwegian national identity number is never stored here"
	identity := map[string]any{"country": "no", "type": "person", "id": fnr, "name": "Kari Nordmann", "source": "manual"}

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Kari Nordmann", "type": "person", "identity": identity})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("create: status %d body %s, want 400", r.Status, r.Body)
	}
	var created validationProblemJSON
	r.JSON(&created)
	if got := created.Errors["identity.id"]; len(got) != 1 || got[0] != refusal {
		t.Errorf("create errors = %v, want identity.id [%q]", created.Errors, refusal)
	}
	if strings.Contains(string(r.Body), fnr[6:]) {
		t.Errorf("create body %s names the number", r.Body)
	}

	person := createCustomerOfType(t, c, "Ola Nordmann", "person")
	r = c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", person.Id), identity)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("legal-identity PUT: status %d body %s, want 400", r.Status, r.Body)
	}
	var put validationProblemJSON
	r.JSON(&put)
	if got := put.Errors["id"]; len(got) != 1 || got[0] != refusal {
		t.Errorf("legal-identity PUT errors = %v, want id [%q]", put.Errors, refusal)
	}

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"name", "type"}, legalHeader),
		[]string{"Kari Nordmann", "person", "no", "person", fnr[:6] + " " + fnr[6:], "Kari Nordmann"},
	)))
	want := []importErrorJSON{{Row: 1, Column: "legalId", Message: refusal}}
	if result.Failed != 1 || result.Created != 0 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Errorf("import = %+v, want only %+v", result, want)
	}

	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE legal_id IS NOT NULL`); n != 0 {
		t.Errorf("%d customers hold a legal id, want none", n)
	}
}
