package staff_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

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

// The accounts worker's preparing link names no user (plan reading 9): it
// does not keep a person created in error, and the delete takes it along —
// but not while the worker holds it, when identity may be creating the user
// it is for: accounts_busy, and nothing deleted (pre-flight I4). A link
// naming a user is still person_in_use.
func TestDeletePerson_APreparingLinkGoesWithThePerson(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	free := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	preparingLink(t, h, free.Id)
	do(t, hr, http.MethodDelete, personPath(free.Id), nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.identity_links WHERE person_id = $1`, free.Id); n != 0 {
		t.Errorf("the preparing link outlived its person: %d", n)
	}

	leased := createPerson(t, hr, newPerson("Ola", "Hansen"))
	preparingLink(t, h, leased.Id)
	h.Exec(t, `UPDATE staff.identity_links SET lease_id = gen_random_uuid(), lease_until = $2 WHERE person_id = $1`,
		leased.Id, h.Now().Add(5*time.Minute))
	refused(t, hr.Do(http.MethodDelete, personPath(leased.Id), nil), http.StatusConflict, "accounts_busy")
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1`, leased.Id); n != 1 {
		t.Errorf("a busy delete removed the person")
	}
	// A lease that has run out is no hold.
	h.Exec(t, `UPDATE staff.identity_links SET lease_until = $2 WHERE person_id = $1`, leased.Id, h.Now().Add(-time.Minute))
	do(t, hr, http.MethodDelete, personPath(leased.Id), nil, http.StatusNoContent, nil)

	linked := createPerson(t, hr, newPerson("Per", "Olsen"))
	managedLink(t, h, linked.Id, uuid.New(), "pending")
	refused(t, hr.Do(http.MethodDelete, personPath(linked.Id), nil), http.StatusConflict, "person_in_use")
}

// The delete racing the accounts worker's claim on a preparing link (review
// L1), on a pool of two with the claim a raw transaction of its own holding
// the link FOR UPDATE, as the worker's claim does: the delete takes the
// person and the manager graph, comes to the link last and waits on it —
// pg_blocking_pids names the claim — then judges the row the claim left. A
// lease set: accounts_busy, the person kept. No lease: deleted. A user named
// meanwhile (a write past the person's lock the delete still meets):
// person_in_use, the person kept. Never 40P01. Not parallel: a race test
// never is.
func TestDeletePerson_RacesTheWorkersClaim(t *testing.T) {
	h := newHarness(t, modtest.WithPoolMaxConns(2))
	hr, _ := signIn(t, h, "staff:manage")
	probe := ownConn(t, h)
	before := deadlocksIn(t, probe)

	for _, c := range []struct {
		name   string
		claim  string
		status int
		code   string
	}{
		{"a lease set", `UPDATE staff.identity_links SET lease_id = gen_random_uuid(), lease_until = $2 WHERE person_id = $1`, http.StatusConflict, "accounts_busy"},
		{"no lease", ``, http.StatusNoContent, ""},
		{"a user named", `UPDATE staff.identity_links SET external_id = gen_random_uuid()::text WHERE person_id = $1 AND $2::timestamptz IS NOT NULL`, http.StatusConflict, "person_in_use"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
			preparingLink(t, h, p.Id)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			claimConn := ownConn(t, h)
			claim, err := claimConn.Begin(ctx)
			if err != nil {
				t.Fatalf("begin the claim: %v", err)
			}
			defer func() { _ = claim.Rollback(context.Background()) }()
			if _, err := claim.Exec(ctx, `SELECT id FROM staff.identity_links WHERE person_id = $1 FOR UPDATE`, p.Id); err != nil {
				t.Fatalf("the claim's lock: %v", err)
			}
			if c.claim != "" {
				if _, err := claim.Exec(ctx, c.claim, p.Id, h.Now().Add(5*time.Minute)); err != nil {
					t.Fatalf("the claim: %v", err)
				}
			}
			answered := make(chan *modtest.Response, 1)
			go func() { answered <- hr.Do(http.MethodDelete, personPath(p.Id), nil, modtest.Context(ctx)) }()
			_, blockers := waiterOn(t, probe)
			if want := []uint32{claimConn.PgConn().PID()}; !slices.Equal(blockers, want) {
				t.Fatalf("the delete waits on %v, want the claim %v", blockers, want)
			}
			if err := claim.Commit(ctx); err != nil {
				t.Fatalf("commit the claim: %v", err)
			}
			r := <-answered
			if c.status == http.StatusNoContent {
				if r.Status != http.StatusNoContent {
					t.Fatalf("the delete: %d %s, want 204", r.Status, r.Body)
				}
				return
			}
			refused(t, r, c.status, c.code)
			if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1`, p.Id); n != 1 {
				t.Errorf("a refused delete removed the person")
			}
		})
	}
	if after := deadlocksIn(t, probe); after != before {
		t.Errorf("deadlocks = %d, want %d: the race ended in 40P01", after, before)
	}
}
