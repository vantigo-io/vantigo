package staff_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The decisions' paths (delivery B, Task 4).
const (
	approvalsPath = "/api/v1/staff/approvals"
	calendarPath  = "/api/v1/staff/calendar"
)

func approvePath(id int64) string { return absencePath(id) + "/approve" }
func rejectPath(id int64) string  { return absencePath(id) + "/reject" }
func correctPath(id int64) string { return absencePath(id) + "/correct" }

// decisionWorld is absenceWorld with the deciders of design S12 around it:
// Sigrid, Bjørn's manager, with no permissions of her own; an Approver
// (staff:approve) linked to nobody; Olav, staff:manage, who heads the
// structure — the department Ledelse is his and has nothing above it — and
// has no manager; and Hilde, staff:manage too, with no manager and in no
// department.
type decisionWorld struct {
	absenceWorld
	approver              *modtest.Client
	sigrid, olav, hilde   gen.StaffPerson
	sigridC, olavC, hildC *modtest.Client
	sigridUser            uuid.UUID
	ledelse               gen.StaffDepartment
}

func newDecisionWorld(t *testing.T, opts ...modtest.Option) decisionWorld {
	t.Helper()
	w := decisionWorld{absenceWorld: newAbsenceWorld(t, opts...)}
	h, hr := w.h, w.hr
	w.approver, _ = signIn(t, h, "staff:approve")

	w.sigrid, _ = employed(t, hr, "Sigrid", "2015-01-01", terms())
	w.sigridC, w.sigridUser = signIn(t, h)
	link(t, h, w.sigrid.Id, w.sigridUser)
	setManager(t, h, w.boss.Id, w.sigrid.Id)

	w.olav, _ = employed(t, hr, "Olav", "2010-01-01", terms())
	w.ledelse = createDepartment(t, hr, map[string]any{"code": "LED", "name": "Ledelse", "managerPersonId": w.olav.Id})
	h.Exec(t, `UPDATE staff.employment_terms SET department_id = $1
		WHERE employment_id IN (SELECT id FROM staff.employments WHERE person_id = $2)`, w.ledelse.Id, w.olav.Id)
	var olavUser uuid.UUID
	w.olavC, olavUser = signIn(t, h, "staff:manage")
	link(t, h, w.olav.Id, olavUser)

	w.hilde, _ = employed(t, hr, "Hilde", "2012-01-01", terms())
	var hildeUser uuid.UUID
	w.hildC, hildeUser = signIn(t, h, "staff:manage")
	link(t, h, w.hilde.Id, hildeUser)
	return w
}

// setManager names manager on every terms row of person's employments.
func setManager(t *testing.T, h *harness, person int64, manager any) {
	t.Helper()
	h.Exec(t, `UPDATE staff.employment_terms SET manager_person_id = $1
		WHERE employment_id IN (SELECT id FROM staff.employments WHERE person_id = $2)`, manager, person)
}

// decide posts a decision and answers the response.
func decide(c *modtest.Client, path string, note string) *modtest.Response {
	body := map[string]any{}
	if note != "" {
		body["note"] = note
	}
	return c.Do(http.MethodPost, path, body)
}

// approved approves id as c and fails the test unless it was approved.
func approved(t *testing.T, c *modtest.Client, id int64) map[string]any {
	t.Helper()
	r := decide(c, approvePath(id), "")
	if r.Status != http.StatusOK {
		t.Fatalf("approve %d: %d %s", id, r.Status, r.Body)
	}
	return raw(t, r)
}

