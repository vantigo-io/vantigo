package planning_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// The people the candidate and intake tests add beside Kari, Ola, Siri and
// Per (unbooked_test.go): one who covers everything, one whose employment
// ends inside the requests' period, and two namesakes for the sort.
const (
	personNils  = int64(1005)
	personLise  = int64(1006)
	personAnne  = int64(1007)
	personBerit = int64(1008)
	personAnne2 = int64(1009)
)

// getCandidates reads request id's candidates as c, failing unless it is
// 200.
func getCandidates(t *testing.T, c *modtest.Client, id int64, includePartial bool) gen.PlanningCandidateList {
	t.Helper()
	path := requestPath(id) + "/candidates"
	if includePartial {
		path += "?includePartial=true"
	}
	var out gen.PlanningCandidateList
	getJSON(t, c, path, http.StatusOK, &out)
	return out
}

// heldSkill is a skill held at level from the start of 2026.
func heldSkill(e contracts.CompetenceEntry, level int16) contracts.HeldCompetence {
	return contracts.HeldCompetence{CompetenceID: e.ID, Key: e.Key, Kind: e.Kind, Level: &level, Verified: true, ValidFrom: date("2026-01-01")}
}

// heldCertificate is a certificate held from the start of 2026 to validTo.
func heldCertificate(e contracts.CompetenceEntry, validTo string) contracts.HeldCompetence {
	to := date(validTo)
	return contracts.HeldCompetence{CompetenceID: e.ID, Key: e.Key, Kind: e.Kind, Verified: true, ValidFrom: date("2026-01-01"), ValidTo: &to}
}

// candidateIDs is the people of candidates, in order.
func candidateIDs(candidates []gen.PlanningCandidate) []int64 {
	out := make([]int64, len(candidates))
	for i, c := range candidates {
		out[i] = c.Person.Id
	}
	return out
}

// candidateOf is personID's candidate, failing when they are not listed.
func candidateOf(t *testing.T, list gen.PlanningCandidateList, personID int64) gen.PlanningCandidate {
	t.Helper()
	for _, c := range list.Candidates {
		if c.Person.Id == personID {
			return c
		}
	}
	t.Fatalf("person %d is not among the candidates %v", personID, candidateIDs(list.Candidates))
	return gen.PlanningCandidate{}
}

