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
// sent; an empty list removes them. The answer is the list as stored with
// the person's revision, which a replace that changes the list moves on.
func TestNextOfKin_FullReplace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))

	var got gen.StaffNextOfKinList
	do(t, hr, http.MethodGet, nextOfKinPath(p.Id), nil, http.StatusOK, &got)
	if got.NextOfKin == nil || len(got.NextOfKin) != 0 || got.Revision != p.Revision {
		t.Errorf("none yet = %#v, want [] at revision %d", got, p.Revision)
	}
	got = putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{
		kin(" Ola Nordmann ", "spouse", "phone", "+47 900 00 001", "note", "Call after 16"),
		kin("Per Nordmann", "child"),
		kin("Anne Nordmann", "parent"),
	}})
	k := got.NextOfKin
	if len(k) != 3 || k[0].Name != "Ola Nordmann" || k[0].Phone == nil || *k[0].Phone != "+47 900 00 001" ||
		k[1].Relationship != "child" || k[1].Phone != nil || k[2].Name != "Anne Nordmann" {
		t.Errorf("replaced = %+v, want three in the order sent, trimmed", k)
	}
	if got.Revision != p.Revision+1 || personRevision(t, h, p.Id) != got.Revision {
		t.Errorf("revision after a change = %d (stored %d), want %d", got.Revision, personRevision(t, h, p.Id), p.Revision+1)
	}
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Per Nordmann", "child")}})
	var read gen.StaffNextOfKinList
	do(t, hr, http.MethodGet, nextOfKinPath(p.Id), nil, http.StatusOK, &read)
	if len(read.NextOfKin) != 1 || read.NextOfKin[0].Name != "Per Nordmann" || read.Revision != p.Revision+2 {
		t.Errorf("after a second replace = %+v, want Per alone at revision %d", read, p.Revision+2)
	}
	same := putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Per Nordmann", "child")}})
	if same.Revision != read.Revision {
		t.Errorf("an unchanged replace moved the revision to %d, want %d", same.Revision, read.Revision)
	}
	got = putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{}})
	if n := h.Count(t, `SELECT count(*) FROM staff.next_of_kin WHERE person_id = $1`, p.Id); n != 0 || len(got.NextOfKin) != 0 {
		t.Errorf("after an empty replace: %d rows, answer %v, want none", n, got)
	}
	r := hr.Do(http.MethodPut, nextOfKinPath(p.Id+1000), map[string]any{"nextOfKin": []any{}}, ifMatch(1))
	if r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unknown person: %d %q, want a bare 404", r.Status, r.Body)
	}
}

// The replace is guarded by the person's revision, as the person's own PUT
// is: a stale one is 409 revision_conflict and nothing changes; none at all
// is a 400; a person's PUT after a next-of-kin change needs the new one.
func TestNextOfKin_RevisionGuarded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Ola", "spouse")}})

	refused(t, hr.Do(http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": []any{}}, ifMatch(p.Revision)),
		http.StatusConflict, "revision_conflict")
	if r := hr.Do(http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": []any{}}); r.Status != http.StatusBadRequest {
		t.Errorf("without If-Match: %d, want 400", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.next_of_kin WHERE person_id = $1`, p.Id); n != 1 {
		t.Errorf("a refused replace changed the list: %d rows", n)
	}
	refused(t, hr.Do(http.MethodPut, personPath(p.Id), newPerson("Kari", "Hansen"), ifMatch(p.Revision)),
		http.StatusConflict, "revision_conflict")
}

// Each next of kin's field is judged and named by its place in the list; a
// fourth is refused on the list; nothing is written when anything fails.
func TestNextOfKin_Validation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Ola", "spouse")}})

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
		p := refused(t, hr.Do(http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": c.list}, ifMatch(personRevision(t, h, p.Id))), http.StatusBadRequest, "")
		if len(p.Errors[c.field]) == 0 {
			t.Errorf("%s: errors = %v, want %s named", c.name, p.Errors, c.field)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.next_of_kin WHERE person_id = $1 AND name = 'Ola'`, p.Id); n != 1 {
		t.Errorf("a refused replace changed the stored list")
	}
}
