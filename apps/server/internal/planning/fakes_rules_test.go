package planning_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestFakeWorkforce_AnswersAsStaffDoes pins the fake register's rules, which
// every lane's tests plan against: People answers who is employed on a day of
// the window with Employed clipped to it — next month's starter only for a
// window reaching her — Capacity clips the segments and the away spans,
// HeldCompetences answers what is valid on the day, Self answers the linked
// person with no spans, and a caller without staff:view gets nothing but Self.
func TestFakeWorkforce_AnswersAsStaffDoes(t *testing.T) {
	t.Parallel()
	w := newFakeWorkforce()
	ctx := context.Background()

	people, err := w.People(ctx, date("2026-11-02"), date("2026-11-29"))
	if err != nil || len(people) != 2 || people[0].ID != personKari || people[1].ID != personOla {
		t.Fatalf("November = %+v, %v; want Kari and Ola, not Siri", people, err)
	}
	if got := people[0].Employed; len(got) != 1 || !got[0].From.Equal(date("2026-11-02")) || !got[0].To.Equal(date("2026-11-29")) {
		t.Errorf("Kari's employment = %+v, want the window", got)
	}
	if people, _ := w.People(ctx, date("2026-11-30"), date("2026-12-06")); len(people) != 3 || people[2].ID != personSiri {
		t.Errorf("the week Siri starts = %+v, want her too", people)
	}

	w.setAway(personOla, contracts.DateSpan{From: date("2026-11-20"), To: date("2026-12-04")})
	capacity, err := w.Capacity(ctx, date("2026-11-30"), date("2026-12-06"), []int64{personOla, personSiri, 1999})
	if err != nil || len(capacity) != 2 {
		t.Fatalf("Capacity = %+v, %v; want Ola and Siri", capacity, err)
	}
	ola, siri := capacity[personOla], capacity[personSiri]
	if len(ola.Away) != 1 || !ola.Away[0].From.Equal(date("2026-11-30")) || ola.Segments[0].HoursPerWeek != 30 {
		t.Errorf("Ola = %+v, want 30 hours and away clipped to the window", ola)
	}
	if len(siri.Segments) != 1 || !siri.Segments[0].From.Equal(date("2026-12-01")) {
		t.Errorf("Siri = %+v, want her segment from her first day", siri)
	}

	until := date("2026-12-31")
	w.setHeld(personKari, contracts.HeldCompetence{CompetenceID: 7001, Key: "hms-kort", Kind: "certificate", Verified: true,
		ValidFrom: date("2025-01-01"), ValidTo: &until})
	if held, _ := w.HeldCompetences(ctx, date("2026-12-31"), []int64{personKari}); len(held[personKari]) != 1 {
		t.Errorf("held on its last day = %+v, want the certificate", held)
	}
	if held, _ := w.HeldCompetences(ctx, date("2027-01-01"), []int64{personKari}); len(held[personKari]) != 0 {
		t.Errorf("held the day after = %+v, want nothing", held)
	}

	user := uuid.New()
	w.link(user, personKari)
	as := contracts.WithPrincipal(ctx, contracts.Principal{UserID: user})
	if self, err := w.Self(as); err != nil || self == nil || self.ID != personKari || self.Employed != nil || self.UserID == nil {
		t.Errorf("Self = %+v, %v; want Kari, no spans, her user", self, err)
	}
	if self, _ := w.Self(contracts.WithPrincipal(ctx, contracts.Principal{UserID: uuid.New()})); self != nil {
		t.Errorf("Self of a user linked to nobody = %+v, want nil", self)
	}

	w.setVisible(false)
	if people, _ := w.People(ctx, date("2026-11-02"), date("2026-11-29")); len(people) != 0 {
		t.Errorf("People without staff:view = %+v, want nobody", people)
	}
	if self, _ := w.Self(as); self == nil {
		t.Error("Self without staff:view answered nil, want Kari: Self is Staff's own rule")
	}

	w.refuseWindow = true
	if _, err := w.People(ctx, date("2020-01-06"), date("2020-01-12")); !errors.Is(err, contracts.ErrWindowOutOfBounds) {
		t.Errorf("a refused window = %v, want ErrWindowOutOfBounds", err)
	}
}

