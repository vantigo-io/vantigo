package planning

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// A request's candidates (design S17 and its sort): the people in
// employment through the whole period, each judged for competence against
// the requirements, for availability against their free hours, against the
// terms of their confirmed assignments — a non-compete toward the request's
// customer excludes them, a release after the request's first day flags them
// — for their wishes, and for fit as Staff scores it, with the figures, the
// flags and the reasons, so the planner sees why. The intake (intake.go)
// judges its coverage over the same list.
//
// Everything is read before any transaction, as every read is: Staff over
// the request's period cut to the Workforce bound, as a write asks it
// (amendment 16), so a request that started long ago is still judged on the
// days Staff can answer about, and a period with no day inside the bound is
// the 400 window_out_of_bounds a write answers.

// ListPlanningRequestCandidates A request's candidates
// (GET /api/v1/planning/requests/{id}/candidates)
//
// planning:manage's (the contract's rule). Answered for a request in any
// status (plan reading 3): a read never refuses on the status, and a filled
// request's candidates show who else could have filled it.
func (s *server) ListPlanningRequestCandidates(ctx context.Context, req gen.ListPlanningRequestCandidatesRequestObject) (gen.ListPlanningRequestCandidatesResponseObject, error) {
	includePartial := req.Params.IncludePartial != nil && *req.Params.IncludePartial
	out, err := s.requestCandidates(ctx, req.Id, includePartial)
	switch status, problem := readProblem(err); status {
	case http.StatusBadRequest:
		return gen.ListPlanningRequestCandidates400ApplicationProblemPlusJSONResponse(problem), nil
	case http.StatusNotFound:
		return gen.ListPlanningRequestCandidates404Response{}, nil
	case http.StatusServiceUnavailable:
		return gen.ListPlanningRequestCandidates503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.ListPlanningRequestCandidates200JSONResponse(gen.PlanningCandidateList{
		Candidates: out, IncludePartial: includePartial,
	}), nil
}

// requestCandidates reads request id, its requirements and — when it has
// any — Staff's catalogue to name them, and judges its candidates.
func (s *server) requestCandidates(ctx context.Context, id int64, includePartial bool) ([]gen.PlanningCandidate, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return nil, err
	}
	r, found, err := s.request(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, notFound()
	}
	requirements, err := requirementsByRequest(ctx, store.New(s.deps.Pool), []int64{id})
	if err != nil {
		return nil, err
	}
	catalogue, err := s.catalogueFor(ctx, requirements)
	if err != nil {
		return nil, err
	}
	return s.candidatesOf(ctx, c, r, requirements[id], catalogue, includePartial)
}

// catalogueFor is Staff's catalogue by id when one of requirements needs
// naming, and asks nothing otherwise.
func (s *server) catalogueFor(ctx context.Context, requirements map[int64][]store.PlanningRequestRequirement) (map[int64]contracts.CompetenceEntry, error) {
	if len(requirements) == 0 {
		return map[int64]contracts.CompetenceEntry{}, nil
	}
	entries, err := s.workforceCompetences(ctx)
	if err != nil {
		return nil, err
	}
	return competencesByID(entries), nil
}

// judged is one candidate with what the sort reads beside the response.
type judged struct {
	candidate gen.PlanningCandidate
	free      int64 // the free hours in hundredths, compared exactly
	days      int32 // the days on the current project, 0 with none
	wishes    bool  // a wishes flag: the person is willing (S17.5)
}

// bindings is what S17's points 3 to 5 read of the people judged, before
// any transaction: the customer of the request's project, nil for none;
// each person's confirmed rows with terms that could bind on or after the
// request's first day (PersonAssignmentsFrom, plan reading 5); their open
// wishes; and their current row — the confirmed row covering today with
// the earliest from date — whose project an off wish names.
type bindings struct {
	customerID  *int32
	assignments map[int64][]store.PersonAssignmentsFromRow
	wishes      map[int64][]store.PlanningWish
	current     map[int64]store.PlanningAllocation
}

