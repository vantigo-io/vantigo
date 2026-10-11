package staff_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const similarErasedPath = "/api/v1/staff/people/similar-erased"

// erasedRecord is a former employee the retention run has erased: an
// employment ended at the end of 2024 with its terms, a document, a national
// identity number still sealed (so a reveal has something to read), a
// self-service link — every kind of row a write could touch — and erased_at
// set as the run sets it.
type erasedRecord struct {
	person     gen.StaffPerson
	employment gen.StaffEmployment
	terms      gen.StaffTerms
	document   gen.StaffDocument
	self       *modtest.Client
	selfID     uuid.UUID
}

func seedErased(t *testing.T, h *harness, hr *modtest.Client) erasedRecord {
	t.Helper()
	company(t, hr)
	p := createPerson(t, hr, with(newPerson("Erik", "Glemt"), "birthDate", "1990-01-01"))
	e := createEmployment(t, hr, p.Id, newEmployment("2020-01-01", terms()))
	do(t, hr, http.MethodPost, endPath(e.Id), map[string]any{"endDate": "2024-12-31", "endReason": "employee_resigned"}, http.StatusOK, &e)
	do(t, hr, http.MethodPut, nationalIDPath(p.Id), map[string]any{"kind": "fodselsnummer", "value": testNationalID("018190", 347)}, http.StatusOK, nil)
	doc := uploadDocument(t, hr, p.Id, pdfUpload(documentCategoryID(t, h, "contract")))
	self, selfID := signIn(t, h)
	link(t, h, p.Id, selfID)
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, p.Id, h.Now())
	var person gen.StaffPerson
	do(t, hr, http.MethodGet, personPath(p.Id), nil, http.StatusOK, &person)
	return erasedRecord{person: person, employment: e, terms: listTerms(t, hr, e.Id)[0], document: doc, self: self, selfID: selfID}
}

