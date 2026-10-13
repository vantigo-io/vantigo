package staff_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The absences' paths (delivery B, Task 3).
const (
	absencesPath         = "/api/v1/staff/absences"
	meAbsencesPath       = "/api/v1/staff/me/absences"
	meHolidayAccountPath = "/api/v1/staff/me/holiday-account"
	adjustmentsBase      = "/api/v1/staff/holiday-adjustments"
)

func absencePath(id int64) string              { return rowPath(absencesPath, id) }
func cancelAbsencePath(id int64) string        { return absencePath(id) + "/cancel" }
func holidayAccountPath(personID int64) string { return personPath(personID) + "/holiday-account" }
func adjustmentsPath(personID int64) string    { return personPath(personID) + "/holiday-adjustments" }
func adjustmentPath(id int64) string           { return rowPath(adjustmentsBase, id) }
func carryOverPath(personID int64) string      { return personPath(personID) + "/carry-over" }

// typeID is the id of the absence type keyed key.
func typeID(t *testing.T, h *harness, key string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM staff.absence_types WHERE key = $1`, key)
}

// absenceWorld is one installation with the audiences of design §5 on one
// person, Kari: Kari herself, her manager Bjørn (her terms name him), a
// colleague holding staff:view, HR (staff:manage), staff:personal-data alone,
// and a caller with staff:access and nothing else, linked to nobody.
type absenceWorld struct {
	h                      *harness
	hr, pd, viewer, nobody *modtest.Client
	kari, boss             gen.StaffPerson
	kariC, bossC           *modtest.Client
	kariUser               uuid.UUID
}

func newAbsenceWorld(t *testing.T, opts ...modtest.Option) absenceWorld {
	t.Helper()
	h := newHarness(t, opts...)
	w := absenceWorld{h: h}
	w.hr, _ = signIn(t, h, "staff:manage")
	company(t, w.hr)
	w.boss, _ = employed(t, w.hr, "Bjørn", "2019-01-01", terms())
	w.kari, _ = employed(t, w.hr, "Kari", "2020-01-01", terms("managerPersonId", w.boss.Id))
	var bossUser uuid.UUID
	w.kariC, w.kariUser = signIn(t, h)
	w.bossC, bossUser = signIn(t, h)
	link(t, h, w.kari.Id, w.kariUser)
	link(t, h, w.boss.Id, bossUser)
	w.pd, _ = signIn(t, h, "staff:personal-data")
	w.viewer, _ = signIn(t, h, "staff:view")
	w.nobody, _ = signIn(t, h)
	return w
}

// absence is a request body for Kari's own absence of type key from first to
// last, with the given fields replaced.
func absence(t *testing.T, h *harness, key, first, last string, kv ...any) map[string]any {
	t.Helper()
	return with(map[string]any{"typeId": typeID(t, h, key), "firstDay": first, "lastDay": last}, kv...)
}

// requestOwn posts one of the caller's own absences and fails the test unless
// it was created.
func requestOwn(t *testing.T, c *modtest.Client, body map[string]any) gen.StaffAbsence {
	t.Helper()
	var out gen.StaffAbsence
	do(t, c, http.MethodPost, meAbsencesPath, body, http.StatusCreated, &out)
	return out
}

// register posts an absence for personID by HR and fails the test unless it
// was created.
func register(t *testing.T, c *modtest.Client, personID int64, body map[string]any) gen.StaffAbsence {
	t.Helper()
	var out gen.StaffAbsence
	do(t, c, http.MethodPost, absencesPath, with(body, "personId", personID), http.StatusCreated, &out)
	return out
}

// eventOf is the newest event of kind about absence id in the history as c
// reads it, nil when c reads none.
func eventOf(t *testing.T, c *modtest.Client, personID int64, kind string, id int64) map[string]any {
	t.Helper()
	var page struct {
		Data []struct {
			Kind    string         `json:"kind"`
			Payload map[string]any `json:"payload"`
		} `json:"data"`
	}
	do(t, c, http.MethodGet, pagedPath(eventsPath(personID), 1, 100), nil, http.StatusOK, &page)
	for _, e := range page.Data {
		if e.Kind == kind && e.Payload["absenceId"] == float64(id) {
			return e.Payload["fields"].(map[string]any)
		}
	}
	return nil
}

// A person requests an absence of a type that needs a decision and
// registers one that is a notification (design S12); HR registers one on a
// person's behalf. The answer carries the working days and, for a type that
// draws on the holiday account, the account after it; each write is an
// event naming the absence, its fields and their classes.
func TestAbsences_RequestAndRegister(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h

	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09", "note", "Høstferie"))
	if statusOf(ferie) != gen.Requested || workingDaysOf(ferie) != 5 || ferie.PersonId != w.kari.Id {
		t.Errorf("ferie = %+v, want requested, five working days, Kari's", ferie)
	}
	if ferie.Accounts == nil || len(*ferie.Accounts) != 1 {
		t.Fatalf("ferie accounts = %v, want 2026's", ferie.Accounts)
	}
	if a := (*ferie.Accounts)[0]; a.Year != 2026 || a.Entitlement != 21 || a.Pending != 5 || a.Remaining != 21 || a.Overdrawn {
		t.Errorf("the account after the request = %+v, want 21 days, 5 pending, 21 remaining", a)
	}
	if ferie.Overdrawn == nil || *ferie.Overdrawn {
		t.Errorf("overdrawn = %v, want false", ferie.Overdrawn)
	}

	self := requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-09-14", "2026-09-15"))
	if statusOf(self) != gen.Registered || self.Accounts != nil {
		t.Errorf("egenmelding = %+v, want registered, no account", self)
	}
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-11-02", "2026-11-13", "degreePercent", 50))
	if statusOf(sick) != gen.Registered || sick.DegreePercent == nil || *sick.DegreePercent != 50 || sick.Percent == nil || *sick.Percent != 100 {
		t.Errorf("sykmelding = %+v, want registered at 50 %%, percent 100", sick)
	}
	hours := requestOwn(t, w.kariC, absence(t, h, "helse", "2026-09-16", "2026-09-16", "hours", 2.5))
	if workingDaysOf(hours) != 0 || hours.Hours == nil || *hours.Hours != 2.5 {
		t.Errorf("helse = %+v, want 2.5 hours counting no working day", hours)
	}

	for _, c := range []struct {
		id      int64
		kind    string
		classes map[string]string
	}{
		{ferie.Id, "absence_requested", map[string]string{"type": "C", "firstDay": "C", "lastDay": "C", "status": "C", "percent": "C", "note": "D"}},
		{sick.Id, "absence_registered", map[string]string{"type": "E", "degree": "E", "firstDay": "C", "lastDay": "C", "status": "C", "percent": "C"}},
	} {
		got := modtest.One[map[string]string](t, h.Harness, `SELECT payload->'classes' FROM staff.events
			WHERE kind = $1 AND (payload->>'absenceId')::bigint = $2`, c.kind, c.id)
		if len(got) != len(c.classes) {
			t.Errorf("%s classes = %v, want %v", c.kind, got, c.classes)
		}
		for k, v := range c.classes {
			if got[k] != v {
				t.Errorf("%s classes = %v, want %v", c.kind, got, c.classes)
				break
			}
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'absence_registered'
		AND (payload->>'absenceId')::bigint = $1 AND payload_classes = '{C,E}'`, sick.Id); n != 1 {
		t.Error("the sykmelding's event does not touch exactly C and E")
	}
}

