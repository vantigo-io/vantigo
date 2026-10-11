package staff_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func kin(name, relationship string, extra ...any) map[string]any {
	return with(map[string]any{"name": name, "relationship": relationship}, extra...)
}

// A person's next of kin are replaced whole, at most three, in the order
// sent; an empty list removes them. The answer is the list as stored.
func TestNextOfKin_FullReplace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))

	var got []gen.StaffNextOfKin
	do(t, hr, http.MethodGet, nextOfKinPath(p.Id), nil, http.StatusOK, &got)
	if got == nil || len(got) != 0 {
		t.Errorf("none yet = %#v, want []", got)
	}
	do(t, hr, http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": []any{
		kin(" Ola Nordmann ", "spouse", "phone", "+47 900 00 001", "note", "Call after 16"),
		kin("Per Nordmann", "child"),
		kin("Anne Nordmann", "parent"),
	}}, http.StatusOK, &got)
	if len(got) != 3 || got[0].Name != "Ola Nordmann" || got[0].Phone == nil || *got[0].Phone != "+47 900 00 001" ||
		got[1].Relationship != "child" || got[1].Phone != nil || got[2].Name != "Anne Nordmann" {
		t.Errorf("replaced = %+v, want three in the order sent, trimmed", got)
	}
	do(t, hr, http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": []any{kin("Per Nordmann", "child")}}, http.StatusOK, &got)
	var read []gen.StaffNextOfKin
	do(t, hr, http.MethodGet, nextOfKinPath(p.Id), nil, http.StatusOK, &read)
	if len(read) != 1 || read[0].Name != "Per Nordmann" {
		t.Errorf("after a second replace = %+v, want Per alone", read)
	}
	do(t, hr, http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": []any{}}, http.StatusOK, &got)
	if n := h.Count(t, `SELECT count(*) FROM staff.next_of_kin WHERE person_id = $1`, p.Id); n != 0 || len(got) != 0 {
		t.Errorf("after an empty replace: %d rows, answer %v, want none", n, got)
	}
	r := hr.Do(http.MethodPut, nextOfKinPath(p.Id+1000), map[string]any{"nextOfKin": []any{}})
	if r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unknown person: %d %q, want a bare 404", r.Status, r.Body)
	}
}

// Each next of kin's field is judged and named by its place in the list; a
// fourth is refused on the list; nothing is written when anything fails.
func TestNextOfKin_Validation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	do(t, hr, http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": []any{kin("Ola", "spouse")}}, http.StatusOK, nil)

	for _, c := range []struct {
		name  string
		list  []any
		field string
	}{
		{"four", []any{kin("A", "other"), kin("B", "other"), kin("C", "other"), kin("D", "other")}, "nextOfKin"},
		{"no name", []any{kin("A", "other"), kin(" ", "other")}, "nextOfKin[1].name"},
		{"name 201", []any{kin(strings.Repeat("a", 201), "other")}, "nextOfKin[0].name"},
		{"relationship outside the list", []any{kin("A", "cousin")}, "nextOfKin[0].relationship"},
		{"phone 33", []any{kin("A", "other", "phone", strings.Repeat("1", 33))}, "nextOfKin[0].phone"},
		{"note 501", []any{kin("A", "other", "note", strings.Repeat("a", 501))}, "nextOfKin[0].note"},
	} {
		p := refused(t, hr.Do(http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": c.list}), http.StatusBadRequest, "")
		if len(p.Errors[c.field]) == 0 {
			t.Errorf("%s: errors = %v, want %s named", c.name, p.Errors, c.field)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.next_of_kin WHERE person_id = $1 AND name = 'Ola'`, p.Id); n != 1 {
		t.Errorf("a refused replace changed the stored list")
	}
}
