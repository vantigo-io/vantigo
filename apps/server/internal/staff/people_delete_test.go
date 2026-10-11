package staff_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// seedEmployment writes an employment for personID directly — employments
// are Task 4's — starting on 1 January 2026 and open-ended, with one terms
// row naming managerID as the manager when it is not 0, and answers the
// employment's id.
func seedEmployment(t *testing.T, h *harness, personID, managerID int64) int64 {
	t.Helper()
	return seedEmploymentDated(t, h, personID, managerID, "2026-01-01", nil)
}

// seedEmploymentDated is seedEmployment from start to end (nil: open-ended).
func seedEmploymentDated(t *testing.T, h *harness, personID, managerID int64, start string, end *string) int64 {
	t.Helper()
	now := h.Now()
	unit := modtest.One[int64](t, h.Harness, `
		WITH le AS (
			INSERT INTO staff.legal_entities (name, is_default, created_at, updated_at)
			VALUES ('Kraft-Verket AS', false, $1, $1) RETURNING id
		)
		INSERT INTO staff.business_units (legal_entity_id, name, is_default, created_at, updated_at)
		SELECT id, 'Oslo', false, $1, $1 FROM le RETURNING id`, now)
	employment := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.employments (person_id, employment_number, relationship_type, business_unit_id, start_date, end_date, end_reason, created_at, updated_at)
		VALUES ($1, $2, 'ordinary', $3, $4::date, $5::date, CASE WHEN $5::date IS NULL THEN NULL ELSE 'contract_ended' END, $6, $6) RETURNING id`,
		personID, fmt.Sprintf("EMP-%d-%d", personID, unit), unit, start, end, now)
	var manager *int64
	if managerID != 0 {
		manager = &managerID
	}
	h.Exec(t, `
		INSERT INTO staff.employment_terms (employment_id, valid_from, employment_type, appointment_form, fte_percent,
			full_time_hours_per_week, manager_person_id, created_at, updated_at)
		VALUES ($1, $2::date, 'fast', 'fast', 100, 37.5, $3, $4, $4)`, employment, start, manager, now)
	return employment
}

// A person is deleted only as created in error (plan reading 5): one with an
// employment, a user link, a department naming them as its manager or
// anyone's terms naming them as the manager is in use — 409 person_in_use —
// and stays.
func TestDeletePerson_RefusedWhileInUse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:view")

	employed := createPerson(t, hr, newPerson("Has", "Employment"))
	seedEmployment(t, h, employed.Id, 0)

	linked := createPerson(t, hr, newPerson("Has", "Link"))
	_, user := signIn(t, h)
	link(t, h, linked.Id, user)

	head := createPerson(t, hr, newPerson("Heads", "Department"))
	createDepartment(t, hr, map[string]any{"code": "OPS", "name": "Operations", "managerPersonId": head.Id})

	manager := createPerson(t, hr, newPerson("Manages", "Someone"))
	report := createPerson(t, hr, newPerson("Is", "Managed"))
	seedEmployment(t, h, report.Id, manager.Id)

	for name, id := range map[string]int64{
		"an employment": employed.Id, "a user link": linked.Id,
		"a department's manager": head.Id, "another person's terms' manager": manager.Id,
	} {
		refused(t, hr.Do(http.MethodDelete, personPath(id), nil), http.StatusConflict, "person_in_use")
		if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1`, id); n != 1 {
			t.Errorf("%s: the person is gone after a refused delete", name)
		}
	}
	r := hr.Do(http.MethodDelete, personPath(report.Id+1000), nil)
	if r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unknown person: %d %q, want a bare 404", r.Status, r.Body)
	}
}

// A person created in error leaves with their next of kin, their roles and
// their history, in one transaction, and the installation keeps one
// person_deleted event carrying the employee number and nothing else.
func TestDeletePerson_CascadesAndRecordsTheEmployeeNumberOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "employeeNumber", "ERR-1", "notes", "created twice"))
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{map[string]any{"name": "Ola", "relationship": "spouse"}}})
	putRoles(t, h, hr, p.Id, map[string]any{"roles": []any{map[string]any{"role": "verneombud"}}})
	keep := createPerson(t, hr, newPerson("Ola", "Nordmann"))

	do(t, hr, http.MethodDelete, personPath(p.Id), nil, http.StatusNoContent, nil)

	for table, n := range map[string]int{
		"people":       h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1`, p.Id),
		"next_of_kin":  h.Count(t, `SELECT count(*) FROM staff.next_of_kin WHERE person_id = $1`, p.Id),
		"person_roles": h.Count(t, `SELECT count(*) FROM staff.person_roles WHERE person_id = $1`, p.Id),
		"events":       h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1`, p.Id),
	} {
		if n != 0 {
			t.Errorf("%s still holds %d rows of the deleted person", table, n)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1`, keep.Id); n != 1 {
		t.Errorf("the other person's events = %d, want their person_created kept", n)
	}
	payload := modtest.One[[]byte](t, h.Harness, `SELECT payload FROM staff.events WHERE person_id IS NULL AND kind = 'person_deleted'`)
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["employeeNumber"] != "ERR-1" {
		t.Errorf("person_deleted payload = %v, want the employee number only", got)
	}
	if r := hr.Do(http.MethodGet, personPath(p.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("GET after delete: %d, want 404", r.Status)
	}
}
