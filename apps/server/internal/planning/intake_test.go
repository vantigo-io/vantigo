package planning_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const intakePath = "/api/v1/planning/intake"

// TestIntake_Coverage is S18 over the planned projects, by code — AAA100,
// with nothing on it, then KVEM2000 — and none other: KVEM1000 is active
// and KVEM9000 on hold. KVEM2000's requests, by from date and id:
//
//   - one wholly before Staff's bound, which nobody can be judged for:
//     uncovered;
//   - one anybody can do, which Kari and Ola both cover: covered by two;
//   - one for Java at level 3, which only Kari covers — Ola, at level 2, is
//     no candidate: covered, key_person;
//   - one for the HSE card nobody holds, so nobody is a candidate:
//     uncovered;
//   - one filled, which Kari alone would cover: covered, no key_person —
//     it has its person;
//
// and a withdrawn one, not listed. The project's requested hours are its
// open requests' (8 + 20 + 10 + 5), its tentative and confirmed hours its
// rows' hours a week — Kari's 50 % of 37.5 and Siri's 100 % of 37.5 from
// her first day, tentative; Ola's 10 hours filling the request and 50 % of
// his 30, confirmed.
func TestIntake_Coverage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalogue(h)
	h.projects.setProject(contracts.ProjectEntry{ID: 2007, Code: "AAA100", Name: "Tidlig fase", Status: "planned", BillingType: "time-and-materials"})
	h.projects.setProject(contracts.ProjectEntry{ID: 2006, Code: "KVEM9000", Name: "På vent", Status: "on-hold", BillingType: "time-and-materials"})
	h.workforce.setHeld(personKari, heldSkill(catalogueJava, 4))
	h.workforce.setHeld(personOla, heldSkill(catalogueJava, 2))
	level := int16(3)
	java := []seedRequirement{{competence: catalogueJava.ID, minLevel: &level}}

	old := seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-01-05", to: "2026-02-27", hours: 8})
	anyone := seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-10-05", to: "2026-10-30", hours: 20})
	key := seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-10-05", to: "2026-10-30", hours: 10, requirements: java})
	card := seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-10-05", to: "2026-10-30", hours: 5,
		requirements: []seedRequirement{{competence: catalogueHMSCard.ID}}})
	row := seedAllocation(t, h, seedRow{person: personOla, project: projectPlanned, from: "2027-03-01", to: "2027-03-26", unit: "hours_per_week", load: 10})
	filled := seedRequest(t, h, seedReq{project: projectPlanned, from: "2027-03-01", to: "2027-03-26", hours: 10, requirements: java, filledBy: row})
	seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-10-05", to: "2026-10-30", status: "withdrawn"})
	seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-30"})
	seedRequest(t, h, seedReq{project: 2006, from: "2026-10-05", to: "2026-10-30"})
	seedAllocation(t, h, seedRow{person: personKari, project: projectPlanned, from: "2027-03-01", to: "2027-03-26", load: 50, status: "tentative"})
	seedAllocation(t, h, seedRow{person: personSiri, project: projectPlanned, from: "2026-12-01", to: "2027-01-29", load: 100, status: "tentative"})
	seedAllocation(t, h, seedRow{person: personOla, project: projectPlanned, from: "2027-04-05", to: "2027-04-30", load: 50})
	seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: "2027-04-05", to: "2027-04-30", load: 50})

	var intake gen.PlanningIntake
	getJSON(t, h.SignIn(t, "planning:access", "planning:view"), intakePath, http.StatusOK, &intake)
	if len(intake.Projects) != 2 || intake.Projects[0].Project.Code != "AAA100" || intake.Projects[1].Project.Code != "KVEM2000" {
		t.Fatalf("projects = %+v, want AAA100 and KVEM2000", intake.Projects)
	}
	if empty := intake.Projects[0]; len(empty.Requests) != 0 || empty.RequestedHoursPerWeek != 0 ||
		empty.TentativeHoursPerWeek != 0 || empty.ConfirmedHoursPerWeek != 0 {
		t.Errorf("AAA100 = %+v, want nothing", empty)
	}
	p := intake.Projects[1]
	if p.Project.Customer == nil || p.RequestedHoursPerWeek != 43 || p.TentativeHoursPerWeek != 56.25 || p.ConfirmedHoursPerWeek != 25 {
		t.Errorf("KVEM2000's figures = requested %v, tentative %v, confirmed %v; want 43, 56.25 and 25 with its customer",
			p.RequestedHoursPerWeek, p.TentativeHoursPerWeek, p.ConfirmedHoursPerWeek)
	}
	type judged struct {
		id         int64
		coverage   gen.PlanningCoverage
		candidates int32
		keyPerson  bool
	}
	want := []judged{
		{old, gen.Uncovered, 0, false},
		{anyone, gen.Covered, 2, false},
		{key, gen.Covered, 1, true},
		{card, gen.Uncovered, 0, false},
		{filled, gen.Covered, 1, false},
	}
	var got []judged
	for _, r := range p.Requests {
		got = append(got, judged{r.Request.Id, r.Coverage, r.Candidates, slices.Contains(r.Flags, gen.KeyPerson)})
		if len(r.Flags) > 0 && !r.Flags[0].Valid() {
			t.Errorf("request %d's flags = %v", r.Request.Id, r.Flags)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("requests = %+v, want %+v", got, want)
	}
	if r := p.Requests[4].Request; r.Status != gen.Filled || r.FilledBy == nil || r.FilledBy.AllocationId != row {
		t.Errorf("the filled request = %+v, want filled by row %d", r, row)
	}

	// Staff answers the caller nobody: no candidates, and only the filled
	// request is covered; a percent row weighs nothing without capacity.
	h.workforce.setVisible(false)
	intake = gen.PlanningIntake{}
	getJSON(t, h.SignIn(t, "planning:access", "planning:view"), intakePath, http.StatusOK, &intake)
	p = intake.Projects[1]
	for _, r := range p.Requests {
		if wantCoverage := gen.Uncovered; r.Request.Id != filled && (r.Coverage != wantCoverage || r.Candidates != 0) {
			t.Errorf("request %d with nobody visible = %s with %d, want uncovered with none", r.Request.Id, r.Coverage, r.Candidates)
		}
	}
	if p.TentativeHoursPerWeek != 0 || p.ConfirmedHoursPerWeek != 10 {
		t.Errorf("figures with nobody visible = tentative %v, confirmed %v; want 0 and the hours row's 10", p.TentativeHoursPerWeek, p.ConfirmedHoursPerWeek)
	}
	getJSON(t, h.SignIn(t, "planning:access"), intakePath, http.StatusForbidden, nil)
}

