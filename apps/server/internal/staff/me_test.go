package staff_test

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const (
	mePath       = "/api/v1/staff/me"
	meEventsPath = mePath + "/events"
)

// A caller linked to no person asks about themselves and finds nobody: My
// page's three operations answer the 404 no_person — a problem, not the bare
// 404 of an unknown id, since there is nothing to hide from the caller about
// themselves.
func TestMe_UnlinkedIsNoPerson(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	alone, _ := signIn(t, h)
	seedPerson(t, h, "Kari", "Nordmann")
	for _, c := range []*modtest.Client{alone, hr} {
		refused(t, c.Do(http.MethodGet, mePath, nil), http.StatusNotFound, "no_person")
		refused(t, c.Do(http.MethodPut, mePath, map[string]any{"nextOfKin": []any{}}, ifMatch(1)), http.StatusNotFound, "no_person")
		refused(t, c.Do(http.MethodGet, meEventsPath, nil), http.StatusNotFound, "no_person")
	}
}

// GET /me is the person's own record through the real link HR set: the Self
// view of design §5 — the work and private groups, the national identity
// number's kind, the bank account's last four digits and the linked user,
// never HR's notes — field for field what the same caller reads on
// GET /people/{id}.
func TestMe_TheSelfViewThroughTheRealLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	p := createPerson(t, hr, map[string]any{
		"givenName": "Kari", "familyName": "Nordmann", "birthDate": "1990-01-01", "gender": "female",
		"workEmail": "kari@example.no", "phoneWork": "22 00 00 00",
		"phoneMobile": "900 00 000", "phonePrivate": "55 00 00 00", "privateEmail": "kari@privat.no",
		"street": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "NO", "notes": "HR's own",
	})
	do(t, pd, http.MethodPut, bankAccountPath(p.Id), map[string]any{"kind": "bban", "value": "12345678903"}, http.StatusOK, nil)
	self, user := signInNamed(t, h, "Kari the user", "staff:view")
	linkThroughAPI(t, hr, p.Id, user)

	me := self.Do(http.MethodGet, mePath, nil)
	if me.Status != http.StatusOK {
		t.Fatalf("GET /me: %d %s, want 200", me.Status, me.Body)
	}
	body := raw(t, me)
	if got, want := slices.Sorted(maps.Keys(body)), fields(workFields, privateFields, sealedFields, linkFields); !slices.Equal(got, want) {
		t.Errorf("GET /me answers %v, want %v", got, want)
	}
	if string(me.Body) != string(self.Do(http.MethodGet, personPath(p.Id), nil).Body) {
		t.Errorf("GET /me differs from the person's own GET /people/{id}:\n%s", me.Body)
	}
	if u, _ := body["user"].(map[string]any); u["id"] != user.String() || u["displayName"] != "Kari the user" {
		t.Errorf("user = %v, want the caller", body["user"])
	}
	if c, _ := body["capabilities"].(map[string]any); c["canEdit"] != false || c["canSeePay"] != true {
		t.Errorf("capabilities = %v, want the person's own: pay readable, nothing editable here", c)
	}
}

