package staff_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func glancePath(personID int64) string { return personPath(personID) + "/glance" }

// fakeTimesheets is time's contracts.Timesheets as a test sets it: the hours
// it answers per user, and every week and user it was asked about; err, when
// set, is its answer instead, and during runs inside every call — between
// the glance's two snapshots.
type fakeTimesheets struct {
	mu     sync.Mutex
	hours  map[uuid.UUID]contracts.WeekHours
	asked  []timesheetsAsk
	err    error
	during func()
}

type timesheetsAsk struct {
	weekStart time.Time
	users     []uuid.UUID
}

func newFakeTimesheets() *fakeTimesheets {
	return &fakeTimesheets{hours: map[uuid.UUID]contracts.WeekHours{}}
}

func (f *fakeTimesheets) WeekHours(_ context.Context, weekStart time.Time, userIDs []uuid.UUID) (map[uuid.UUID]contracts.WeekHours, error) {
	f.mu.Lock()
	during := f.during
	f.mu.Unlock()
	if during != nil {
		during()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, timesheetsAsk{weekStart: weekStart, users: slices.Clone(userIDs)})
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]contracts.WeekHours{}
	for _, id := range userIDs {
		if h, ok := f.hours[id]; ok {
			out[id] = h
		}
	}
	return out, nil
}

// failWith makes every later call answer err.
func (f *fakeTimesheets) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// whenAsked runs fn inside every later call.
func (f *fakeTimesheets) whenAsked(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.during = fn
}

