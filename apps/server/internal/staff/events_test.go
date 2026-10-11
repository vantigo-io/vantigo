package staff_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func listEvents(t *testing.T, c *modtest.Client, id int64) []gen.StaffEvent {
	t.Helper()
	var out gen.PaginatedResponseOfStaffEvent
	do(t, c, http.MethodGet, eventsPath(id), nil, http.StatusOK, &out)
	return out.Data
}

// fieldsOf is an event payload's fields.
func fieldsOf(e gen.StaffEvent) map[string]any {
	f, _ := e.Payload["fields"].(map[string]any)
	return f
}

// Every write of a person is an event, newest first, the actor as the
// directory named them: the creation with the values set, a change with each
// changed field's before and after (a field cleared has no after), and the
// next of kin and the roles each as one field.
func TestEvents_EveryWriteIsRecorded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signInNamed(t, h, "Hanne HR", "staff:manage")
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "employeeNumber", "E-1", "phoneWork", "22 00 00 00"))
	h.Advance(time.Minute)
	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Hansen"), "employeeNumber", "E-1"), http.StatusOK, nil, ifMatch(p.Revision))
	h.Advance(time.Minute)
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Ola", "spouse")}})
	h.Advance(time.Minute)
	putRoles(t, h, hr, p.Id, map[string]any{"roles": []any{map[string]any{"role": "verneombud"}}})
	// A replace that changes nothing writes no event.
	putRoles(t, h, hr, p.Id, map[string]any{"roles": []any{map[string]any{"role": "verneombud"}}})

	events := listEvents(t, hr, p.Id)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
		if e.Actor.Kind != "user" || e.Actor.DisplayName != "Hanne HR" || e.Actor.UserId == nil || *e.Actor.UserId != hrID {
			t.Errorf("%s actor = %+v, want Hanne HR", e.Kind, e.Actor)
		}
	}
	if want := []string{"roles_updated", "next_of_kin_updated", "person_updated", "person_created"}; !slices.Equal(kinds, want) {
		t.Fatalf("kinds = %v, want %v newest first", kinds, want)
	}
	created := fieldsOf(events[3])
	if created["employeeNumber"].(map[string]any)["to"] != "E-1" || created["givenName"].(map[string]any)["to"] != "Kari" {
		t.Errorf("person_created fields = %v, want the values set", created)
	}
	if _, ok := created["phonePrivate"]; ok {
		t.Errorf("person_created names phonePrivate, which was not set: %v", created)
	}
	updated := fieldsOf(events[2])
	if len(updated) != 2 {
		t.Errorf("person_updated fields = %v, want familyName and phoneWork only", updated)
	}
	if f := updated["familyName"].(map[string]any); f["from"] != "Nordmann" || f["to"] != "Hansen" {
		t.Errorf("familyName = %v, want Nordmann → Hansen", f)
	}
	if f := updated["phoneWork"].(map[string]any); f["from"] != "22 00 00 00" || f["to"] != nil {
		t.Errorf("phoneWork = %v, want a from and no to", f)
	}
	kinChange := fieldsOf(events[1])["nextOfKin"].(map[string]any)
	if to := kinChange["to"].([]any); len(to) != 1 || to[0].(map[string]any)["name"] != "Ola" {
		t.Errorf("nextOfKin = %v, want Ola after", kinChange)
	}
	if roles := fieldsOf(events[0])["roles"].(map[string]any); len(roles["to"].([]any)) != 1 {
		t.Errorf("roles = %v, want one after", roles)
	}

	var page gen.PaginatedResponseOfStaffEvent
	do(t, hr, http.MethodGet, pagedPath(eventsPath(p.Id), 2, 3), nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.Data[0].Kind != "person_created" || page.Pagination.TotalCount != 4 {
		t.Errorf("page 2 of 3 = %+v, want person_created alone of four", page)
	}
	refused(t, hr.Do(http.MethodGet, eventsPath(p.Id)+"?page=0", nil), http.StatusBadRequest, "")
}

// A payload is shaped per field by the caller's groups (design S21 as
// amended): HR's notes are the HR-notes group, so the person sees a notes
// change not at all — HR's notes are left out of the payload for anyone who
// may not read them, not marked "changed" — and HR sees its values; a private field's change is
// the person's to read; a colleague reads no history at all.
func TestEvents_ShapedPerField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	self, user := signIn(t, h)
	link(t, h, p.Id, user)
	personalData, _ := signIn(t, h, "staff:personal-data")
	colleague, _ := signIn(t, h, "staff:view")
	do(t, hr, http.MethodPut, personPath(p.Id),
		with(newPerson("Kari", "Nordmann"), "notes", "On probation", "privateEmail", "kari@privat.no", "phoneWork", "22"),
		http.StatusOK, nil, ifMatch(p.Revision))

	latest := func(c *modtest.Client) map[string]any { return fieldsOf(listEvents(t, c, p.Id)[0]) }
	mine := latest(self)
	if v, ok := mine["notes"]; ok {
		t.Errorf("self sees notes as %v, want them left out", v)
	}
	if f, ok := mine["privateEmail"].(map[string]any); !ok || f["to"] != "kari@privat.no" {
		t.Errorf("self sees privateEmail as %v, want its value", mine["privateEmail"])
	}
	if f, ok := mine["phoneWork"].(map[string]any); !ok || f["to"] != "22" {
		t.Errorf("self sees phoneWork as %v, want its value", mine["phoneWork"])
	}
	for name, c := range map[string]*modtest.Client{"staff:manage": hr, "staff:personal-data": personalData} {
		if f, ok := latest(c)["notes"].(map[string]any); !ok || f["to"] != "On probation" {
			t.Errorf("%s sees notes as %v, want its value", name, latest(c)["notes"])
		}
	}
	if r := colleague.Do(http.MethodGet, eventsPath(p.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("colleague: %d %q, want the bare 404", r.Status, r.Body)
	}
	classes := modtest.One[[]string](t, h.Harness, `SELECT payload_classes FROM staff.events WHERE person_id = $1 AND kind = 'person_updated'`, p.Id)
	if !slices.Equal(classes, []string{"D"}) {
		t.Errorf("payload_classes = %v, want [D] (notes, private email, work phone)", classes)
	}
}