// readBindings reads the bindings of personIDs for requests starting on or
// after from — the assignments, the open wishes and the current rows, today
// being today — in three queries of the module's own tables, nothing asked
// for an empty batch. The customer is the caller's to set.
func (s *server) readBindings(ctx context.Context, today, from time.Time, personIDs []int64) (bindings, error) {
	b := bindings{
		assignments: map[int64][]store.PersonAssignmentsFromRow{},
		wishes:      map[int64][]store.PlanningWish{},
		current:     map[int64]store.PlanningAllocation{},
	}
	if len(personIDs) == 0 {
		return b, nil
	}
	q := store.New(s.deps.Pool)
	assignments, err := q.PersonAssignmentsFrom(ctx, store.PersonAssignmentsFromParams{PersonIds: personIDs, FromDate: dayParam(from)})
	if err != nil {
		return bindings{}, fmt.Errorf("planning: read the candidates' assignments: %w", err)
	}
	for _, a := range assignments {
		b.assignments[a.PlanningAllocation.PersonID] = append(b.assignments[a.PlanningAllocation.PersonID], a)
	}
	wishes, err := q.CandidateWishes(ctx, personIDs)
	if err != nil {
		return bindings{}, fmt.Errorf("planning: read the candidates' wishes: %w", err)
	}
	for _, w := range wishes {
		b.wishes[w.PersonID] = append(b.wishes[w.PersonID], w)
	}
	rows, err := q.ConfirmedAllocationsCovering(ctx, store.ConfirmedAllocationsCoveringParams{PersonIds: personIDs, Day: dayParam(today)})
	if err != nil {
		return bindings{}, fmt.Errorf("planning: read the candidates' current rows: %w", err)
	}
	for _, a := range rows {
		if _, seen := b.current[a.PersonID]; !seen {
			b.current[a.PersonID] = a
		}
	}
	return b, nil
}

// candidatesOf is r's candidates in S17's order, never nil: the people Staff
// answers over r's period cut to the bound, kept when employed on every day
// of it Staff answers about (S8, employedThroughout), each judged
// (judgeCandidates) and then ranked (rankCandidates). Only this read ranks:
// the intake judges its coverage without the fit or the current project.
func (s *server) candidatesOf(ctx context.Context, c *callerContext, r store.PlanningRequest, requirements []store.PlanningRequestRequirement,
	catalogue map[int64]contracts.CompetenceEntry, includePartial bool) ([]gen.PlanningCandidate, error) {
	from, to := civil(r.FromDate), civil(r.ToDate)
	lo, hi, ok := clipToBound(from, to, c.Today)
	if !ok {
		return nil, refuseInvalid(invalidRequestTitle, boundErrors(from, to, c.Today))
	}
	w, err := s.loadWindow(ctx, lo, hi, nil)
	if err != nil {
		return nil, outOfStaffBound(err, lo, c.Today, invalidRequestTitle)
	}
	people := employedOver(w.People, from, to, lo, hi)
	held, err := s.workforceHeld(ctx, latestOf(from, c.Today), personIDsOf(people))
	if err != nil {
		return nil, err
	}
	project, err := s.projectEntry(ctx, r.ProjectID)
	if err != nil {
		return nil, err
	}
	b, err := s.readBindings(ctx, c.Today, from, personIDsOf(people))
	if err != nil {
		return nil, err
	}
	if project != nil {
		b.customerID = project.CustomerID
	}
	out := judgeCandidates(w, people, held, r, lo, hi, requirements, catalogue, b, includePartial)
	return s.rankCandidates(ctx, c, out, lo, hi, loadOf(r.HoursPerWeek), b.current)
}

// employedOver is the people of people employed on every day of from..to
// inside lo..hi, the request's period cut to the bound (S8).
func employedOver(people []contracts.PersonEntry, from, to, lo, hi time.Time) []contracts.PersonEntry {
	return slices.DeleteFunc(slices.Clone(people), func(e contracts.PersonEntry) bool {
		return !employedThroughout(e.Employed, from, to, lo, hi)
	})
}