// Who may write an absence (plan Global Constraints, S12, pre-flight M2): the
// person for themselves, staff:manage for anyone — nobody else, a manager
// registering for a report included; a caller linked to nobody has no own.
func TestAbsences_WhoMayWrite(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	body := absence(t, h, "ferie", "2026-10-05", "2026-10-09")
	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "a colleague": w.viewer, "personal-data alone": w.pd} {
		if r := c.Do(http.MethodPost, absencesPath, with(body, "personId", w.kari.Id)); r.Status != http.StatusForbidden {
			t.Errorf("%s registering for Kari: %d %s, want 403", name, r.Status, r.Body)
		}
	}
	if r := w.kariC.Do(http.MethodPost, absencesPath, with(body, "personId", w.boss.Id)); r.Status != http.StatusForbidden {
		t.Errorf("Kari registering for her manager: %d, want 403", r.Status)
	}
	refused(t, w.nobody.Do(http.MethodPost, meAbsencesPath, body), http.StatusNotFound, "no_person")
	refused(t, w.nobody.Do(http.MethodGet, meAbsencesPath, nil), http.StatusNotFound, "no_person")
	refused(t, w.nobody.Do(http.MethodGet, meHolidayAccountPath, nil), http.StatusNotFound, "no_person")
	if r := w.hr.Do(http.MethodPost, absencesPath, with(body, "personId", w.kari.Id+1000)); r.Status != http.StatusNotFound {
		t.Errorf("HR for an unknown person: %d, want 404", r.Status)
	}
	var own gen.StaffAbsence
	do(t, w.kariC, http.MethodPost, absencesPath, with(body, "personId", w.kari.Id), http.StatusCreated, &own)
	if statusOf(own) != gen.Requested {
		t.Errorf("Kari's own through POST /absences = %+v, want requested", own)
	}
	// The person's own request refuses a property it does not name.
	p := refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, with(body, "personId", w.boss.Id, "firstDay", "2026-11-02", "lastDay", "2026-11-02")),
		http.StatusBadRequest, "")
	if len(p.Errors["personId"]) == 0 {
		t.Errorf("errors = %v, want personId refused", p.Errors)
	}
	h.Exec(t, `UPDATE staff.people SET erased_at = now() WHERE id = $1`, w.kari.Id)
	refused(t, w.hr.Do(http.MethodPost, absencesPath, with(absence(t, h, "ferie", "2026-12-07", "2026-12-07"), "personId", w.kari.Id)),
		http.StatusConflict, "person_erased")
}

