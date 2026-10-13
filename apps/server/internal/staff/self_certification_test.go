package staff_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The paths of egenmelding's standing and care days (delivery B, Task 5).
const (
	meSelfCertPath = "/api/v1/staff/me/self-certification"
	meCareDaysPath = "/api/v1/staff/me/care-days"
)

func selfCertPath(personID int64) string   { return personPath(personID) + "/self-certification" }
func withdrawalPath(personID int64) string { return selfCertPath(personID) + "/withdrawal" }
func careDaysPath(personID int64) string   { return personPath(personID) + "/care-days" }

// Egenmelding under the minimum policy (design §4 Egenmelding, ftrl.
// § 8-23, plan pre-flight I2, Revision 2 Q7a): three calendar days an
// occurrence, a run of contiguous days being one — 1+1+1+1 on four weekdays
// is refused on day four, and Friday then Monday counts the weekend, so
// Monday is day four — and never past the case's sixteenth day. Each refusal
// tells the person to register a sykmelding.
func TestSelfCertification_TheMinimumPolicy(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	for _, d := range []string{"2026-10-05", "2026-10-06", "2026-10-07"} {
		requestOwn(t, w.kariC, absence(t, h, "egenmelding", d, d))
	}
	p := refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "egenmelding", "2026-10-08", "2026-10-08")), http.StatusConflict, "self_certification_exhausted")
	if !strings.Contains(p.Detail, "sykmelding") {
		t.Errorf("detail %q does not say to register a sykmelding", p.Detail)
	}
	requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-10-16", "2026-10-16"))
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "egenmelding", "2026-10-19", "2026-10-19")), http.StatusConflict, "self_certification_exhausted")

	var standing gen.StaffSelfCertification
	do(t, w.kariC, http.MethodGet, meSelfCertPath, nil, http.StatusOK, &standing)
	if standing.Policy != gen.Minimum || standing.DaysPerOccurrence != 3 || standing.OccurrencesPerYear == nil || *standing.OccurrencesPerYear != 4 || standing.DaysPerYear != nil {
		t.Errorf("standing = %+v, want the minimum's 3 days and 4 occurrences", standing)
	}

	// Past the sixteenth day of a case: a sykmelding's period has ended.
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2027-01-04", "2027-01-24"))
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "egenmelding", "2027-01-25", "2027-01-25")), http.StatusConflict, "self_certification_exhausted")
}

// The limits key on the policy's name (Task 2 review): under extended, eight
// days at once whatever the stored counters say.
func TestSelfCertification_TheExtendedPolicy(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	h.Exec(t, `UPDATE staff.settings SET self_cert_policy = 'extended', self_cert_days_per_occurrence = 3`)
	requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-10-05", "2026-10-12"))
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "egenmelding", "2026-10-13", "2026-10-13")), http.StatusConflict, "self_certification_exhausted")
	var standing gen.StaffSelfCertification
	do(t, w.hr, http.MethodGet, selfCertPath(w.kari.Id), nil, http.StatusOK, &standing)
	if standing.DaysPerOccurrence != 8 || standing.DaysPerYear == nil || *standing.DaysPerYear != 24 {
		t.Errorf("standing = %+v, want extended's 8 and 24", standing)
	}
}

// Egenmelding before the months of employment the policy asks: too early,
// and the standing says from when.
func TestSelfCertification_TooEarly(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	nils, _ := employed(t, w.hr, "Nils", "2026-09-01", terms())
	refused(t, w.hr.Do(http.MethodPost, absencesPath, with(absence(t, h, "egenmelding", "2026-10-05", "2026-10-05"), "personId", nils.Id)),
		http.StatusConflict, "self_certification_too_early")
	register(t, w.hr, nils.Id, absence(t, h, "egenmelding", "2026-11-02", "2026-11-02"))
	var standing gen.StaffSelfCertification
	do(t, w.hr, http.MethodGet, selfCertPath(nils.Id), nil, http.StatusOK, &standing)
	if civilOf(standing.EligibleFrom) != "2026-11-01" {
		t.Errorf("eligibleFrom = %s, want 2026-11-01", civilOf(standing.EligibleFrom))
	}
}

