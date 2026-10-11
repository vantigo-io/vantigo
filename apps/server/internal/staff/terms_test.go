package staff_test

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func dateOf(d *gen.StaffTerms) string { return d.ValidFrom.Format(time.DateOnly) }

// A later row takes every field it leaves out from the row in force before
// it, and clears one sent as null; the hours for a full position follow a
// new scheme when they are left out. Each row is in force until the day
// before the next one's.
func TestTerms_DefaultsFromThePreviousRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	_, e := employed(t, hr, "Kari", "2026-01-01", terms("title", "Developer", "costCentre", "CC-1"))

	second := addTerms(t, hr, e.Id, map[string]any{"validFrom": "2026-03-01", "ftePercent": 80})
	if second.Title == nil || *second.Title != "Developer" || second.FtePercent != 80 || second.EmploymentType != "fast" {
		t.Errorf("second row = %+v, want the title and type kept, 80 %%", second)
	}
	third := addTerms(t, hr, e.Id, map[string]any{"validFrom": "2026-05-01", "title": nil})
	if third.Title != nil || third.CostCentre == nil || third.FtePercent != 80 {
		t.Errorf("third row = %+v, want the title cleared, the rest kept", third)
	}
	shift := addTerms(t, hr, e.Id, map[string]any{"validFrom": "2026-06-01", "workingHoursScheme": "shift_365"})
	if shift.FullTimeHoursPerWeek == nil || *shift.FullTimeHoursPerWeek != 36.5 {
		t.Errorf("shift row's hours = %v, want the scheme's 36.5", shift.FullTimeHoursPerWeek)
	}
	rows := listTerms(t, hr, e.Id)
	var tos []string
	for _, r := range rows {
		if r.ValidTo == nil {
			tos = append(tos, "open")
			continue
		}
		tos = append(tos, r.ValidTo.Format(time.DateOnly))
	}
	if want := []string{"2026-02-28", "2026-04-30", "2026-05-31", "open"}; !slices.Equal(tos, want) {
		t.Errorf("validTo = %v, want %v", tos, want)
	}
}