// Every rule of a new absence (plan "Exact values"), each a 400 naming its
// field, the design's rules with their codes.
func TestAbsences_Refusals(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	for _, c := range []struct {
		name  string
		body  map[string]any
		field string
		code  string
	}{
		{"a percent on ferie", absence(t, h, "ferie", "2026-10-05", "2026-10-05", "percent", 50), "percent", "percent_not_allowed"},
		{"a percent on a sykmelding", absence(t, h, "sykmelding", "2026-10-05", "2026-10-05", "percent", 50), "percent", "percent_not_allowed"},
		{"hours on a day type", absence(t, h, "ferie", "2026-10-05", "2026-10-05", "hours", 2), "hours", "hours_on_day_type"},
		{"an hour type without hours", absence(t, h, "helse", "2026-10-05", "2026-10-05"), "hours", "hours_on_day_type"},
		{"an hour type over two days", absence(t, h, "helse", "2026-10-05", "2026-10-06", "hours", 2), "lastDay", "hours_on_day_type"},
		{"a note on egenmelding", absence(t, h, "egenmelding", "2026-10-05", "2026-10-05", "note", "Influensa"), "note", "absence_note_on_sick"},
		{"a note on sykt barn", absence(t, h, "sykt_barn", "2026-10-05", "2026-10-05", "note", "Feber"), "note", "absence_note_on_sick"},
		{"a degree on ferie", absence(t, h, "ferie", "2026-10-05", "2026-10-05", "degreePercent", 50), "degreePercent", ""},
		{"a degree of 0", absence(t, h, "sykmelding", "2026-10-05", "2026-10-05", "degreePercent", 0), "degreePercent", ""},
		{"a percent of 101", absence(t, h, "foreldrepermisjon", "2026-10-05", "2026-10-05", "percent", 101), "percent", ""},
		{"a percent of three decimals", absence(t, h, "foreldrepermisjon", "2026-10-05", "2026-10-05", "percent", 33.333), "percent", ""},
		{"hours not in quarters", absence(t, h, "helse", "2026-10-05", "2026-10-05", "hours", 0.3), "hours", ""},
		{"hours past a day", absence(t, h, "helse", "2026-10-05", "2026-10-05", "hours", 24.25), "hours", ""},
		{"the last day before the first", absence(t, h, "ferie", "2026-10-09", "2026-10-05"), "lastDay", ""},
		{"more than 366 days", absence(t, h, "foreldrepermisjon", "2026-01-01", "2027-01-02"), "lastDay", ""},
		{"before the employment", absence(t, h, "ferie", "2019-12-30", "2020-01-03"), "firstDay", ""},
		{"the reserved type", absence(t, h, "absent", "2026-10-05", "2026-10-05"), "typeId", ""},
		{"an unknown type", with(absence(t, h, "ferie", "2026-10-05", "2026-10-05"), "typeId", 99999), "typeId", ""},
		{"a note too long", absence(t, h, "ferie", "2026-10-05", "2026-10-05", "note", string(make([]byte, 501))), "note", ""},
	} {
		p := refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, c.body), http.StatusBadRequest, c.code)
		if len(p.Errors[c.field]) == 0 {
			t.Errorf("%s: errors = %v, want %s named", c.name, p.Errors, c.field)
		}
	}
	// A hidden company type is not one to pick either.
	h.Exec(t, `UPDATE staff.absence_types SET hidden = true WHERE key = 'velferdspermisjon'`)
	p := refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "velferdspermisjon", "2026-10-05", "2026-10-05")), http.StatusBadRequest, "")
	if len(p.Errors["typeId"]) == 0 {
		t.Errorf("a hidden type: errors = %v, want typeId named", p.Errors)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE person_id = $1`, w.kari.Id); n != 0 {
		t.Errorf("%d absences written, want none", n)
	}
}

// Overlap (design §3.5, readings 17 and 27): two rows of types that allow a
// share stand together up to 100 % a day; a requested row blocks another as
// an approved one would; an hour row stands beside a day row; a change is
// judged without the row itself.
func TestAbsences_Overlap(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	requestOwn(t, w.kariC, absence(t, h, "foreldrepermisjon", "2026-10-05", "2026-10-09", "percent", 50))
	requestOwn(t, w.kariC, absence(t, h, "offentlig_verv", "2026-10-07", "2026-10-07", "percent", 50))
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "pleie_naerstaende", "2026-10-07", "2026-10-07", "percent", 10)),
		http.StatusConflict, "absence_overlap")
	requestOwn(t, w.kariC, absence(t, h, "pleie_naerstaende", "2026-10-08", "2026-10-08", "percent", 10))

	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-11-02", "2026-11-06"))
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "ferie", "2026-11-06", "2026-11-10")),
		http.StatusConflict, "absence_overlap")
	refused(t, w.hr.Do(http.MethodPost, absencesPath, with(absence(t, h, "egenmelding", "2026-11-04", "2026-11-04"), "personId", w.kari.Id)),
		http.StatusConflict, "absence_overlap")
	requestOwn(t, w.kariC, absence(t, h, "helse", "2026-11-04", "2026-11-04", "hours", 2))

	var moved gen.StaffAbsence
	do(t, w.kariC, http.MethodPut, absencePath(ferie.Id), absence(t, h, "ferie", "2026-11-03", "2026-11-09"), http.StatusOK, &moved, ifMatch(ferie.Revision))
	if workingDaysOf(moved) != 5 {
		t.Errorf("moved = %+v, want five working days", moved)
	}
	// Cancelled, it blocks nothing.
	do(t, w.kariC, http.MethodPost, cancelAbsencePath(ferie.Id), nil, http.StatusOK, nil)
	requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-11-03", "2026-11-04"))
}

// Editing, deleting and cancelling (plan Task 3, readings 16, pre-flight M8):
// the person changes, deletes and cancels their own request; a registered
// row — a sick one included — is HR's to change and delete; a decided row is
// never deleted; anyone else who sees the row is refused with the uniform
// 403, and someone who does not sees the 404 of an unknown id.
func TestAbsences_EditDeleteCancel(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	req := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	sick := requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-09-14", "2026-09-15"))

	refused(t, w.kariC.Do(http.MethodPut, absencePath(req.Id), absence(t, h, "ferie", "2026-10-05", "2026-10-07"), ifMatch(req.Revision+1)),
		http.StatusConflict, "revision_conflict")
	var changed gen.StaffAbsence
	do(t, w.kariC, http.MethodPut, absencePath(req.Id), absence(t, h, "ferie", "2026-10-05", "2026-10-07", "note", "Kortere"),
		http.StatusOK, &changed, ifMatch(req.Revision))
	if workingDaysOf(changed) != 3 || changed.Revision != req.Revision+1 {
		t.Errorf("changed = %+v, want three days and the revision moved on", changed)
	}
	if f := eventOf(t, w.kariC, w.kari.Id, "absence_updated", req.Id); f == nil || f["lastDay"] == nil || f["note"] == nil || f["type"] != nil {
		t.Errorf("absence_updated fields = %v, want lastDay and note, not the unchanged type", f)
	}
	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "a colleague": w.viewer, "personal-data alone": w.pd} {
		if r := c.Do(http.MethodPut, absencePath(req.Id), absence(t, h, "ferie", "2026-10-05", "2026-10-06"), ifMatch(changed.Revision)); r.Status != http.StatusForbidden && r.Status != http.StatusNotFound {
			t.Errorf("%s changing Kari's request: %d, want 403 or 404", name, r.Status)
		}
		if r := c.Do(http.MethodDelete, absencePath(req.Id), nil); r.Status != http.StatusForbidden && r.Status != http.StatusNotFound {
			t.Errorf("%s deleting Kari's request: %d, want 403 or 404", name, r.Status)
		}
		if r := c.Do(http.MethodPost, cancelAbsencePath(req.Id), nil); r.Status != http.StatusForbidden && r.Status != http.StatusNotFound {
			t.Errorf("%s cancelling Kari's request: %d, want 403 or 404", name, r.Status)
		}
	}
	if r := w.bossC.Do(http.MethodDelete, absencePath(req.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("her manager, who sees the row, deleting it: %d, want the uniform 403", r.Status)
	}
	if r := w.nobody.Do(http.MethodGet, absencePath(req.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a stranger reading the row: %d, want 404", r.Status)
	}
	if r := w.hr.Do(http.MethodGet, absencePath(req.Id+1000), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown id: %d, want 404", r.Status)
	}

	// The registered egenmelding: the person asks; HR ends it early.
	refused(t, w.kariC.Do(http.MethodPut, absencePath(sick.Id), absence(t, h, "egenmelding", "2026-09-14", "2026-09-14"), ifMatch(sick.Revision)),
		http.StatusConflict, "absence_not_requested")
	refused(t, w.kariC.Do(http.MethodDelete, absencePath(sick.Id), nil), http.StatusConflict, "absence_not_requested")
	refused(t, w.kariC.Do(http.MethodPost, cancelAbsencePath(sick.Id), nil), http.StatusConflict, "absence_not_cancellable")
	var ended gen.StaffAbsence
	do(t, w.hr, http.MethodPut, absencePath(sick.Id), absence(t, h, "egenmelding", "2026-09-14", "2026-09-14"), http.StatusOK, &ended, ifMatch(sick.Revision))
	if ended.LastDay.Format("2006-01-02") != "2026-09-14" || statusOf(ended) != gen.Registered {
		t.Errorf("ended = %+v, want one day, registered", ended)
	}
	do(t, w.hr, http.MethodDelete, absencePath(sick.Id), nil, http.StatusNoContent, nil)
	if f := eventOf(t, w.hr, w.kari.Id, "absence_deleted", sick.Id); f == nil || f["firstDay"] == nil {
		t.Errorf("absence_deleted fields = %v, want the row's fields as they were", f)
	}

	// The request: cancelled, it stays as history and is cancelled no more.
	var cancelled gen.StaffAbsence
	do(t, w.kariC, http.MethodPost, cancelAbsencePath(req.Id), nil, http.StatusOK, &cancelled)
	if statusOf(cancelled) != gen.Cancelled {
		t.Errorf("cancelled = %+v", cancelled)
	}
	refused(t, w.kariC.Do(http.MethodPost, cancelAbsencePath(req.Id), nil), http.StatusConflict, "absence_not_cancellable")
	refused(t, w.kariC.Do(http.MethodDelete, absencePath(req.Id), nil), http.StatusConflict, "absence_not_requested")
	refused(t, w.hr.Do(http.MethodDelete, absencePath(req.Id), nil), http.StatusConflict, "absence_not_requested")
	refused(t, w.kariC.Do(http.MethodPut, absencePath(req.Id), absence(t, h, "ferie", "2026-10-05", "2026-10-06"), ifMatch(cancelled.Revision)),
		http.StatusConflict, "absence_not_requested")
	if f := eventOf(t, w.kariC, w.kari.Id, "absence_cancelled", req.Id); f == nil || f["status"] == nil {
		t.Errorf("absence_cancelled fields = %v, want the status", f)
	}

	// A fresh request, deleted by its person.
	gone := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-12-07", "2026-12-08"))
	do(t, w.kariC, http.MethodDelete, absencePath(gone.Id), nil, http.StatusNoContent, nil)
	if r := w.kariC.Do(http.MethodGet, absencePath(gone.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a deleted row: %d, want 404", r.Status)
	}
	// HR cancels a request too.
	other := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-12-14", "2026-12-15"))
	do(t, w.hr, http.MethodPost, cancelAbsencePath(other.Id), nil, http.StatusOK, nil)
}

// The shaping matrix of an absence (design §5, plan pre-flight I4; no
// Approver column until Task 4, M10): the dates and the status to everyone
// who sees the row; the type to the person, the manager and HR — an HR-only
// type "leave" to the manager and to HR without staff:personal-data; percent,
// hours, note and degree never to a colleague, who sees only approved and
// registered rows, as "absent".
func TestAbsences_ShapingMatrix(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09", "note", "Høstferie"))
	hrOnly := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	leave := requestOwn(t, w.kariC, absence(t, h, "foreldrepermisjon", "2026-12-07", "2026-12-11", "percent", 40))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved' WHERE id = ANY($1)`, []int64{hrOnly.Id, leave.Id})
	ferieType, hrOnlyType := typeID(t, h, "ferie"), typeID(t, h, "svangerskapspermisjon")

	type shown struct {
		as      gen.StaffAbsenceShownAs
		typeID  int64
		detail  bool
		visible bool
	}
	for _, c := range []struct {
		name string
		c    *modtest.Client
		rows map[int64]shown
	}{
		{"the person", w.kariC, map[int64]shown{
			ferie.Id: {gen.StaffAbsenceShownAsType, ferieType, true, true}, hrOnly.Id: {gen.StaffAbsenceShownAsType, hrOnlyType, true, true}, leave.Id: {gen.StaffAbsenceShownAsType, 0, true, true}}},
		{"her manager", w.bossC, map[int64]shown{
			ferie.Id: {gen.StaffAbsenceShownAsType, ferieType, true, true}, hrOnly.Id: {gen.StaffAbsenceShownAsLeave, 0, true, true}, leave.Id: {gen.StaffAbsenceShownAsType, 0, true, true}}},
		{"HR", w.hr, map[int64]shown{
			ferie.Id: {gen.StaffAbsenceShownAsType, ferieType, true, true}, hrOnly.Id: {gen.StaffAbsenceShownAsLeave, 0, true, true}, leave.Id: {gen.StaffAbsenceShownAsType, 0, true, true}}},
		{"personal-data", w.pd, map[int64]shown{
			ferie.Id: {gen.StaffAbsenceShownAsType, ferieType, true, true}, hrOnly.Id: {gen.StaffAbsenceShownAsType, hrOnlyType, true, true}, leave.Id: {gen.StaffAbsenceShownAsType, 0, true, true}}},
		{"a colleague", w.viewer, map[int64]shown{
			ferie.Id: {visible: false}, hrOnly.Id: {gen.StaffAbsenceShownAsAbsent, 0, false, true}, leave.Id: {gen.StaffAbsenceShownAsAbsent, 0, false, true}}},
		{"a stranger", w.nobody, map[int64]shown{ferie.Id: {}, hrOnly.Id: {}, leave.Id: {}}},
	} {
		var list gen.PaginatedResponseOfStaffAbsence
		do(t, c.c, http.MethodGet, absencesPath+fmt.Sprintf("?personId=%d", w.kari.Id), nil, http.StatusOK, &list)
		var listed []int64
		for _, a := range list.Data {
			listed = append(listed, a.Id)
		}
		for id, want := range c.rows {
			if slices.Contains(listed, id) != want.visible {
				t.Errorf("%s: row %d listed %v, want %v", c.name, id, slices.Contains(listed, id), want.visible)
			}
			r := c.c.Do(http.MethodGet, absencePath(id), nil)
			if !want.visible {
				if r.Status != http.StatusNotFound {
					t.Errorf("%s: row %d answered %d, want 404", c.name, id, r.Status)
				}
				continue
			}
			if r.Status != http.StatusOK {
				t.Fatalf("%s: row %d answered %d %s", c.name, id, r.Status, r.Body)
			}
			body := raw(t, r)
			if body["shownAs"] != string(want.as) {
				t.Errorf("%s: row %d shownAs %v, want %s", c.name, id, body["shownAs"], want.as)
			}
			_, hasType := body["typeId"]
			if hasType != (want.as == gen.StaffAbsenceShownAsType) || (want.typeID != 0 && body["typeId"] != float64(want.typeID)) {
				t.Errorf("%s: row %d typeId %v, want %v", c.name, id, body["typeId"], want.typeID)
			}
			_, hasPercent := body["percent"]
			if hasPercent != want.detail {
				t.Errorf("%s: row %d percent present %v, want %v", c.name, id, hasPercent, want.detail)
			}
			if _, ok := body["firstDay"]; !ok {
				t.Errorf("%s: row %d without its dates", c.name, id)
			}
		}
	}
	var mine gen.PaginatedResponseOfStaffAbsence
	do(t, w.kariC, http.MethodGet, meAbsencesPath+"?from=2026-11-01&to=2026-12-31", nil, http.StatusOK, &mine)
	if len(mine.Data) != 2 || mine.Pagination.TotalCount != 2 {
		t.Errorf("my absences in November and December = %+v, want two", mine)
	}
	do(t, w.kariC, http.MethodGet, meAbsencesPath+"?status=requested", nil, http.StatusOK, &mine)
	if len(mine.Data) != 1 || mine.Data[0].Id != ferie.Id {
		t.Errorf("my requests = %+v, want the ferie", mine.Data)
	}
	refused(t, w.kariC.Do(http.MethodGet, meAbsencesPath+"?status=pondering", nil), http.StatusBadRequest, "")
	refused(t, w.kariC.Do(http.MethodGet, absencesPath+"?from=2026-12-01&to=2026-11-01", nil), http.StatusBadRequest, "")
}