// TestFakeWorkforce_PreferenceFits pins the fake's fit: a person with no fit
// set scores 100 with nothing lowered, as Staff answers a person with no
// preference; setFit is answered for any booking; an unknown id is absent;
// a caller without staff:view gets nothing; and the booking asked about is
// kept in lastBooking.
func TestFakeWorkforce_PreferenceFits(t *testing.T) {
	t.Parallel()
	w := newFakeWorkforce()
	ctx := context.Background()
	hours := 20.0
	b := contracts.Booking{From: date("2026-11-02"), To: date("2026-11-29"), HoursPerWeek: &hours}

	w.setFit(personOla, contracts.Fit{Score: 70, Lowered: []string{"weekdays", "max_hours_per_week"}})
	fits, err := w.PreferenceFits(ctx, []int64{personKari, personOla, 1999}, b)
	if err != nil || len(fits) != 2 {
		t.Fatalf("PreferenceFits = %+v, %v; want Kari and Ola, not the unknown id", fits, err)
	}
	if f := fits[personKari]; f.Score != 100 || f.Lowered != nil {
		t.Errorf("Kari = %+v, want 100 and nothing lowered", f)
	}
	if f := fits[personOla]; f.Score != 70 || len(f.Lowered) != 2 || f.Lowered[0] != "weekdays" || f.Lowered[1] != "max_hours_per_week" {
		t.Errorf("Ola = %+v, want the fit set", f)
	}
	if got := w.lastBooking; got == nil || !got.From.Equal(b.From) || !got.To.Equal(b.To) || got.HoursPerWeek == nil || *got.HoursPerWeek != 20 {
		t.Errorf("lastBooking = %+v, want the booking asked about", got)
	}

	w.setVisible(false)
	later := contracts.Booking{From: date("2026-12-07"), To: date("2026-12-13")}
	if fits, err := w.PreferenceFits(ctx, []int64{personKari, personOla}, later); err != nil || fits != nil {
		t.Errorf("without staff:view = %+v, %v; want nil, nil", fits, err)
	}
	if got := w.lastBooking; got == nil || !got.From.Equal(later.From) {
		t.Errorf("lastBooking without staff:view = %+v, want the later booking", got)
	}
}

// TestFakeProjects_AnswersAsProjectsDoes pins the fake directory's rules: the
// listing by status by code then id, an unknown id answering nothing, a
// project a test adds, the second customer's project, and a customer a test
// adds.
func TestFakeProjects_AnswersAsProjectsDoes(t *testing.T) {
	t.Parallel()
	p := newFakeProjects()
	ctx := context.Background()

	listed, err := p.ProjectsByStatus(ctx, []string{"planned", "active"})
	if err != nil || len(listed) != 4 || listed[0].ID != projectHydro || listed[1].ID != projectInternal ||
		listed[2].ID != projectActive || listed[3].ID != projectPlanned {
		t.Errorf("planned and active = %+v, %v; want HYDRO100, INTERN, KVEM1000, KVEM2000", listed, err)
	}
	if e, err := p.Project(ctx, projectUnknown); e != nil || err != nil {
		t.Errorf("an unknown project = %+v, %v; want nil, nil", e, err)
	}
	p.setProject(contracts.ProjectEntry{ID: projectUnknown, Code: "NY", Name: "Nytt", Status: "on-hold"})
	if e, _ := p.ProjectByCode(ctx, "ny"); e == nil || e.ID != projectUnknown {
		t.Errorf("the added project by code = %+v, want it", e)
	}
	if entries, _ := p.Projects(ctx, []int32{projectCompleted, 2998}); len(entries) != 1 || entries[0].Status != "completed" {
		t.Errorf("Projects = %+v, want the completed one alone", entries)
	}
	if own, _ := p.ProjectsForCustomer(ctx, customerKraftVerket); len(own) != 3 {
		t.Errorf("Kraft-Verket's projects = %+v, want three", own)
	}
	if own, _ := p.ProjectsForCustomer(ctx, customerHydro); len(own) != 1 || own[0].ID != projectHydro || own[0].Status != "active" {
		t.Errorf("Hydro's projects = %+v, want HYDRO100, active", own)
	}

	c := newFakeCustomers()
	if names, _ := c.Customers(ctx, []int32{customerKraftVerket, customerKraftVerket, 3999}); len(names) != 1 || names[0].Name != "Kraft-Verket AS" {
		t.Errorf("Customers = %+v, want Kraft-Verket AS once", names)
	}
	if one, _ := c.Customer(ctx, 3999); one != nil {
		t.Errorf("an unknown customer = %+v, want nil", one)
	}
	c.set(3999, "Ny kunde AS")
	if names, _ := c.Customers(ctx, []int32{3999, customerHydro}); len(names) != 2 || names[0].Name != "Hydro ASA" || names[1].Name != "Ny kunde AS" {
		t.Errorf("Customers after set = %+v, want Hydro ASA, Ny kunde AS", names)
	}
}