// Every refusal of a new row (design §4 Terms, plan Task 4): the 400s name
// the field and the rule, the 409s the state of the employment.
func TestTerms_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	p, e := employed(t, hr, "Kari", "2026-01-15", terms())
	_, short := employed(t, hr, "Ola", "2026-01-01", terms())
	do(t, hr, http.MethodPost, endPath(short.Id), map[string]any{"endDate": "2026-06-30", "endReason": "contract_ended"}, http.StatusOK, nil)
	addTerms(t, hr, e.Id, terms("validFrom", "2026-03-01"))
	components := make([]any, 21)
	for i := range components {
		components[i] = map[string]any{"name": "C", "amount": 1, "currency": "NOK", "period": "monthly"}
	}

	for _, c := range []struct {
		name   string
		id     int64
		body   map[string]any
		status int
		code   string
		field  string
	}{
		{"no validFrom", e.Id, map[string]any{"ftePercent": 50}, http.StatusBadRequest, "", "validFrom"},
		{"the type cleared", e.Id, map[string]any{"validFrom": "2026-04-01", "employmentType": nil}, http.StatusBadRequest, "terms_required_fields", "employmentType"},
		{"the percentage cleared", e.Id, map[string]any{"validFrom": "2026-04-01", "ftePercent": nil}, http.StatusBadRequest, "terms_required_fields", "ftePercent"},
		{"temporary without a ground", e.Id, terms("validFrom", "2026-04-01", "employmentType", "midlertidig"), http.StatusBadRequest, "temporary_ground_required", "temporaryGround"},
		{"a form the type does not take", e.Id, terms("validFrom", "2026-04-01", "appointmentForm", "midlertidig"), http.StatusBadRequest, "appointment_form_mismatch", "appointmentForm"},
		{"probation over six months", e.Id, terms("validFrom", "2026-04-01", "probationEndDate", "2026-07-16"), http.StatusBadRequest, "probation_too_long", "probationEndDate"},
		{"probation over half a temporary term", e.Id, terms("validFrom", "2026-04-01", "employmentType", "midlertidig", "temporaryGround", "a",
			"expectedEndDate", "2026-07-15", "probationEndDate", "2026-04-16"), http.StatusBadRequest, "probation_too_long", "probationEndDate"},
		{"an extension not agreed", e.Id, terms("validFrom", "2026-04-01", "probationEndDate", "2026-07-15", "probationExtendedTo", "2026-08-15",
			"probationExtensionReason", "Sick"), http.StatusBadRequest, "probation_too_long", "probationExtendedTo"},
		{"shift_365 at 37.5 hours", e.Id, terms("validFrom", "2026-04-01", "workingHoursScheme", "shift_365", "fullTimeHoursPerWeek", 37.5), http.StatusBadRequest, "full_time_hours_mismatch", "fullTimeHoursPerWeek"},
		{"offshore at 27.5 hours", e.Id, terms("validFrom", "2026-04-01", "workingHoursScheme", "offshore_336", "fullTimeHoursPerWeek", 27.5), http.StatusBadRequest, "full_time_hours_mismatch", "fullTimeHoursPerWeek"},
		{"no percentage", e.Id, terms("validFrom", "2026-04-01", "ftePercent", 0), http.StatusBadRequest, "", "ftePercent"},
		{"over a hundred percent", e.Id, terms("validFrom", "2026-04-01", "ftePercent", 100.5), http.StatusBadRequest, "", "ftePercent"},
		{"three decimals", e.Id, terms("validFrom", "2026-04-01", "ftePercent", 50.125), http.StatusBadRequest, "", "ftePercent"},
		{"a six-digit occupation code", e.Id, terms("validFrom", "2026-04-01", "occupationCode", "251201"), http.StatusBadRequest, "", "occupationCode"},
		{"twenty-one pay components", e.Id, terms("validFrom", "2026-04-01", "payComponents", components), http.StatusBadRequest, "", "payComponents"},
		{"a negative amount", e.Id, terms("validFrom", "2026-04-01", "payComponents", []any{map[string]any{"name": "Base", "amount": -1, "currency": "NOK", "period": "monthly"}}), http.StatusBadRequest, "", "payComponents[0].amount"},
		{"a notice without a unit", e.Id, terms("validFrom", "2026-04-01", "noticeEmployee", 1), http.StatusBadRequest, "", "noticeUnit"},
		{"an unknown department", e.Id, terms("validFrom", "2026-04-01", "departmentId", 999999), http.StatusBadRequest, "", "departmentId"},
		{"an unknown manager", e.Id, terms("validFrom", "2026-04-01", "managerPersonId", 999999), http.StatusBadRequest, "", "managerPersonId"},
		{"correction on a new row", e.Id, terms("validFrom", "2026-04-01", "correction", true), http.StatusBadRequest, "", "correction"},
		{"a date already used", e.Id, terms("validFrom", "2026-03-01"), http.StatusConflict, "terms_valid_from_taken", ""},
		{"before the start", e.Id, terms("validFrom", "2026-01-14"), http.StatusConflict, "terms_before_start", ""},
		{"after the end", short.Id, terms("validFrom", "2026-07-01"), http.StatusConflict, "terms_after_end", ""},
		{"the person as their own manager", e.Id, terms("validFrom", "2026-04-01", "managerPersonId", p.Id), http.StatusConflict, "self_manager", ""},
	} {
		r := hr.Do(http.MethodPost, termsPath(c.id), c.body)
		if r.Status != c.status {
			t.Errorf("%s: %d %s, want %d %s", c.name, r.Status, r.Body, c.status, c.code)
			continue
		}
		pr := refused(t, r, c.status, c.code)
		if c.field != "" && len(pr.Errors[c.field]) == 0 {
			t.Errorf("%s: errors %v, want one on %s", c.name, pr.Errors, c.field)
		}
	}
	if rows := listTerms(t, hr, e.Id); len(rows) != 2 {
		t.Errorf("%d rows after the refusals, want the two written before", len(rows))
	}
	// The agreed extension with a reason, no longer than the probation, passes.
	addTerms(t, hr, e.Id, terms("validFrom", "2026-04-01", "probationEndDate", "2026-07-15", "probationExtensionAgreed", true,
		"probationExtendedTo", "2026-09-15", "probationExtensionReason", "Sick leave during the probation"))
	if r := hr.Do(http.MethodPost, termsPath(e.Id+1000), terms("validFrom", "2026-04-01")); r.Status != http.StatusNotFound {
		t.Errorf("unknown employment: %d, want 404", r.Status)
	}
}