// judgeCandidates is S17's judgement of people — r's people employed over
// its period, as employedOver keeps them — for r over lo..hi, its period cut
// to the bound, unsorted and with no fit or current project: what the intake
// needs, and what rankCandidates completes for the read. w is a window
// covering lo..hi (it may be wider: the intake reads one for all its
// requests) and held is each person's competences on the later of r's first
// day and today. Each person is judged for
//
//   - competence (S17.1): a requirement is met by a competence held on the
//     later of r's first day and today — the day Staff can answer holding
//     on, as the board's filter judges it (amendment 33) — at or above its
//     minLevel; one not met is the requirement's key in missing and leaves
//     the person out unless includePartial. A certificate met but valid only
//     to a day before r's last is the flag certificate_expires with that
//     day.
//   - availability (S17.2, plan reading 1): totals over lo..hi, not week by
//     week — requiredHours the hours r would book on the person's days with
//     capacity (hoursPerWeek / 5 each, as an hours row books), freeHours the
//     sum of the person's free hours over their confirmed rows (S6); fits
//     when free is at or above required, else partial. Never excluded.
//   - the non-compete, the release and the wishes (S17.3–5, bindings.of and
//     bindings.wishFlags):
//     a non-compete toward r's customer in force on its first day excludes
//     the person, whatever includePartial; a release after the first day
//     and an open wish are flags.
//
// covers is fits with no flag and nothing missing (S18's covered); the fit
// does not enter it, so it is judged here, before any fit is asked.
func judgeCandidates(w *window, people []contracts.PersonEntry, held map[int64][]contracts.HeldCompetence, r store.PlanningRequest,
	lo, hi time.Time, requirements []store.PlanningRequestRequirement, catalogue map[int64]contracts.CompetenceEntry, b bindings, includePartial bool) []judged {
	to := civil(r.ToDate)
	request := capacityRow{From: civil(r.FromDate), To: to, LoadUnit: loadUnitHours, Load: loadOf(r.HoursPerWeek)}
	var out []judged
	for _, e := range people {
		barred, termFlags := b.of(e.ID, r)
		if barred {
			continue
		}
		missing, flags := competenceOf(requirements, catalogue, held[e.ID], to)
		if len(missing) > 0 && !includePartial {
			continue
		}
		flags = append(flags, termFlags...)
		wishFlags := b.wishFlags(e.ID, r)
		flags = append(flags, wishFlags...)
		capacity := w.Capacity[e.ID]
		var required, free float64
		for _, week := range capacityWeeks(capacityInput{From: lo, To: hi, Segments: capacity.Segments, Away: capacity.Away,
			Confirmed: []capacityRow{request}}) {
			required += week.Booked
		}
		own := w.inputOf(e.ID, false, nil)
		own.From, own.To = lo, hi
		for _, week := range capacityWeeks(own) {
			free += week.Free
		}
		availability := gen.Fits
		if hundredths(free) < hundredths(required) {
			availability = gen.Partial
		}
		out = append(out, judged{
			candidate: gen.PlanningCandidate{
				Person: personView(e), Availability: availability,
				FreeHours: roundHours(free), RequiredHours: roundHours(required),
				Fit: gen.PlanningFit{Score: 100, Lowered: []string{}}, Flags: flags, Missing: missing,
				Covers: availability == gen.Fits && len(flags) == 0 && len(missing) == 0,
			},
			free:   hundredths(free),
			wishes: len(wishFlags) > 0,
		})
	}
	return out
}