// TestCandidates_Judged is S17's points 1, 2 and 6 over a request for 20
// hours a week of Java at level 3 and the HSE card, through October 2026:
// Nils covers it; Per holds everything but his card expires before the
// last day (certificate_expires) and fits his preferences less; Kari is
// booked half the period elsewhere (partial: 75 free of 80); Ola's Java is
// level 2 (missing, listed only with includePartial); Siri starts in
// December and Lise leaves mid-October, so neither is employed throughout.
// The fit is asked once, for the kept people, over the request's period at
// its hours a week.
func TestCandidates_Judged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalogue(h)
	c := planner(t, h)
	w := h.workforce
	w.addPerson(personPer, "Per Hansen", "A-1004", date("2026-01-01"), date("2029-12-31"), 37.5)
	w.addPerson(personNils, "Nils Dekker", "A-1005", date("2026-01-01"), date("2029-12-31"), 37.5)
	w.addPerson(personLise, "Lise Slutter", "A-1006", date("2026-01-01"), date("2026-10-15"), 37.5)
	for _, id := range []int64{personKari, personNils, personLise} {
		w.setHeld(id, heldSkill(catalogueJava, 4), heldCertificate(catalogueHMSCard, "2028-01-01"))
	}
	w.setHeld(personPer, heldSkill(catalogueJava, 3), heldCertificate(catalogueHMSCard, "2026-10-20"))
	w.setHeld(personOla, heldSkill(catalogueJava, 2), heldCertificate(catalogueHMSCard, "2028-01-01"))
	w.setHeld(personSiri, heldSkill(catalogueJava, 4), heldCertificate(catalogueHMSCard, "2028-01-01"))
	w.setFit(personPer, contracts.Fit{Score: 70, Lowered: []string{"weekdays"}})
	seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: "2026-10-05", to: "2026-10-16", load: 100})
	level := int16(3)
	id := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-30", hours: 20,
		requirements: []seedRequirement{{competence: catalogueJava.ID, minLevel: &level}, {competence: catalogueHMSCard.ID}}})

	list := getCandidates(t, c, id, false)
	if list.IncludePartial {
		t.Error("includePartial = true, want false by default")
	}
	if got, want := candidateIDs(list.Candidates), []int64{personNils, personPer, personKari}; !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v: fits first, by fit score; Ola missing, Siri and Lise not employed throughout", got, want)
	}
	w.mu.Lock()
	booking := w.lastBooking
	w.mu.Unlock()
	if booking == nil || !booking.From.Equal(date("2026-10-05")) || !booking.To.Equal(date("2026-10-30")) ||
		booking.HoursPerWeek == nil || *booking.HoursPerWeek != 20 {
		t.Errorf("the booking asked about = %+v, want the request's period at 20 hours a week", booking)
	}

	nils := candidateOf(t, list, personNils)
	if nils.Availability != gen.Fits || nils.FreeHours != 150 || nils.RequiredHours != 80 || !nils.Covers ||
		nils.Fit.Score != 100 || len(nils.Fit.Lowered) != 0 || len(nils.Flags) != 0 || len(nils.Missing) != 0 || nils.CurrentProject != nil {
		t.Errorf("Nils = %+v, want fits 150 of 80, covers, 100", nils)
	}
	per := candidateOf(t, list, personPer)
	if per.Availability != gen.Fits || per.Covers || per.Fit.Score != 70 || !slices.Equal(per.Fit.Lowered, []string{"weekdays"}) {
		t.Errorf("Per = %+v, want fits, flagged so not covering, score 70 lowered by weekdays", per)
	}
	if len(per.Flags) != 1 || per.Flags[0].Code != gen.PlanningCandidateFlagCodeCertificateExpires ||
		per.Flags[0].CompetenceKey == nil || *per.Flags[0].CompetenceKey != "hms-kort" ||
		per.Flags[0].Date == nil || per.Flags[0].Date.String() != "2026-10-20" {
		t.Errorf("Per's flags = %+v, want certificate_expires hms-kort on 2026-10-20", per.Flags)
	}
	kari := candidateOf(t, list, personKari)
	if kari.Availability != gen.Partial || kari.FreeHours != 75 || kari.RequiredHours != 80 || kari.Covers {
		t.Errorf("Kari = %+v, want partial with 75 free of 80", kari)
	}

	partial := getCandidates(t, c, id, true)
	if !partial.IncludePartial {
		t.Error("includePartial = false, want true when asked")
	}
	if got, want := candidateIDs(partial.Candidates), []int64{personNils, personOla, personPer, personKari}; !slices.Equal(got, want) {
		t.Fatalf("candidates with partial matches = %v, want %v", got, want)
	}
	ola := candidateOf(t, partial, personOla)
	if !slices.Equal(ola.Missing, []string{"java"}) || ola.Covers || ola.Availability != gen.Fits || ola.FreeHours != 120 {
		t.Errorf("Ola = %+v, want missing java, fitting 120 free, not covering", ola)
	}

	// Staff answers the caller nobody: there is nobody to judge.
	w.setVisible(false)
	if list := getCandidates(t, c, id, true); len(list.Candidates) != 0 {
		t.Errorf("candidates with nobody visible = %v, want none", candidateIDs(list.Candidates))
	}
}

// TestCandidates_SortTies is S17's sort past fit and free hours: Berit,
// Anne and the second Anne fit equally with equal free hours, so the
// longest on their current project comes first — Berit since June on
// INTERN (her earliest confirmed row covering today, not her later one on
// KVEM1000), Anne since September — then by name and id with none; Ola,
// with fewer free hours, last. A tentative row is no current project.
func TestCandidates_SortTies(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	w := h.workforce
	w.addPerson(personAnne, "Anne Aas", "A-1007", date("2026-01-01"), date("2029-12-31"), 37.5)
	w.addPerson(personBerit, "Berit Berg", "A-1008", date("2026-01-01"), date("2029-12-31"), 37.5)
	w.addPerson(personAnne2, "Anne Aas", "A-1009", date("2026-01-01"), date("2029-12-31"), 37.5)
	seedAllocation(t, h, seedRow{person: personAnne, project: projectActive, from: "2026-09-01", to: "2026-09-30", load: 50})
	seedAllocation(t, h, seedRow{person: personBerit, project: projectActive, from: "2026-08-03", to: "2026-09-30", load: 20})
	seedAllocation(t, h, seedRow{person: personBerit, project: projectInternal, from: "2026-06-15", to: "2026-09-30", load: 20})
	seedAllocation(t, h, seedRow{person: personAnne2, project: projectInternal, from: "2026-05-04", to: "2026-09-30", load: 20, status: "tentative"})
	id := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-30", hours: 20})

	list := getCandidates(t, c, id, false)
	want := []int64{personBerit, personAnne, personAnne2, personKari, personOla}
	if got := candidateIDs(list.Candidates); !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	berit := candidateOf(t, list, personBerit).CurrentProject
	if berit == nil || berit.Project.Id != projectInternal || berit.Project.Code == nil || *berit.Project.Code != "INTERN" ||
		berit.Since.String() != "2026-06-15" || berit.Days != 89 {
		t.Errorf("Berit's current project = %+v, want INTERN since 2026-06-15, 89 days", berit)
	}
	if anne := candidateOf(t, list, personAnne).CurrentProject; anne == nil || anne.Project.Id != projectActive || anne.Days != 11 {
		t.Errorf("Anne's current project = %+v, want KVEM1000, 11 days", anne)
	}
	if other := candidateOf(t, list, personAnne2).CurrentProject; other != nil {
		t.Errorf("the second Anne's current project = %+v, want none: her row is tentative", other)
	}
}

