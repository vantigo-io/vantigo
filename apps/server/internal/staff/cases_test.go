package staff_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The sick-leave cases' paths (delivery B, Task 5).
const casesPath = "/api/v1/staff/cases"

func casePath(id int64) string { return rowPath(casesPath, id) }
func milestonePath(id int64, kind, action string) string {
	return fmt.Sprintf("%s/milestones/%s/%s", casePath(id), kind, action)
}

// listCases is the cases c reads, query appended to the path.
func listCases(t *testing.T, c *modtest.Client, query string) []gen.StaffCase {
	t.Helper()
	var page struct {
		Data []gen.StaffCase `json:"data"`
	}
	do(t, c, http.MethodGet, casesPath+query, nil, http.StatusOK, &page)
	return page.Data
}

// onlyCase is personID's one case as HR reads it, failing the test unless
// there is exactly one.
func onlyCase(t *testing.T, c *modtest.Client, personID int64) gen.StaffCase {
	t.Helper()
	cases := listCases(t, c, fmt.Sprintf("?status=all&personId=%d", personID))
	if len(cases) != 1 {
		t.Fatalf("cases = %+v, want one", cases)
	}
	return cases[0]
}

// milestoneOf is c's milestone of kind.
func milestoneOf(t *testing.T, c gen.StaffCase, kind string) gen.StaffCaseMilestone {
	t.Helper()
	for _, m := range c.Milestones {
		if string(m.Kind) == kind {
			return m
		}
	}
	t.Fatalf("case %d has no milestone %s: %+v", c.Id, kind, c.Milestones)
	return gen.StaffCaseMilestone{}
}

// civilOf is a contract date as text, "" for none.
func civilOf(d *openapi_types.Date) string {
	if d == nil {
		return ""
	}
	return d.Format(time.DateOnly)
}

// eventsAs is personID's history as c reads it, newest first.
func eventsAs(t *testing.T, c *modtest.Client, personID int64) []gen.StaffEvent {
	t.Helper()
	var page struct {
		Data []gen.StaffEvent `json:"data"`
	}
	do(t, c, http.MethodGet, pagedPath(eventsPath(personID), 1, 100), nil, http.StatusOK, &page)
	return page.Data
}