// TestIntake_PartlyCoveredAndUncovered is S18's two judgements short of
// covered, with Kari the only person employed through October (Ola leaves
// at the end of September, Siri starts in December): a request for the HSE
// card she does not hold has no candidate — uncovered — while one for more
// hours than she has free (40 a week of her 37.5) and one whose Java she
// meets but whose certificate she holds only to mid-October each have her
// as their only candidate, partial or flagged — partly_covered, covering
// nobody, so no key_person.
func TestIntake_PartlyCoveredAndUncovered(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalogue(h)
	h.workforce.endEmployment(personOla, date("2026-09-30"))
	h.workforce.setHeld(personKari, heldSkill(catalogueJava, 4))
	missing := seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-10-05", to: "2026-10-30", hours: 10,
		requirements: []seedRequirement{{competence: catalogueHMSCard.ID}}})
	partial := seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-10-05", to: "2026-10-30", hours: 40})

	var intake gen.PlanningIntake
	getJSON(t, h.SignIn(t, "planning:access", "planning:view"), intakePath, http.StatusOK, &intake)
	if len(intake.Projects) != 1 || len(intake.Projects[0].Requests) != 2 {
		t.Fatalf("intake = %+v, want KVEM2000 with two requests", intake.Projects)
	}
	for _, r := range intake.Projects[0].Requests {
		want := gen.PartlyCovered
		if r.Request.Id == missing {
			want = gen.Uncovered
		}
		if r.Request.Id != missing && r.Request.Id != partial {
			t.Errorf("unexpected request %d", r.Request.Id)
		}
		if r.Coverage != want || r.Candidates != 0 || len(r.Flags) != 0 {
			t.Errorf("request %d = %s with %d, flags %v; want %s with none", r.Request.Id, r.Coverage, r.Candidates, r.Flags, want)
		}
	}

	// The certificate held, but only to 15 October: Kari is the flagged
	// candidate of the card request, which is now partly covered.
	h.workforce.setHeld(personKari, heldSkill(catalogueJava, 4), heldCertificate(catalogueHMSCard, "2026-10-15"))
	intake = gen.PlanningIntake{}
	getJSON(t, h.SignIn(t, "planning:access", "planning:view"), intakePath, http.StatusOK, &intake)
	for _, r := range intake.Projects[0].Requests {
		if r.Coverage != gen.PartlyCovered || r.Candidates != 0 {
			t.Errorf("request %d with the expiring card = %s with %d, want partly_covered with none", r.Request.Id, r.Coverage, r.Candidates)
		}
	}
}