// HR withdraws a person's egenmelding for a period (ftrl. § 8-27): refused
// self_certification_withdrawn while it lasts, restored by until null. The
// withdrawal, its standing and its events go to the readers of the care-day
// fields alone — the person, staff:manage, staff:personal-data — never the
// manager or the Approver (plan pre-flight I6).
func TestSelfCertification_TheWithdrawal(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	for _, body := range []map[string]any{
		{"until": "2026-09-01", "reason": "Repeated absence"},
		{"until": "2027-12-31", "reason": "Repeated absence"},
		{"until": "2026-12-31"},
		{"until": "2026-12-31", "reason": strings.Repeat("x", 501)},
	} {
		refused(t, w.hr.Do(http.MethodPut, withdrawalPath(w.kari.Id), body), http.StatusBadRequest, "")
	}
	if r := w.bossC.Do(http.MethodPut, withdrawalPath(w.kari.Id), map[string]any{"until": "2026-12-31", "reason": "x"}); r.Status != http.StatusForbidden {
		t.Errorf("the manager withdrawing: %d, want 403", r.Status)
	}
	var standing gen.StaffSelfCertification
	do(t, w.hr, http.MethodPut, withdrawalPath(w.kari.Id), map[string]any{"until": "2026-12-31", "reason": "Repeated absence"}, http.StatusOK, &standing)
	if civilOf(standing.WithdrawnUntil) != "2026-12-31" {
		t.Errorf("withdrawn until %s, want 2026-12-31", civilOf(standing.WithdrawnUntil))
	}
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "egenmelding", "2026-11-30", "2026-11-30")), http.StatusConflict, "self_certification_withdrawn")
	// The withdrawal applies from the day it was decided (review L1): an
	// egenmelding for days before it stands.
	if civilOf(standing.WithdrawnFrom) != today(t, h) {
		t.Errorf("withdrawn from %s, want today %s", civilOf(standing.WithdrawnFrom), today(t, h))
	}
	requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-09-01", "2026-09-01"))

	for name, c := range map[string]*modtest.Client{"Kari": w.kariC, "HR": w.hr, "personal-data": w.pd} {
		var got map[string]any
		path := selfCertPath(w.kari.Id)
		if c == w.kariC {
			path = meSelfCertPath
		}
		do(t, c, http.MethodGet, path, nil, http.StatusOK, &got)
		if got["withdrawnUntil"] != "2026-12-31" || got["withdrawnReason"] != "Repeated absence" {
			t.Errorf("%s reads %v, want the withdrawal", name, got)
		}
		if !slices.Contains(kindsOf(eventsAs(t, c, w.kari.Id)), "self_cert_withdrawn") {
			t.Errorf("%s's history lacks self_cert_withdrawn", name)
		}
	}
	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "the Approver": w.approver} {
		var got map[string]any
		do(t, c, http.MethodGet, selfCertPath(w.kari.Id), nil, http.StatusOK, &got)
		if _, ok := got["withdrawnUntil"]; ok {
			t.Errorf("%s reads the withdrawal: %v", name, got)
		}
		if _, ok := got["withdrawnReason"]; ok {
			t.Errorf("%s reads the withdrawal's reason: %v", name, got)
		}
	}
	if kinds := kindsOf(eventsAs(t, w.bossC, w.kari.Id)); slices.Contains(kinds, "self_cert_withdrawn") {
		t.Errorf("the manager's history lists the withdrawal: %v", kinds)
	}
	if r := w.viewer.Do(http.MethodGet, selfCertPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a colleague reading the standing: %d, want 404", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'self_cert_withdrawn' AND payload_classes = '{E}'`); n != 1 {
		t.Errorf("%d self_cert_withdrawn events of class E, want 1", n)
	}

	// Re-issuing a withdrawal in force keeps the day it was decided
	// (re-check 2): extending it does not open the days between. The day is
	// moved back by hand, as a withdrawal decided earlier would stand.
	h.Exec(t, `UPDATE staff.people SET self_cert_withdrawn_from = '2026-09-02' WHERE id = $1`, w.kari.Id)
	standing = gen.StaffSelfCertification{}
	do(t, w.hr, http.MethodPut, withdrawalPath(w.kari.Id), map[string]any{"until": "2027-01-31", "reason": "Extended"}, http.StatusOK, &standing)
	if civilOf(standing.WithdrawnFrom) != "2026-09-02" || civilOf(standing.WithdrawnUntil) != "2027-01-31" {
		t.Errorf("re-issued: from %s until %s, want from 2026-09-02 until 2027-01-31", civilOf(standing.WithdrawnFrom), civilOf(standing.WithdrawnUntil))
	}
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "egenmelding", "2026-09-07", "2026-09-07")), http.StatusConflict, "self_certification_withdrawn")
	standing = gen.StaffSelfCertification{}
	do(t, w.hr, http.MethodPut, withdrawalPath(w.kari.Id), map[string]any{"until": nil}, http.StatusOK, &standing)
	if standing.WithdrawnUntil != nil {
		t.Errorf("restored, yet withdrawn until %s", civilOf(standing.WithdrawnUntil))
	}
	requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-11-30", "2026-11-30"))
	if !slices.Contains(kindsOf(eventsAs(t, w.kariC, w.kari.Id)), "self_cert_restored") {
		t.Error("Kari's history lacks self_cert_restored")
	}
	refused(t, w.nobody.Do(http.MethodGet, meSelfCertPath, nil), http.StatusNotFound, "no_person")
}