// A row is replaced when it is the latest, or with correction true when it is
// not — HR's correction of a past row — each a terms_corrected event with the
// values before and after; anything else is terms_not_latest. The first row
// stays dated the start, and a stale If-Match is a conflict.
func TestTerms_ChangeTheLatestOrCorrect(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	p, e := employed(t, hr, "Kari", "2026-01-01", terms("title", "Developer"))
	first := listTerms(t, hr, e.Id)[0]
	latest := addTerms(t, hr, e.Id, terms("validFrom", "2026-03-01", "ftePercent", 80))

	refused(t, hr.Do(http.MethodPut, termsRowPath(first.Id), terms("validFrom", "2026-01-01", "title", "Senior"), ifMatch(first.Revision)),
		http.StatusConflict, "terms_not_latest")
	var corrected gen.StaffTerms
	do(t, hr, http.MethodPut, termsRowPath(first.Id), terms("validFrom", "2026-01-01", "title", "Senior", "correction", true),
		http.StatusOK, &corrected, ifMatch(first.Revision))
	if corrected.Title == nil || *corrected.Title != "Senior" || corrected.Revision == first.Revision {
		t.Errorf("corrected = %+v, want the new title and revision", corrected)
	}
	refused(t, hr.Do(http.MethodPut, termsRowPath(first.Id), terms("validFrom", "2026-01-02", "correction", true), ifMatch(corrected.Revision)),
		http.StatusConflict, "terms_first_row_start")
	refused(t, hr.Do(http.MethodPut, termsRowPath(latest.Id), terms("validFrom", "2026-03-01"), ifMatch(latest.Revision+1)),
		http.StatusConflict, "revision_conflict")
	var changed gen.StaffTerms
	do(t, hr, http.MethodPut, termsRowPath(latest.Id), terms("validFrom", "2026-03-15", "ftePercent", 60), http.StatusOK, &changed, ifMatch(latest.Revision))
	if dateOf(&changed) != "2026-03-15" || changed.FtePercent != 60 || changed.Title != nil {
		t.Errorf("changed = %+v, want a full replace from 2026-03-15 at 60 %% with no title", changed)
	}
	if r := hr.Do(http.MethodPut, termsRowPath(latest.Id+1000), terms("validFrom", "2026-03-01"), ifMatch(1)); r.Status != http.StatusNotFound {
		t.Errorf("unknown row: %d, want 404", r.Status)
	}

	events := listEvents(t, hr, p.Id)
	var correction *gen.StaffEvent
	for i := range events {
		if events[i].Kind == "terms_corrected" && fieldsOf(events[i])["title"] != nil {
			correction = &events[i]
		}
	}
	if correction == nil {
		t.Fatalf("events %v, want a terms_corrected naming the title", kinds(events))
	}
	if f := fieldsOf(*correction)["title"].(map[string]any); f["from"] != "Developer" || f["to"] != "Senior" {
		t.Errorf("title = %v, want Developer → Senior", f)
	}
	if correction.Payload["termsId"] != float64(first.Id) || correction.Payload["employmentId"] != float64(e.Id) {
		t.Errorf("payload %v, want the row and the employment named", correction.Payload)
	}
}

// Only the latest row is deleted, never the first: the one before it is in
// force again, and the deletion is an event.
func TestTerms_DeleteTheLatestOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	p, e := employed(t, hr, "Kari", "2026-01-01", terms())
	first := listTerms(t, hr, e.Id)[0]
	refused(t, hr.Do(http.MethodDelete, termsRowPath(first.Id), nil), http.StatusConflict, "terms_first_row_start")
	middle := addTerms(t, hr, e.Id, terms("validFrom", "2026-03-01", "ftePercent", 80))
	latest := addTerms(t, hr, e.Id, terms("validFrom", "2026-05-01", "ftePercent", 60))
	refused(t, hr.Do(http.MethodDelete, termsRowPath(middle.Id), nil), http.StatusConflict, "terms_not_latest")
	do(t, hr, http.MethodDelete, termsRowPath(latest.Id), nil, http.StatusNoContent, nil)
	if rows := listTerms(t, hr, e.Id); len(rows) != 2 || rows[1].Id != middle.Id || rows[1].ValidTo != nil {
		t.Errorf("rows = %+v, want the first two, the middle one open-ended again", rows)
	}
	if r := hr.Do(http.MethodDelete, termsRowPath(latest.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("deleted twice: %d, want 404", r.Status)
	}
	if got := kinds(listEvents(t, hr, p.Id)); got[0] != "terms_deleted" {
		t.Errorf("events %v, want terms_deleted first", got)
	}
}