// An erased person is read-only (design S15 as amended, plan reading 7):
// every write of theirs — the record, the next of kin, the roles, the sealed
// fields, a link to a user, an employment and its terms, a document, and the
// person's own My page — answers 409 person_erased, judged on the person's
// locked row before anything else, and changes nothing: no row, no event, no
// object left behind by a refused upload.
func TestErased_EveryWriteIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data", "staff:view")
	r := seedErased(t, h, hr)
	id := r.person.Id
	_, other := signIn(t, h)
	events := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1`, id)
	objects := h.objects.keys()
	updated := modtest.One[time.Time](t, h.Harness, `SELECT updated_at FROM staff.people WHERE id = $1`, id)

	unit := r.employment.BusinessUnitId
	for _, c := range []struct {
		name   string
		client *modtest.Client
		send   func(c *modtest.Client) *modtest.Response
	}{
		{"PUT /people/{id}", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, personPath(id), with(newPerson("Erik", "Glemt"), "employeeNumber", r.person.EmployeeNumber), ifMatch(r.person.Revision))
		}},
		{"DELETE /people/{id}", hr, func(c *modtest.Client) *modtest.Response { return c.Do(http.MethodDelete, personPath(id), nil) }},
		{"PUT /people/{id}/next-of-kin", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, nextOfKinPath(id), map[string]any{"nextOfKin": []any{kin("Ola", "spouse")}}, ifMatch(r.person.Revision))
		}},
		{"PUT /people/{id}/roles", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, rolesPath(id), map[string]any{"roles": []any{map[string]any{"role": "verneombud"}}}, ifMatch(r.person.Revision))
		}},
		{"PUT /people/{id}/national-id", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, nationalIDPath(id), map[string]any{"kind": "fodselsnummer", "value": testNationalID("018190", 400)})
		}},
		{"PUT /people/{id}/bank-account", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, bankAccountPath(id), map[string]any{"kind": "bban", "value": "12345678903"})
		}},
		{"PUT /people/{id}/user (a link)", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, userPath(id), map[string]any{"userId": other})
		}},
		{"PUT /employments/{id}", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, employmentPath(r.employment.Id), map[string]any{
				"startDate": "2020-02-01", "relationshipType": "ordinary", "businessUnitId": unit,
			}, ifMatch(r.employment.Revision))
		}},
		{"POST /employments/{id}/end", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPost, endPath(r.employment.Id), map[string]any{"endDate": "2024-11-30", "endReason": "employee_resigned"})
		}},
		{"POST /employments/{id}/reopen", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPost, reopenPath(r.employment.Id), nil)
		}},
		{"POST /employments/{id}/terms", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPost, termsPath(r.employment.Id), terms("validFrom", "2023-01-01"))
		}},
		{"PUT /terms/{id}", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, termsRowPath(r.terms.Id), terms("validFrom", "2020-01-01", "ftePercent", 80), ifMatch(r.terms.Revision))
		}},
		{"DELETE /terms/{id}", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodDelete, termsRowPath(r.terms.Id), nil)
		}},
		{"POST /people/{id}/documents", hr, func(c *modtest.Client) *modtest.Response {
			return postDocument(t, c, id, pdfUpload(documentCategoryID(t, h, "other")))
		}},
		{"PUT /documents/{id}", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, documentPath(r.document.Id), map[string]any{"categoryId": documentCategoryID(t, h, "other")}, ifMatch(r.document.Revision))
		}},
		{"DELETE /documents/{id}", hr, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodDelete, documentPath(r.document.Id), nil)
		}},
		{"PUT /me", r.self, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPut, mePath, map[string]any{"nextOfKin": []any{}, "phoneMobile": "900 00 000"}, ifMatch(r.person.Revision))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			refused(t, c.send(c.client), http.StatusConflict, "person_erased")
		})
	}

	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1`, id); n != events {
		t.Errorf("events = %d after the refusals, want %d", n, events)
	}
	if got := h.objects.keys(); !slices.Equal(got, objects) {
		t.Errorf("objects = %v after the refusals, want %v", got, objects)
	}
	if now := modtest.One[time.Time](t, h.Harness, `SELECT updated_at FROM staff.people WHERE id = $1`, id); !now.Equal(updated) {
		t.Errorf("the person's row changed: updated_at %s, want %s", now, updated)
	}
	for table, want := range map[string]int{"employments": 1, "employment_terms": 1, "documents": 1, "identity_links": 1, "next_of_kin": 0, "person_roles": 0} {
		query := `SELECT count(*) FROM staff.` + table + ` WHERE person_id = $1`
		if table == "employment_terms" {
			query = `SELECT count(*) FROM staff.employment_terms t JOIN staff.employments e ON e.id = t.employment_id WHERE e.person_id = $1`
		}
		if n := h.Count(t, query, id); n != want {
			t.Errorf("%s = %d after the refusals, want %d", table, n, want)
		}
	}
}

// What an erased person still allows (plan reading 7): every read — the
// record, the history, the documents and their downloads, the employments,
// a reveal, both exports — and the unlink of their user, so the user can be
// linked elsewhere.
func TestErased_ReadsAndTheUnlinkStayOpen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	r := seedErased(t, h, hr)
	id := r.person.Id

	for _, path := range []string{
		personPath(id), eventsPath(id), documentsPath(id), documentPath(r.document.Id), employmentsPath(id),
		employmentPath(r.employment.Id), termsPath(r.employment.Id), nextOfKinPath(id), rolesPath(id), userPath(id),
		personalDataPath(id),
	} {
		if got := hr.Do(http.MethodGet, path, nil); got.Status != http.StatusOK {
			t.Errorf("GET %s: %d %s, want 200", path, got.Status, got.Body)
		}
	}
	if got := hr.Do(http.MethodPost, nationalIDRevealPath(id), nil); got.Status != http.StatusOK {
		t.Errorf("reveal: %d %s, want 200", got.Status, got.Body)
	}
	if got := r.self.Do(http.MethodGet, meExportPath, nil); got.Status != http.StatusOK {
		t.Errorf("GET /me/export: %d %s, want 200", got.Status, got.Body)
	}

	var unlinked gen.StaffPersonLink
	do(t, hr, http.MethodPut, userPath(id), map[string]any{"userId": nil}, http.StatusOK, &unlinked)
	if unlinked.Linked || h.Count(t, `SELECT count(*) FROM staff.identity_links WHERE person_id = $1`, id) != 0 {
		t.Errorf("unlink = %+v, want the link gone", unlinked)
	}
	if latest := listEvents(t, hr, id)[0]; latest.Kind != "user_unlinked" {
		t.Errorf("latest event = %s, want user_unlinked", latest.Kind)
	}
}

