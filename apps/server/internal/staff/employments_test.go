package staff_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const (
	employmentsBase = "/api/v1/staff/employments"
	termsBase       = "/api/v1/staff/terms"
)

// The employment paths, each by id.
func employmentsPath(personID int64) string { return personPath(personID) + "/employments" }
func employmentPath(id int64) string        { return rowPath(employmentsBase, id) }
func endPath(id int64) string               { return employmentPath(id) + "/end" }
func reopenPath(id int64) string            { return employmentPath(id) + "/reopen" }
func termsPath(id int64) string             { return employmentPath(id) + "/terms" }
func termsRowPath(id int64) string          { return rowPath(termsBase, id) }
func contractCheckPath(id int64) string     { return employmentPath(id) + "/contract-check" }
func orgPath(personID int64) string         { return personPath(personID) + "/org" }

// terms is the smallest terms row a write accepts — the two fields every row
// must carry — with the given fields replaced.
func terms(kv ...any) map[string]any {
	return with(map[string]any{"employmentType": "fast", "ftePercent": 100}, kv...)
}

// newEmployment is an employment starting on start with the first row's
// terms, and the given fields replaced.
func newEmployment(start string, first map[string]any, kv ...any) map[string]any {
	return with(map[string]any{"startDate": start, "terms": first}, kv...)
}

// company gives a fresh installation its default legal entity and business
// unit, which an employment that names none is put in, and answers the unit.
func company(t *testing.T, hr *modtest.Client) gen.StaffBusinessUnit {
	t.Helper()
	le := createLegalEntity(t, hr, map[string]any{"name": "Kraft-Verket AS"})
	return createBusinessUnit(t, hr, map[string]any{"legalEntityId": le.Id, "name": "Oslo"})
}

func createEmployment(t *testing.T, c *modtest.Client, personID int64, body map[string]any) gen.StaffEmployment {
	t.Helper()
	var out gen.StaffEmployment
	do(t, c, http.MethodPost, employmentsPath(personID), body, http.StatusCreated, &out)
	return out
}

func addTerms(t *testing.T, c *modtest.Client, employmentID int64, body map[string]any) gen.StaffTerms {
	t.Helper()
	var out gen.StaffTerms
	do(t, c, http.MethodPost, termsPath(employmentID), body, http.StatusCreated, &out)
	return out
}

func listTerms(t *testing.T, c *modtest.Client, employmentID int64) []gen.StaffTerms {
	t.Helper()
	var out []gen.StaffTerms
	do(t, c, http.MethodGet, termsPath(employmentID), nil, http.StatusOK, &out)
	return out
}

func getEmployment(t *testing.T, c *modtest.Client, id int64) gen.StaffEmployment {
	t.Helper()
	var out gen.StaffEmployment
	do(t, c, http.MethodGet, employmentPath(id), nil, http.StatusOK, &out)
	return out
}

// employed is a person with one employment from start, created through the
// API with first as the first row's terms.
func employed(t *testing.T, hr *modtest.Client, given, start string, first map[string]any) (gen.StaffPerson, gen.StaffEmployment) {
	t.Helper()
	p := createPerson(t, hr, newPerson(given, "Nordmann"))
	return p, createEmployment(t, hr, p.Id, newEmployment(start, first))
}

func kinds(events []gen.StaffEvent) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