// rankCandidates completes and sorts the read's judged candidates, never
// nil:
//
//   - fit (S17.6): Staff's score for the booking of lo..hi — the request's
//     period cut to the bound — at hours a week, asked once for every
//     candidate; a person Staff leaves out of the answer scores 100 with
//     nothing lowered.
//   - the current project: the project of the person's confirmed row
//     covering today with the earliest from date, and the days since it.
//
// The sort is fits first, then the willing — a wishes flag — first (S17's
// second key), then fit score, free hours and days on the current project
// descending, then display name and id, so the order is total. current is
// each person's current row, as readBindings read it.
func (s *server) rankCandidates(ctx context.Context, c *callerContext, out []judged, lo, hi time.Time, hours float64,
	current map[int64]store.PlanningAllocation) ([]gen.PlanningCandidate, error) {
	ids := make([]int64, len(out))
	for i, j := range out {
		ids[i] = j.candidate.Person.Id
	}
	fits, err := s.workforceFits(ctx, ids, contracts.Booking{From: lo, To: hi, HoursPerWeek: &hours})
	if err != nil {
		return nil, outOfStaffBound(err, lo, c.Today, invalidRequestTitle)
	}
	projects, err := s.currentProjects(ctx, c.Today, current)
	if err != nil {
		return nil, err
	}
	for i := range out {
		j := &out[i]
		if f, ok := fits[j.candidate.Person.Id]; ok {
			j.candidate.Fit = gen.PlanningFit{Score: int32(f.Score), Lowered: append([]string{}, f.Lowered...)}
		}
		if p, ok := projects[j.candidate.Person.Id]; ok {
			j.candidate.CurrentProject, j.days = &p, p.Days
		}
	}
	slices.SortFunc(out, func(a, b judged) int {
		x, y := a.candidate, b.candidate
		return cmp.Or(
			cmp.Compare(availabilityRank(x.Availability), availabilityRank(y.Availability)),
			cmp.Compare(wishRank(a.wishes), wishRank(b.wishes)),
			cmp.Compare(y.Fit.Score, x.Fit.Score),
			cmp.Compare(b.free, a.free),
			cmp.Compare(b.days, a.days),
			strings.Compare(x.Person.DisplayName, y.Person.DisplayName),
			cmp.Compare(x.Person.Id, y.Person.Id),
		)
	})
	candidates := make([]gen.PlanningCandidate, len(out))
	for i, j := range out {
		candidates[i] = j.candidate
	}
	return candidates, nil
}

// availabilityRank sorts fits before partial.
func availabilityRank(a gen.PlanningCandidateAvailability) int {
	if a == gen.Fits {
		return 0
	}
	return 1
}

// wishRank sorts the willing — a wishes flag — before the rest.
func wishRank(wishes bool) int {
	if wishes {
		return 0
	}
	return 1
}

// of is S17.3 and S17.4 for personID against r, from the person's
// assignments: barred when one's terms name r's customer with a non-compete
// until r's first day or later; else a release_after_start flag for each
// whose earliest release (S15) is after r's first day — dated on the
// release, with the handover start and the row — and beside it
// needs_customer_approval when its terms say so, since only a row the person
// must be released from early needs the customer's word. The row that fills
// r itself binds nothing on r: it is the request's own booking.
func (b bindings) of(personID int64, r store.PlanningRequest) (bool, []gen.PlanningCandidateFlag) {
	first := civil(r.FromDate)
	var flags []gen.PlanningCandidateFlag
	for _, a := range b.assignments[personID] {
		row, t := a.PlanningAllocation, a.PlanningAllocationTerm
		if row.RequestID != nil && *row.RequestID == r.ID {
			continue
		}
		if b.customerID != nil && t.NonCompeteUntil.Valid && !civil(t.NonCompeteUntil).Before(first) &&
			slices.Contains(t.NonCompeteCustomerIds, *b.customerID) {
			return true, nil
		}
		release, handover := derivedDates(row, &t)
		if !release.After(first) {
			continue
		}
		flag := gen.PlanningCandidateFlag{Code: gen.PlanningCandidateFlagCodeReleaseAfterStart, Date: ptrTo(apiDate(release)), AllocationId: ptrTo(row.ID)}
		if handover != nil {
			flag.HandoverFrom = ptrTo(apiDate(*handover))
		}
		flags = append(flags, flag)
		if t.ReplacementNeedsApproval {
			flags = append(flags, gen.PlanningCandidateFlag{Code: gen.PlanningCandidateFlagCodeNeedsCustomerApproval, AllocationId: ptrTo(row.ID)})
		}
	}
	return false, flags
}