// An absence's events, shaped (plan pre-flight B2, I6): every type value by
// its own type — a correction away from an HR-only type shows the manager
// "leave" before and the new type after; a deleted HR-only row's event shows
// "leave"; a sick row's event touches class E and is still listed to the
// manager and to HR without staff:personal-data, its degree shown.
func TestAbsences_EventsShaped(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	ferieType, hrOnlyType := typeID(t, h, "ferie"), typeID(t, h, "svangerskapspermisjon")

	hrOnly := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	do(t, w.hr, http.MethodPut, absencePath(hrOnly.Id), absence(t, h, "ferie", "2026-11-02", "2026-11-06"), http.StatusOK, nil, ifMatch(hrOnly.Revision))
	// A reader of "leave" lists the event under the neutral absence_changed
	// (review H1), since the row was HR-only on one side.
	for _, c := range []struct {
		name     string
		c        *modtest.Client
		kind     string
		from, to any
	}{
		{"her manager", w.bossC, "absence_changed", "leave", float64(ferieType)},
		{"HR", w.hr, "absence_changed", "leave", float64(ferieType)},
		{"personal-data", w.pd, "absence_updated", float64(hrOnlyType), float64(ferieType)},
		{"the person", w.kariC, "absence_updated", float64(hrOnlyType), float64(ferieType)},
	} {
		f := eventOf(t, c.c, w.kari.Id, c.kind, hrOnly.Id)
		typ, _ := f["type"].(map[string]any)
		if typ["from"] != c.from || typ["to"] != c.to {
			t.Errorf("%s reads the correction's type as %v, want from %v to %v", c.name, f["type"], c.from, c.to)
		}
	}

	gone := requestOwn(t, w.kariC, absence(t, h, "religios_hoytid", "2026-12-07", "2026-12-07"))
	do(t, w.kariC, http.MethodDelete, absencePath(gone.Id), nil, http.StatusNoContent, nil)
	if f := eventOf(t, w.bossC, w.kari.Id, "absence_changed", gone.Id); f == nil || f["type"].(map[string]any)["from"] != "leave" {
		t.Errorf("the manager reads the deleted HR-only row's event as %v, want its type leave", f)
	}

	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-09-21", "2026-09-25", "degreePercent", 60))
	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "HR": w.hr} {
		f := eventOf(t, c, w.kari.Id, "absence_registered", sick.Id)
		if f == nil {
			t.Errorf("%s is not listed the sykmelding's event", name)
			continue
		}
		if d, ok := f["degree"].(map[string]any); !ok || d["to"] != float64(60) {
			t.Errorf("%s reads the degree as %v, want 60", name, f["degree"])
		}
	}
	if r := w.viewer.Do(http.MethodGet, eventsPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a colleague reads Kari's history: %d, want 404", r.Status)
	}
}

