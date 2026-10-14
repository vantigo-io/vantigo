package staff_test

import (
	"math"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// What absence adds to the dashboard (design S20; plan Task 6, reading 14):
// the figures on GET /stats and the absence items on GET /stats/attention,
// each per audience. The harness's today is Saturday 12 September 2026.

// approveRow marks absence id approved in the table.
func approveRow(t *testing.T, h *harness, id int64) {
	t.Helper()
	h.Exec(t, `UPDATE staff.absences SET status = 'approved', decided_at = $2 WHERE id = $1`, id, h.Now())
}

// onlyItem is the one item of kind in items; the test fails unless there is
// exactly one.
func onlyItem(t *testing.T, items []gen.StaffStatsAttentionItem, kind gen.StaffStatsAttentionItemType) gen.StaffStatsAttentionItem {
	t.Helper()
	got := ofType(items, kind)
	if len(got) != 1 {
		t.Fatalf("%s items = %+v, want one", kind, got)
	}
	return got[0]
}

// countOf is an item's count, 0 when it has none.
func countOf(i gen.StaffStatsAttentionItem) int32 {
	if i.Count == nil {
		return 0
	}
	return *i.Count
}

// onLeaveToday counts the people employed today with an approved or
// registered day row covering today, of any kind — a sykmelding as well as
// ferie, so the figure says no more than the calendar shows a colleague — and
// never a request. The sick figures — the open cases and the sick-leave rate
// over the twelve months to today, by degree and full-time percentage — go
// to staff:manage, staff:approve and staff:personal-data alone; a holder of
// staff:view alone gets neither.
func TestStats_TheAbsenceFigures(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	holiday := register(t, w.hr, w.kari.Id, absence(t, h, "ferie", "2026-09-10", "2026-09-16"))
	approveRow(t, h, holiday.Id)
	register(t, w.hr, w.boss.Id, absence(t, h, "sykmelding", "2026-09-11", "2026-09-14"))
	other, _ := employed(t, w.hr, "Annen", "2020-01-01", terms())
	register(t, w.hr, other.Id, absence(t, h, "velferdspermisjon", "2026-09-11", "2026-09-14")) // requested
	hrPD, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	register(t, hrPD, other.Id, absence(t, h, "svangerskapspermisjon", "2026-09-15", "2026-09-30"))

	from := time.Date(2025, time.September, 13, 0, 0, 0, 0, time.UTC)
	agreed := 0
	for d := from; !d.After(time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		if calendar.BusinessDay(d) {
			agreed++
		}
	}
	// Bjørn's one working day sick (Friday 11 September) over the three
	// people's agreed working days, all full-time.
	rate := math.Round(10000/float64(3*agreed)) / 100

	viewer := getStats(t, w.viewer)
	if viewer.OnLeaveToday != 2 {
		t.Errorf("onLeaveToday = %d, want 2: Kari's ferie and Bjørn's sykmelding, no request", viewer.OnLeaveToday)
	}
	if viewer.OpenSickCases != nil || viewer.SickAbsenceRate != nil {
		t.Errorf("staff:view reads the sick figures: %v %v", viewer.OpenSickCases, viewer.SickAbsenceRate)
	}
	approver, _ := signIn(t, h, "staff:view", "staff:approve")
	pdView, _ := signIn(t, h, "staff:view", "staff:personal-data")
	for name, c := range map[string]*modtest.Client{"staff:manage": w.hr, "staff:approve": approver, "staff:personal-data": pdView} {
		s := getStats(t, c)
		if s.OpenSickCases == nil || *s.OpenSickCases != 1 {
			t.Errorf("%s: openSickCases = %v, want 1", name, s.OpenSickCases)
		}
		if s.SickAbsenceRate == nil || *s.SickAbsenceRate != rate {
			t.Errorf("%s: sickAbsenceRate = %v, want %v", name, s.SickAbsenceRate, rate)
		}
		if s.OnLeaveToday != 2 {
			t.Errorf("%s: onLeaveToday = %d, want 2", name, s.OnLeaveToday)
		}
	}
}

// absenceAwaitingDecision is a decider's count of the requests they may
// decide, never their own: an HR-only request counts for
// staff:personal-data alone, so neither the manager nor HR without it can
// tell one is waiting. hrOnlyAwaitingDecision is staff:personal-data's: the
// HR-only requests waiting more than seven days, their own included — the
// only holder's own request, which nobody may decide, does not stall
// unseen (open question 9).
func TestAttention_AbsenceAwaitingDecision(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-30"))
	pdPerson, _ := employed(t, w.hr, "Personal", "2020-01-01", terms())
	pdC, pdUser := signIn(t, h, "staff:personal-data")
	link(t, h, pdPerson.Id, pdUser)
	requestOwn(t, pdC, absence(t, h, "religios_hoytid", "2026-12-01", "2026-12-01"))
	approver, _ := signIn(t, h, "staff:approve")

	for name, c := range map[string]struct {
		client *modtest.Client
		want   int32
	}{
		"the manager":         {w.bossC, 1},
		"staff:manage":        {w.hr, 1},
		"staff:approve":       {approver, 1},
		"staff:personal-data": {w.pd, 2},
		"the PD holder":       {pdC, 1},
	} {
		item := onlyItem(t, getAttention(t, c.client), gen.AbsenceAwaitingDecision)
		if countOf(item) != c.want || item.EntityId != "approvals" {
			t.Errorf("%s: absenceAwaitingDecision = %+v, want count %d on approvals", name, item, c.want)
		}
	}
	for name, c := range map[string]*modtest.Client{"Kari": w.kariC, "staff:view": w.viewer, "nobody": w.nobody} {
		if got := ofType(getAttention(t, c), gen.AbsenceAwaitingDecision); len(got) != 0 {
			t.Errorf("%s: absenceAwaitingDecision = %+v, want none", name, got)
		}
	}
	if got := ofType(getAttention(t, w.pd), gen.HrOnlyAwaitingDecision); len(got) != 0 {
		t.Errorf("hrOnlyAwaitingDecision before seven days: %+v", got)
	}

	// Eight days on — a session lasts less, so everyone signs in again.
	h.Advance(8 * 24 * time.Hour)
	pdAgain, _ := signIn(t, h, "staff:personal-data")
	pdC, pdUser = signIn(t, h, "staff:personal-data")
	h.Exec(t, `UPDATE staff.identity_links SET external_id = $2 WHERE person_id = $1`, pdPerson.Id, pdUser.String())
	hrAgain, _ := signIn(t, h, "staff:manage")
	approver, _ = signIn(t, h, "staff:approve")
	bossAgain, bossUser := signIn(t, h)
	h.Exec(t, `UPDATE staff.identity_links SET external_id = $2 WHERE person_id = $1`, w.boss.Id, bossUser.String())
	for name, c := range map[string]*modtest.Client{"staff:personal-data": pdAgain, "the PD holder": pdC} {
		if item := onlyItem(t, getAttention(t, c), gen.HrOnlyAwaitingDecision); countOf(item) != 2 {
			t.Errorf("%s: hrOnlyAwaitingDecision = %+v, want 2", name, item)
		}
	}
	for name, c := range map[string]*modtest.Client{"staff:manage": hrAgain, "the manager": bossAgain, "staff:approve": approver} {
		if got := ofType(getAttention(t, c), gen.HrOnlyAwaitingDecision); len(got) != 0 {
			t.Errorf("%s: hrOnlyAwaitingDecision = %+v, want none", name, got)
		}
	}
}

// sickMilestoneDue is a milestone due by Task 5's rule — the case open and
// lasted to within a week of it, neither done nor waived — whose date is
// within seven days or past, for the person's manager, staff:approve and
// staff:manage; never the end of the arbeidsgiverperiode, never a 3-day
// case's, never a colleague's or staff:personal-data alone's.
func TestAttention_SickMilestoneDue(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-10", "2026-09-30"))
	register(t, w.hr, w.boss.Id, absence(t, h, "sykmelding", "2026-09-08", "2026-09-10"))
	approver, _ := signIn(t, h, "staff:approve")
	kariCase := modtest.One[int64](t, h.Harness, `SELECT id FROM staff.sick_cases WHERE person_id = $1`, w.kari.Id)

	for name, c := range map[string]*modtest.Client{"the manager": w.bossC, "staff:manage": w.hr, "staff:approve": approver} {
		item := onlyItem(t, getAttention(t, c), gen.SickMilestoneDue)
		if item.EntityId != idString(w.kari.Id) || item.Id != "sickMilestoneDue/"+idString(kariCase)+"/follow_up_plan" ||
			item.OccurredAt.Format(time.DateOnly) != "2026-09-07" {
			t.Errorf("%s: sickMilestoneDue = %+v, want Kari's follow-up plan of 7 September", name, item)
		}
	}
	for name, c := range map[string]*modtest.Client{"Kari": w.kariC, "staff:view": w.viewer, "staff:personal-data": w.pd} {
		if got := ofType(getAttention(t, c), gen.SickMilestoneDue); len(got) != 0 {
			t.Errorf("%s: sickMilestoneDue = %+v, want none", name, got)
		}
	}
	h.Exec(t, `UPDATE staff.sick_case_milestones SET done_at = $2 WHERE case_id = $1 AND kind = 'follow_up_plan'`, kariCase, h.Now())
	if got := ofType(getAttention(t, w.hr), gen.SickMilestoneDue); len(got) != 0 {
		t.Errorf("a done milestone: %+v", got)
	}
}

// holidayUntaken is staff:manage's in January: a person employed today with
// last year's holiday remaining and no carry-over from it recorded. A
// carry-over recorded, or a carry-in derived by automatic carry-over,
// counts as recorded; in February there is none.
func TestAttention_HolidayUntaken(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	setClock(t, h, time.Date(2027, time.January, 5, 12, 0, 0, 0, time.UTC))
	w.hr, _ = signIn(t, h, "staff:manage")
	var bossUser uuid.UUID
	w.bossC, bossUser = signIn(t, h)
	h.Exec(t, `UPDATE staff.identity_links SET external_id = $2 WHERE person_id = $1`, w.boss.Id, bossUser.String())
	do(t, w.hr, http.MethodPost, carryOverPath(w.boss.Id), map[string]any{"fromYear": 2026, "kind": "agreed"},
		http.StatusCreated, nil)
	items := ofType(getAttention(t, w.hr), gen.HolidayUntaken)
	if got := entities(items); len(items) != 1 || items[0].EntityId != idString(w.kari.Id) {
		t.Errorf("holidayUntaken = %v, want Kari's alone", got)
	} else if items[0].OccurredAt.Format(time.DateOnly) != "2027-01-01" {
		t.Errorf("holidayUntaken occurredAt = %s, want 1 January", items[0].OccurredAt)
	}
	if got := ofType(getAttention(t, w.bossC), gen.HolidayUntaken); len(got) != 0 {
		t.Errorf("a manager reads holidayUntaken: %+v", got)
	}
	h.Exec(t, `UPDATE staff.settings SET auto_carry_over = true, auto_carry_over_since = '2026-12-01'`)
	if got := ofType(getAttention(t, w.hr), gen.HolidayUntaken); len(got) != 0 {
		t.Errorf("an auto-carried person raises holidayUntaken: %+v", got)
	}
	h.Exec(t, `UPDATE staff.settings SET auto_carry_over = false, auto_carry_over_since = NULL`)
	setClock(t, h, time.Date(2027, time.February, 1, 12, 0, 0, 0, time.UTC))
	w.hr, _ = signIn(t, h, "staff:manage")
	if got := ofType(getAttention(t, w.hr), gen.HolidayUntaken); len(got) != 0 {
		t.Errorf("holidayUntaken in February: %+v", got)
	}
}

// mainPeriodShort is staff:manage's from 1 April to 30 September (reading
// 23): a person employed today since before 1 June with fewer approved
// holiday working days in 1 June – 30 September than their scheme's main
// period converted (18 virkedager: 15 days). A requested ferie counts
// nothing; in October there is none.
func TestAttention_MainPeriodShort(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	summer := register(t, w.hr, w.boss.Id, absence(t, h, "ferie", "2026-07-06", "2026-07-24"))
	approveRow(t, h, summer.Id)
	register(t, w.hr, w.kari.Id, absence(t, h, "ferie", "2026-07-06", "2026-07-24")) // requested
	employed(t, w.hr, "Ny", "2026-08-01", terms())

	items := ofType(getAttention(t, w.hr), gen.MainPeriodShort)
	if got := entities(items); len(items) != 1 || items[0].EntityId != idString(w.kari.Id) {
		t.Errorf("mainPeriodShort = %v, want Kari's alone", got)
	} else if items[0].OccurredAt.Format(time.DateOnly) != "2026-04-01" {
		t.Errorf("mainPeriodShort occurredAt = %s, want 1 April", items[0].OccurredAt)
	}
	if got := ofType(getAttention(t, w.bossC), gen.MainPeriodShort); len(got) != 0 {
		t.Errorf("a manager reads mainPeriodShort: %+v", got)
	}
	setClock(t, h, time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC))
	w.hr, _ = signIn(t, h, "staff:manage")
	if got := ofType(getAttention(t, w.hr), gen.MainPeriodShort); len(got) != 0 {
		t.Errorf("mainPeriodShort in October: %+v", got)
	}
}