// statusIn is absence id's stored status.
func statusIn(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT status FROM staff.absences WHERE id = $1`, id)
}

// Who decides (design S12, plan Task 4, reading 2, pre-flight I7): a
// manager of the person today by the one manager rule, staff:approve and
// staff:manage; never one's own — a manager's own request goes to their
// manager — but the sole head of the structure holding a decider's right,
// with no manager who has an active user, approves their own: the event
// absence_self_approved and the attention item selfApproved for HR.
func TestDecisions_WhoDecides(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	ferie := func(c *modtest.Client, first string) gen.StaffAbsence {
		return requestOwn(t, c, absence(t, h, "ferie", first, first))
	}

	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "an Approver": w.approver, "HR": w.hr} {
		a := ferie(w.kariC, map[string]string{"her manager": "2026-10-05", "an Approver": "2026-10-06", "HR": "2026-10-07"}[name])
		if got := approved(t, c, a.Id); got["status"] != "approved" {
			t.Errorf("%s approving Kari's request: %v", name, got)
		}
	}
	own := ferie(w.kariC, "2026-10-08")
	refused(t, decide(w.kariC, approvePath(own.Id), ""), http.StatusForbidden, "own_request")
	// staff:personal-data alone reads the request and decides nothing; a
	// manager of someone else does not even read it.
	refused(t, decide(w.pd, approvePath(own.Id), ""), http.StatusForbidden, "decision_not_yours")
	if r := decide(w.sigridC, approvePath(own.Id), ""); r.Status != http.StatusNotFound {
		t.Errorf("another person's manager approving: %d, want 404", r.Status)
	}
	for name, c := range map[string]*modtest.Client{"a colleague": w.viewer, "a stranger": w.nobody} {
		if r := decide(c, approvePath(own.Id), ""); r.Status != http.StatusNotFound {
			t.Errorf("%s approving a request they do not see: %d, want 404", name, r.Status)
		}
	}
	if statusIn(t, h, own.Id) != "requested" {
		t.Error("a refused approval changed the row")
	}

	// A manager's own request goes to their manager.
	bjorn := ferie(w.bossC, "2026-10-12")
	refused(t, decide(w.bossC, approvePath(bjorn.Id), ""), http.StatusForbidden, "own_request")
	approved(t, w.sigridC, bjorn.Id)

	// HR with no manager who does not head the structure waits for another
	// holder.
	hilde := ferie(w.hildC, "2026-10-13")
	refused(t, decide(w.hildC, approvePath(hilde.Id), ""), http.StatusForbidden, "own_request")
	approved(t, w.hr, hilde.Id)

	// The head of the structure approves their own.
	olav := ferie(w.olavC, "2026-10-14")
	approved(t, w.olavC, olav.Id)
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE id = $1 AND self_approved`, olav.Id); n != 1 {
		t.Error("the self-approval is not recorded on the row")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'absence_self_approved'
		AND (payload->>'absenceId')::bigint = $1 AND payload->'typeIds' IS NOT NULL`, olav.Id); n != 1 {
		t.Error("no absence_self_approved event naming the row's types")
	}
	items := ofType(getAttention(t, w.hr), gen.SelfApproved)
	if len(items) != 1 || items[0].EntityId != fmt.Sprint(w.olav.Id) || items[0].Id != fmt.Sprintf("selfApproved/%d", olav.Id) {
		t.Errorf("HR's selfApproved items = %+v, want Olav's", items)
	}
	if len(ofType(getAttention(t, w.approver), gen.SelfApproved)) != 0 || len(ofType(getAttention(t, w.bossC), gen.SelfApproved)) != 0 {
		t.Error("selfApproved listed to someone who is not staff:manage")
	}

	// A manager with an active user takes the self-approval away; one whose
	// user is disabled, or who has no user, does not.
	setManager(t, h, w.olav.Id, w.sigrid.Id)
	again := ferie(w.olavC, "2026-10-15")
	refused(t, decide(w.olavC, approvePath(again.Id), ""), http.StatusForbidden, "own_request")
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, w.sigridUser)
	approved(t, w.olavC, again.Id)
	h.Exec(t, `UPDATE identity.users SET is_disabled = false WHERE id = $1`, w.sigridUser)
	h.Exec(t, `DELETE FROM staff.identity_links WHERE person_id = $1`, w.sigrid.Id)
	third := ferie(w.olavC, "2026-10-16")
	approved(t, w.olavC, third.Id)

	// An employee without rights never approves their own, heading nothing.
	refused(t, decide(w.kariC, approvePath(own.Id), ""), http.StatusForbidden, "own_request")
}

// A manager who has left decides nothing (reading 2): Per's terms name Mona,
// whose employment ended, so the department Drift's manager Dagny decides.
func TestDecisions_ADepartedManagerIsSkipped(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h, hr := w.h, w.hr
	mona := createPerson(t, hr, newPerson("Mona", "Nordmann"))
	pastEmployment(t, hr, mona.Id, "2015-01-01", "2026-06-30", terms())
	dagny, _ := employed(t, hr, "Dagny", "2015-01-01", terms())
	drift := createDepartment(t, hr, map[string]any{"code": "DRIFT", "name": "Drift", "managerPersonId": dagny.Id})
	per, _ := employed(t, hr, "Per", "2020-01-01", terms("managerPersonId", mona.Id, "departmentId", drift.Id))
	monaC, monaUser := signIn(t, h)
	link(t, h, mona.Id, monaUser)
	dagnyC, dagnyUser := signIn(t, h)
	link(t, h, dagny.Id, dagnyUser)
	perC, perUser := signIn(t, h)
	link(t, h, per.Id, perUser)

	req := requestOwn(t, perC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	if r := decide(monaC, approvePath(req.Id), ""); r.Status != http.StatusNotFound && r.Status != http.StatusForbidden {
		t.Errorf("the departed manager approving: %d, want refused", r.Status)
	}
	approved(t, dagnyC, req.Id)
}

// Reject, cancel and correct (design S12, plan Task 4, pre-flight M7): a
// rejection needs its reason; a decided row is cancelled by its decider
// while it lies ahead and by HR alone once it has begun; HR corrects an
// approved or registered row — never a request, a rejected or a cancelled
// one — and a correction never crosses the request/registration line.
func TestDecisions_RejectCancelCorrect(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h

	req := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	p := refused(t, decide(w.bossC, rejectPath(req.Id), ""), http.StatusBadRequest, "")
	if len(p.Errors["note"]) == 0 {
		t.Errorf("a rejection without a reason: errors %v, want note named", p.Errors)
	}
	refused(t, decide(w.bossC, rejectPath(req.Id), strings.Repeat("x", 501)), http.StatusBadRequest, "")
	r := decide(w.bossC, rejectPath(req.Id), "Vi har for mange borte den uka")
	if r.Status != http.StatusOK || raw(t, r)["status"] != "rejected" {
		t.Fatalf("reject: %d %s", r.Status, r.Body)
	}
	if a := raw(t, w.kariC.Do(http.MethodGet, absencePath(req.Id), nil)); a["decisionNote"] != "Vi har for mange borte den uka" {
		t.Errorf("Kari reads the rejection as %v, want its reason", a)
	}
	if c := modtest.One[map[string]string](t, h.Harness, `SELECT payload->'classes' FROM staff.events
		WHERE kind = 'absence_rejected' AND (payload->>'absenceId')::bigint = $1`, req.Id); c["decisionNote"] != "D" || c["status"] != "C" {
		t.Errorf("absence_rejected classes = %v, want decisionNote D and status C", c)
	}
	refused(t, decide(w.bossC, approvePath(req.Id), ""), http.StatusConflict, "absence_not_requested")
	refused(t, w.bossC.Do(http.MethodPost, cancelAbsencePath(req.Id), nil), http.StatusConflict, "absence_not_cancellable")
	refused(t, w.hr.Do(http.MethodPost, correctPath(req.Id), absence(t, h, "ferie", "2026-10-05", "2026-10-06"), ifMatchAs(t, w.hr, req.Id)),
		http.StatusConflict, "absence_not_cancellable")

	// An approved row ahead: the person may not cancel it, her manager may.
	ahead := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-11-02", "2026-11-06"))
	refused(t, w.hr.Do(http.MethodPost, correctPath(ahead.Id), absence(t, h, "ferie", "2026-11-02", "2026-11-03"), ifMatchAs(t, w.hr, ahead.Id)),
		http.StatusConflict, "absence_not_cancellable")
	approved(t, w.bossC, ahead.Id)
	refused(t, w.kariC.Do(http.MethodPost, cancelAbsencePath(ahead.Id), nil), http.StatusConflict, "absence_not_cancellable")
	refused(t, w.viewer.Do(http.MethodPost, cancelAbsencePath(ahead.Id), nil), http.StatusForbidden, "decision_not_yours")
	refused(t, w.pd.Do(http.MethodPost, cancelAbsencePath(ahead.Id), nil), http.StatusForbidden, "decision_not_yours")
	do(t, w.bossC, http.MethodPost, cancelAbsencePath(ahead.Id), nil, http.StatusOK, nil)
	if statusIn(t, h, ahead.Id) != "cancelled" {
		t.Error("the decider's cancel did not cancel")
	}
	refused(t, w.hr.Do(http.MethodPost, correctPath(ahead.Id), absence(t, h, "ferie", "2026-11-02", "2026-11-03"), ifMatchAs(t, w.hr, ahead.Id)),
		http.StatusConflict, "absence_not_cancellable")

	// An approved row that has begun: HR's alone to cancel.
	past := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-09-07", "2026-09-18"))
	approved(t, w.bossC, past.Id)
	refused(t, w.bossC.Do(http.MethodPost, cancelAbsencePath(past.Id), nil), http.StatusConflict, "absence_not_cancellable")

	// HR corrects it: the days, as an event with before and after.
	var fixed gen.StaffAbsence
	do(t, w.hr, http.MethodPost, correctPath(past.Id), absence(t, h, "ferie", "2026-09-07", "2026-09-11"), http.StatusOK, &fixed, ifMatchAs(t, w.hr, past.Id))
	if statusOf(fixed) != gen.Approved || workingDaysOf(fixed) != 5 {
		t.Errorf("corrected = %+v, want approved, five days", fixed)
	}
	if f := eventOf(t, w.kariC, w.kari.Id, "absence_corrected", past.Id); f == nil || f["lastDay"] == nil {
		t.Errorf("absence_corrected fields = %v, want lastDay before and after", f)
	}
	refused(t, w.hr.Do(http.MethodPost, correctPath(past.Id), absence(t, h, "egenmelding", "2026-09-07", "2026-09-08"), ifMatchAs(t, w.hr, past.Id)),
		http.StatusConflict, "absence_status_change")
	if r := w.bossC.Do(http.MethodPost, correctPath(past.Id), absence(t, h, "ferie", "2026-09-07", "2026-09-08"), ifMatch(1)); r.Status != http.StatusForbidden {
		t.Errorf("a manager correcting: %d, want 403", r.Status)
	}
	do(t, w.hr, http.MethodPost, cancelAbsencePath(past.Id), nil, http.StatusOK, nil)

	// A registered sykmelding is corrected too.
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-03", "2026-08-14", "degreePercent", 50))
	do(t, w.hr, http.MethodPost, correctPath(sick.Id), absence(t, h, "sykmelding", "2026-08-03", "2026-08-07", "degreePercent", 40), http.StatusOK, nil, ifMatchAs(t, w.hr, sick.Id))
	if statusIn(t, h, sick.Id) != "registered" {
		t.Error("the correction moved the registration's status")
	}
}

// The Approver (staff:approve, design §5): every person's work and non-pay
// employment, their absences with every type but an HR-only one (leave) and
// the detail; no events, no private fields, no pay, no revision. canDecide
// on the person says who decides their requests.
func TestDecisions_TheApproverAudience(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09", "note", "Høstferie"))
	hrOnly := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-12-07", "2026-12-11", "degreePercent", 50))

	person := raw(t, w.approver.Do(http.MethodGet, personPath(w.kari.Id), nil))
	if person["givenName"] != "Kari" {
		t.Fatalf("the Approver reads Kari as %v", person)
	}
	for _, k := range []string{"birthDate", "phoneMobile", "notes", "user", "nationalId", "bankAccount", "childrenUnder12"} {
		if _, ok := person[k]; ok {
			t.Errorf("the Approver reads %s", k)
		}
	}
	if r := w.approver.Do(http.MethodGet, eventsPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("the Approver reads Kari's history: %d, want 404", r.Status)
	}
	for _, c := range []struct {
		id     int64
		shown  string
		detail bool
	}{{ferie.Id, "type", true}, {hrOnly.Id, "leave", true}, {sick.Id, "type", true}} {
		a := raw(t, w.approver.Do(http.MethodGet, absencePath(c.id), nil))
		if a["shownAs"] != c.shown {
			t.Errorf("row %d shownAs %v, want %s", c.id, a["shownAs"], c.shown)
		}
		if _, ok := a["percent"]; ok != c.detail {
			t.Errorf("row %d detail %v, want %v", c.id, ok, c.detail)
		}
		for _, k := range []string{"revision", "etag"} {
			if _, ok := a[k]; ok {
				t.Errorf("the Approver reads row %d's %s", c.id, k)
			}
		}
	}
	if rows := rowsAs(t, w.approver, fmt.Sprintf("?personId=%d", w.kari.Id)); len(rows) != 3 {
		t.Errorf("the Approver lists %d of Kari's rows, want 3", len(rows))
	}

	canDecide := func(c *modtest.Client, id int64) any {
		r := c.Do(http.MethodGet, personPath(id), nil)
		if r.Status != http.StatusOK {
			return nil
		}
		return raw(t, r)["capabilities"].(map[string]any)["canDecide"]
	}
	for _, c := range []struct {
		name string
		c    *modtest.Client
		id   int64
		want any
	}{
		{"the Approver on Kari", w.approver, w.kari.Id, true},
		{"HR on Kari", w.hr, w.kari.Id, true},
		{"her manager on Kari", w.bossC, w.kari.Id, true},
		{"Bjørn on himself", w.bossC, w.boss.Id, false},
		{"Kari on herself", w.kariC, w.kari.Id, false},
		{"a colleague on Kari", w.viewer, w.kari.Id, false},
		{"Olav on himself", w.olavC, w.olav.Id, true},
		{"Hilde on herself", w.hildC, w.hilde.Id, false},
	} {
		if got := canDecide(c.c, c.id); got != c.want {
			t.Errorf("%s: canDecide %v, want %v", c.name, got, c.want)
		}
	}
}

// The inbox (plan Task 4): the requests the caller decides, oldest first,
// with the person; a manager's reports' but not their own; every person's
// for staff:approve and staff:manage but their own, the self-approver's own
// included; shaped for the caller — an HR-only type leave, no revision to
// a manager — and nothing once decided.
func TestApprovals_TheInbox(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	first := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-11-02", "2026-11-03"))
	hrOnly := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-10-05", "2026-10-09"))
	bjorn := requestOwn(t, w.bossC, absence(t, h, "ferie", "2026-10-12", "2026-10-13"))
	olav := requestOwn(t, w.olavC, absence(t, h, "ferie", "2026-10-14", "2026-10-14"))
	hilde := requestOwn(t, w.hildC, absence(t, h, "ferie", "2026-10-15", "2026-10-15"))
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-12-07", "2026-12-11"))

	inbox := func(c *modtest.Client) []map[string]any {
		var page struct {
			Data []map[string]any `json:"data"`
		}
		do(t, c, http.MethodGet, approvalsPath, nil, http.StatusOK, &page)
		return page.Data
	}
	ids := func(items []map[string]any) []int64 {
		var out []int64
		for _, it := range items {
			out = append(out, int64(it["absence"].(map[string]any)["id"].(float64)))
		}
		return out
	}
	for _, c := range []struct {
		name string
		c    *modtest.Client
		want []int64
	}{
		{"Kari's manager", w.bossC, []int64{first.Id, hrOnly.Id}},
		{"Bjørn's manager", w.sigridC, []int64{bjorn.Id}},
		{"the Approver", w.approver, []int64{first.Id, hrOnly.Id, bjorn.Id, olav.Id, hilde.Id}},
		{"Olav, the self-approver", w.olavC, []int64{first.Id, hrOnly.Id, bjorn.Id, olav.Id, hilde.Id}},
		{"Hilde", w.hildC, []int64{first.Id, hrOnly.Id, bjorn.Id, olav.Id}},
		{"Kari", w.kariC, nil},
		{"a colleague", w.viewer, nil},
	} {
		if got := ids(inbox(c.c)); !slices.Equal(got, c.want) {
			t.Errorf("%s's inbox = %v, want %v", c.name, got, c.want)
		}
	}
	for _, it := range inbox(w.bossC) {
		a := it["absence"].(map[string]any)
		if _, ok := a["revision"]; ok {
			t.Errorf("the manager's inbox carries a revision: %v", a)
		}
		if a["id"] == float64(hrOnly.Id) && (a["shownAs"] != "leave" || a["typeId"] != nil) {
			t.Errorf("the manager's inbox shows the HR-only request as %v, want leave", a)
		}
		if it["person"].(map[string]any)["displayName"] != "Kari Nordmann" {
			t.Errorf("the inbox names %v, want Kari", it["person"])
		}
	}
	approved(t, w.bossC, first.Id)
	if got := ids(inbox(w.bossC)); !slices.Equal(got, []int64{hrOnly.Id}) {
		t.Errorf("after the approval the inbox = %v, want only the HR-only request", got)
	}
}

// calendarAs is the calendar from..to as c reads it, by person.
func calendarAs(t *testing.T, c *modtest.Client, query string) (map[int64][]map[string]any, []any) {
	t.Helper()
	var cal struct {
		PublicHolidays []any `json:"publicHolidays"`
		People         []struct {
			Person   map[string]any   `json:"person"`
			Absences []map[string]any `json:"absences"`
		} `json:"people"`
	}
	do(t, c, http.MethodGet, calendarPath+query, nil, http.StatusOK, &cal)
	out := map[int64][]map[string]any{}
	for _, p := range cal.People {
		out[int64(p.Person["id"].(float64))] = p.Absences
	}
	return out, cal.PublicHolidays
}

// The calendar (plan Task 4, design §5, §7): a colleague sees every active
// person and only that each is away — the dates of approved and registered
// rows, shown absent; a manager without the directory their reports and
// themselves, an HR-only type as leave without its hours, a sykmelding as
// itself; the person themselves; the public holidays of the range from the
// platform calendar.
func TestCalendar_PerAudience(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-12-14", "2026-12-18"))
	approved(t, w.bossC, ferie.Id)
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-12-07", "2026-12-09", "degreePercent", 50))
	check := requestOwn(t, w.kariC, absence(t, h, "svangerskapskontroll", "2026-12-10", "2026-12-10", "hours", 2))
	pending := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-12-28", "2026-12-30"))
	no := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-12-21", "2026-12-21"))
	if r := decide(w.bossC, rejectPath(no.Id), "Nei"); r.Status != http.StatusOK {
		t.Fatalf("reject: %d %s", r.Status, r.Body)
	}
	const december = "?from=2026-12-01&to=2026-12-31"

	cal, holidays := calendarAs(t, w.viewer, december)
	if !slices.Equal(holidays, []any{"2026-12-25", "2026-12-26"}) {
		t.Errorf("public holidays = %v, want Christmas Day and Boxing Day", holidays)
	}
	if _, ok := cal[w.olav.Id]; !ok {
		t.Error("a colleague's calendar leaves out an active person")
	}
	bars := cal[w.kari.Id]
	if len(bars) != 3 {
		t.Fatalf("a colleague sees %d of Kari's bars, want the three approved and registered", len(bars))
	}
	for _, b := range bars {
		if got := keysOf(b); !slices.Equal(got, []string{"firstDay", "id", "lastDay", "personId", "shownAs"}) || b["shownAs"] != "absent" {
			t.Errorf("a colleague's bar %v, want the dates alone, absent", b)
		}
	}

	cal, _ = calendarAs(t, w.bossC, december)
	if len(cal) != 2 {
		t.Errorf("the manager's calendar has %d people, want Kari and himself", len(cal))
	}
	byID := map[float64]map[string]any{}
	for _, b := range cal[w.kari.Id] {
		byID[b["id"].(float64)] = b
	}
	if len(byID) != 4 || byID[float64(no.Id)] != nil || byID[float64(pending.Id)] == nil {
		t.Errorf("the manager's bars %v, want the four but the rejected one", byID)
	}
	if b := byID[float64(check.Id)]; b["shownAs"] != "leave" || b["hours"] != nil || b["status"] != "approved" || b["revision"] != nil {
		t.Errorf("the manager reads the check as %v, want leave, approved, no hours, no revision", b)
	}
	if b := byID[float64(sick.Id)]; b["shownAs"] != "type" {
		t.Errorf("the manager reads the sykmelding as %v, want its type", b)
	}

	cal, _ = calendarAs(t, w.kariC, december)
	if len(cal) != 1 || len(cal[w.kari.Id]) != 4 {
		t.Errorf("Kari's calendar = %v, want her own four bars", cal)
	}
	for _, b := range cal[w.kari.Id] {
		if b["id"] == float64(check.Id) && b["hours"] != float64(2) {
			t.Errorf("Kari reads her check as %v, want its hours", b)
		}
	}
	if cal, _ := calendarAs(t, w.nobody, december); len(cal) != 0 {
		t.Errorf("a caller linked to nobody sees %v, want nobody", cal)
	}
	cal, _ = calendarAs(t, w.approver, december)
	if b := cal[w.kari.Id]; len(b) != 4 {
		t.Errorf("the Approver sees %d of Kari's bars, want 4", len(b))
	}
	cal, _ = calendarAs(t, w.hr, december+fmt.Sprintf("&departmentId=%d", w.ledelse.Id))
	if len(cal) != 1 || cal[w.olav.Id] == nil {
		t.Errorf("Ledelse's calendar = %v, want Olav alone", cal)
	}
	refused(t, w.hr.Do(http.MethodGet, calendarPath+"?from=2026-12-31&to=2026-12-01", nil), http.StatusBadRequest, "")
	refused(t, w.hr.Do(http.MethodGet, calendarPath+"?from=2026-01-01&to=2026-04-04", nil), http.StatusBadRequest, "")
	do(t, w.hr, http.MethodGet, calendarPath+"?from=2026-01-01&to=2026-04-03", nil, http.StatusOK, nil)
}

// What a reader is shown of the three rows that say most (plan Task 4):
// a sykmelding, an HR-only hour type and a ferie, through the inbox's
// decisions, the calendar and the history — a leave-reader is listed none of
// the HR-only row's events, and the decision events carry typeIds.
func TestDecisions_EventsCarryTypesAndStayShaped(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	perm := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	r := decide(w.approver, approvePath(ferie.Id), "God tur")
	if r.Status != http.StatusOK {
		t.Fatalf("approve: %d %s", r.Status, r.Body)
	}
	approved(t, w.approver, perm.Id)
	do(t, w.bossC, http.MethodPost, cancelAbsencePath(perm.Id), nil, http.StatusOK, nil)

	for _, kind := range []string{"absence_approved", "absence_cancelled"} {
		if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = $1 AND jsonb_array_length(payload->'typeIds') = 1
			AND jsonb_array_length(payload->'typeClasses') = 1`, kind); n == 0 {
			t.Errorf("no %s event naming its types", kind)
		}
	}
	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "HR": w.hr} {
		if e := eventsAbout(t, c, w.kari.Id, perm.Id); len(e) != 0 {
			t.Errorf("%s is listed the HR-only row's decisions: %v", name, e)
		}
		e := eventsAbout(t, c, w.kari.Id, ferie.Id)
		if len(e) != 2 || e[0].Kind != "absence_approved" || e[0].Fields["decisionNote"] == nil {
			t.Errorf("%s reads the ferie's events as %v, want its request and its approval with the note", name, e)
		}
		if a := raw(t, c.Do(http.MethodGet, absencePath(perm.Id), nil)); a["decisionNote"] != nil {
			t.Errorf("%s reads a decision field on the leave-shown row: %v", name, a)
		}
	}
	if e := eventsAbout(t, w.pd, w.kari.Id, perm.Id); len(e) != 3 {
		t.Errorf("personal-data reads %d of the HR-only row's events, want its request, approval and cancel", len(e))
	}
}
