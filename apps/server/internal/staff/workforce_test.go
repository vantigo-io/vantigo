package staff_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
)

// contracts.Workforce (staff competence design S15, §4 "The contract"; plan
// Task 3b): Staff its own gatekeeper — staff:view, and the person active
// today, for the principal in ctx: a request's caller or the user AsUser
// names; nothing, never an error, for anyone else or a bare context. A
// declared certificate is not held; a declared skill is, unverified.

// workforceOf is the module's provider over h's Deps, as Compose builds it.
func workforceOf(h *harness) contracts.Workforce {
	return staff.Module().Workforce(h.Deps())
}

// heldKeys is each person's held competences' keys, in the catalogue's order.
func heldKeys(held map[int64][]contracts.HeldCompetence) map[int64][]string {
	out := map[int64][]string{}
	for id, list := range held {
		for _, c := range list {
			out[id] = append(out[id], c.Key)
		}
	}
	return out
}

func TestWorkforce_HeldCompetences(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	skill := companySkill(t, h, "sveising", "Sveising")
	two := int16(2)
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "hms-kort"), from: "2026-01-01", to: strPtr("2027-12-31"), verified: true})
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "truck"), from: "2021-09-02", to: strPtr("2026-09-01"), verified: true})
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "kran"), from: "2026-10-01", verified: true})
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "fse"), from: "2026-01-01", to: strPtr("2026-12-31")})
	rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: skill, level: &two, from: "2026-01-01"})
	leaver := seedPerson(t, h, "Per", "Sluttet")
	seedEmploymentDated(t, h, leaver, w.boss, "2024-01-01", dayPtr("2026-08-31"))
	rawPersonCompetence(t, h, competenceRow{person: leaver, competence: catalogueID(t, h, "hms-kort"), from: "2026-01-01", verified: true})

	viewCtx, viewUser := calendarCaller(t, h.Harness, "staff:access", "staff:view")
	ids := []int64{w.kari, leaver, w.boss}
	held, err := workforceOf(h).HeldCompetences(viewCtx, calDay(2026, 9, 12), ids)
	if err != nil {
		t.Fatalf("HeldCompetences: %v", err)
	}
	// On 12 September: HMS-kort and the declared skill; not the truck
	// certificate ended on 1 September, not the crane one from October,
	// not the declared FSE; the leaver absent; Bjørn holds nothing.
	got := heldKeys(held)
	if len(got) != 1 || !slices.Equal(got[w.kari], []string{"hms-kort", "sveising"}) {
		t.Fatalf("held on 12 September = %v", got)
	}
	hmsHeld, skillHeld := held[w.kari][0], held[w.kari][1]
	if !hmsHeld.Verified || hmsHeld.Kind != "certificate" || hmsHeld.Level != nil ||
		!hmsHeld.ValidFrom.Equal(calDay(2026, 1, 1)) || hmsHeld.ValidTo == nil || !hmsHeld.ValidTo.Equal(calDay(2027, 12, 31)) {
		t.Errorf("HMS-kort = %+v", hmsHeld)
	}
	if skillHeld.Verified || skillHeld.Kind != "skill" || skillHeld.Level == nil || *skillHeld.Level != 2 || skillHeld.ValidTo != nil {
		t.Errorf("the declared skill = %+v", skillHeld)
	}

	// The validity window is the date asked about's.
	held, err = workforceOf(h).HeldCompetences(viewCtx, calDay(2026, 8, 15), ids)
	if err != nil {
		t.Fatalf("HeldCompetences: %v", err)
	}
	if got := heldKeys(held); !slices.Equal(got[w.kari], []string{"hms-kort", "truck", "sveising"}) {
		t.Errorf("held on 15 August = %v", got)
	}
	held, err = workforceOf(h).HeldCompetences(viewCtx, calDay(2026, 10, 1), ids)
	if err != nil {
		t.Fatalf("HeldCompetences: %v", err)
	}
	if got := heldKeys(held); !slices.Equal(got[w.kari], []string{"hms-kort", "kran", "sveising"}) {
		t.Errorf("held on 1 October = %v", got)
	}

	// The same user through AsUser, with no request.
	held, err = workforceOf(h).HeldCompetences(contracts.AsUser(context.Background(), viewUser), calDay(2026, 9, 12), ids)
	if err != nil || !slices.Equal(heldKeys(held)[w.kari], []string{"hms-kort", "sveising"}) {
		t.Errorf("under AsUser = %v, %v", heldKeys(held), err)
	}

	// Without staff:view — staff:manage alone included — and with no
	// principal at all: nothing, and no error.
	noView, _ := calendarCaller(t, h.Harness, "staff:access")
	manageOnly, manageUser := calendarCaller(t, h.Harness, "staff:access", "staff:manage")
	for name, ctx := range map[string]context.Context{
		"a request without staff:view": noView,
		"a request with staff:manage":  manageOnly,
		"AsUser without staff:view":    contracts.AsUser(context.Background(), manageUser),
		"a bare context":               context.Background(),
	} {
		if held, err := workforceOf(h).HeldCompetences(ctx, calDay(2026, 9, 12), ids); err != nil || held != nil {
			t.Errorf("%s: %v, %v; want nothing", name, held, err)
		}
	}
}