func (f *fakeTimesheets) asks() []timesheetsAsk {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// glanceWorld is one installation, on the harness clock's Saturday 12
// September 2026, with the audiences of design §5 on Kari: herself, her
// manager Bjørn, the Approver, HR (staff:manage), staff:personal-data alone,
// a colleague (staff:view) and a caller with staff:access alone. Kari started
// on 15 April 2026 at 80 % of 37.5 hours as a Senior engineer, her probation
// ending on 14 October; she has one child under twelve recorded and is
// linked to a user who logged 31.5 hours this week, 20 of them approved.
// Her year: an egenmelding of three days in July, a sykt barn day in August,
// five days of ferie in August, and a 50 % sykmelding from Wednesday 9 to
// Monday 14 September — four working days, covering today, an open case —
// with ferie ahead: two days requested from 1 October and three approved
// from 6 October.
type glanceWorld struct {
	h                                *harness
	times                            *fakeTimesheets
	hr, pd, approver, viewer, nobody *modtest.Client
	kariC, bossC                     *modtest.Client
	kari, boss                       gen.StaffPerson
	kariUser                         uuid.UUID
	sick, requested, planned         gen.StaffAbsence
	ferieType, sykmeldingType        int64
}

func newGlanceWorld(t *testing.T) glanceWorld {
	t.Helper()
	times := newFakeTimesheets()
	h := newHarness(t, modtest.WithTimesheets(times))
	w := glanceWorld{h: h, times: times}
	w.hr, _ = signIn(t, h, "staff:manage")
	company(t, w.hr)
	w.boss, _ = employed(t, w.hr, "Bjørn", "2019-01-01", terms())
	w.kari, _ = employed(t, w.hr, "Kari", "2026-04-15", terms(
		"managerPersonId", w.boss.Id, "ftePercent", 80, "fullTimeHoursPerWeek", 37.5,
		"title", "Senior engineer", "probationEndDate", "2026-10-14"))
	var bossUser uuid.UUID
	w.kariC, w.kariUser = signIn(t, h)
	w.bossC, bossUser = signIn(t, h)
	link(t, h, w.kari.Id, w.kariUser)
	link(t, h, w.boss.Id, bossUser)
	w.pd, _ = signIn(t, h, "staff:personal-data")
	w.approver, _ = signIn(t, h, "staff:approve")
	w.viewer, _ = signIn(t, h, "staff:view")
	w.nobody, _ = signIn(t, h)
	h.Exec(t, `UPDATE staff.people SET children_under_12 = 1 WHERE id = $1`, w.kari.Id)
	times.hours[w.kariUser] = contracts.WeekHours{Logged: 31.5, Approved: 20}

	w.ferieType, w.sykmeldingType = typeID(t, h, "ferie"), typeID(t, h, "sykmelding")
	requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-07-07", "2026-07-09"))
	requestOwn(t, w.kariC, absence(t, h, "sykt_barn", "2026-08-18", "2026-08-18"))
	taken := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-08-10", "2026-08-14"))
	w.requested = requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-01", "2026-10-02"))
	w.planned = requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-06", "2026-10-08", "note", "Høstferie"))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved' WHERE id = ANY($1)`, []int64{taken.Id, w.planned.Id})
	w.sick = register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-09-09", "2026-09-14", "degreePercent", 50))
	h.Exec(t, `UPDATE staff.sick_cases SET accommodation_note = 'Tilrettelegging: kortere dager' WHERE person_id = $1`, w.kari.Id)
	return w
}

// glanceOf reads personID's glance as c, failing the test unless it was 200,
// both decoded and as a bare object.
func glanceOf(t *testing.T, c *modtest.Client, personID int64) (gen.StaffPersonGlance, map[string]any) {
	t.Helper()
	r := c.Do(http.MethodGet, glancePath(personID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("glance of %d: status %d body %s, want 200", personID, r.Status, r.Body)
	}
	var out gen.StaffPersonGlance
	r.JSON(&out)
	return out, raw(t, r)
}

// The manager's glance carries every card but the contract, with the
// figures the tabs would show: sick days by kind and degree, the
// egenmelding standing, the holiday split against today with the account's
// remaining, the sick-child days without the entitlement, the employment
// with its probation, today's sykmelding and the request ahead, the open
// case on its fourth day, and the week's hours against the 30 the contract
// expects — read from time for Kari's user, this week's Monday.
func TestGlance_TheManagersCards(t *testing.T) {
	t.Parallel()
	w := newGlanceWorld(t)
	g, body := glanceOf(t, w.bossC, w.kari.Id)

	if g.SickDays == nil || g.SickDays.SelfCertified != 3 || g.SickDays.Certified != 2 {
		t.Errorf("sickDays = %+v, want 3 egenmelding and 2 sykmelding days", g.SickDays)
	}
	var standing gen.StaffSelfCertification
	do(t, w.bossC, http.MethodGet, selfCertPath(w.kari.Id), nil, http.StatusOK, &standing)
	if s := g.SelfCertification; s == nil || s.OccurrencesUsed != 1 || s.OccurrencesUsed != standing.OccurrencesUsed ||
		s.DaysUsed != standing.DaysUsed || !slices.Equal(ptrs(s.OccurrencesLimit), ptrs(standing.OccurrencesPerYear)) ||
		s.OccurrencesLimit == nil || s.DaysLimit != nil {
		t.Errorf("selfCertification = %+v, want the standing's %+v under the minimum policy", s, standing)
	}
	var account gen.StaffHolidayAccount
	do(t, w.bossC, http.MethodGet, holidayAccountPath(w.kari.Id), nil, http.StatusOK, &account)
	if hol := g.Holiday; hol == nil || hol.TakenToDate != 5 || hol.Planned != 5 || hol.Remaining != account.Remaining || hol.Overdrawn != account.Overdrawn {
		t.Errorf("holiday = %+v, want 5 taken, 5 planned and the account's %v remaining", hol, account.Remaining)
	}
	if c := g.ChildSick; c == nil || c.Days != 1 || c.Entitlement != nil || c.Remaining != nil {
		t.Errorf("childSick = %+v, want 1 day and no entitlement", c)
	}
	if e := g.Employment; e == nil || e.StartDate == nil || e.StartDate.Format(time.DateOnly) != "2026-04-15" ||
		e.TenureMonths == nil || *e.TenureMonths != 4 || e.FtePercent == nil || *e.FtePercent != 80 ||
		e.Title == nil || *e.Title != "Senior engineer" || e.ProbationEndsOn == nil || e.ProbationEndsOn.Format(time.DateOnly) != "2026-10-14" {
		t.Errorf("employment = %+v", e)
	}
	a := g.Absence
	if a == nil || a.Now == nil || a.Now.FirstDay.Format(time.DateOnly) != "2026-09-09" || a.Now.LastDay.Format(time.DateOnly) != "2026-09-14" ||
		a.Now.ShownAs != gen.StaffAbsenceShownAsType || a.Now.TypeId == nil || *a.Now.TypeId != w.sykmeldingType {
		t.Errorf("absence.now = %+v, want the sykmelding", a)
	}
	if a == nil || a.Next == nil || a.Next.FirstDay.Format(time.DateOnly) != "2026-10-01" || a.Next.TypeId == nil || *a.Next.TypeId != w.ferieType {
		t.Errorf("absence.next = %+v, want the request from 1 October", a)
	}
	if a == nil || a.OpenCase == nil || a.OpenCase.FirstDay.Format(time.DateOnly) != "2026-09-09" || a.OpenCase.Day != 4 {
		t.Errorf("absence.openCase = %+v, want the case from 9 September on its day 4", a)
	}
	if _, ok := body["contract"]; ok {
		t.Errorf("the manager reads the contract card: %v", body["contract"])
	}
	if hr := g.Hours; hr == nil || !hr.TimeTracking || hr.WeekStart == nil || hr.WeekStart.Format(time.DateOnly) != "2026-09-07" ||
		hr.Logged == nil || *hr.Logged != 31.5 || hr.Approved == nil || *hr.Approved != 20 || hr.Expected == nil || *hr.Expected != 30 {
		t.Errorf("hours = %+v, want 31.5 logged, 20 approved of 30 in the week of 7 September", hr)
	}
	asks := w.times.asks()
	if len(asks) != 1 || asks[0].weekStart.Format(time.DateOnly) != "2026-09-07" || asks[0].weekStart.Location() != time.UTC ||
		!slices.Equal(asks[0].users, []uuid.UUID{w.kariUser}) {
		t.Errorf("time was asked %+v, want once for Kari's user and the week of 7 September", asks)
	}
}

// ptrs is an optional number as a comparable slice: empty when absent.
func ptrs(v *int32) []int32 {
	if v == nil {
		return nil
	}
	return []int32{*v}
}

// Every audience who sees Kari gets her glance, each with its own cards:
// HR the contract — not signed — and the sick-child entitlement; personal
// data the entitlement and the hours but no contract; the Approver neither
// hours nor contract; the colleague the employment without its probation and
// the absence as "absent" — approved rows only, so the request ahead is not
// their next — without the case; Kari her own hours. A caller who sees
// nobody gets the 404, an unknown id too. Time is asked only for an audience
// that reads the hours.
func TestGlance_EachAudience(t *testing.T) {
	t.Parallel()
	w := newGlanceWorld(t)

	hr, hrBody := glanceOf(t, w.hr, w.kari.Id)
	if hr.Contract == nil || hr.Contract.Signed == nil || *hr.Contract.Signed {
		t.Errorf("HR's contract = %+v, want not signed", hr.Contract)
	}
	if c := hr.ChildSick; c == nil || c.Days != 1 || c.Entitlement == nil || *c.Entitlement != 10 || c.Remaining == nil || *c.Remaining != 9 {
		t.Errorf("HR's childSick = %+v, want 1 of 10, 9 left", c)
	}
	if hr.Hours == nil || hr.Hours.Logged == nil || *hr.Hours.Logged != 31.5 {
		t.Errorf("HR's hours = %+v", hr.Hours)
	}
	for _, k := range []string{"sickDays", "selfCertification", "holiday", "childSick", "employment", "absence", "contract", "hours"} {
		if _, ok := hrBody[k]; !ok {
			t.Errorf("HR's glance has no %s", k)
		}
	}

	w.h.Exec(t, `UPDATE staff.employments SET contract_signed_at = '2026-04-10' WHERE person_id = $1`, w.kari.Id)
	if signed, _ := glanceOf(t, w.hr, w.kari.Id); signed.Contract == nil || signed.Contract.Signed == nil || !*signed.Contract.Signed {
		t.Errorf("HR's contract after signing = %+v, want signed", signed.Contract)
	}

	pd, pdBody := glanceOf(t, w.pd, w.kari.Id)
	if _, ok := pdBody["contract"]; ok {
		t.Errorf("staff:personal-data reads the contract card, which is staff:manage's")
	}
	if pd.ChildSick == nil || pd.ChildSick.Entitlement == nil || pd.Hours == nil || pd.Hours.Logged == nil {
		t.Errorf("personal-data's glance = %+v, want the entitlement and the hours", pd)
	}

	approver, approverBody := glanceOf(t, w.approver, w.kari.Id)
	for _, k := range []string{"hours", "contract"} {
		if _, ok := approverBody[k]; ok {
			t.Errorf("the Approver reads %s", k)
		}
	}
	if approver.Employment == nil || approver.Employment.ProbationEndsOn == nil || approver.Absence == nil || approver.Absence.OpenCase == nil ||
		approver.ChildSick == nil || approver.ChildSick.Entitlement != nil {
		t.Errorf("the Approver's glance = %+v, want the probation, the case, and no entitlement", approver)
	}

	colleague, colleagueBody := glanceOf(t, w.viewer, w.kari.Id)
	if keys := sortedKeys(colleagueBody); !slices.Equal(keys, []string{"absence", "employment"}) {
		t.Errorf("the colleague's sections = %v, want absence and employment", keys)
	}
	if e := colleague.Employment; e == nil || e.StartDate == nil || e.TenureMonths == nil || e.FtePercent == nil || e.Title == nil || e.ProbationEndsOn != nil {
		t.Errorf("the colleague's employment = %+v, want the dates, the percentage and the title, no probation", e)
	}
	if a := colleague.Absence; a == nil || a.OpenCase != nil || a.Now == nil || a.Now.ShownAs != gen.StaffAbsenceShownAsAbsent || a.Now.TypeId != nil ||
		a.Now.FirstDay.Format(time.DateOnly) != "2026-09-09" || a.Next == nil || a.Next.FirstDay.Format(time.DateOnly) != "2026-10-06" ||
		a.Next.ShownAs != gen.StaffAbsenceShownAsAbsent || a.Next.TypeId != nil {
		t.Errorf("the colleague's absence = %+v, want now and the approved ferie next, as absent", a)
	}

	kari, _ := glanceOf(t, w.kariC, w.kari.Id)
	if kari.Hours == nil || kari.Hours.Logged == nil || *kari.Hours.Logged != 31.5 || kari.Contract != nil ||
		kari.ChildSick == nil || kari.ChildSick.Entitlement == nil {
		t.Errorf("Kari's own glance = %+v, want her hours and entitlement, no contract", kari)
	}

	before := len(w.times.asks())
	if r := w.nobody.Do(http.MethodGet, glancePath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a caller who sees nobody: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := w.hr.Do(http.MethodGet, glancePath(w.kari.Id+1000), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown person: status %d body %s, want 404", r.Status, r.Body)
	}
	glanceOf(t, w.approver, w.kari.Id)
	glanceOf(t, w.viewer, w.kari.Id)
	if n := len(w.times.asks()); n != before {
		t.Errorf("time was asked %d times for audiences that do not read the hours", n-before)
	}
}

// With time disabled the hours card says time tracking is off — the section
// {timeTracking: false} and nothing else — never zero hours.
func TestGlance_TimeTrackingOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	kari, _ := employed(t, hr, "Kari", "2026-01-01", terms())
	_, body := glanceOf(t, hr, kari.Id)
	hours, ok := body["hours"].(map[string]any)
	if !ok || len(hours) != 1 || hours["timeTracking"] != false {
		t.Errorf("hours = %v, want exactly {timeTracking: false}", body["hours"])
	}
}

// A person linked to no user has the week and the expected hours but no
// logged or approved figure, and time is not asked; a person with no
// employment in force has no expected hours, no start and no tenure, and
// HR's contract card says null — there is no employment to have signed.
func TestGlance_NoLinkNoEmployment(t *testing.T) {
	t.Parallel()
	times := newFakeTimesheets()
	h := newHarness(t, modtest.WithTimesheets(times))
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	ola, _ := employed(t, hr, "Ola", "2025-02-01", terms("fullTimeHoursPerWeek", 40, "ftePercent", 50))

	g, body := glanceOf(t, hr, ola.Id)
	hours := body["hours"].(map[string]any)
	if g.Hours == nil || !g.Hours.TimeTracking || g.Hours.Expected == nil || *g.Hours.Expected != 20 || g.Hours.WeekStart == nil {
		t.Errorf("Ola's hours = %+v, want time tracking on and 20 expected", g.Hours)
	}
	for _, k := range []string{"logged", "approved"} {
		if _, ok := hours[k]; ok {
			t.Errorf("Ola, linked to nobody, has %s", k)
		}
	}
	if len(times.asks()) != 0 {
		t.Errorf("time was asked %+v for a person linked to nobody", times.asks())
	}
	if g.Employment == nil || g.Employment.TenureMonths == nil || *g.Employment.TenureMonths != 19 {
		t.Errorf("Ola's employment = %+v, want 19 months", g.Employment)
	}

	nils := createPerson(t, hr, newPerson("Nils", "Nordmann"))
	g, body = glanceOf(t, hr, nils.Id)
	if e, _ := body["employment"].(map[string]any); e == nil || len(e) != 0 {
		t.Errorf("Nils's employment = %v, want an empty card", body["employment"])
	}
	if g.Hours == nil || !g.Hours.TimeTracking || g.Hours.Expected != nil || g.Hours.Logged != nil {
		t.Errorf("Nils's hours = %+v, want time tracking on, nothing expected", g.Hours)
	}
	contract, ok := body["contract"].(map[string]any)
	if signed, has := contract["signed"]; !ok || !has || signed != nil {
		t.Errorf("Nils's contract = %v, want {signed: null}", body["contract"])
	}
}

// A time module that fails to answer fails the glance — a 500, never a
// zero (module boundaries: a failing provider is an error); an audience
// that does not read the hours never asks it, and reads its glance.
func TestGlance_AFailingTimeModuleFailsIt(t *testing.T) {
	t.Parallel()
	w := newGlanceWorld(t)
	w.times.failWith(errors.New("time is down"))
	if r := w.bossC.Do(http.MethodGet, glancePath(w.kari.Id), nil, modtest.SkipContract("a fault the test injected")); r.Status != http.StatusInternalServerError {
		t.Errorf("the manager with time failing: status %d body %s, want 500", r.Status, r.Body)
	}
	glanceOf(t, w.approver, w.kari.Id)
}

// The audience is judged again in the glance's own snapshot, after time's
// hours were read: a manager who stops being one while time answers — and
// sees the person in no other way — gets the bare 404, not the cards the
// first judgement allowed.
func TestGlance_TheAudienceIsJudgedInBothSnapshots(t *testing.T) {
	t.Parallel()
	w := newGlanceWorld(t)
	w.times.whenAsked(func() {
		w.h.Exec(t, `UPDATE staff.employment_terms SET manager_person_id = NULL
			WHERE employment_id IN (SELECT id FROM staff.employments WHERE person_id = $1)`, w.kari.Id)
	})
	if r := w.bossC.Do(http.MethodGet, glancePath(w.kari.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("a manager no longer: status %d body %s, want the bare 404", r.Status, r.Body)
	}
	if n := len(w.times.asks()); n != 1 {
		t.Errorf("time was asked %d times, want once: the first judgement read the hours", n)
	}
}

// A colleague reads the absence card as GET /absences gives it to them:
// spans coalesced over the person's whole history, never where one row ends
// and the next begins. Ferie from 1 to 10 October and a sykmelding
// registered from the 5th to the 7th — the ferie split around it once the
// postponement is demanded — read to the colleague as one span, 1 to 10,
// with nothing next, on the 3rd and on the 5th; HR reads the rows as they
// are.
func TestGlance_AColleagueReadsCoalescedSpans(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-01", "2026-10-10"))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved' WHERE id = $1`, ferie.Id)
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-05", "2026-10-07"))

	span := func(s *gen.StaffGlanceAbsenceSpan) string {
		if s == nil {
			return "none"
		}
		return s.FirstDay.Format(time.DateOnly) + ".." + s.LastDay.Format(time.DateOnly)
	}
	check := func(when string, hr, viewer *modtest.Client, hrNow, hrNext string) {
		t.Helper()
		g, body := glanceOf(t, viewer, w.kari.Id)
		if a := g.Absence; a == nil || span(a.Now) != "2026-10-01..2026-10-10" || a.Next != nil ||
			a.Now.ShownAs != gen.StaffAbsenceShownAsAbsent || a.Now.TypeId != nil {
			t.Errorf("%s: the colleague's absence = now %s next %s, want now 2026-10-01..2026-10-10 as absent and nothing next", when, span(a.Now), span(a.Next))
		}
		if now, _ := body["absence"].(map[string]any)["now"].(map[string]any); len(now) != 3 {
			t.Errorf("%s: the colleague's now = %v, want the days and shownAs alone", when, now)
		}
		g, _ = glanceOf(t, hr, w.kari.Id)
		if a := g.Absence; a == nil || span(a.Now) != hrNow || span(a.Next) != hrNext {
			t.Errorf("%s: HR's absence = now %s next %s, want now %s next %s", when, span(a.Now), span(a.Next), hrNow, hrNext)
		}
	}

	// Three weeks on — a session lasts less, so the readers sign in again.
	h.Advance(21 * 24 * time.Hour)
	hr, _ := signIn(t, h, "staff:manage")
	viewer, _ := signIn(t, h, "staff:view")
	check("on the 3rd", hr, viewer, "2026-10-01..2026-10-10", "2026-10-05..2026-10-07")

	h.Advance(2 * 24 * time.Hour)
	hr, _ = signIn(t, h, "staff:manage")
	viewer, _ = signIn(t, h, "staff:view")
	do(t, hr, http.MethodPost, postponePath(sick.Id), map[string]any{"demandedOn": "2026-10-05"}, http.StatusOK, nil)
	check("on the 5th, the ferie split", hr, viewer, "2026-10-05..2026-10-07", "2026-10-08..2026-10-10")
}
