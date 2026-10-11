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

	var got gen.StaffPersonRoleList
	do(t, hr, http.MethodGet, rolesPath(p.Id), nil, http.StatusOK, &got)
	if got.Roles == nil || len(got.Roles) != 0 || got.Revision != p.Revision {
		t.Errorf("none yet = %#v, want [] at revision %d", got, p.Revision)
	}
	got = putRoles(t, h, hr, p.Id, map[string]any{"roles": []any{
		map[string]any{"role": "verneombud", "fromDate": "2026-01-01", "toDate": "2027-12-31"},
		map[string]any{"role": "amu_member"},
		map[string]any{"role": "verneombud", "fromDate": "2028-01-01"},
	}})
	r := got.Roles
	if len(r) != 3 || r[0].Role != "verneombud" || r[0].ToDate == nil || r[0].ToDate.String() != "2027-12-31" ||
		r[1].FromDate != nil || r[2].FromDate.String() != "2028-01-01" || r[2].ToDate != nil {
		t.Errorf("replaced = %+v, want three in the order sent", r)
	}
	if got.Revision != p.Revision+1 {
		t.Errorf("revision after a change = %d, want %d", got.Revision, p.Revision+1)
	}
	refused(t, hr.Do(http.MethodPut, rolesPath(p.Id), map[string]any{"roles": []any{}}, ifMatch(p.Revision)),
		http.StatusConflict, "revision_conflict")
	got = putRoles(t, h, hr, p.Id, map[string]any{"roles": []any{}})
	if n := h.Count(t, `SELECT count(*) FROM staff.person_roles WHERE person_id = $1`, p.Id); n != 0 || len(got.Roles) != 0 {
		t.Errorf("after an empty replace: %d rows, answer %v, want none", n, got)
	}
	resp := hr.Do(http.MethodGet, rolesPath(p.Id+1000), nil)
	if resp.Status != http.StatusNotFound || len(resp.Body) != 0 {
		t.Errorf("unknown person: %d %q, want a bare 404", resp.Status, resp.Body)
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
		{"twenty-one", twentyOneRoles(), "roles"},
	} {
		pr := refused(t, hr.Do(http.MethodPut, rolesPath(p.Id), map[string]any{"roles": c.roles}, ifMatch(personRevision(t, h, p.Id))), http.StatusBadRequest, "")
		if len(pr.Errors[c.field]) == 0 {
			t.Errorf("%s: errors = %v, want %s named", c.name, pr.Errors, c.field)
		}
	}
}

func twentyOneRoles() []any {
	out := make([]any, 21)
	for i := range out {
		out[i] = map[string]any{"role": "amu_member"}
	}
	return out
}