// TestIntake_TermsAndNonCompete is S18 over S17.3 and S17.4: Kari is barred
// from Kraft-Verket's planned project and Ola's only candidacy is flagged by
// a release after the request's first day, so the request is
// partly_covered with nobody covering; with Ola's terms cleared he covers it
// alone (key_person); with Kari's non-compete expired before the first day,
// both do.
func TestIntake_TermsAndNonCompete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	barring := seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: "2026-09-01", to: "2026-09-30", load: 20})
	seedTerms(t, h, barring, seedTerm{nonCompeteUntil: "2027-06-30", customers: []int32{customerKraftVerket}})
	keeping := seedAllocation(t, h, seedRow{person: personOla, project: projectHydro, from: "2026-09-01", to: "2026-10-09", load: 20})
	seedTerms(t, h, keeping, seedTerm{minimumUntil: "2026-10-30"})
	id := seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-10-05", to: "2026-10-30", hours: 10})

	judge := func() gen.PlanningIntakeRequest {
		t.Helper()
		var intake gen.PlanningIntake
		getJSON(t, h.SignIn(t, "planning:access", "planning:view"), intakePath, http.StatusOK, &intake)
		if len(intake.Projects) != 1 || len(intake.Projects[0].Requests) != 1 || intake.Projects[0].Requests[0].Request.Id != id {
			t.Fatalf("intake = %+v, want KVEM2000 with the request", intake.Projects)
		}
		return intake.Projects[0].Requests[0]
	}
	if r := judge(); r.Coverage != gen.PartlyCovered || r.Candidates != 0 || len(r.Flags) != 0 {
		t.Errorf("with Kari barred and Ola flagged = %s with %d %v, want partly_covered with none", r.Coverage, r.Candidates, r.Flags)
	}
	h.Exec(t, `DELETE FROM planning.allocation_terms WHERE allocation_id = $1`, keeping)
	if r := judge(); r.Coverage != gen.Covered || r.Candidates != 1 || !slices.Contains(r.Flags, gen.KeyPerson) {
		t.Errorf("with Ola free = %s with %d %v, want covered by one, key_person", r.Coverage, r.Candidates, r.Flags)
	}
	h.Exec(t, `UPDATE planning.allocation_terms SET non_compete_until = '2026-10-04' WHERE allocation_id = $1`, barring)
	if r := judge(); r.Coverage != gen.Covered || r.Candidates != 2 || len(r.Flags) != 0 {
		t.Errorf("with Kari's non-compete over = %s with %d %v, want covered by two", r.Coverage, r.Candidates, r.Flags)
	}
}