// A rehire is POST /people/{id}/employments on the erased record (plan
// reading 6): the employment is added, and in the same transaction the
// person's erased_at and reassessed_at are cleared and a person_rehired event
// names what was cleared — before the employment's own events. The record is
// writable again; what the erase blanked stays blank until HR types it.
func TestErased_ARehireOpensTheRecordAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage", "staff:personal-data")
	r := seedErased(t, h, hr)
	id := r.person.Id
	erasedAt := modtest.One[time.Time](t, h.Harness, `SELECT erased_at FROM staff.people WHERE id = $1`, id)
	h.Exec(t, `UPDATE staff.people SET reassessed_at = $2, reassessed_by_user_id = $3 WHERE id = $1`, id, h.Now(), hrID)
	h.Advance(time.Hour)

	createEmployment(t, hr, id, newEmployment("2026-10-01", terms()))
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NULL AND reassessed_at IS NULL AND reassessed_by_user_id IS NULL`, id); n != 1 {
		t.Fatal("the rehire left erased_at or reassessed_at set")
	}
	events := listEvents(t, hr, id)
	if got := kinds(events[:3]); !slices.Equal(got, []string{"terms_added", "employment_created", "person_rehired"}) {
		t.Fatalf("latest events = %v, want person_rehired before the employment's", got)
	}
	rehired := fieldsOf(events[2])
	for _, field := range []string{"erasedAt", "reassessedAt"} {
		change, _ := rehired[field].(map[string]any)
		if change == nil || change["from"] == nil || change["to"] != nil {
			t.Errorf("person_rehired %s = %v, want from the time and no to", field, rehired[field])
		}
	}
	from, _ := rehired["erasedAt"].(map[string]any)["from"].(string)
	if at, err := time.Parse(time.RFC3339Nano, from); err != nil || !at.Equal(erasedAt) {
		t.Errorf("person_rehired erasedAt from %q, want %s", from, erasedAt)
	}
	do(t, hr, http.MethodPut, personPath(id), with(newPerson("Erik", "Glemt"), "employeeNumber", r.person.EmployeeNumber,
		"birthDate", "1990-01-01", "phoneMobile", "900 00 000"), http.StatusOK, nil, ifMatch(personRevision(t, h, id)))

	// A rehire on a person who was never erased records nothing of the kind.
	createEmployment(t, hr, id, newEmployment("2027-01-01", terms()))
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'person_rehired'`, id); n != 1 {
		t.Errorf("person_rehired events = %d, want 1", n)
	}
}