func TestWorkforce_Competences(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	skill := companySkill(t, h, "sveising", "Sveising")
	h.Exec(t, `UPDATE staff.competences SET hidden = true WHERE key = 'kran'`)
	h.Exec(t, `UPDATE staff.competences SET archived_at = $1 WHERE key = 'truck'`, h.Now())

	view, _ := calendarCaller(t, h.Harness, "staff:access", "staff:view")
	manage, _ := calendarCaller(t, h.Harness, "staff:access", "staff:manage")
	for name, ctx := range map[string]context.Context{"staff:view": view, "staff:manage": manage} {
		entries, err := workforceOf(h).Competences(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		byKey := map[string]contracts.CompetenceEntry{}
		for _, e := range entries {
			byKey[e.Key] = e
		}
		if len(entries) != 15 {
			t.Errorf("%s: %d entries, want the fifteen seeds less the archived one, and the skill", name, len(entries))
		}
		if _, ok := byKey["truck"]; ok {
			t.Errorf("%s: the archived competence is listed", name)
		}
		if e := byKey["kran"]; !e.Hidden {
			t.Errorf("%s: the hidden competence = %+v", name, e)
		}
		hms := byKey["hms-kort"]
		if hms.CategoryKey != "sertifikat" || hms.Kind != "certificate" || hms.HasLevel || hms.ValidityMonths == nil || *hms.ValidityMonths != 24 ||
			hms.NameEn != "HMS card" || hms.NameNb != "HMS-kort" {
			t.Errorf("%s: HMS-kort = %+v", name, hms)
		}
		if e := byKey["sveising"]; e.ID != skill || e.CategoryKey != "fag" || e.Kind != "skill" || !e.HasLevel || e.ValidityMonths != nil {
			t.Errorf("%s: the skill = %+v", name, e)
		}
	}

	none, _ := calendarCaller(t, h.Harness, "staff:access")
	for name, ctx := range map[string]context.Context{"staff:access alone": none, "a bare context": context.Background()} {
		if entries, err := workforceOf(h).Competences(ctx); err != nil || entries != nil {
			t.Errorf("%s: %v, %v; want nothing", name, entries, err)
		}
	}
}

// The window reads (planning design S8). The harness's today is Saturday
// 12 September 2026: the bound runs from Monday 8 June 2026 — the Monday of
// the week thirteen weeks back — to 12 September 2029.

// seedTerms writes a terms row of employment from validFrom, at fte percent
// of a full position of hours a week.
func seedTerms(t *testing.T, h *harness, employment int64, validFrom string, fte, hours float64) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO staff.employment_terms (employment_id, valid_from, employment_type, appointment_form, fte_percent,
			full_time_hours_per_week, created_at, updated_at)
		VALUES ($1, $2::date, 'fast', 'fast', $3, $4, $5, $5)`, employment, validFrom, fte, hours, h.Now())
}

// spans is ds as "from..to" texts, for a readable comparison.
func spans(ds []contracts.DateSpan) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.From.Format(time.DateOnly)+".."+d.To.Format(time.DateOnly))
	}
	return out
}

// segments is cs as "from..to=hours" texts.
func segments(cs []contracts.CapacitySegment) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, fmt.Sprintf("%s..%s=%g", c.From.Format(time.DateOnly), c.To.Format(time.DateOnly), c.HoursPerWeek))
	}
	return out
}

// windowWorld is a register for the window reads: Anne employed throughout
// in Drift, moving to Salg on 1 November and linked to her user; Bo starting
// on 15 October in Salg, linked to nobody; Cato, who left yesterday; Dina,
// whose worker link the accounts worker blocked as someone else's account;
// and Eva, whose worker link is blocked for another reason. Anne's and
// Dina's users hold staff:access, Eva's nothing.
type windowWorld struct {
	h                  *harness
	anne, bo, cato     int64
	dina, eva          int64
	anneEmp            int64
	drift, salg        int64
	anneUser, dinaUser uuid.UUID
	evaUser            uuid.UUID
	view               context.Context
	viewUser           uuid.UUID
}

func newWindowWorld(t *testing.T) windowWorld {
	t.Helper()
	h := newHarness(t)
	w := windowWorld{h: h}
	w.drift = seedDepartment(t, h, "DRIFT", 0, 0)
	w.salg = seedDepartment(t, h, "SALG", 0, 0)
	w.anne = seedPerson(t, h, "Anne", "Aas")
	w.anneEmp = seedEmploymentDated(t, h, w.anne, 0, "2025-01-01", nil)
	h.Exec(t, `UPDATE staff.employment_terms SET department_id = $2 WHERE employment_id = $1`, w.anneEmp, w.drift)
	h.Exec(t, `
		INSERT INTO staff.employment_terms (employment_id, valid_from, employment_type, appointment_form, fte_percent,
			full_time_hours_per_week, department_id, created_at, updated_at)
		VALUES ($1, '2026-11-01', 'fast', 'fast', 100, 37.5, $2, $3, $3)`, w.anneEmp, w.salg, h.Now())
	_, w.anneUser = signIn(t, h)
	link(t, h, w.anne, w.anneUser)

	w.bo = seedPerson(t, h, "Bo", "Berg")
	boEmp := seedEmploymentDated(t, h, w.bo, 0, "2026-10-15", nil)
	h.Exec(t, `UPDATE staff.employment_terms SET department_id = $2 WHERE employment_id = $1`, boEmp, w.salg)

	w.cato = seedPerson(t, h, "Cato", "Carlsen")
	seedEmploymentDated(t, h, w.cato, 0, "2024-01-01", dayPtr("2026-09-11"))

	w.dina = seedPerson(t, h, "Dina", "Dahl")
	seedEmploymentDated(t, h, w.dina, 0, "2025-01-01", nil)
	_, w.dinaUser = signIn(t, h)
	link(t, h, w.dina, w.dinaUser)
	h.Exec(t, `UPDATE staff.identity_links SET state = 'blocked', managed = true, blocked_reason = 'mismatch' WHERE person_id = $1`, w.dina)

	w.eva = seedPerson(t, h, "Eva", "Ek")
	seedEmploymentDated(t, h, w.eva, 0, "2025-01-01", nil)
	_, w.evaUser = h.SignInUser(t)
	link(t, h, w.eva, w.evaUser)
	h.Exec(t, `UPDATE staff.identity_links SET state = 'blocked', managed = true, blocked_reason = 'external_privileged' WHERE person_id = $1`, w.eva)

	w.view, w.viewUser = calendarCaller(t, h.Harness, "staff:access", "staff:view")
	return w
}

func TestWorkforce_People(t *testing.T) {
	t.Parallel()
	w := newWindowWorld(t)
	people, err := workforceOf(w.h).People(w.view, calDay(2026, 9, 1), calDay(2026, 11, 30))
	if err != nil {
		t.Fatalf("People: %v", err)
	}
	// Anne, Bo, Dina and Eva by name; not Cato, who left yesterday.
	byID := map[int64]contracts.PersonEntry{}
	var names []string
	for _, p := range people {
		byID[p.ID] = p
		names = append(names, p.DisplayName)
	}
	if !slices.Equal(names, []string{"Anne Aas", "Bo Berg", "Dina Dahl", "Eva Ek"}) {
		t.Fatalf("People = %v", names)
	}
	anne, bo := byID[w.anne], byID[w.bo]
	if anne.EmployeeNumber == "" || anne.DepartmentID == nil || *anne.DepartmentID != w.drift || anne.DepartmentName != "DRIFT" ||
		anne.UserID == nil || *anne.UserID != w.anneUser || !slices.Equal(spans(anne.Employed), []string{"2026-09-01..2026-11-30"}) {
		t.Errorf("Anne = %+v", anne)
	}
	// The starter: the place on the first day, the days from it, no user.
	if bo.DepartmentID == nil || *bo.DepartmentID != w.salg || bo.DepartmentName != "SALG" || bo.UserID != nil ||
		!slices.Equal(spans(bo.Employed), []string{"2026-10-15..2026-11-30"}) {
		t.Errorf("Bo = %+v", bo)
	}
	// A worker link blocked as someone else's account names no user; one
	// blocked for another reason still gives its user Self.
	if p := byID[w.dina]; p.UserID != nil || p.DepartmentID != nil || p.DepartmentName != "" {
		t.Errorf("Dina = %+v; want no user and no department", p)
	}
	if p := byID[w.eva]; p.UserID == nil || *p.UserID != w.evaUser {
		t.Errorf("Eva = %+v; want her user", p)
	}

	// A window from November reads Anne's place on its first day.
	later, err := workforceOf(w.h).People(w.view, calDay(2026, 11, 2), calDay(2026, 11, 30))
	if err != nil {
		t.Fatalf("People: %v", err)
	}
	for _, p := range later {
		if p.ID == w.anne && (p.DepartmentID == nil || *p.DepartmentID != w.salg) {
			t.Errorf("Anne in November = %+v; want Salg", p)
		}
	}

	// A window ending before Bo starts does not list him; a window wholly
	// past lists who is in employment today, never yesterday's leaver.
	early, err := workforceOf(w.h).People(w.view, calDay(2026, 9, 1), calDay(2026, 10, 14))
	if err != nil || slices.ContainsFunc(early, func(p contracts.PersonEntry) bool { return p.ID == w.bo }) {
		t.Errorf("People to 14 October = %v, %v; want no Bo", early, err)
	}
	past, err := workforceOf(w.h).People(w.view, calDay(2026, 6, 8), calDay(2026, 8, 31))
	if err != nil {
		t.Fatalf("People over the summer: %v", err)
	}
	if len(past) != 3 || slices.ContainsFunc(past, func(p contracts.PersonEntry) bool { return p.ID == w.cato }) {
		t.Errorf("People over the summer = %+v; want Anne, Dina and Eva", past)
	}
	for _, p := range past {
		if p.ID == w.anne && (p.DepartmentID == nil || *p.DepartmentID != w.drift) {
			t.Errorf("Anne over the summer = %+v; want her place today", p)
		}
	}

	// The same user through AsUser, with no request.
	asUser, err := workforceOf(w.h).People(contracts.AsUser(context.Background(), w.viewUser), calDay(2026, 9, 1), calDay(2026, 11, 30))
	if err != nil || len(asUser) != 4 {
		t.Errorf("under AsUser = %d people, %v; want four", len(asUser), err)
	}

	// The bound.
	for name, window := range map[string][2]time.Time{
		"fourteen weeks back": {calDay(2026, 6, 6), calDay(2026, 9, 30)},
		"four years ahead":    {calDay(2026, 9, 1), calDay(2030, 9, 12)},
		"to before from":      {calDay(2026, 10, 1), calDay(2026, 9, 30)},
	} {
		if got, err := workforceOf(w.h).People(w.view, window[0], window[1]); !errors.Is(err, contracts.ErrWindowOutOfBounds) || got != nil {
			t.Errorf("%s: %v, %v; want ErrWindowOutOfBounds", name, got, err)
		}
	}
	if _, err := workforceOf(w.h).People(w.view, calDay(2026, 6, 8), calDay(2029, 9, 12)); err != nil {
		t.Errorf("the bound's own ends: %v", err)
	}

	// Without staff:view — staff:manage alone included — nothing, no error.
	noView, _ := calendarCaller(t, w.h.Harness, "staff:access")
	manageOnly, _ := calendarCaller(t, w.h.Harness, "staff:access", "staff:manage")
	for name, ctx := range map[string]context.Context{
		"a request without staff:view": noView, "a request with staff:manage": manageOnly, "a bare context": context.Background(),
	} {
		if got, err := workforceOf(w.h).People(ctx, calDay(2026, 9, 1), calDay(2026, 11, 30)); err != nil || got != nil {
			t.Errorf("%s: %v, %v; want nothing", name, got, err)
		}
	}
}

func TestWorkforce_Capacity(t *testing.T) {
	t.Parallel()
	w := newWindowWorld(t)
	h := w.h
	// Anne goes to 50 % on 1 October.
	seedTerms(t, h, w.anneEmp, "2026-10-01", 50, 37.5)
	// Dina takes a second employment at 20 % from 1 October.
	dinaSecond := seedEmploymentDated(t, h, w.dina, 0, "2026-10-01", nil)
	h.Exec(t, `UPDATE staff.employment_terms SET fte_percent = 20 WHERE employment_id = $1`, dinaSecond)
	// Eva gets a new terms row on 1 October at the same figure, and leaves
	// on 31 October.
	evaEmp := modtest.One[int64](t, h.Harness, `SELECT id FROM staff.employments WHERE person_id = $1`, w.eva)
	seedTerms(t, h, evaEmp, "2026-10-01", 100, 37.5)
	h.Exec(t, `UPDATE staff.employments SET end_date = '2026-10-31', end_reason = 'contract_ended' WHERE person_id = $1`, w.eva)

	me := w.viewUser
	// Eva's days away: a holiday from August into September, clipped; an
	// approved holiday and a registered egenmelding the day after, one span;
	// a 50 % leave, a whole day away; an hour row and a request, nothing; an
	// HR-only type, plain days.
	insertCalendarAbsence(t, h, w.eva, "ferie", "2026-08-25", "2026-09-02", "approved", nil, me)
	insertCalendarAbsence(t, h, w.eva, "ferie", "2026-09-14", "2026-09-18", "approved", nil, me)
	insertCalendarAbsence(t, h, w.eva, "egenmelding", "2026-09-19", "2026-09-21", "registered", nil, me)
	half := insertCalendarAbsence(t, h, w.eva, "omsorgspermisjon", "2026-10-05", "2026-10-06", "approved", nil, me)
	h.Exec(t, `UPDATE staff.absences SET percent = 50 WHERE id = $1`, half)
	two := 2.0
	insertCalendarAbsence(t, h, w.eva, "helse", "2026-10-12", "2026-10-12", "registered", &two, me)
	insertCalendarAbsence(t, h, w.eva, "ferie", "2026-10-19", "2026-10-23", "requested", nil, me)
	insertCalendarAbsence(t, h, w.eva, "svangerskapspermisjon", "2026-10-26", "2026-10-30", "approved", nil, me)
	insertCalendarAbsence(t, h, w.eva, "ferie", "2026-12-01", "2026-12-04", "approved", nil, me)

	ids := []int64{w.anne, w.bo, w.cato, w.dina, w.eva, 999999}
	capacity, err := workforceOf(h).Capacity(w.view, calDay(2026, 9, 1), calDay(2026, 11, 30), ids)
	if err != nil {
		t.Fatalf("Capacity: %v", err)
	}
	if _, ok := capacity[w.cato]; ok || len(capacity) != 4 {
		t.Errorf("Capacity answers %d people; want Anne, Bo, Dina and Eva, not yesterday's leaver nor an unknown id", len(capacity))
	}
	for _, tc := range []struct {
		name string
		id   int64
		want []string
	}{
		// Each FTE change cuts the window: 50 % from October, the full
		// position again with November's row.
		{"Anne", w.anne, []string{"2026-09-01..2026-09-30=37.5", "2026-10-01..2026-10-31=18.75", "2026-11-01..2026-11-30=37.5"}},
		// No segment before the first day.
		{"Bo", w.bo, []string{"2026-10-15..2026-11-30=37.5"}},
		// Two employments summed.
		{"Dina", w.dina, []string{"2026-09-01..2026-09-30=37.5", "2026-10-01..2026-11-30=45"}},
		// No segment after the last day; a row at the same figure does not
		// cut.
		{"Eva", w.eva, []string{"2026-09-01..2026-10-31=37.5"}},
	} {
		if got := segments(capacity[tc.id].Segments); !slices.Equal(got, tc.want) {
			t.Errorf("%s's segments = %v, want %v", tc.name, got, tc.want)
		}
	}
	wantAway := []string{"2026-09-01..2026-09-02", "2026-09-14..2026-09-21", "2026-10-05..2026-10-06", "2026-10-26..2026-10-30"}
	if got := spans(capacity[w.eva].Away); !slices.Equal(got, wantAway) {
		t.Errorf("Eva away = %v, want %v", got, wantAway)
	}
	if len(capacity[w.anne].Away) != 0 {
		t.Errorf("Anne away = %v, want none", spans(capacity[w.anne].Away))
	}

	// The bound, the gate.
	if got, err := workforceOf(h).Capacity(w.view, calDay(2026, 6, 6), calDay(2026, 9, 30), ids); !errors.Is(err, contracts.ErrWindowOutOfBounds) || got != nil {
		t.Errorf("fourteen weeks back: %v, %v; want ErrWindowOutOfBounds", got, err)
	}
	if got, err := workforceOf(h).Capacity(w.view, calDay(2026, 9, 1), calDay(2030, 9, 12), ids); !errors.Is(err, contracts.ErrWindowOutOfBounds) || got != nil {
		t.Errorf("four years ahead: %v, %v; want ErrWindowOutOfBounds", got, err)
	}
	manageOnly, _ := calendarCaller(t, h.Harness, "staff:access", "staff:manage")
	if got, err := workforceOf(h).Capacity(manageOnly, calDay(2026, 9, 1), calDay(2026, 11, 30), ids); err != nil || got != nil {
		t.Errorf("staff:manage alone: %v, %v; want nothing", got, err)
	}
	if got, err := workforceOf(h).Capacity(contracts.AsUser(context.Background(), w.viewUser), calDay(2026, 9, 1), calDay(2026, 11, 30), ids); err != nil || len(got) != 4 {
		t.Errorf("under AsUser: %d people, %v; want four", len(got), err)
	}
}

func TestWorkforce_Self(t *testing.T) {
	t.Parallel()
	w := newWindowWorld(t)
	h := w.h
	anne := contracts.AsUser(context.Background(), w.anneUser)
	self, err := workforceOf(h).Self(anne)
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	if self == nil || self.ID != w.anne || self.DisplayName != "Anne Aas" || self.EmployeeNumber == "" ||
		self.DepartmentID == nil || *self.DepartmentID != w.drift || self.DepartmentName != "DRIFT" ||
		self.UserID == nil || *self.UserID != w.anneUser || len(self.Employed) != 0 {
		t.Errorf("Self = %+v; want Anne, in Drift, with her user and no days", self)
	}

	unlinked, _ := calendarCaller(t, h.Harness, "staff:access")
	noAccess := contracts.AsUser(context.Background(), w.evaUser)
	for name, ctx := range map[string]context.Context{
		"an unlinked user":                   unlinked,
		"a link blocked as mismatch":         contracts.AsUser(context.Background(), w.dinaUser),
		"a linked user without staff:access": noAccess,
		"a bare context":                     context.Background(),
	} {
		if got, err := workforceOf(h).Self(ctx); err != nil || got != nil {
			t.Errorf("%s: %+v, %v; want nothing", name, got, err)
		}
	}
}

// HeldCompetences widened (planning design S8): a person starting next
// month is answered for a day of their employment, not for today, and not
// for a past day, which keeps today's rule.
func TestWorkforce_HeldCompetencesOfAStarter(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	starter := seedPerson(t, h, "Siv", "Start")
	seedEmploymentDated(t, h, starter, w.boss, "2026-10-15", nil)
	rawPersonCompetence(t, h, competenceRow{person: starter, competence: catalogueID(t, h, "hms-kort"), from: "2026-01-01", verified: true})
	viewCtx, _ := calendarCaller(t, h.Harness, "staff:access", "staff:view")
	for _, tc := range []struct {
		on   time.Time
		want bool
	}{{calDay(2026, 10, 15), true}, {calDay(2026, 9, 12), false}, {calDay(2026, 8, 15), false}, {calDay(2026, 10, 14), false}} {
		held, err := workforceOf(h).HeldCompetences(viewCtx, tc.on, []int64{starter, w.kari})
		if err != nil {
			t.Fatalf("HeldCompetences: %v", err)
		}
		if _, ok := held[starter]; ok != tc.want {
			t.Errorf("on %s: the starter answered %v, want %v", tc.on.Format(time.DateOnly), ok, tc.want)
		}
	}
}