// An employment is created with its first terms row, dated the start, in the
// default business unit unless one is named; the employment number is the
// one typed, or the person's employee number and the first free -n, skipping
// past a number typed by hand however it is cased. The creation is an
// employment_created and a terms_added event.
func TestEmployments_CreateWithTheFirstTerms(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	unit := company(t, hr)
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "employeeNumber", "E-7"))

	first := createEmployment(t, hr, p.Id, newEmployment("2026-01-01", terms("title", "Developer")))
	if first.EmploymentNumber == nil || *first.EmploymentNumber != "E-7-1" {
		t.Errorf("generated number = %v, want E-7-1", first.EmploymentNumber)
	}
	if first.BusinessUnitId == nil || *first.BusinessUnitId != unit.Id || first.RelationshipType == nil || *first.RelationshipType != "ordinary" {
		t.Errorf("employment = %+v, want the default unit and ordinary", first)
	}
	if first.StartDate.Format("2006-01-02") != "2026-01-01" || first.EndDate != nil {
		t.Errorf("dates = %v–%v, want from 2026-01-01, open-ended", first.StartDate, first.EndDate)
	}
	rows := listTerms(t, hr, first.Id)
	if len(rows) != 1 {
		t.Fatalf("terms = %+v, want the first row", rows)
	}
	r := rows[0]
	if r.ValidFrom.Format("2006-01-02") != "2026-01-01" || r.ValidTo != nil || r.EmploymentType != "fast" || r.FtePercent != 100 ||
		r.AppointmentForm == nil || *r.AppointmentForm != "fast" || r.WorkingHoursScheme == nil || *r.WorkingHoursScheme != "not_shift" ||
		r.FullTimeHoursPerWeek == nil || *r.FullTimeHoursPerWeek != 37.5 || r.Title == nil || *r.Title != "Developer" {
		t.Errorf("first row = %+v, want dated the start, fast, 100 %%, not_shift at 37.5 hours, the title", r)
	}

	typed := createEmployment(t, hr, p.Id, newEmployment("2026-02-01", terms(), "employmentNumber", "e-7-2"))
	if typed.EmploymentNumber == nil || *typed.EmploymentNumber != "e-7-2" {
		t.Errorf("typed number = %v, want e-7-2 as typed", typed.EmploymentNumber)
	}
	third := createEmployment(t, hr, p.Id, newEmployment("2026-03-01", terms()))
	if third.EmploymentNumber == nil || *third.EmploymentNumber != "E-7-3" {
		t.Errorf("generated after a typed one = %v, want E-7-3, skipping e-7-2", third.EmploymentNumber)
	}
	for _, number := range []string{"E-7-1", "e-7-3"} {
		pr := refused(t, hr.Do(http.MethodPost, employmentsPath(p.Id), newEmployment("2026-04-01", terms(), "employmentNumber", number)),
			http.StatusBadRequest, "employment_number_taken")
		if len(pr.Errors["employmentNumber"]) != 1 {
			t.Errorf("%s: errors %v, want one on employmentNumber", number, pr.Errors)
		}
	}
	refused(t, hr.Do(http.MethodPost, employmentsPath(p.Id), newEmployment("2026-04-01", terms(), "employmentNumber", "E 7")), http.StatusBadRequest, "")

	var list []gen.StaffEmployment
	do(t, hr, http.MethodGet, employmentsPath(p.Id), nil, http.StatusOK, &list)
	if len(list) != 3 || list[0].Id != first.Id || list[2].Id != third.Id {
		t.Errorf("list = %+v, want the three by start", list)
	}
	got := kinds(listEvents(t, hr, p.Id))
	if n := len(slices.DeleteFunc(slices.Clone(got), func(k string) bool { return k != "employment_created" })); n != 3 {
		t.Errorf("events %v, want three employment_created", got)
	}
	if !slices.Contains(got, "terms_added") {
		t.Errorf("events %v, want terms_added for the first rows", got)
	}
}