// PUT /me replaces what the person edits themselves (design S17): the
// private phones and email, the home address and the next of kin, under the
// person's revision. A field of any other group — the work group, the birth
// date, HR's notes, a sealed field — is refused as a property the request
// does not name, and nothing is written. Each change is an event the person
// is the actor of, read back on GET /me/events shaped as the person reads
// it: HR's notes changed by HR are left out.
func TestMe_PutChangesThePrivateGroupOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "workEmail", "kari@example.no", "phoneMobile", "1"))
	self, user := signInNamed(t, h, "Kari the user")
	linkThroughAPI(t, hr, p.Id, user)

	for _, property := range []string{"givenName", "workEmail", "displayName", "birthDate", "gender", "notes", "nationalId", "bankAccount"} {
		body := map[string]any{"nextOfKin": []any{}, property: "x"}
		pr := refused(t, self.Do(http.MethodPut, mePath, body, ifMatch(p.Revision)), http.StatusBadRequest, "")
		if len(pr.Errors[property]) == 0 {
			t.Errorf("%s: errors = %v, want it named", property, pr.Errors)
		}
	}
	if rev := personRevision(t, h, p.Id); rev != p.Revision {
		t.Fatalf("a refused PUT /me moved the revision to %d", rev)
	}

	var out gen.StaffPerson
	do(t, self, http.MethodPut, mePath, map[string]any{
		"phonePrivate": "55 00 00 00", "privateEmail": "Kari@Privat.no",
		"street": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no",
		"nextOfKin": []any{kin("Ola", "spouse", "phone", "900")},
	}, http.StatusOK, &out, ifMatch(p.Revision))
	if out.PrivateEmail == nil || *out.PrivateEmail != "kari@privat.no" || out.Country == nil || *out.Country != "NO" ||
		out.PhoneMobile != nil || out.WorkEmail == nil || *out.WorkEmail != "kari@example.no" || out.GivenName != "Kari" {
		t.Errorf("after PUT /me = %+v, want the private group replaced (the mobile cleared) and the work group kept", out)
	}
	if out.Revision != p.Revision+1 {
		t.Errorf("revision = %d, want %d: one write, one step", out.Revision, p.Revision+1)
	}
	refused(t, self.Do(http.MethodPut, mePath, map[string]any{"nextOfKin": []any{}}, ifMatch(p.Revision)), http.StatusConflict, "revision_conflict")
	refused(t, self.Do(http.MethodPut, mePath, map[string]any{"nextOfKin": []any{}}, modtest.Header("If-Match", "x")), http.StatusBadRequest, "")
	refused(t, self.Do(http.MethodPut, mePath, map[string]any{"nextOfKin": []any{kin("", "spouse")}}, ifMatch(out.Revision)), http.StatusBadRequest, "")

	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Nordmann"), "notes", "On probation",
		"workEmail", "kari@example.no", "privateEmail", "kari@privat.no", "phonePrivate", "55 00 00 00",
		"street", "Storgata 1", "postalCode", "0155", "city", "Oslo", "country", "NO"), http.StatusOK, nil, ifMatch(out.Revision))

	var history gen.PaginatedResponseOfStaffEvent
	do(t, self, http.MethodGet, meEventsPath, nil, http.StatusOK, &history)
	var kinds []string
	for _, e := range history.Data {
		kinds = append(kinds, e.Kind)
	}
	if !slices.Equal(kinds[:3], []string{"person_updated", "next_of_kin_updated", "person_updated"}) {
		t.Fatalf("kinds = %v, want HR's change, then the person's two", kinds)
	}
	if v, ok := fieldsOf(history.Data[0])["notes"]; ok {
		t.Errorf("HR's notes read %v on /me/events, want them left out", v)
	}
	for _, e := range history.Data[1:3] {
		if e.Actor.UserId == nil || *e.Actor.UserId != user || e.Actor.DisplayName != "Kari the user" {
			t.Errorf("%s actor = %+v, want the person", e.Kind, e.Actor)
		}
	}
	mine := fieldsOf(history.Data[2])
	if f, _ := mine["privateEmail"].(map[string]any); f["to"] != "kari@privat.no" {
		t.Errorf("the person's change = %v, want the private email set", mine)
	}
	if _, ok := mine["givenName"]; ok {
		t.Errorf("the person's change names givenName: %v", mine)
	}
	var page gen.PaginatedResponseOfStaffEvent
	do(t, self, http.MethodGet, pagedPath(meEventsPath, 2, 2), nil, http.StatusOK, &page)
	if page.Pagination.TotalCount != int32(len(history.Data)) || len(page.Data) != 2 {
		t.Errorf("page 2 of 2 = %d of %d, want two of %d", len(page.Data), page.Pagination.TotalCount, len(history.Data))
	}
	refused(t, self.Do(http.MethodGet, meEventsPath+"?pageSize=0", nil), http.StatusBadRequest, "")
	if strings.Contains(string(self.Do(http.MethodGet, meEventsPath, nil).Body), "On probation") {
		t.Error("the person reads HR's notes in their history")
	}
}

// /me is pure Self whatever else the caller holds: HR linked to their own
// person reads their record on My page without HR's notes, which
// GET /people/{id} shows them as staff:manage, and their own history there
// leaves a notes change out.
func TestMe_IsPureSelfForHR(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signInNamed(t, h, "Hanne HR", "staff:manage")
	p := createPerson(t, hr, with(newPerson("Hanne", "Hansen"), "notes", "HR's own"))
	linkThroughAPI(t, hr, p.Id, hrID)

	if _, ok := raw(t, hr.Do(http.MethodGet, mePath, nil))["notes"]; ok {
		t.Error("GET /me answers notes to HR reading their own record, want pure Self")
	}
	if raw(t, hr.Do(http.MethodGet, personPath(p.Id), nil))["notes"] != "HR's own" {
		t.Error("GET /people/{id} hides notes from staff:manage")
	}
	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Hanne", "Hansen"), "notes", "Changed"), http.StatusOK, nil, ifMatch(personRevision(t, h, p.Id)))
	var history gen.PaginatedResponseOfStaffEvent
	do(t, hr, http.MethodGet, meEventsPath, nil, http.StatusOK, &history)
	if got, ok := fieldsOf(history.Data[0])["notes"]; ok {
		t.Errorf("/me/events shows notes as %v, want them left out", got)
	}
	if f, ok := fieldsOf(listEvents(t, hr, p.Id)[0])["notes"].(map[string]any); !ok || f["to"] != "Changed" {
		t.Errorf("/people/{id}/events shows HR notes as %v, want the value", f)
	}
}