// kindsOf is the kinds of events.
func kindsOf(events []gen.StaffEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

// A sick row joins a case when first_day ≤ last_day + 16 (ftrl. § 8-19 (3),
// pre-flight I1), else opens one; the arbeidsgiverperiode ends on the
// sixteenth sick day, a weekend between two rows bridged (Revision 2 Q7a):
// egenmelding Monday–Friday under the extended policy and a sykmelding from
// the next Monday end it on the Tuesday of week 3. The milestones are dated
// from the first day and due only while the case has lasted to within a week
// of them (I5); the estimate is first day + 364 (M5).
func TestCases_TheJoinAndTheEmployerPeriod(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	h.Exec(t, `UPDATE staff.settings SET self_cert_policy = 'extended'`)
	requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-10-05", "2026-10-09"))
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-12", "2026-10-30"))

	c := onlyCase(t, w.kariC, w.kari.Id)
	if civilOf(&c.FirstDay) != "2026-10-05" || civilOf(&c.LastDay) != "2026-10-30" || !c.Open {
		t.Errorf("case = %s..%s open %v, want 2026-10-05..2026-10-30, open", civilOf(&c.FirstDay), civilOf(&c.LastDay), c.Open)
	}
	if m := milestoneOf(t, c, "employer_period_end"); civilOf(m.DueDate) != "2026-10-20" || !m.Due {
		t.Errorf("employer_period_end = %s due %v, want 2026-10-20 (the Tuesday of week 3), due", civilOf(m.DueDate), m.Due)
	}
	for kind, want := range map[string]struct {
		date string
		due  bool
	}{"follow_up_plan": {"2026-11-02", true}, "dialogue_1": {"2026-11-23", false}, "nav_activity": {"2026-11-30", false}, "dialogue_2": {"2027-04-05", false}} {
		if m := milestoneOf(t, c, kind); civilOf(m.DueDate) != want.date || m.Due != want.due {
			t.Errorf("%s = %s due %v, want %s due %v", kind, civilOf(m.DueDate), m.Due, want.date, want.due)
		}
	}
	if civilOf(&c.MaxBenefitEstimate) != "2027-10-04" || c.MaxBenefitEstimateEdited {
		t.Errorf("estimate = %s edited %v, want the derived 2027-10-04", civilOf(&c.MaxBenefitEstimate), c.MaxBenefitEstimateEdited)
	}

	// 30 October + 16 is 15 November: a row from then joins, one from
	// 3 December — 17 days after 16 November — opens a second case.
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-11-15", "2026-11-16"))
	if c := onlyCase(t, w.hr, w.kari.Id); civilOf(&c.LastDay) != "2026-11-16" {
		t.Errorf("after a row from 15 November: last day %s, want 2026-11-16 in the same case", civilOf(&c.LastDay))
	}
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-12-03", "2026-12-04"))
	if cases := listCases(t, w.hr, fmt.Sprintf("?status=all&personId=%d", w.kari.Id)); len(cases) != 2 {
		t.Errorf("after a row from 3 December: %d cases, want 2", len(cases))
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE person_id = $1 AND case_id IS NULL`, w.kari.Id); n != 0 {
		t.Errorf("%d sick rows name no case", n)
	}
}

// A three-day case raises nothing (pre-flight I5): no milestone is due and
// the arbeidsgiverperiode has no end.
func TestCases_AThreeDayCaseHasNothingDue(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	requestOwn(t, w.kariC, absence(t, w.h, "egenmelding", "2026-10-05", "2026-10-07"))
	c := onlyCase(t, w.hr, w.kari.Id)
	for _, m := range c.Milestones {
		if m.Due {
			t.Errorf("%s is due on a three-day case", m.Kind)
		}
	}
	if m := milestoneOf(t, c, "employer_period_end"); m.DueDate != nil {
		t.Errorf("employer_period_end = %s, want none before the sixteenth day", civilOf(m.DueDate))
	}
	if len(c.Milestones) != 5 {
		t.Errorf("%d milestones, want 5", len(c.Milestones))
	}
}

// Sickness during ferie returns the days (S9, fl. § 9 (1), readings 24, 25,
// 29): a 100 % sykmelding over approved ferie splits it around itself — one
// absence_split event — and the account takes back its days; a graded one
// stands beside the ferie and splits nothing; ferie it covers whole is
// cancelled; a sykmelding over a requested ferie is refused as overlap.
func TestCases_SickLeaveOverFerie(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	f := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-16"))
	approved(t, w.bossC, f.Id)
	if a := accountOf(t, w.hr, w.kari.Id, 2026); a.Taken != 10 {
		t.Fatalf("taken = %v, want 10", a.Taken)
	}
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-07", "2026-10-09"))
	pieces := modtest.One[string](t, h.Harness, `SELECT string_agg(first_day::text || '..' || last_day::text || ' ' || status, ', ' ORDER BY first_day)
		FROM staff.absences WHERE person_id = $1 AND type_id = $2`, w.kari.Id, typeID(t, h, "ferie"))
	if want := "2026-10-05..2026-10-06 approved, 2026-10-10..2026-10-16 approved"; pieces != want {
		t.Errorf("the ferie after a 100 %% sykmelding = %s, want %s", pieces, want)
	}
	if a := accountOf(t, w.hr, w.kari.Id, 2026); a.Taken != 7 {
		t.Errorf("taken = %v, want 7: the three sick days returned", a.Taken)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'absence_split' AND (payload->>'absenceId')::bigint = $1`, f.Id); n != 1 {
		t.Errorf("%d absence_split events, want 1", n)
	}

	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-12", "2026-10-13", "degreePercent", 50))
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE person_id = $1 AND type_id = $2 AND status = 'approved'`, w.kari.Id, typeID(t, h, "ferie")); n != 2 {
		t.Errorf("after a graded sykmelding: %d ferie rows, want the two, unsplit", n)
	}
	if a := accountOf(t, w.hr, w.kari.Id, 2026); a.Taken != 7 {
		t.Errorf("taken = %v, want 7 still", a.Taken)
	}

	whole := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-11-02", "2026-11-03"))
	approved(t, w.bossC, whole.Id)
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-11-02", "2026-11-06"))
	if s := statusIn(t, h, whole.Id); s != "cancelled" {
		t.Errorf("ferie a sykmelding covers whole = %s, want cancelled", s)
	}

	requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-11-23", "2026-11-27"))
	refused(t, w.hr.Do(http.MethodPost, absencesPath, with(absence(t, h, "sykmelding", "2026-11-23", "2026-11-24"), "personId", w.kari.Id)),
		http.StatusConflict, "absence_overlap")
}

// A sykmelding covering an egenmelding trims it (reading 24) — deleted when
// wholly covered — and keeps the case; an egenmelding on a day a sykmelding
// covers is absence_overlap (§9).
func TestCases_ASykmeldingTrimsTheEgenmelding(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	e := requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-10-05", "2026-10-07"))
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-06", "2026-10-20"))
	if got := modtest.One[string](t, h.Harness, `SELECT first_day::text || '..' || last_day::text FROM staff.absences WHERE id = $1`, e.Id); got != "2026-10-05..2026-10-05" {
		t.Errorf("the egenmelding = %s, want 2026-10-05..2026-10-05", got)
	}
	if c := onlyCase(t, w.hr, w.kari.Id); civilOf(&c.FirstDay) != "2026-10-05" {
		t.Errorf("the case starts %s, want 2026-10-05", civilOf(&c.FirstDay))
	}
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "egenmelding", "2026-10-15", "2026-10-15")), http.StatusConflict, "absence_overlap")

	gone := requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-12-07", "2026-12-08"))
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-12-07", "2026-12-11"))
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE id = $1`, gone.Id); n != 0 {
		t.Errorf("an egenmelding a sykmelding covers whole is still there")
	}
}

