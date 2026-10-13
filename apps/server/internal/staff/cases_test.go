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

// postponePath is a sykmelding's postponement of the ferie under it.
func postponePath(id int64) string { return absencePath(id) + "/postpone-holiday" }

// ferieRows is the person's ferie rows as "first..last status", oldest first.
func ferieRows(t *testing.T, h *harness, personID int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT coalesce(string_agg(first_day::text || '..' || last_day::text || ' ' || status, ', ' ORDER BY first_day), '')
		FROM staff.absences WHERE person_id = $1 AND type_id = (SELECT id FROM staff.absence_types WHERE key = 'ferie')`, personID)
}

// A sykmelding over ferie leaves the ferie as it is (ferieloven § 9, Task 5
// review H1): postponing the holiday is the employee's demand, with a
// legeerklæring, not the sykmelding's. Until HR records it, HR's attention
// list names the ferie (holidayAffectedBySickness); a sykmelding over a
// requested ferie is accepted and the request is refused at its approval
// (review M2).
func TestCases_SickLeaveOverFerieStandsBesideIt(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	f := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-08-24", "2026-09-04"))
	approved(t, w.bossC, f.Id)
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-26", "2026-08-28"))
	if got := ferieRows(t, h, w.kari.Id); got != "2026-08-24..2026-09-04 approved" {
		t.Errorf("the ferie after a 100 %% sykmelding = %s, want it unchanged", got)
	}
	if a := accountOf(t, w.hr, w.kari.Id, 2026); a.Taken != 10 {
		t.Errorf("taken = %v, want 10 still", a.Taken)
	}
	items := ofType(getAttention(t, w.hr), gen.HolidayAffectedBySickness)
	if len(items) != 1 || items[0].Id != fmt.Sprintf("holidayAffectedBySickness/%d", f.Id) || items[0].EntityId != fmt.Sprint(w.kari.Id) {
		t.Errorf("HR's items = %+v, want the ferie named once", items)
	}
	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "a colleague": w.viewer, "personal-data alone": w.pd} {
		if got := ofType(getAttention(t, c), gen.HolidayAffectedBySickness); len(got) != 0 {
			t.Errorf("%s is shown %+v", name, got)
		}
	}

	r := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-11-23", "2026-11-27"))
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-11-23", "2026-11-24"))
	if s := statusIn(t, h, r.Id); s != "requested" {
		t.Errorf("a requested ferie under a sykmelding = %s, want requested still", s)
	}
	refused(t, decide(w.bossC, approvePath(r.Id), ""), http.StatusConflict, "absence_overlap")
}

// HR records the employee's demand to postpone the holiday (ferieloven § 9):
// the ferie is split around the sykmelding and the days return. Sickness
// during the holiday (§ 9 (2)) gives replacement days whenever demanded;
// sickness before it (§ 9 (1)) must have been demanded by the last working
// day before the holiday. A graded sykmelding, or a row that is no
// sykmelding, postpones nothing.
func TestCases_PostponingHolidayForSickness(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	f := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-08-24", "2026-09-04"))
	approved(t, w.bossC, f.Id)
	during := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-26", "2026-08-28"))
	if r := w.bossC.Do(http.MethodPost, postponePath(during.Id), map[string]any{}); r.Status != http.StatusForbidden {
		t.Errorf("the manager recording a demand: %d, want 403", r.Status)
	}
	refused(t, w.hr.Do(http.MethodPost, postponePath(during.Id), map[string]any{"demandedOn": "2026-09-13"}), http.StatusBadRequest, "")
	var out gen.StaffHolidayPostponement
	do(t, w.hr, http.MethodPost, postponePath(during.Id), map[string]any{"demandedOn": "2026-09-07"}, http.StatusOK, &out)
	if civilOf(&out.DemandedOn) != "2026-09-07" || len(out.Absences) != 2 {
		t.Errorf("postponement = %+v, want the two parts of the ferie", out)
	}
	if got, want := ferieRows(t, h, w.kari.Id), "2026-08-24..2026-08-25 approved, 2026-08-29..2026-09-04 approved"; got != want {
		t.Errorf("the ferie = %s, want %s", got, want)
	}
	if a := accountOf(t, w.hr, w.kari.Id, 2026); a.Taken != 7 {
		t.Errorf("taken = %v, want 7: the three sick days returned", a.Taken)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'absence_split' AND (payload->>'absenceId')::bigint = $1
		AND payload->'fields'->'demandedOn'->>'to' = '2026-09-07' AND payload->'fields'->'postponedUnder'->>'to' = '9 (2)'
		AND payload->'classes'->>'demandedOn' = 'E'`, f.Id); n != 1 {
		t.Errorf("%d absence_split events carrying the demand, want 1", n)
	}
	if got := ofType(getAttention(t, w.hr), gen.HolidayAffectedBySickness); len(got) != 0 {
		t.Errorf("after the postponement HR is still shown %+v", got)
	}

	// Sick from the Thursday before a holiday starting Monday 8 June: the
	// demand is due by Friday 5 June.
	june := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-06-08", "2026-06-12"))
	approved(t, w.bossC, june.Id)
	before := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-06-04", "2026-06-09"))
	refused(t, w.hr.Do(http.MethodPost, postponePath(before.Id), map[string]any{"demandedOn": "2026-06-08"}), http.StatusConflict, "holiday_postponement_too_late")
	do(t, w.hr, http.MethodPost, postponePath(before.Id), map[string]any{"demandedOn": "2026-06-05"}, http.StatusOK, &out)
	if got := modtest.One[string](t, h.Harness, `SELECT first_day::text || '..' || last_day::text FROM staff.absences WHERE id = $1`, june.Id); got != "2026-06-10..2026-06-12" {
		t.Errorf("June's ferie = %s, want 2026-06-10..2026-06-12", got)
	}

	july := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-07-06", "2026-07-10"))
	approved(t, w.bossC, july.Id)
	graded := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-07-06", "2026-07-07", "degreePercent", 50))
	refused(t, w.hr.Do(http.MethodPost, postponePath(graded.Id), map[string]any{}), http.StatusConflict, "holiday_postponement_not_applicable")
	refused(t, w.hr.Do(http.MethodPost, postponePath(july.Id), map[string]any{}), http.StatusConflict, "holiday_postponement_not_applicable")
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
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-03", "2026-09-30"))
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

	// The accommodation note: the manager writes it; the estimate is not his,
	// not even to reset it.
	var out gen.StaffCase
	do(t, w.bossC, http.MethodPut, casePath(c.Id), map[string]any{"accommodationNote": "Lighter tasks"}, http.StatusOK, &out, ifMatch(c.Revision))
	if out.AccommodationNote == nil || *out.AccommodationNote != "Lighter tasks" {
		t.Errorf("after the manager's PUT: note %v", out.AccommodationNote)
	}
	for _, estimate := range []any{"2027-06-01", nil} {
		refused(t, w.bossC.Do(http.MethodPut, casePath(c.Id), map[string]any{"maxBenefitEstimate": estimate}, ifMatch(out.Revision)),
			http.StatusBadRequest, "")
	}
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

	// A field left out is kept; null resets it (review L6). The estimate is
	// HR's; null returns it to the derived day.
	out = gen.StaffCase{}
	do(t, w.hr, http.MethodPut, casePath(c.Id), map[string]any{"maxBenefitEstimate": "2027-06-01"}, http.StatusOK, &out, ifMatch(c.Revision+1))
	if civilOf(&out.MaxBenefitEstimate) != "2027-06-01" || !out.MaxBenefitEstimateEdited || out.AccommodationNote == nil {
		t.Errorf("edited estimate = %s edited %v note %v, want the note kept", civilOf(&out.MaxBenefitEstimate), out.MaxBenefitEstimateEdited, out.AccommodationNote)
	}
	refused(t, w.hr.Do(http.MethodPut, casePath(c.Id), map[string]any{}, ifMatch(out.Revision-1)), http.StatusConflict, "revision_conflict")
	out = gen.StaffCase{}
	do(t, w.hr, http.MethodPut, casePath(c.Id), map[string]any{"accommodationNote": nil}, http.StatusOK, &out, ifMatch(c.Revision+2))
	if out.AccommodationNote != nil || civilOf(&out.MaxBenefitEstimate) != "2027-06-01" {
		t.Errorf("after the note's null: note %v estimate %s, want no note and the estimate kept", out.AccommodationNote, civilOf(&out.MaxBenefitEstimate))
	}
	do(t, w.hr, http.MethodPut, casePath(c.Id), map[string]any{"maxBenefitEstimate": nil}, http.StatusOK, &out, ifMatch(out.Revision))
	if civilOf(&out.MaxBenefitEstimate) != "2027-08-02" || out.MaxBenefitEstimateEdited {
		t.Errorf("reset estimate = %s edited %v, want the derived 2027-08-02", civilOf(&out.MaxBenefitEstimate), out.MaxBenefitEstimateEdited)
	}

	// Milestones: done by the manager and the Approver, waived by HR; done
	// or waived, one is due no more (L7); never done before the case began,
	// and the derived end of the arbeidsgiverperiode is neither done nor
	// waived (L8).
	if !milestoneOf(t, out, "follow_up_plan").Due {
		t.Fatal("the follow-up plan is not due on a case lasting past it")
	}
	do(t, w.bossC, http.MethodPost, milestonePath(c.Id, "follow_up_plan", "done"), map[string]any{}, http.StatusOK, &out)
	if m := milestoneOf(t, out, "follow_up_plan"); civilOf(m.DoneAt) != today(t, h) || m.Due {
		t.Errorf("follow_up_plan done %s due %v, want today %s and no longer due", civilOf(m.DoneAt), m.Due, today(t, h))
	}
	do(t, w.approver, http.MethodPost, milestonePath(c.Id, "dialogue_1", "done"), map[string]any{"doneAt": "2026-09-10"}, http.StatusOK, &out)
	refused(t, w.approver.Do(http.MethodPost, milestonePath(c.Id, "dialogue_1", "done"), map[string]any{"doneAt": "2027-01-01"}), http.StatusBadRequest, "")
	refused(t, w.approver.Do(http.MethodPost, milestonePath(c.Id, "dialogue_1", "done"), map[string]any{"doneAt": "2026-08-01"}), http.StatusBadRequest, "")
	refused(t, w.hr.Do(http.MethodPost, milestonePath(c.Id, "employer_period_end", "done"), map[string]any{}), http.StatusBadRequest, "")
	refused(t, w.hr.Do(http.MethodPost, milestonePath(c.Id, "employer_period_end", "waive"), map[string]any{"reason": "x"}), http.StatusBadRequest, "")
	do(t, w.hr, http.MethodPost, milestonePath(c.Id, "nav_activity", "waive"), map[string]any{"reason": "Back at work"}, http.StatusOK, &out)
	if m := milestoneOf(t, out, "nav_activity"); !m.Waived || m.Due {
		t.Errorf("nav_activity = %+v, want waived and not due", m)
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

	// The milestone's note and the waiver's reason: health data, read by Kari
	// and staff:personal-data alone (L5).
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
		if r := milestoneOf(t, got, "nav_activity").WaivedReason; r == nil || *r != "Back at work" {
			t.Errorf("%s reads the waiver's reason %v", name, r)
		}
	}
	for name, cl := range map[string]*modtest.Client{"her manager": w.bossC, "the Approver": w.approver, "HR": w.hr} {
		var got gen.StaffCase
		do(t, cl, http.MethodGet, casePath(c.Id), nil, http.StatusOK, &got)
		if n := milestoneOf(t, got, "dialogue_1").Note; n != nil {
			t.Errorf("%s reads the milestone note %q", name, *n)
		}
		if r := milestoneOf(t, got, "nav_activity").WaivedReason; r != nil {
			t.Errorf("%s reads the waiver's reason %q", name, *r)
		}
	}

	// The case's events: listed to the manager, the notes left out; a
	// milestone's note's own event is the health readers' alone (L4).
	events := eventsAs(t, w.bossC, w.kari.Id)
	for _, kind := range []string{"case_opened", "case_updated", "milestone_done", "milestone_waived"} {
		if !slices.Contains(kindsOf(events), kind) {
			t.Errorf("the manager's history %v lacks %s", kindsOf(events), kind)
		}
	}
	if slices.Contains(kindsOf(events), "milestone_note_updated") {
		t.Error("the manager's history lists milestone_note_updated")
	}
	for _, e := range events {
		if strings.Contains(fmt.Sprint(e.Payload), "Talked about the back") || strings.Contains(fmt.Sprint(e.Payload), "Back at work") {
			t.Errorf("the manager reads health data in %s: %v", e.Kind, e.Payload)
		}
	}
	for name, cl := range map[string]*modtest.Client{"Kari": w.kariC, "personal-data": w.pd} {
		if !slices.Contains(kindsOf(eventsAs(t, cl, w.kari.Id)), "milestone_note_updated") {
			t.Errorf("%s's history lacks milestone_note_updated", name)
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
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-10", "2026-10-11"))
	out = gen.StaffCase{}
	do(t, w.hr, http.MethodGet, casePath(c.Id), nil, http.StatusOK, &out)
	if !out.Open || out.ClosedAt != nil || civilOf(&out.LastDay) != "2026-10-11" {
		t.Errorf("after a joining row: open %v closedAt %v last %s, want reopened to 2026-10-11", out.Open, out.ClosedAt, civilOf(&out.LastDay))
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

// A row that fills the gap between two cases merges them into the older
// (review M3): the younger's milestones' done days and waivers carry over
// where the older's have none, their notes and the accommodation note are
// appended, and a case_merged event of class E on the survivor names the
// case it took in.
func TestCases_AMergeCarriesTheMilestones(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-03", "2026-08-05"))
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-24", "2026-08-25"))
	cases := listCases(t, w.hr, "?status=all")
	if len(cases) != 2 {
		t.Fatalf("%d cases, want 2", len(cases))
	}
	older, younger := cases[1], cases[0]
	var out gen.StaffCase
	do(t, w.hr, http.MethodPost, milestonePath(younger.Id, "follow_up_plan", "done"), map[string]any{"doneAt": "2026-09-01"}, http.StatusOK, &out)
	do(t, w.hr, http.MethodPost, milestonePath(younger.Id, "nav_activity", "waive"), map[string]any{"reason": "Back at work"}, http.StatusOK, &out)
	do(t, w.hr, http.MethodPut, casePath(younger.Id), map[string]any{"accommodationNote": "Lighter tasks"}, http.StatusOK, &out, ifMatch(out.Revision))
	for _, c := range []gen.StaffCase{older, younger} {
		do(t, w.pd, http.MethodGet, casePath(c.Id), nil, http.StatusOK, &out)
		note := fmt.Sprintf("Note of %d", c.Id)
		do(t, w.pd, http.MethodPut, milestonePath(c.Id, "dialogue_1", "note"), map[string]any{"note": note}, http.StatusOK, &out,
			ifMatch(milestoneOf(t, out, "dialogue_1").Revision))
	}

	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-12", "2026-08-13"))
	merged := onlyCase(t, w.pd, w.kari.Id)
	if merged.Id != older.Id || civilOf(&merged.LastDay) != "2026-08-25" {
		t.Fatalf("merged = %d..%s, want case %d to 2026-08-25", merged.Id, civilOf(&merged.LastDay), older.Id)
	}
	if m := milestoneOf(t, merged, "follow_up_plan"); civilOf(m.DoneAt) != "2026-09-01" {
		t.Errorf("follow_up_plan done %s, want the younger case's 2026-09-01", civilOf(m.DoneAt))
	}
	if m := milestoneOf(t, merged, "nav_activity"); !m.Waived || m.WaivedReason == nil || *m.WaivedReason != "Back at work" {
		t.Errorf("nav_activity = %+v, want the younger case's waiver", m)
	}
	if n := milestoneOf(t, merged, "dialogue_1").Note; n == nil || *n != fmt.Sprintf("Note of %d\n\nNote of %d", older.Id, younger.Id) {
		t.Errorf("dialogue_1's note = %v, want both, the older first", n)
	}
	if merged.AccommodationNote == nil || *merged.AccommodationNote != "Lighter tasks" {
		t.Errorf("accommodation note = %v, want the younger case's", merged.AccommodationNote)
	}
	if r := w.hr.Do(http.MethodGet, casePath(younger.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("the merged-away case: %d, want 404", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'case_merged' AND (payload->>'caseId')::bigint = $1
		AND payload->'fields'->'mergedCaseId'->>'to' = $2 AND payload_classes = '{E}'`, older.Id, fmt.Sprint(younger.Id)); n != 1 {
		t.Errorf("%d case_merged events naming the younger case, want 1", n)
	}
}

// One illness is one egenmelding (review M4): Monday–Wednesday cut in two by
// a Tuesday sykmelding stays one occurrence of two self-certified days.
func TestSelfCertification_OneIllnessIsOneOccurrence(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-09-07", "2026-09-09"))
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-09-08", "2026-09-08"))
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE person_id = $1 AND type_id = $2`, w.kari.Id, typeID(t, h, "egenmelding")); n != 2 {
		t.Fatalf("%d egenmelding rows, want Monday and Wednesday", n)
	}
	var standing gen.StaffSelfCertification
	do(t, w.kariC, http.MethodGet, meSelfCertPath, nil, http.StatusOK, &standing)
	if standing.OccurrencesUsed != 1 || standing.DaysUsed != 2 {
		t.Errorf("used = %d occurrences, %d days, want 1 and 2", standing.OccurrencesUsed, standing.DaysUsed)
	}
	requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-09-10", "2026-09-10"))
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "egenmelding", "2026-09-11", "2026-09-11")), http.StatusConflict, "self_certification_exhausted")
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

// A milestone written on a case a concurrent row has merged away (review
// L9): the merge holds the person's lock; the milestone's write waits on it
// and then finds the case gone — the 404 of a case that is not there, never
// a write on the survivor it did not ask for, never 40P01.
func TestCases_AMilestoneOnACaseMergedAway(t *testing.T) {
	w := newDecisionWorld(t, modtest.WithPoolMaxConns(2))
	register(t, w.hr, w.kari.Id, absence(t, w.h, "sykmelding", "2026-08-03", "2026-08-05"))
	register(t, w.hr, w.kari.Id, absence(t, w.h, "sykmelding", "2026-08-24", "2026-08-25"))
	cases := listCases(t, w.hr, "?status=all")
	older, younger := cases[1], cases[0]
	r := raceOn(t, w, `WITH moved AS (UPDATE staff.absences SET case_id = $1 WHERE case_id = $2),
		gone AS (DELETE FROM staff.sick_case_milestones WHERE case_id = $2)
		DELETE FROM staff.sick_cases WHERE id = $2`, []any{older.Id, younger.Id},
		func(ctx context.Context) *modtest.Response {
			return w.hr.Do(http.MethodPost, milestonePath(younger.Id, "follow_up_plan", "done"), map[string]any{"doneAt": "2026-09-01"}, modtest.Context(ctx))
		})
	if r.Status != http.StatusNotFound {
		t.Errorf("the milestone on the merged-away case: %d %s, want 404", r.Status, r.Body)
	}
	if n := w.h.Count(t, `SELECT count(*) FROM staff.sick_case_milestones WHERE case_id = $1 AND done_at IS NOT NULL`, older.Id); n != 0 {
		t.Errorf("%d of the survivor's milestones were marked by a write on the other case", n)
	}
}

// Changing or removing an absence reads its type FOR KEY SHARE (Task 4
// re-check): a change of the type's own, holding it FOR UPDATE, is waited
// for, so the type is not widened between the read and the write.
func TestAbsences_AChangeHoldsTheTypeForKeyShare(t *testing.T) {
	w := newAbsenceWorld(t, modtest.WithPoolMaxConns(2))
	h := w.h
	a := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	probe := ownConn(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	holderConn := ownConn(t, h)
	holder, err := holderConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(ctx, `SELECT id FROM staff.absence_types WHERE key = 'ferie' FOR UPDATE`); err != nil {
		t.Fatalf("hold the type: %v", err)
	}
	answered := make(chan *modtest.Response, 1)
	go func() { answered <- w.kariC.Do(http.MethodDelete, absencePath(a.Id), nil, modtest.Context(ctx)) }()
	if _, blockers := waiterOn(t, probe); !slices.Equal(blockers, []uint32{holderConn.PgConn().PID()}) {
		t.Fatalf("the delete waits on %v, want the type's holder", blockers)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if r := <-answered; r.Status != http.StatusNoContent {
		t.Errorf("the delete: %d %s, want 204", r.Status, r.Body)
	}
}
