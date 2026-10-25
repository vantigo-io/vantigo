package staff_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The competence attention items (staff competence design §4's table; plan
// Task 3b): computed on read, whatever the worker stamped. A declared row is
// staff:manage's, staff:approve's and the person's managers'; an expiring or
// expired one staff:manage's and the managers'. Only active, unerased
// people's rows raise items, and a caller never sees their own.

// competenceItemIDs is the competence items' ids among items, sorted.
func competenceItemIDs(items []gen.StaffStatsAttentionItem) []string {
	var out []string
	for _, it := range items {
		if strings.HasPrefix(string(it.Type), "competence") {
			out = append(out, it.Id)
		}
	}
	slices.Sort(out)
	return out
}

// itemID is the id of the item of kind about row.
func itemID(kind gen.StaffStatsAttentionItemType, row int64) string {
	return string(kind) + "/" + idText(row)
}

func sortedItemIDs(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

func TestCompetenceAttention_PerAudience(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	fse, hms, truck := catalogueID(t, h, "fse"), catalogueID(t, h, "hms-kort"), catalogueID(t, h, "truck")
	skill := companySkill(t, h, "sveising", "Sveising")
	two := int16(2)

	// Kari, Bjørn's report: a declaration (FSE, its end beyond its lead of
	// 30 days), HMS-kort within its lead of 60 days, a truck certificate
	// ended on 1 September.
	kariDeclared := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: fse, from: "2026-09-01", to: strPtr("2027-09-01")})
	kariExpiring := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: hms, from: "2024-10-13", to: strPtr("2026-10-12"), verified: true})
	kariExpired := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: truck, from: "2021-09-02", to: strPtr("2026-09-01"), verified: true})
	// Bjørn's own declaration.
	bossDeclared := rawPersonCompetence(t, h, competenceRow{person: w.boss, competence: hms, from: "2026-09-01"})
	// Ola has no manager: a declared skill and an ended FSE.
	ola := seedPerson(t, h, "Ola", "Nordmann")
	seedEmployment(t, h, ola, 0)
	olaDeclared := rawPersonCompetence(t, h, competenceRow{person: ola, competence: skill, level: &two, from: "2026-09-01"})
	olaExpired := rawPersonCompetence(t, h, competenceRow{person: ola, competence: fse, from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	// A leaver, an erased person and a hire not yet started raise nothing.
	leaver := seedPerson(t, h, "Per", "Sluttet")
	seedEmploymentDated(t, h, leaver, w.boss, "2024-01-01", dayPtr("2026-08-31"))
	rawPersonCompetence(t, h, competenceRow{person: leaver, competence: hms, from: "2026-01-01"})
	rawPersonCompetence(t, h, competenceRow{person: leaver, competence: fse, from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	erased := seedPerson(t, h, "Eva", "Glemt")
	seedEmployment(t, h, erased, w.boss)
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, erased, h.Now())
	rawPersonCompetence(t, h, competenceRow{person: erased, competence: hms, from: "2026-01-01"})
	hire := seedPerson(t, h, "Nina", "Ny")
	seedEmploymentDated(t, h, hire, w.boss, "2026-10-01", nil)
	rawPersonCompetence(t, h, competenceRow{person: hire, competence: hms, from: "2026-01-01"})

	hr, _ := signIn(t, h, "staff:manage")
	approver, _ := signIn(t, h, "staff:approve")
	olaApprover, olaUser := signIn(t, h, "staff:approve")
	link(t, h, ola, olaUser)
	kariC, kariUser := signIn(t, h)
	link(t, h, w.kari, kariUser)
	viewer, _ := signIn(t, h, "staff:view")

	kariItems := []string{
		itemID(gen.CompetenceDeclared, kariDeclared), itemID(gen.CompetenceExpiring, kariExpiring), itemID(gen.CompetenceExpired, kariExpired),
	}
	for _, tc := range []struct {
		who    string
		caller *modtest.Client
		want   []string
	}{
		{"staff:manage", hr, sortedItemIDs(append(slices.Clone(kariItems), itemID(gen.CompetenceDeclared, bossDeclared),
			itemID(gen.CompetenceDeclared, olaDeclared), itemID(gen.CompetenceExpired, olaExpired))...)},
		{"staff:approve", approver, sortedItemIDs(itemID(gen.CompetenceDeclared, kariDeclared), itemID(gen.CompetenceDeclared, bossDeclared),
			itemID(gen.CompetenceDeclared, olaDeclared))},
		{"staff:approve, never their own", olaApprover, sortedItemIDs(itemID(gen.CompetenceDeclared, kariDeclared), itemID(gen.CompetenceDeclared, bossDeclared))},
		{"the manager, their report's only", w.bossC, sortedItemIDs(kariItems...)},
		{"the person", kariC, nil},
		{"staff:view", viewer, nil},
	} {
		if got := competenceItemIDs(attentionItems(t, tc.caller)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: items = %v, want %v", tc.who, got, tc.want)
		}
	}

	byID := map[string]gen.StaffStatsAttentionItem{}
	for _, it := range attentionItems(t, hr) {
		byID[it.Id] = it
	}
	declared, expiring := byID[itemID(gen.CompetenceDeclared, kariDeclared)], byID[itemID(gen.CompetenceExpiring, kariExpiring)]
	if declared.Title != "Kari Nordmann" || declared.EntityId != idText(w.kari) || !declared.OccurredAt.Equal(h.Now()) {
		t.Errorf("the declaration's item = %+v", declared)
	}
	if expiring.Title != "Kari Nordmann" || expiring.EntityId != idText(w.kari) ||
		!expiring.OccurredAt.Equal(time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the expiring item = %+v", expiring)
	}

	// The items do not wait for the worker, nor go when it has stamped.
	runCompetence(t, h)
	if got := competenceItemIDs(attentionItems(t, w.bossC)); !slices.Equal(got, sortedItemIDs(kariItems...)) {
		t.Errorf("after the worker's cycle the manager's items = %v", got)
	}
}

// reviewOverdueIDs is the overdue review items' ids among items, sorted.
func reviewOverdueIDs(items []gen.StaffStatsAttentionItem) []string {
	var out []string
	for _, it := range items {
		if it.Type == gen.CompetenceReviewOverdue {
			out = append(out, it.Id)
		}
	}
	slices.Sort(out)
	return out
}

// seedReview records a competence review of personID on day directly.
func seedReview(t *testing.T, h *harness, personID int64, day string, reviewer uuid.UUID) {
	t.Helper()
	h.Exec(t, `INSERT INTO staff.competence_reviews (person_id, reviewed_on, reviewer_user_id, created_at)
		VALUES ($1, $2::date, $3, $4)`, personID, day, reviewer, h.Now())
}

// The overdue review (staff competence design S11, reading 8): a person in
// employment today whose latest review, or the start of their current
// unbroken employment run when that is later, lies more than the interval
// ago — to their manager and to staff:manage, never to staff:approve, never
// to the person; dated the day it fell due, keyed by the person, titled with
// their name. An interval of 0 raises none. Today is 12 September 2026, the
// interval the default 12 months.
func TestCompetenceAttention_ReviewOverdue(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	// Bjørn's reports. Kari, employed since January, is not due yet.
	// Never reviewed, employed since 2024: due 1 January 2025.
	never := seedPerson(t, h, "Nils", "Aldri")
	seedEmploymentDated(t, h, never, w.boss, "2024-01-01", nil)
	// Reviewed eleven months ago: due 12 October 2026, not yet.
	recent := seedPerson(t, h, "Rita", "Nylig")
	seedEmploymentDated(t, h, recent, w.boss, "2024-01-01", nil)
	seedReview(t, h, recent, "2024-06-01", w.bossUser)
	seedReview(t, h, recent, "2025-10-12", w.bossUser)
	// Reviewed thirteen months ago: due 12 August 2026.
	stale := seedPerson(t, h, "Siri", "Sist")
	seedEmploymentDated(t, h, stale, w.boss, "2024-01-01", nil)
	seedReview(t, h, stale, "2025-08-12", w.bossUser)
	// Hired two months ago: a new hire is not overdue on day one.
	hire := seedPerson(t, h, "Nora", "Ny")
	seedEmploymentDated(t, h, hire, w.boss, "2026-07-12", nil)
	// Employed until 2024, then again from June 2025 after a gap: counted
	// from the restart, due 1 June 2026 — a review before the gap counts
	// for nothing.
	back := seedPerson(t, h, "Bård", "Tilbake")
	seedEmploymentDated(t, h, back, w.boss, "2020-01-01", dayPtr("2024-12-31"))
	seedEmploymentDated(t, h, back, w.boss, "2025-06-01", nil)
	seedReview(t, h, back, "2024-06-01", w.bossUser)
	// Ola has no manager and is never reviewed: staff:manage's alone.
	ola := seedPerson(t, h, "Ola", "Nordmann")
	seedEmploymentDated(t, h, ola, 0, "2024-01-01", nil)
	// A leaver and an erased person raise nothing.
	leaver := seedPerson(t, h, "Per", "Sluttet")
	seedEmploymentDated(t, h, leaver, 0, "2020-01-01", dayPtr("2026-08-31"))
	erased := seedPerson(t, h, "Eva", "Glemt")
	seedEmploymentDated(t, h, erased, 0, "2024-01-01", nil)
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, erased, h.Now())

	hr, _ := signIn(t, h, "staff:manage")
	olaHR, olaUser := signIn(t, h, "staff:manage")
	link(t, h, ola, olaUser)
	approver, _ := signIn(t, h, "staff:approve")
	neverC, neverUser := signIn(t, h)
	link(t, h, never, neverUser)

	overdue := func(id int64) string { return itemID(gen.CompetenceReviewOverdue, id) }
	reports := sortedItemIDs(overdue(never), overdue(stale), overdue(back))
	for _, tc := range []struct {
		who    string
		caller *modtest.Client
		want   []string
	}{
		{"staff:manage", hr, sortedItemIDs(append(slices.Clone(reports), overdue(ola))...)},
		{"staff:manage, never their own", olaHR, reports},
		{"the manager, their reports'", w.bossC, reports},
		{"staff:approve", approver, nil},
		{"the person", neverC, nil},
	} {
		if got := reviewOverdueIDs(attentionItems(t, tc.caller)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: items = %v, want %v", tc.who, got, tc.want)
		}
	}

	byID := map[string]gen.StaffStatsAttentionItem{}
	for _, it := range attentionItems(t, w.bossC) {
		byID[it.Id] = it
	}
	day := func(m time.Month, d, y int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	for _, c := range []struct {
		person int64
		title  string
		due    time.Time
	}{
		{never, "Nils Aldri", day(time.January, 1, 2025)},
		{stale, "Siri Sist", day(time.August, 12, 2026)},
		{back, "Bård Tilbake", day(time.June, 1, 2026)},
	} {
		it := byID[overdue(c.person)]
		if it.Title != c.title || it.EntityId != idText(c.person) || !it.OccurredAt.Equal(c.due) {
			t.Errorf("the overdue item of %s = %+v, want occurredAt %s", c.title, it, c.due.Format(time.DateOnly))
		}
	}

	// A review recorded today clears it; an interval of 0 raises nothing.
	seedReview(t, h, never, "2026-09-12", w.bossUser)
	if got := reviewOverdueIDs(attentionItems(t, w.bossC)); !slices.Equal(got, sortedItemIDs(overdue(stale), overdue(back))) {
		t.Errorf("after Nils's review the manager's items = %v", got)
	}
	h.Exec(t, `UPDATE staff.settings SET competence_review_interval_months = 0`)
	for _, c := range []*modtest.Client{hr, w.bossC} {
		if got := reviewOverdueIDs(attentionItems(t, c)); got != nil {
			t.Errorf("interval 0: items = %v, want none", got)
		}
	}
}

// gapItems is the competenceGap items among items, by id.
func gapItems(items []gen.StaffStatsAttentionItem) map[string]gen.StaffStatsAttentionItem {
	out := map[string]gen.StaffStatsAttentionItem{}
	for _, it := range items {
		if it.Type == gen.CompetenceGap {
			out[it.Id] = it
		}
	}
	return out
}

// The competenceGap item (staff competence design §4; plan reading 31): one
// per person whose unarchived profiles — assigned, or their department's
// default — have items not met, counted, dated today, titled with the
// person's name; to their manager and to staff:manage, never to the
// Approver, never to the person; gone when the profiles are met or
// archived.
func TestCompetenceAttention_Gap(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	fse := catalogueID(t, h, "fse")
	golang := companySkill(t, h, "golang", "Go")
	two := int16(2)

	electrician := rawProfile(t, h, "electrician", false,
		gapItemFor{competence: fse, mustBeValid: true}, gapItemFor{competence: golang, minLevel: int16Ptr(3), mustBeValid: true})
	rawAssign(t, h, w.kari, electrician)
	golangRow := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: golang, level: &two, from: "2026-01-01"})
	// Bjørn's own profile is not met either; he is never told of his own.
	rawAssign(t, h, w.boss, electrician)

	hr, _ := signIn(t, h, "staff:manage")
	approver, _ := signIn(t, h, "staff:approve")
	kariC, kariUser := signIn(t, h)
	link(t, h, w.kari, kariUser)

	kariItem := itemID(gen.CompetenceGap, w.kari)
	got := gapItems(attentionItems(t, w.bossC))
	if len(got) != 1 {
		t.Fatalf("the manager's gap items = %+v, want Kari's alone", got)
	}
	it := got[kariItem]
	if it.Count == nil || *it.Count != 2 || it.Title != "Kari Nordmann" || it.EntityId != idText(w.kari) ||
		it.OccurredAt.Format(time.DateOnly) != today(t, h) || !it.OccurredAt.Equal(time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Kari's item = %+v, want count 2, her name, today", it)
	}
	if got := gapItems(attentionItems(t, hr)); len(got) != 2 || got[kariItem].Count == nil || *got[kariItem].Count != 2 {
		t.Errorf("HR's gap items = %+v, want Kari's and Bjørn's", got)
	}
	for name, c := range map[string]*modtest.Client{"the Approver": approver, "Kari": kariC} {
		if got := gapItems(attentionItems(t, c)); len(got) != 0 {
			t.Errorf("%s's gap items = %+v, want none", name, got)
		}
	}

	// Her manager records the certificate and verifies the skill at 3: met.
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: fse, from: "2026-09-01", to: strPtr("2027-09-01"), verified: true})
	h.Exec(t, `UPDATE staff.person_competences SET level = 3, verified_at = $2, verified_basis = 'observed', source = 'manager'
		WHERE id = $1`, golangRow, h.Now())
	if got := gapItems(attentionItems(t, w.bossC)); len(got) != 0 {
		t.Errorf("met: the manager's gap items = %+v, want none", got)
	}

	// A department's default asking what she lacks raises it without an
	// assignment; archived, it raises nothing — nor does an archived
	// profile assigned.
	truck := rawProfile(t, h, "truck-driver", false, gapItemFor{competence: catalogueID(t, h, "truck"), mustBeValid: true})
	rawDepartmentDefault(t, h, "WH", w.kari, truck)
	if got := gapItems(attentionItems(t, w.bossC)); got[kariItem].Count == nil || *got[kariItem].Count != 1 {
		t.Errorf("the department's default: the manager's gap items = %+v, want Kari's with 1", got)
	}
	h.Exec(t, `UPDATE staff.competence_profiles SET archived_at = $2 WHERE id = $1`, truck, h.Now())
	h.Exec(t, `UPDATE staff.person_competences SET level = 2 WHERE id = $1`, golangRow)
	h.Exec(t, `UPDATE staff.competence_profiles SET archived_at = $2 WHERE id = $1`, electrician, h.Now())
	if got := gapItems(attentionItems(t, hr)); len(got) != 0 {
		t.Errorf("archived: HR's gap items = %+v, want none", got)
	}
}