// TestCandidates_Refusals: an unknown request is the bare 404; a request
// with no day inside Staff's bound is the 400 window_out_of_bounds a write
// answers; a filled request is answered (plan reading 3); planning:view
// alone may not read candidates.
func TestCandidates_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	getJSON(t, c, requestPath(999999)+"/candidates", http.StatusNotFound, nil)

	old := seedRequest(t, h, seedReq{project: projectActive, from: "2026-01-05", to: "2026-02-27"})
	res := getJSON(t, c, requestPath(old)+"/candidates", http.StatusBadRequest, nil)
	p := problemOf(t, res, http.StatusBadRequest)
	wantCode(t, p, "window_out_of_bounds")
	if p.Errors == nil || len((*p.Errors)["fromDate"]) == 0 {
		t.Errorf("errors = %+v, want one on fromDate", p.Errors)
	}

	row := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", unit: "hours_per_week", load: 37.5})
	filled := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-30", filledBy: row})
	list := getCandidates(t, c, filled, false)
	if kari := candidateOf(t, list, personKari); kari.Availability != gen.Partial || kari.FreeHours != 0 {
		t.Errorf("Kari on the request she fills = %+v, want partial with nothing free", kari)
	}
	getJSON(t, h.SignIn(t, "planning:access", "planning:view"), requestPath(filled)+"/candidates", http.StatusForbidden, nil)
}