// selfApproved names ordinary types only: a row of an HR-only type marked
// self-approved — which the deciders never write — raises no item, since
// staff:manage without staff:personal-data must never be told of one.
func TestAttention_SelfApprovedNamesNoHROnlyType(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	plain := register(t, w.hr, w.kari.Id, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	hrPD, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	hrOnly := register(t, hrPD, w.kari.Id, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-30"))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved', self_approved = true, decided_at = $2 WHERE id = ANY($1)`,
		[]int64{plain.Id, hrOnly.Id}, h.Now())
	items := ofType(getAttention(t, w.hr), gen.SelfApproved)
	if len(items) != 1 || items[0].Id != "selfApproved/"+idString(plain.Id) {
		t.Errorf("selfApproved = %+v, want the ferie alone", items)
	}
}

// mainPeriodShort reads the scheme the holiday account reads — the earliest
// employment's in the year (whole-branch review), not the first one in
// force today: Kari's 2018 employment, ended on 31 March, follows a scheme
// with a 24-virkedager main period (20 days), so sixteen days taken in the
// summer are short though her 2020 employment's scheme asks fifteen.
func TestAttention_MainPeriodShortReadsTheEarliestScheme(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	long := addScheme(t, w.hr, "Long summer", 30, "mainPeriodVirkedager", 24)
	early := createEmployment(t, w.hr, w.kari.Id, newEmployment("2018-01-01", terms(), "holidaySchemeId", long.Id))
	do(t, w.hr, http.MethodPost, endPath(early.Id), map[string]any{"endDate": "2026-03-31", "endReason": "employee_resigned"}, http.StatusOK, nil)
	summer := register(t, w.hr, w.kari.Id, absence(t, h, "ferie", "2026-07-06", "2026-07-27"))
	approveRow(t, h, summer.Id)
	items := ofType(getAttention(t, w.hr), gen.MainPeriodShort)
	if !slices.ContainsFunc(items, func(i gen.StaffStatsAttentionItem) bool { return i.EntityId == idString(w.kari.Id) }) {
		t.Errorf("mainPeriodShort = %v, want Kari's: 16 of the earliest scheme's 20 days", entities(items))
	}
}