// Nobody manages themselves, and the manager graph never loops (design §4
// Managers): a row naming a manager is refused manager_cycle when it closes a
// loop on its own date or on any later date the graph changes — through
// someone else's later row, or through a department's manager — and a
// department's head is managed by the parent department's, never by
// themselves. A department write that closes a loop is refused the same way.
func TestTerms_ManagerCycles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	a, ea := employed(t, hr, "Anne", "2026-01-01", terms())
	b, eb := employed(t, hr, "Bjørn", "2026-01-01", terms())
	c, ec := employed(t, hr, "Cecilie", "2026-01-01", terms())

	addTerms(t, hr, ea.Id, terms("validFrom", "2026-06-01", "managerPersonId", b.Id))
	// B names A from March: no loop in March, but one from June, when A's
	// row naming B takes effect.
	refused(t, hr.Do(http.MethodPost, termsPath(eb.Id), terms("validFrom", "2026-03-01", "managerPersonId", a.Id)), http.StatusConflict, "manager_cycle")
	// C names A: A → B, C → A, no loop.
	addTerms(t, hr, ec.Id, terms("validFrom", "2026-02-01", "managerPersonId", a.Id))

	// Through a department: B heads Sales and C is in it with no manager of
	// their own, so B manages C; B naming C closes the loop.
	sales := createDepartment(t, hr, map[string]any{"code": "SALES", "name": "Sales", "managerPersonId": b.Id})
	addTerms(t, hr, ec.Id, map[string]any{"validFrom": "2026-07-01", "managerPersonId": nil, "departmentId": sales.Id})
	refused(t, hr.Do(http.MethodPost, termsPath(eb.Id), terms("validFrom", "2026-08-01", "managerPersonId", c.Id)), http.StatusConflict, "manager_cycle")
	// B in their own department is managed by the parent's head, not by
	// themselves: no self-manager, no loop.
	addTerms(t, hr, eb.Id, terms("validFrom", "2026-08-01", "departmentId", sales.Id))

	// A department write: making A the head of Sales while A is managed by
	// B (from June) and B is in Sales gives B → A → B.
	refused(t, hr.Do(http.MethodPut, rowPath(departmentsPath, sales.Id), map[string]any{"code": "SALES", "name": "Sales", "managerPersonId": a.Id},
		ifMatch(sales.Revision)), http.StatusConflict, "manager_cycle")
}

// Two writes that would each close the loop the other opens are judged one
// after the other under the manager-graph lock: exactly one is written, the
// other refused manager_cycle. A write that changes a manager waits for the
// lock while another transaction holds it.
func TestTerms_ConcurrentWritesSerialisedByTheGraphLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	a, ea := employed(t, hr, "Anne", "2026-01-01", terms())
	b, eb := employed(t, hr, "Bjørn", "2026-01-01", terms())

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, w := range []struct {
		employment, manager int64
	}{{ea.Id, b.Id}, {eb.Id, a.Id}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = hr.Do(http.MethodPost, termsPath(w.employment), terms("validFrom", "2026-02-01", "managerPersonId", w.manager)).Status
		}()
	}
	wg.Wait()
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusCreated, http.StatusConflict}) {
		t.Errorf("statuses %v, want one 201 and one 409", statuses)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.employment_terms WHERE manager_person_id IS NOT NULL`); n != 1 {
		t.Errorf("%d rows name a manager, want 1", n)
	}

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('staff-manager-graph'))`); err != nil {
		t.Fatal(err)
	}
	_, ec := employed(t, hr, "Cecilie", "2026-01-01", terms())
	done := make(chan *modtest.Response, 1)
	go func() {
		done <- hr.Do(http.MethodPost, termsPath(ec.Id), terms("validFrom", "2026-03-01", "managerPersonId", a.Id))
	}()
	select {
	case r := <-done:
		t.Errorf("answered %d while the graph lock was held, want it to wait", r.Status)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.Status != http.StatusCreated {
			t.Errorf("after the lock was released: %d %s, want 201", r.Status, r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("still waiting after the lock was released")
	}
}