// The erase keeps an absence event's dates and types (plan pre-flight B1):
// a leaver's D erase blanks the note and keeps the rest — a sykmelding's
// type and degree included, which are class E — and the health erase, once
// its longer period has run, blanks a sick row's type and degree and leaves
// a ferie row's type alone.
func TestAbsences_TheEraseKeepsDatesAndTypes(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	fieldsOf := func(id int64, kind string) map[string]any {
		raw := modtest.One[map[string]any](t, h.Harness, `SELECT payload->'fields' FROM staff.events
			WHERE kind = $1 AND (payload->>'absenceId')::bigint = $2`, kind, id)
		return raw
	}
	leaver := func(given, end string) (int64, int64, int64) {
		p, e := employed(t, w.hr, given, "2020-01-01", terms())
		ferie := register(t, w.hr, p.Id, absence(t, h, "ferie", "2024-06-03", "2024-06-07", "note", "Sommer"))
		sick := register(t, w.hr, p.Id, absence(t, h, "sykmelding", "2024-09-02", "2024-09-06", "degreePercent", 50))
		endEmployment(t, w.hr, e.Id, end)
		return p.Id, ferie.Id, sick.Id
	}
	_, recentFerie, recentSick := leaver("Nylig", "2026-05-01")
	_, oldFerie, oldSick := leaver("Gammel", "2024-12-31")
	runCycle(t, h)
	runCycle(t, h)

	kept := func(name string, f map[string]any, fields ...string) {
		for _, k := range fields {
			if v, ok := f[k]; !ok || v == "changed" {
				t.Errorf("%s: %s = %v, want kept", name, k, v)
			}
		}
	}
	blanked := func(name string, f map[string]any, fields ...string) {
		for _, k := range fields {
			if f[k] != "changed" {
				t.Errorf("%s: %s = %v, want blanked", name, k, f[k])
			}
		}
	}
	f := fieldsOf(recentFerie, "absence_requested")
	kept("the D erase, ferie", f, "type", "firstDay", "lastDay", "status")
	blanked("the D erase, ferie", f, "note")
	kept("the D erase, sykmelding", fieldsOf(recentSick, "absence_registered"), "type", "degree", "firstDay", "lastDay")
	f = fieldsOf(oldFerie, "absence_requested")
	kept("the health erase, ferie", f, "type", "firstDay", "lastDay")
	blanked("the health erase, ferie", f, "note")
	f = fieldsOf(oldSick, "absence_registered")
	kept("the health erase, sykmelding", f, "firstDay", "lastDay", "status")
	blanked("the health erase, sykmelding", f, "type", "degree")
}