// The refusals of a new employment: the first row's two required fields, a
// first row dated other than the start, a business unit that does not exist
// or is archived, an unknown person, and a caller without staff:manage.
func TestEmployments_CreateRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	unit := company(t, hr)
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))

	pr := refused(t, hr.Do(http.MethodPost, employmentsPath(p.Id), newEmployment("2026-01-01", map[string]any{"title": "x"})),
		http.StatusBadRequest, "terms_required_fields")
	for _, field := range []string{"terms.employmentType", "terms.ftePercent"} {
		if len(pr.Errors[field]) == 0 {
			t.Errorf("errors %v, want one on %s", pr.Errors, field)
		}
	}
	pr = refused(t, hr.Do(http.MethodPost, employmentsPath(p.Id), newEmployment("2026-01-01", map[string]any{"employmentType": "fast"})),
		http.StatusBadRequest, "terms_required_fields")
	if len(pr.Errors["terms.employmentType"]) != 0 || len(pr.Errors["terms.ftePercent"]) == 0 {
		t.Errorf("errors %v, want ftePercent alone", pr.Errors)
	}
	refused(t, hr.Do(http.MethodPost, employmentsPath(p.Id), newEmployment("2026-01-01", terms("validFrom", "2026-02-01"))),
		http.StatusConflict, "terms_first_row_start")
	refused(t, hr.Do(http.MethodPost, employmentsPath(p.Id), newEmployment("2026-01-01", terms(), "businessUnitId", unit.Id+1000)),
		http.StatusBadRequest, "")
	other := createBusinessUnit(t, hr, map[string]any{"legalEntityId": unit.LegalEntityId, "name": "Bergen"})
	do(t, hr, http.MethodPost, archivePath(businessUnitsPath, other.Id), nil, http.StatusOK, nil)
	refused(t, hr.Do(http.MethodPost, employmentsPath(p.Id), newEmployment("2026-01-01", terms(), "businessUnitId", other.Id)),
		http.StatusConflict, "structure_archived")
	if r := hr.Do(http.MethodPost, employmentsPath(p.Id+1000), newEmployment("2026-01-01", terms())); r.Status != http.StatusNotFound {
		t.Errorf("unknown person: %d, want 404", r.Status)
	}
	viewer, _ := signIn(t, h, "staff:view")
	if r := viewer.Do(http.MethodPost, employmentsPath(p.Id), newEmployment("2026-01-01", terms())); r.Status != http.StatusForbidden {
		t.Errorf("staff:view: %d, want 403", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.employments WHERE person_id = $1`, p.Id); n != 0 {
		t.Errorf("%d employments written by refused requests, want 0", n)
	}
}

// A change replaces the employment's own fields under If-Match: a new start
// moves the first terms row with it, and is refused when a later row would
// then lie before it; the employment number never changes; a stale revision
// is a conflict. The change is an employment_updated event.
func TestEmployments_ChangeMovesTheFirstRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	unit := company(t, hr)
	p, e := employed(t, hr, "Kari", "2026-01-01", terms())
	addTerms(t, hr, e.Id, terms("validFrom", "2026-03-01", "ftePercent", 80))

	change := func(start string, kv ...any) map[string]any {
		return with(map[string]any{"startDate": start, "relationshipType": "ordinary", "businessUnitId": unit.Id}, kv...)
	}
	var moved gen.StaffEmployment
	do(t, hr, http.MethodPut, employmentPath(e.Id), change("2026-02-01", "contractSignedAt", "2026-01-20"), http.StatusOK, &moved, ifMatch(e.Revision))
	if moved.StartDate.Format("2006-01-02") != "2026-02-01" || moved.ContractSignedAt == nil || moved.Revision == e.Revision {
		t.Errorf("changed = %+v, want the new start, the signing date and a new revision", moved)
	}
	if rows := listTerms(t, hr, e.Id); rows[0].ValidFrom.Format("2006-01-02") != "2026-02-01" {
		t.Errorf("first row from %v, want it moved to the new start", rows[0].ValidFrom)
	}
	refused(t, hr.Do(http.MethodPut, employmentPath(e.Id), change("2026-04-01"), ifMatch(moved.Revision)), http.StatusConflict, "terms_before_start")
	refused(t, hr.Do(http.MethodPut, employmentPath(e.Id), change("2026-02-01"), ifMatch(e.Revision)), http.StatusConflict, "revision_conflict")
	refused(t, hr.Do(http.MethodPut, employmentPath(e.Id), change("2026-02-01", "employmentNumber", "OTHER-1"), ifMatch(moved.Revision)),
		http.StatusBadRequest, "")
	if r := hr.Do(http.MethodPut, employmentPath(e.Id+1000), change("2026-02-01"), ifMatch(1)); r.Status != http.StatusNotFound {
		t.Errorf("unknown employment: %d, want 404", r.Status)
	}
	if got := kinds(listEvents(t, hr, p.Id)); got[0] != "employment_updated" {
		t.Errorf("latest event %v, want employment_updated", got)
	}
}

// An employment ends on a date — in the future too — for one of the six
// reasons, which is required; a terms row dated after the end refuses the
// end, and once ended a row after it is refused. Reopening takes the end
// away. Both are events.
func TestEmployments_EndAndReopen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	p, e := employed(t, hr, "Kari", "2026-01-01", terms())
	addTerms(t, hr, e.Id, terms("validFrom", "2026-06-01", "ftePercent", 80))

	refused(t, hr.Do(http.MethodPost, endPath(e.Id), map[string]any{"endDate": "2026-08-31"}), http.StatusBadRequest, "end_reason_required")
	refused(t, hr.Do(http.MethodPost, endPath(e.Id), map[string]any{"endReason": "employee_resigned"}), http.StatusBadRequest, "")
	refused(t, hr.Do(http.MethodPost, endPath(e.Id), map[string]any{"endDate": "2025-12-31", "endReason": "employee_resigned"}), http.StatusBadRequest, "")
	refused(t, hr.Do(http.MethodPost, endPath(e.Id), map[string]any{"endDate": "2026-05-31", "endReason": "employee_resigned"}), http.StatusConflict, "terms_after_end")

	future := shiftDay(t, today(t, h), 30)
	var ended gen.StaffEmployment
	do(t, hr, http.MethodPost, endPath(e.Id), map[string]any{"endDate": future, "endReason": "employee_resigned"}, http.StatusOK, &ended)
	if ended.EndDate == nil || ended.EndDate.Format("2006-01-02") != future || ended.EndReason == nil || *ended.EndReason != "employee_resigned" {
		t.Errorf("ended = %+v, want the future end and its reason", ended)
	}
	refused(t, hr.Do(http.MethodPost, termsPath(e.Id), terms("validFrom", shiftDay(t, future, 1))), http.StatusConflict, "terms_after_end")
	addTerms(t, hr, e.Id, terms("validFrom", future, "ftePercent", 60)) // the end day itself is still worked

	var reopened gen.StaffEmployment
	do(t, hr, http.MethodPost, reopenPath(e.Id), nil, http.StatusOK, &reopened)
	if reopened.EndDate != nil || reopened.EndReason != nil {
		t.Errorf("reopened = %+v, want no end and no reason", reopened)
	}
	got := kinds(listEvents(t, hr, p.Id))
	if got[0] != "employment_reopened" || !slices.Contains(got, "employment_ended") {
		t.Errorf("events %v, want employment_ended then employment_reopened", got)
	}
	if r := hr.Do(http.MethodPost, endPath(e.Id+1000), map[string]any{"endDate": future, "endReason": "employee_resigned"}); r.Status != http.StatusNotFound {
		t.Errorf("end an unknown employment: %d, want 404", r.Status)
	}
	if r := hr.Do(http.MethodPost, reopenPath(e.Id+1000), nil); r.Status != http.StatusNotFound {
		t.Errorf("reopen an unknown employment: %d, want 404", r.Status)
	}
}