// Care days (S11, ftrl. § 9-6 and § 9-8): the entitlement from the three
// fields — two children under twelve and one chronically ill child are three,
// 15 days plus 10 — the year's child-sick working days against it, the ten
// the employer bears and those beyond; read by the person, staff:manage and
// staff:personal-data. A person employed under four weeks has no right yet.
func TestCareDays(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	h.Exec(t, `UPDATE staff.people SET children_under_12 = 2, chronically_ill_children = 1, sole_carer = false WHERE id = $1`, w.kari.Id)
	requestOwn(t, w.kariC, absence(t, h, "sykt_barn", "2026-10-05", "2026-10-09"))
	requestOwn(t, w.kariC, absence(t, h, "sykt_barn", "2026-11-02", "2026-11-13"))
	requestOwn(t, w.kariC, absence(t, h, "sykt_barn", "2027-01-04", "2027-01-04"))

	var got gen.StaffCareDays
	do(t, w.kariC, http.MethodGet, meCareDaysPath, nil, http.StatusOK, &got)
	want := gen.StaffCareDays{PersonId: w.kari.Id, Year: 2026, Recorded: true, ChildrenCounted: 3, Entitlement: 25, Used: 15, IneligibleDays: 0, Remaining: 10,
		EmployerPaid: 10, BeyondEmployer: 5, Eligible: true}
	got.EligibleFrom = nil
	if got != want {
		t.Errorf("care days = %+v, want %+v", got, want)
	}
	do(t, w.hr, http.MethodGet, careDaysPath(w.kari.Id)+"?year=2027", nil, http.StatusOK, &got)
	if got.Used != 1 || got.Year != 2027 {
		t.Errorf("2027 = %+v, want one day used", got)
	}
	do(t, w.pd, http.MethodGet, careDaysPath(w.kari.Id), nil, http.StatusOK, nil)
	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "the Approver": w.approver, "a colleague": w.viewer} {
		if r := c.Do(http.MethodGet, careDaysPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s reading care days: %d, want 404", name, r.Status)
		}
	}
	refused(t, w.hr.Do(http.MethodGet, careDaysPath(w.kari.Id)+"?year=1999", nil), http.StatusBadRequest, "")

	nils, _ := employed(t, w.hr, "Nils", "2026-09-01", terms())
	do(t, w.hr, http.MethodGet, careDaysPath(nils.Id), nil, http.StatusOK, &got)
	if got.Eligible || civilOf(got.EligibleFrom) != "2026-09-29" || got.Recorded || got.Entitlement != 0 {
		t.Errorf("a hire of 1 September = %+v, want not eligible before 2026-09-29, nothing recorded", got)
	}
	// The right is judged per day used (review L10): a sick child's day in
	// the first four weeks is no care day; an employment that began the day
	// after another ended continues it.
	register(t, w.hr, nils.Id, absence(t, h, "sykt_barn", "2026-09-14", "2026-09-14"))
	register(t, w.hr, nils.Id, absence(t, h, "sykt_barn", "2026-10-05", "2026-10-05"))
	do(t, w.hr, http.MethodGet, careDaysPath(nils.Id), nil, http.StatusOK, &got)
	if got.Used != 1 || got.IneligibleDays != 1 {
		t.Errorf("a hire of 1 September = %+v, want one day used and one before the right", got)
	}
	h.Exec(t, `INSERT INTO staff.employments (person_id, employment_number, relationship_type, business_unit_id, start_date, end_date, end_reason, created_at, updated_at)
		SELECT person_id, employment_number || '-0', relationship_type, business_unit_id, '2026-01-01', '2026-08-31', 'contract_ended', now(), now()
		FROM staff.employments WHERE person_id = $1`, nils.Id)
	got = gen.StaffCareDays{}
	do(t, w.hr, http.MethodGet, careDaysPath(nils.Id), nil, http.StatusOK, &got)
	if !got.Eligible || civilOf(got.EligibleFrom) != "2026-01-29" || got.Used != 2 || got.IneligibleDays != 0 {
		t.Errorf("after an unbroken earlier employment = %+v, want eligible from 2026-01-29, both days used", got)
	}
	refused(t, w.nobody.Do(http.MethodGet, meCareDaysPath, nil), http.StatusNotFound, "no_person")
}