// rowsAs is personID's absences as c lists them, each a bare JSON object.
func rowsAs(t *testing.T, c *modtest.Client, query string) []map[string]any {
	t.Helper()
	var page struct {
		Data []map[string]any `json:"data"`
	}
	do(t, c, http.MethodGet, absencesPath+query, nil, http.StatusOK, &page)
	return page.Data
}

// A colleague cannot tell a sickness from a holiday (review C1): a registered
// sykmelding, a registered sick child, a registered hour type and an approved
// ferie look alike to staff:view — no status, no working days, no type —
// and a status filter never reaches a colleague's rows, so asking for the
// registered ones lists none of them.
func TestAbsences_AColleagueSeesNoKindOfAbsence(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-05", "2026-10-09", "degreePercent", 50))
	register(t, w.hr, w.kari.Id, absence(t, h, "sykt_barn", "2026-10-12", "2026-10-12"))
	register(t, w.hr, w.kari.Id, absence(t, h, "helse", "2026-10-13", "2026-10-13", "hours", 2))
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-19", "2026-10-23"))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved' WHERE id = $1`, ferie.Id)

	rows := rowsAs(t, w.viewer, fmt.Sprintf("?personId=%d", w.kari.Id))
	if len(rows) != 4 {
		t.Fatalf("a colleague lists %d rows, want 4", len(rows))
	}
	want := []string{"firstDay", "id", "lastDay", "personId", "revision", "shownAs"}
	for _, r := range rows {
		if got := keysOf(r); !slices.Equal(got, want) || r["shownAs"] != "absent" {
			t.Errorf("a colleague reads %v, want only %v and shownAs absent", r, want)
		}
		one := raw(t, w.viewer.Do(http.MethodGet, absencePath(int64(r["id"].(float64))), nil))
		if got := keysOf(one); !slices.Equal(got, want) {
			t.Errorf("a colleague reads row %v as %v, want only %v", r["id"], one, want)
		}
	}
	for _, status := range []string{"registered", "approved", "requested"} {
		if rows := rowsAs(t, w.viewer, fmt.Sprintf("?personId=%d&status=%s", w.kari.Id, status)); len(rows) != 0 {
			t.Errorf("a colleague filtering on %s lists %d rows, want none", status, len(rows))
		}
	}
}

