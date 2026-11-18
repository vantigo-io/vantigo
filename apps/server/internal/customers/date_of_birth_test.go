package customers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type dateOfBirthJSON struct {
	DateOfBirth *string `json:"dateOfBirth"`
	Revision    int32   `json:"revision"`
}

// A person's date of birth (invoices phase 6 design I30): set, read, cleared;
// refused on a business and outside its range; cleared when the person
// becomes a business; and answered by the billing profile.
func TestDateOfBirth_PersonOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Kari Nordmann")
	path := fmt.Sprintf("/api/v1/customers/%d/date-of-birth", created.Id)

	if r := c.Do(http.MethodPut, path, map[string]any{"dateOfBirth": "1980-05-17"}); r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "date_of_birth_person_only") {
		t.Fatalf("a business's date of birth: %d %s, want 400 date_of_birth_person_only", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{"type": "person"}); r.Status != http.StatusOK {
		t.Fatalf("to person: %d %s", r.Status, r.Body)
	}
	for _, bad := range []string{"1899-12-31", "2999-01-01"} {
		if r := c.Do(http.MethodPut, path, map[string]any{"dateOfBirth": bad}); r.Status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", bad, r.Status, r.Body)
		}
	}
	r := c.Do(http.MethodPut, path, map[string]any{"dateOfBirth": "1980-05-17"})
	if r.Status != http.StatusOK {
		t.Fatalf("set: %d %s", r.Status, r.Body)
	}
	var got dateOfBirthJSON
	r = c.Do(http.MethodGet, path, nil)
	r.JSON(&got)
	if got.DateOfBirth == nil || *got.DateOfBirth != "1980-05-17" {
		t.Fatalf("read back %+v", got)
	}
	if r := c.Do(http.MethodPut, path, map[string]any{"dateOfBirth": "1980-05-17", "revision": got.Revision - 1}); r.Status != http.StatusConflict {
		t.Errorf("a stale revision: %d, want 409", r.Status)
	}

	// Back to a business: the date goes with the type.
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{"type": "business"}); r.Status != http.StatusOK {
		t.Fatalf("to business: %d %s", r.Status, r.Body)
	}
	got = dateOfBirthJSON{}
	c.Do(http.MethodGet, path, nil).JSON(&got)
	if got.DateOfBirth != nil {
		t.Errorf("after the change to business: %v, want none", *got.DateOfBirth)
	}
}
