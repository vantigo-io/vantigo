package staff_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// A person's roles (verneombud, AMU member, HMS responsible) are replaced
// whole, each with the dates it was elected for, in the order sent.
func TestPersonRoles_FullReplace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))

	var got []gen.StaffPersonRole
	do(t, hr, http.MethodGet, rolesPath(p.Id), nil, http.StatusOK, &got)
	if got == nil || len(got) != 0 {
		t.Errorf("none yet = %#v, want []", got)
	}
	do(t, hr, http.MethodPut, rolesPath(p.Id), map[string]any{"roles": []any{
		map[string]any{"role": "verneombud", "fromDate": "2026-01-01", "toDate": "2027-12-31"},
		map[string]any{"role": "amu_member"},
		map[string]any{"role": "verneombud", "fromDate": "2028-01-01"},
	}}, http.StatusOK, &got)
	if len(got) != 3 || got[0].Role != "verneombud" || got[0].ToDate == nil || got[0].ToDate.String() != "2027-12-31" ||
		got[1].FromDate != nil || got[2].FromDate.String() != "2028-01-01" || got[2].ToDate != nil {
		t.Errorf("replaced = %+v, want three in the order sent", got)
	}
	do(t, hr, http.MethodPut, rolesPath(p.Id), map[string]any{"roles": []any{}}, http.StatusOK, &got)
	if n := h.Count(t, `SELECT count(*) FROM staff.person_roles WHERE person_id = $1`, p.Id); n != 0 || len(got) != 0 {
		t.Errorf("after an empty replace: %d rows, answer %v, want none", n, got)
	}
	r := hr.Do(http.MethodGet, rolesPath(p.Id+1000), nil)
	if r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unknown person: %d %q, want a bare 404", r.Status, r.Body)
	}
}

// A role outside the list, or a period that ends before it starts, is named
// by its place in the list.
func TestPersonRoles_Validation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	for _, c := range []struct {
		name  string
		roles []any
		field string
	}{
		{"role outside the list", []any{map[string]any{"role": "chair"}}, "roles[0].role"},
		{"ends before it starts", []any{map[string]any{"role": "amu_member"}, map[string]any{"role": "verneombud", "fromDate": "2026-05-01", "toDate": "2026-04-30"}}, "roles[1].toDate"},
	} {
		pr := refused(t, hr.Do(http.MethodPut, rolesPath(p.Id), map[string]any{"roles": c.roles}), http.StatusBadRequest, "")
		if len(pr.Errors[c.field]) == 0 {
			t.Errorf("%s: errors = %v, want %s named", c.name, pr.Errors, c.field)
		}
	}
}