// eventsAbout is the events about absence id in the history as c reads it,
// newest first: each event's kind and fields.
func eventsAbout(t *testing.T, c *modtest.Client, personID, id int64) []struct {
	Kind   string
	Fields map[string]any
} {
	t.Helper()
	var page struct {
		Data []struct {
			Kind    string         `json:"kind"`
			Payload map[string]any `json:"payload"`
		} `json:"data"`
	}
	do(t, c, http.MethodGet, pagedPath(eventsPath(personID), 1, 100), nil, http.StatusOK, &page)
	var out []struct {
		Kind   string
		Fields map[string]any
	}
	for _, e := range page.Data {
		if e.Payload["absenceId"] == float64(id) {
			fields, _ := e.Payload["fields"].(map[string]any)
			out = append(out, struct {
				Kind   string
				Fields map[string]any
			}{e.Kind, fields})
		}
	}
	return out
}

// An HR-only hour type looks like any other leave to whoever reads it as
// "leave" (review H1): a registered svangerskapskontroll, to the manager and
// to HR without staff:personal-data, carries no hours, the working days of
// its day and the status approved — as an approved religious holiday does —
// and its events have the neutral kind absence_changed, no hours and the
// same fields. staff:personal-data and the person read it as it is.
func TestAbsences_AnHROnlyHourTypeLooksLikeLeave(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	check := requestOwn(t, w.kariC, absence(t, h, "svangerskapskontroll", "2026-10-07", "2026-10-07", "hours", 2))
	holy := requestOwn(t, w.kariC, absence(t, h, "religios_hoytid", "2026-10-08", "2026-10-08"))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved' WHERE id = $1`, holy.Id)
	if statusOf(check) != gen.Registered {
		t.Fatalf("svangerskapskontroll = %+v, want registered", check)
	}

	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "HR without personal-data": w.hr} {
		a := raw(t, c.Do(http.MethodGet, absencePath(check.Id), nil))
		b := raw(t, c.Do(http.MethodGet, absencePath(holy.Id), nil))
		if !slices.Equal(keysOf(a), keysOf(b)) {
			t.Errorf("%s: the check's keys %v, the holiday's %v, want the same", name, keysOf(a), keysOf(b))
		}
		for _, k := range []string{"shownAs", "status", "workingDays", "percent"} {
			if a[k] != b[k] {
				t.Errorf("%s: %s = %v for the check, %v for the holiday, want the same", name, k, a[k], b[k])
			}
		}
		if _, ok := a["hours"]; ok {
			t.Errorf("%s reads the check's hours: %v", name, a)
		}
		for _, r := range rowsAs(t, c, fmt.Sprintf("?personId=%d", w.kari.Id)) {
			if _, ok := r["hours"]; ok {
				t.Errorf("%s lists a row with hours: %v", name, r)
			}
		}
		ce, he := eventsAbout(t, c, w.kari.Id, check.Id), eventsAbout(t, c, w.kari.Id, holy.Id)
		if len(ce) != 1 || len(he) != 1 {
			t.Fatalf("%s: events %v and %v, want one each", name, ce, he)
		}
		if ce[0].Kind != "absence_changed" || he[0].Kind != "absence_changed" {
			t.Errorf("%s: kinds %s and %s, want absence_changed for both", name, ce[0].Kind, he[0].Kind)
		}
		if !slices.Equal(keysOf(ce[0].Fields), keysOf(he[0].Fields)) {
			t.Errorf("%s: the check's event fields %v, the holiday's %v, want the same", name, keysOf(ce[0].Fields), keysOf(he[0].Fields))
		}
		if s, _ := ce[0].Fields["status"].(map[string]any); s["to"] != "approved" {
			t.Errorf("%s: the check's event status %v, want approved", name, ce[0].Fields["status"])
		}
	}
	for name, c := range map[string]*modtest.Client{"personal-data": w.pd, "the person": w.kariC} {
		if a := raw(t, c.Do(http.MethodGet, absencePath(check.Id), nil)); a["hours"] != float64(2) || a["status"] != "registered" {
			t.Errorf("%s reads the check as %v, want its hours and registered", name, a)
		}
		if e := eventsAbout(t, c, w.kari.Id, check.Id); len(e) != 1 || e[0].Kind != "absence_registered" || e[0].Fields["hours"] == nil {
			t.Errorf("%s reads the check's event as %v, want absence_registered with hours", name, e)
		}
	}
}