// TestCandidates_TermsAndWishes is S17's points 3 to 5 and the sort's second
// key, over a request for 10 hours a week on a Kraft-Verket project through
// October 2026:
//
//   - Kari's confirmed row bars Kraft-Verket until the request's first day:
//     not listed, with includePartial too;
//   - Ola's confirmed row on Hydro keeps him until 30 November, with a
//     fortnight's handover and the customer's approval for a replacement:
//     release_after_start and needs_customer_approval naming the row; his
//     other row with terms releases him before the first day and flags
//     nothing;
//   - Nils wishes for something new from 1 October (his wish from November
//     is too late to count), Anne to leave INTERN, her current project (her
//     wish off Hydro, which she is not on, counts for nothing), and the
//     second Anne for something new from the first day itself;
//   - Berit wishes nothing and is bound by nothing: the one who covers.
//
// The willing come first — Anne before Berit although Berit has more free
// hours — the two Annes tied on everything but their id.
func TestCandidates_TermsAndWishes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	w := h.workforce
	w.addPerson(personNils, "Nils Dekker", "A-1005", date("2026-01-01"), date("2029-12-31"), 37.5)
	w.addPerson(personAnne, "Anne Aas", "A-1007", date("2026-01-01"), date("2029-12-31"), 37.5)
	w.addPerson(personBerit, "Berit Berg", "A-1008", date("2026-01-01"), date("2029-12-31"), 37.5)
	w.addPerson(personAnne2, "Anne Aas", "A-1009", date("2026-01-01"), date("2029-12-31"), 37.5)

	barring := seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: "2026-09-01", to: "2026-09-30", load: 20})
	seedTerms(t, h, barring, seedTerm{nonCompeteUntil: "2026-10-05", customers: []int32{customerHydro, customerKraftVerket}})
	keeping := seedAllocation(t, h, seedRow{person: personOla, project: projectHydro, from: "2026-09-01", to: "2026-10-16", load: 50})
	seedTerms(t, h, keeping, seedTerm{minimumUntil: "2026-11-30", handoverDays: ptr(int16(14)), approval: true})
	released := seedAllocation(t, h, seedRow{person: personOla, project: projectInternal, from: "2026-09-01", to: "2026-09-18", load: 10})
	seedTerms(t, h, released, seedTerm{minimumUntil: "2026-10-02", approval: true})
	seedOpenWish(t, h, personNils, "new", nil, "2026-10-01", 0)
	seedOpenWish(t, h, personNils, "new", nil, "2026-11-01", 0)
	for _, anne := range []int64{personAnne, personAnne2} {
		seedAllocation(t, h, seedRow{person: anne, project: projectInternal, from: "2026-09-01", to: "2026-12-31", load: 20})
	}
	seedOpenWish(t, h, personAnne, "off", ptr(projectInternal), "2026-09-20", 0)
	seedOpenWish(t, h, personAnne, "off", ptr(projectHydro), "2026-09-20", 0)
	seedOpenWish(t, h, personAnne2, "new", nil, "2026-10-05", 0)
	id := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-30", hours: 10})

	list := getCandidates(t, c, id, false)
	want := []int64{personNils, personAnne, personAnne2, personBerit, personOla}
	if got := candidateIDs(list.Candidates); !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v: the willing first, Kari barred", got, want)
	}
	if got := candidateIDs(getCandidates(t, c, id, true).Candidates); slices.Contains(got, personKari) {
		t.Errorf("with partial matches = %v, want Kari still barred", got)
	}

	ola := candidateOf(t, list, personOla)
	if len(ola.Flags) != 2 || ola.Covers || ola.Availability != gen.Fits {
		t.Fatalf("Ola = %+v, want fits, two flags, not covering", ola)
	}
	release, approval := ola.Flags[0], ola.Flags[1]
	if release.Code != gen.PlanningCandidateFlagCodeReleaseAfterStart || release.Date == nil || release.Date.String() != "2026-11-30" ||
		release.HandoverFrom == nil || release.HandoverFrom.String() != "2026-11-16" || release.AllocationId == nil || *release.AllocationId != keeping {
		t.Errorf("Ola's release = %+v, want 2026-11-30 with the handover from 2026-11-16 on row %d", release, keeping)
	}
	if approval.Code != gen.PlanningCandidateFlagCodeNeedsCustomerApproval || approval.AllocationId == nil || *approval.AllocationId != keeping {
		t.Errorf("Ola's approval = %+v, want needs_customer_approval on row %d", approval, keeping)
	}

	for person, wish := range map[int64]struct {
		kind gen.PlanningWishKind
		date string
	}{personNils: {gen.New, "2026-10-01"}, personAnne: {gen.Off, "2026-09-20"}, personAnne2: {gen.New, "2026-10-05"}} {
		got := candidateOf(t, list, person)
		if len(got.Flags) != 1 || got.Flags[0].Code != gen.PlanningCandidateFlagCodeWishes || got.Flags[0].WishKind == nil ||
			*got.Flags[0].WishKind != wish.kind || got.Flags[0].Date == nil || got.Flags[0].Date.String() != wish.date || got.Covers {
			t.Errorf("person %d's flags = %+v, want one wishes flag %s from %s, not covering", person, got.Flags, wish.kind, wish.date)
		}
	}
	if berit := candidateOf(t, list, personBerit); !berit.Covers || len(berit.Flags) != 0 {
		t.Errorf("Berit = %+v, want covering with no flag", berit)
	}

	// The row filling the request binds nothing on it: Ola's Hydro terms
	// moved onto the row that fills the request flag nothing.
	h.Exec(t, `UPDATE planning.allocations SET request_id = $1 WHERE id = $2`, id, keeping)
	if ola := candidateOf(t, getCandidates(t, c, id, false), personOla); len(ola.Flags) != 0 {
		t.Errorf("Ola on the request his row fills = %+v, want no flag", ola.Flags)
	}
}

// TestCandidates_NonCompeteLimits is S17.3's filter at its edges: a
// non-compete that ended before the request's first day, one on a
// tentative row (reading 8) and any non-compete against a request whose
// project has no customer exclude nobody; the same terms on a confirmed row
// still in force exclude Kari.
func TestCandidates_NonCompeteLimits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	binding := seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: "2026-09-01", to: "2026-09-30", load: 20})
	seedTerms(t, h, binding, seedTerm{nonCompeteUntil: "2026-10-04", customers: []int32{customerKraftVerket}})
	kraftVerket := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-30", hours: 10})
	internal := seedRequest(t, h, seedReq{project: projectInternal, from: "2026-10-05", to: "2026-10-30", hours: 10})
	listed := func(id int64) bool {
		return slices.Contains(candidateIDs(getCandidates(t, c, id, false).Candidates), personKari)
	}

	if !listed(kraftVerket) {
		t.Errorf("a non-compete ended the day before the request excluded Kari")
	}
	h.Exec(t, `UPDATE planning.allocation_terms SET non_compete_until = '2027-06-30' WHERE allocation_id = $1`, binding)
	if listed(kraftVerket) {
		t.Errorf("a non-compete in force on a confirmed row did not exclude Kari")
	}
	if !listed(internal) {
		t.Errorf("a request on a project with no customer excluded Kari")
	}
	h.Exec(t, `UPDATE planning.allocations SET status = 'tentative', confirmed_at = NULL WHERE id = $1`, binding)
	if !listed(kraftVerket) {
		t.Errorf("a non-compete on a tentative row excluded Kari")
	}
}