// The rehire warning (design S15): POST /people/similar-erased answers the
// erased people with the same given and family names, however cased, and the
// same birth date — not one who is not erased, not one born on another day —
// most recently erased first.
func TestErased_SimilarErased(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	erase := func(p gen.StaffPerson, at time.Time) {
		h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, p.Id, at)
	}
	older := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "birthDate", "1990-02-28"))
	erase(older, h.Now().Add(-48*time.Hour))
	newer := createPerson(t, hr, with(newPerson("KARI", "nordmann"), "birthDate", "1990-02-28"))
	erase(newer, h.Now().Add(-time.Hour))
	otherDay := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "birthDate", "1990-03-01"))
	erase(otherDay, h.Now())
	createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "birthDate", "1990-02-28")) // not erased

	var found []gen.StaffErasedPerson
	do(t, hr, http.MethodPost, similarErasedPath, map[string]any{"givenName": " kari ", "familyName": "NORDMANN", "birthDate": "1990-02-28"}, http.StatusOK, &found)
	if len(found) != 2 || found[0].Id != newer.Id || found[1].Id != older.Id {
		t.Fatalf("found = %+v, want the two erased Kari Nordmanns born 1990-02-28, newest first", found)
	}
	if found[0].EmployeeNumber != newer.EmployeeNumber || found[0].DisplayName != newer.DisplayName || !found[0].ErasedAt.Equal(h.Now().Add(-time.Hour)) {
		t.Errorf("found[0] = %+v, want the number, the name and when", found[0])
	}

	var none []gen.StaffErasedPerson
	do(t, hr, http.MethodPost, similarErasedPath, map[string]any{"givenName": "Ola", "familyName": "Nordmann", "birthDate": "1990-02-28"}, http.StatusOK, &none)
	if none == nil || len(none) != 0 {
		t.Errorf("no match = %v, want []", none)
	}
	p := refused(t, hr.Do(http.MethodPost, similarErasedPath, map[string]any{"givenName": "", "familyName": "Nordmann", "birthDate": "1990-02-28"}), http.StatusBadRequest, "")
	if len(p.Errors["givenName"]) == 0 {
		t.Errorf("errors = %v, want givenName", p.Errors)
	}
	viewer, _ := signIn(t, h, "staff:view")
	if r := viewer.Do(http.MethodPost, similarErasedPath, map[string]any{"givenName": "Kari", "familyName": "Nordmann", "birthDate": "1990-02-28"}); r.Status != http.StatusForbidden {
		t.Errorf("staff:view: %d, want 403", r.Status)
	}
}

// The directory shows an erased person to HR under status ended and all,
// their state ended (no new state) with erasedAt beside it; a leaver who is
// not erased has none, and status current leaves both out.
func TestErased_TheDirectoryShowsErasedAt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	erased := createPerson(t, hr, newPerson("Erik", "Glemt"))
	seedEmploymentDated(t, h, erased.Id, 0, "2020-01-01", ptr("2024-12-31"))
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, erased.Id, h.Now())
	leaver := createPerson(t, hr, newPerson("Lars", "Slutta"))
	seedEmploymentDated(t, h, leaver.Id, 0, "2020-01-01", ptr("2025-12-31"))

	for _, status := range []string{"ended", "all"} {
		body := raw(t, hr.Do(http.MethodGet, peoplePath+"?status="+status, nil))
		rows, _ := body["data"].([]any)
		seen := map[float64]map[string]any{}
		for _, row := range rows {
			item := row.(map[string]any)
			seen[item["id"].(float64)] = item
		}
		e, l := seen[float64(erased.Id)], seen[float64(leaver.Id)]
		if e == nil || l == nil {
			t.Fatalf("status=%s lists %v, want both", status, rows)
		}
		if at, err := time.Parse(time.RFC3339Nano, e["erasedAt"].(string)); err != nil || !at.Equal(h.Now()) {
			t.Errorf("status=%s erased erasedAt = %v, want %s", status, e["erasedAt"], h.Now())
		}
		if state, _ := e["status"].(map[string]any); state["state"] != "ended" {
			t.Errorf("status=%s erased status = %v, want ended", status, e["status"])
		}
		if _, ok := l["erasedAt"]; ok {
			t.Errorf("status=%s the leaver carries erasedAt", status)
		}
	}
	for _, item := range listPeople(t, hr, "?status=current").Data {
		if item.Id == erased.Id {
			t.Error("status=current lists the erased person")
		}
	}

	// staff:view never reads erasedAt: it lists the people active today, which
	// an erased person is not — and were one, the hold group is not its own.
	active := createPerson(t, hr, newPerson("Aktiv", "Glemt"))
	seedEmployment(t, h, active.Id, 0)
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, active.Id, h.Now())
	viewer, _ := signIn(t, h, "staff:view")
	for _, status := range []string{"", "?status=all"} {
		body := raw(t, viewer.Do(http.MethodGet, peoplePath+status, nil))
		rows, _ := body["data"].([]any)
		if len(rows) == 0 {
			t.Fatalf("staff:view lists nobody (%q)", status)
		}
		for _, row := range rows {
			if _, ok := row.(map[string]any)["erasedAt"]; ok {
				t.Errorf("staff:view reads erasedAt on %v", row)
			}
		}
	}
}

func ptr[T any](v T) *T { return &v }