// A change never moves a row between a request and a registration (review
// L2): a registered row changed to a type that needs a decision, or a request
// changed to a notification, is absence_status_change.
func TestAbsences_NoChangeAcrossStatuses(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	sick := requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-09-14", "2026-09-14"))
	refused(t, w.hr.Do(http.MethodPut, absencePath(sick.Id), absence(t, h, "ferie", "2026-09-14", "2026-09-14"), ifMatch(sick.Revision)),
		http.StatusConflict, "absence_status_change")
	req := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	refused(t, w.kariC.Do(http.MethodPut, absencePath(req.Id), absence(t, h, "egenmelding", "2026-10-05", "2026-10-05"), ifMatch(req.Revision)),
		http.StatusConflict, "absence_status_change")
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE id = ANY($1) AND revision = 1`, []int64{sick.Id, req.Id}); n != 2 {
		t.Errorf("%d rows unchanged, want both", n)
	}
}

// POST /absences refuses a property it does not take (review L3), as the
// person's own request does.
func TestAbsences_RequestIsClosed(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	p := refused(t, w.hr.Do(http.MethodPost, absencesPath, with(absence(t, w.h, "ferie", "2026-10-05", "2026-10-05"), "personId", w.kari.Id, "status", "approved")),
		http.StatusBadRequest, "")
	if len(p.Errors["status"]) == 0 {
		t.Errorf("errors = %v, want status refused", p.Errors)
	}
}

// statusOf and workingDaysOf are an absence's status and working days, zero
// when the caller is not shown them.
func statusOf(a gen.StaffAbsence) gen.StaffAbsenceStatus {
	if a.Status == nil {
		return ""
	}
	return *a.Status
}

func workingDaysOf(a gen.StaffAbsence) int32 {
	if a.WorkingDays == nil {
		return -1
	}
	return *a.WorkingDays
}