// Who reads and writes a case (design §5's case rows, plan Task 5): the
// person, their manager, the Approver and HR read it — the accommodation
// note all but the Approver, a milestone's note the person and
// staff:personal-data alone; a colleague reads none. The manager, the
// Approver and HR mark milestones done or waived; the accommodation note is
// the manager's and HR's; the estimate and the close HR's; the milestone note
// staff:personal-data's. Anyone else gets the 404 of a case they cannot
// write, whatever its state (authority before status).
func TestCases_WhoReadsAndWritesWhat(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-05", "2026-11-06"))
	c := onlyCase(t, w.hr, w.kari.Id)

	if got := listCases(t, w.viewer, ""); len(got) != 0 {
		t.Errorf("a colleague lists %d cases, want none", len(got))
	}
	if got := listCases(t, w.nobody, ""); len(got) != 0 {
		t.Errorf("a caller with nothing lists %d cases, want none", len(got))
	}
	for name, cl := range map[string]*modtest.Client{"a colleague": w.viewer, "a caller with nothing": w.nobody} {
		if r := cl.Do(http.MethodGet, casePath(c.Id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s reading the case: %d, want 404", name, r.Status)
		}
	}
	for name, cl := range map[string]*modtest.Client{"Kari": w.kariC, "her manager": w.bossC, "the Approver": w.approver, "personal-data": w.pd} {
		if got := listCases(t, cl, ""); len(got) != 1 || got[0].Id != c.Id {
			t.Errorf("%s lists %+v, want Kari's case", name, got)
		}
	}

	// The accommodation note: the manager writes it; the estimate is not his.
	var out gen.StaffCase
	do(t, w.bossC, http.MethodPut, casePath(c.Id), map[string]any{"accommodationNote": "Lighter tasks"}, http.StatusOK, &out, ifMatch(c.Revision))
	if out.AccommodationNote == nil || *out.AccommodationNote != "Lighter tasks" {
		t.Errorf("after the manager's PUT: note %v", out.AccommodationNote)
	}
	refused(t, w.bossC.Do(http.MethodPut, casePath(c.Id), map[string]any{"accommodationNote": "x", "maxBenefitEstimate": "2027-06-01"}, ifMatch(out.Revision)),
		http.StatusBadRequest, "")
	for name, cl := range map[string]*modtest.Client{"the Approver": w.approver, "Kari": w.kariC, "a colleague": w.viewer} {
		if r := cl.Do(http.MethodPut, casePath(c.Id), map[string]any{"accommodationNote": "x"}, ifMatch(out.Revision)); r.Status != http.StatusNotFound {
			t.Errorf("%s writing the case: %d, want 404", name, r.Status)
		}
	}
	var approverView map[string]any
	do(t, w.approver, http.MethodGet, casePath(c.Id), nil, http.StatusOK, &approverView)
	if _, ok := approverView["accommodationNote"]; ok {
		t.Error("the Approver reads the accommodation note")
	}

	// The estimate is HR's; null returns it to the derived day.
	do(t, w.hr, http.MethodPut, casePath(c.Id), map[string]any{"accommodationNote": "Lighter tasks", "maxBenefitEstimate": "2027-06-01"}, http.StatusOK, &out, ifMatch(out.Revision))
	if civilOf(&out.MaxBenefitEstimate) != "2027-06-01" || !out.MaxBenefitEstimateEdited {
		t.Errorf("edited estimate = %s edited %v", civilOf(&out.MaxBenefitEstimate), out.MaxBenefitEstimateEdited)
	}
	refused(t, w.hr.Do(http.MethodPut, casePath(c.Id), map[string]any{}, ifMatch(out.Revision-1)), http.StatusConflict, "revision_conflict")
	do(t, w.hr, http.MethodPut, casePath(c.Id), map[string]any{"accommodationNote": "Lighter tasks", "maxBenefitEstimate": nil}, http.StatusOK, &out, ifMatch(out.Revision))
	if civilOf(&out.MaxBenefitEstimate) != "2027-10-04" || out.MaxBenefitEstimateEdited {
		t.Errorf("reset estimate = %s edited %v, want the derived 2027-10-04", civilOf(&out.MaxBenefitEstimate), out.MaxBenefitEstimateEdited)
	}

	// Milestones: done by the manager and the Approver, waived by HR.
	do(t, w.bossC, http.MethodPost, milestonePath(c.Id, "follow_up_plan", "done"), map[string]any{}, http.StatusOK, &out)
	if m := milestoneOf(t, out, "follow_up_plan"); civilOf(m.DoneAt) != today(t, h) {
		t.Errorf("follow_up_plan done %s, want today %s", civilOf(m.DoneAt), today(t, h))
	}
	do(t, w.approver, http.MethodPost, milestonePath(c.Id, "dialogue_1", "done"), map[string]any{"doneAt": "2026-09-10"}, http.StatusOK, &out)
	refused(t, w.approver.Do(http.MethodPost, milestonePath(c.Id, "dialogue_1", "done"), map[string]any{"doneAt": "2027-01-01"}), http.StatusBadRequest, "")
	do(t, w.hr, http.MethodPost, milestonePath(c.Id, "nav_activity", "waive"), map[string]any{"reason": "Back at work"}, http.StatusOK, &out)
	if m := milestoneOf(t, out, "nav_activity"); !m.Waived || m.WaivedReason == nil || *m.WaivedReason != "Back at work" {
		t.Errorf("nav_activity = %+v, want waived with its reason", m)
	}
	refused(t, w.hr.Do(http.MethodPost, milestonePath(c.Id, "nav_activity", "waive"), map[string]any{"reason": ""}), http.StatusBadRequest, "")
	for name, cl := range map[string]*modtest.Client{"Kari": w.kariC, "a colleague": w.viewer} {
		if r := cl.Do(http.MethodPost, milestonePath(c.Id, "dialogue_2", "done"), map[string]any{}); r.Status != http.StatusNotFound {
			t.Errorf("%s marking a milestone: %d, want 404", name, r.Status)
		}
		if r := cl.Do(http.MethodPost, milestonePath(c.Id, "dialogue_2", "waive"), map[string]any{"reason": "x"}); r.Status != http.StatusNotFound {
			t.Errorf("%s waiving a milestone: %d, want 404", name, r.Status)
		}
	}

	// The milestone's note: staff:personal-data's, read by Kari and them.
	m := milestoneOf(t, out, "dialogue_1")
	do(t, w.pd, http.MethodPut, milestonePath(c.Id, "dialogue_1", "note"), map[string]any{"note": "Talked about the back"}, http.StatusOK, &out, ifMatch(m.Revision))
	if r := w.hr.Do(http.MethodPut, milestonePath(c.Id, "dialogue_1", "note"), map[string]any{"note": "x"}, ifMatch(m.Revision+1)); r.Status != http.StatusForbidden {
		t.Errorf("HR without personal-data writing a note: %d, want 403", r.Status)
	}
	for name, cl := range map[string]*modtest.Client{"Kari": w.kariC, "personal-data": w.pd} {
		var got gen.StaffCase
		do(t, cl, http.MethodGet, casePath(c.Id), nil, http.StatusOK, &got)
		if n := milestoneOf(t, got, "dialogue_1").Note; n == nil || *n != "Talked about the back" {
			t.Errorf("%s reads the note %v", name, n)
		}
	}
	for name, cl := range map[string]*modtest.Client{"her manager": w.bossC, "the Approver": w.approver, "HR": w.hr} {
		var got gen.StaffCase
		do(t, cl, http.MethodGet, casePath(c.Id), nil, http.StatusOK, &got)
		if n := milestoneOf(t, got, "dialogue_1").Note; n != nil {
			t.Errorf("%s reads the milestone note %q", name, *n)
		}
	}

	// The case's events: listed to the manager, the notes left out.
	events := eventsAs(t, w.bossC, w.kari.Id)
	for _, kind := range []string{"case_opened", "case_updated", "milestone_done", "milestone_waived", "milestone_note_updated"} {
		if !slices.Contains(kindsOf(events), kind) {
			t.Errorf("the manager's history %v lacks %s", kindsOf(events), kind)
		}
	}
	for _, e := range events {
		if strings.Contains(fmt.Sprint(e.Payload), "Talked about the back") {
			t.Errorf("the manager reads the milestone note in %s: %v", e.Kind, e.Payload)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind LIKE 'case_%' AND payload_classes <> '{E}'`, w.kari.Id); n != 0 {
		t.Errorf("%d case events touch another class than E", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'case_opened' AND jsonb_array_length(payload->'typeIds') = 1`, w.kari.Id); n != 1 {
		t.Error("case_opened does not carry the case's type")
	}

	// The close is HR's; a row that joins opens the case again.
	if r := w.bossC.Do(http.MethodPost, casePath(c.Id)+"/close", nil); r.Status != http.StatusForbidden {
		t.Errorf("the manager closing: %d, want 403", r.Status)
	}
	do(t, w.hr, http.MethodPost, casePath(c.Id)+"/close", nil, http.StatusOK, &out)
	if out.Open || out.ClosedAt == nil {
		t.Errorf("closed case = open %v closedAt %v", out.Open, out.ClosedAt)
	}
	if got := listCases(t, w.hr, ""); len(got) != 0 {
		t.Errorf("the open list holds %d cases after the close, want none", len(got))
	}
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-11-10", "2026-11-11"))
	out = gen.StaffCase{}
	do(t, w.hr, http.MethodGet, casePath(c.Id), nil, http.StatusOK, &out)
	if !out.Open || out.ClosedAt != nil || civilOf(&out.LastDay) != "2026-11-11" {
		t.Errorf("after a joining row: open %v closedAt %v last %s, want reopened to 2026-11-11", out.Open, out.ClosedAt, civilOf(&out.LastDay))
	}
	if m := milestoneOf(t, out, "follow_up_plan"); m.DoneAt == nil {
		t.Error("recomputing the case touched a milestone's done day")
	}
}

// The derived close: sixteen days after its last day a case is closed
// without HR; the list's status filter reads it so.
func TestCases_TheDerivedClose(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	register(t, w.hr, w.kari.Id, absence(t, w.h, "sykmelding", "2026-08-03", "2026-08-07"))
	register(t, w.hr, w.kari.Id, absence(t, w.h, "sykmelding", "2026-09-07", "2026-09-30"))
	all := listCases(t, w.hr, "?status=all")
	closed := listCases(t, w.hr, "?status=closed")
	open := listCases(t, w.hr, "")
	if len(all) != 2 || len(closed) != 1 || len(open) != 1 || civilOf(&closed[0].LastDay) != "2026-08-07" || closed[0].Open {
		t.Errorf("all %d, closed %+v, open %d: want August's closed (last day 7 August, today 12 September), September's open", len(all), closed, len(open))
	}
	refused(t, w.hr.Do(http.MethodGet, casesPath+"?status=shut", nil), http.StatusBadRequest, "")
}

// A health document is filed under one of the person's cases (S15) by
// staff:personal-data, and only a document of a restricted category; one
// moved out of it leaves the case.
func TestCases_AHealthDocumentIsFiledUnderTheCase(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-05", "2026-10-09"))
	register(t, w.hr, w.boss.Id, absence(t, h, "sykmelding", "2026-10-05", "2026-10-09"))
	kariCase, bossCase := onlyCase(t, w.hr, w.kari.Id), onlyCase(t, w.hr, w.boss.Id)
	health := documentCategoryID(t, h, "health")
	doc := seedDocument(t, h, w.kari.Id, "health")

	var out gen.StaffDocument
	do(t, w.pd, http.MethodPut, documentPath(doc), map[string]any{"categoryId": health, "caseId": kariCase.Id}, http.StatusOK, &out, ifMatch(1))
	if out.CaseId == nil || *out.CaseId != kariCase.Id {
		t.Errorf("caseId = %v, want %d", out.CaseId, kariCase.Id)
	}
	refused(t, w.pd.Do(http.MethodPut, documentPath(doc), map[string]any{"categoryId": health, "caseId": bossCase.Id}, ifMatch(out.Revision)), http.StatusBadRequest, "")
	other := seedDocument(t, h, w.kari.Id, "contract")
	refused(t, w.pd.Do(http.MethodPut, documentPath(other), map[string]any{"categoryId": documentCategoryID(t, h, "contract"), "caseId": kariCase.Id}, ifMatch(1)), http.StatusBadRequest, "")
	do(t, w.pd, http.MethodPut, documentPath(doc), map[string]any{"categoryId": health}, http.StatusOK, &out, ifMatch(out.Revision))
	if out.CaseId == nil {
		t.Error("a change that does not name caseId cleared it")
	}
	revision := out.Revision
	out = gen.StaffDocument{}
	do(t, w.pd, http.MethodPut, documentPath(doc), map[string]any{"categoryId": documentCategoryID(t, h, "other")}, http.StatusOK, &out, ifMatch(revision))
	if out.CaseId != nil {
		t.Errorf("a document moved out of the restricted category keeps case %d", *out.CaseId)
	}
}

// ---- Races (plan Global Constraints "Races") ------------------------------

// A case write racing a new sick row on the same person: both take the
// person's lock. A milestone done under the lock is kept when the row that
// joins the case recomputes it; HR's change of the case behind the manager's
// read is a revision conflict once the lock is free; neither is 40P01.
func TestCases_CaseWritesRaceANewSickRow(t *testing.T) {
	t.Run("a milestone done, then a joining row", func(t *testing.T) {
		w := newDecisionWorld(t, modtest.WithPoolMaxConns(2))
		register(t, w.hr, w.kari.Id, absence(t, w.h, "sykmelding", "2026-10-05", "2026-10-30"))
		c := onlyCase(t, w.hr, w.kari.Id)
		r := raceOn(t, w, `UPDATE staff.sick_case_milestones SET done_at = '2026-09-01', revision = revision + 1 WHERE case_id = $1 AND kind = 'follow_up_plan'`, []any{c.Id},
			func(ctx context.Context) *modtest.Response {
				return w.hr.Do(http.MethodPost, absencesPath, with(absence(t, w.h, "sykmelding", "2026-11-02", "2026-11-04"), "personId", w.kari.Id), modtest.Context(ctx))
			})
		if r.Status != http.StatusCreated {
			t.Fatalf("the joining row: %d %s", r.Status, r.Body)
		}
		c = onlyCase(t, w.hr, w.kari.Id)
		if civilOf(&c.LastDay) != "2026-11-04" || milestoneOf(t, c, "follow_up_plan").DoneAt == nil {
			t.Errorf("case = last %s, follow_up_plan %+v: want the row joined and the done day kept", civilOf(&c.LastDay), milestoneOf(t, c, "follow_up_plan"))
		}
	})
	t.Run("a new row, then the manager's note", func(t *testing.T) {
		w := newDecisionWorld(t, modtest.WithPoolMaxConns(2))
		register(t, w.hr, w.kari.Id, absence(t, w.h, "sykmelding", "2026-10-05", "2026-10-30"))
		c := onlyCase(t, w.hr, w.kari.Id)
		r := raceOn(t, w, `UPDATE staff.sick_cases SET last_day = '2026-11-04', revision = revision + 1 WHERE id = $1`, []any{c.Id},
			func(ctx context.Context) *modtest.Response {
				return w.bossC.Do(http.MethodPut, casePath(c.Id), map[string]any{"accommodationNote": "Lighter tasks"}, ifMatch(c.Revision), modtest.Context(ctx))
			})
		refused(t, r, http.StatusConflict, "revision_conflict")
	})
}