// wishFlags is S17.5 for personID against r: a wishes flag, with the kind
// and the wish's date, for each open wish from r's first day or before that
// is new, or off the person's current project.
func (b bindings) wishFlags(personID int64, r store.PlanningRequest) []gen.PlanningCandidateFlag {
	first := civil(r.FromDate)
	var flags []gen.PlanningCandidateFlag
	for _, w := range b.wishes[personID] {
		if civil(w.FromDate).After(first) {
			continue
		}
		kind := gen.PlanningWishKind(w.Kind)
		if kind == gen.Off {
			current, ok := b.current[personID]
			if !ok || w.ProjectID == nil || *w.ProjectID != current.ProjectID {
				continue
			}
		}
		flags = append(flags, gen.PlanningCandidateFlag{Code: gen.PlanningCandidateFlagCodeWishes, WishKind: &kind, Date: ptrTo(apiDate(civil(w.FromDate)))})
	}
	return flags
}

// competenceOf judges one person's held competences against requirements
// (S16, S17.1), both never nil: the keys of the requirements not met — a
// competence not held, or held below the requirement's minLevel — named
// from the catalogue, by id when it no longer answers it; and a
// certificate_expires flag per certificate requirement met only by
// certificates valid to a day before last, dated on the latest of them.
func competenceOf(requirements []store.PlanningRequestRequirement, catalogue map[int64]contracts.CompetenceEntry,
	held []contracts.HeldCompetence, last time.Time) ([]string, []gen.PlanningCandidateFlag) {
	missing, flags := []string{}, []gen.PlanningCandidateFlag{}
	for _, req := range requirements {
		var meeting []contracts.HeldCompetence
		for _, h := range held {
			if h.CompetenceID != req.CompetenceID {
				continue
			}
			if req.MinLevel != nil && (h.Level == nil || *h.Level < *req.MinLevel) {
				continue
			}
			meeting = append(meeting, h)
		}
		if len(meeting) == 0 {
			key := strconv.FormatInt(req.CompetenceID, 10)
			if e, ok := catalogue[req.CompetenceID]; ok {
				key = e.Key
			}
			missing = append(missing, key)
			continue
		}
		if expires, ok := expiresBefore(meeting, last); ok {
			key := meeting[0].Key
			flags = append(flags, gen.PlanningCandidateFlag{
				Code: gen.PlanningCandidateFlagCodeCertificateExpires, CompetenceKey: &key, Date: ptrTo(apiDate(expires)),
			})
		}
	}
	return missing, flags
}

// expiresBefore is the latest validTo of meeting when every one of them is
// a certificate valid only to a day before last — the flag
// certificate_expires — and false when one of them lasts the period, or is
// not a certificate (a skill does not expire).
func expiresBefore(meeting []contracts.HeldCompetence, last time.Time) (time.Time, bool) {
	var latest time.Time
	for _, h := range meeting {
		if h.Kind != certificateKind || h.ValidTo == nil || !h.ValidTo.Before(last) {
			return time.Time{}, false
		}
		latest = latestOf(latest, *h.ValidTo)
	}
	return latest, true
}

// currentProjects is each person's current project (S17) from first, their
// confirmed row covering today with the earliest from date (readBindings):
// the project named when the directory resolves it, and the days since that
// date. A person with no such row is absent. Nothing is asked for an empty
// map.
func (s *server) currentProjects(ctx context.Context, today time.Time, first map[int64]store.PlanningAllocation) (map[int64]gen.PlanningCurrentProject, error) {
	out := map[int64]gen.PlanningCurrentProject{}
	if len(first) == 0 {
		return out, nil
	}
	firsts := make([]store.PlanningAllocation, 0, len(first))
	for _, a := range first {
		firsts = append(firsts, a)
	}
	slices.SortFunc(firsts, func(a, b store.PlanningAllocation) int { return cmp.Compare(a.ID, b.ID) })
	projects, err := s.projectsOfRows(ctx, firsts)
	if err != nil {
		return nil, err
	}
	for id, a := range first {
		var project *contracts.ProjectEntry
		if p, ok := projects[a.ProjectID]; ok {
			project = &p
		}
		since := civil(a.FromDate)
		out[id] = gen.PlanningCurrentProject{
			Project: allocationResponse(a, nil, project, false).Project,
			Since:   apiDate(since),
			Days:    int32(daysBetween(since, today)),
		}
	}
	return out, nil
}
