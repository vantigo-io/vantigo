package planning

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The intake (design S18): the pipeline — every project in status planned,
// by code, with its open and filled requests judged for coverage and its
// hours a week. on-hold projects are not the pipeline (reading 10), and a
// withdrawn request is not demand. The coverage is judged over the same
// candidates GET /requests/{id}/candidates answers (candidates.go), without
// includePartial: a candidate is a person employed through the period who
// meets every requirement (S17.1 excludes anyone missing one), so a request
// nobody meets is uncovered, not partly covered. The candidates themselves
// are planning:manage's; the intake answers how many cover.

// GetPlanningIntake Intake
// (GET /api/v1/planning/intake)
//
// planning:view's.
func (s *server) GetPlanningIntake(ctx context.Context, _ gen.GetPlanningIntakeRequestObject) (gen.GetPlanningIntakeResponseObject, error) {
	out, err := s.intake(ctx)
	if status, problem := readProblem(err); status == http.StatusServiceUnavailable {
		return gen.GetPlanningIntake503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningIntake200JSONResponse(out), nil
}

// intake builds GET /intake's answer: the planned projects from the
// directory, their open and filled requests and their rows from the
// module's own tables, then per request its candidates.
func (s *server) intake(ctx context.Context) (gen.PlanningIntake, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningIntake{}, err
	}
	out := gen.PlanningIntake{Projects: []gen.PlanningIntakeProject{}}
	entries, err := s.projectsByStatus(ctx, []string{string(gen.Planned)})
	if err != nil {
		return gen.PlanningIntake{}, err
	}
	if len(entries) == 0 {
		return out, nil
	}
	ids := make([]int32, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	q := store.New(s.deps.Pool)
	rows, err := q.RequestsOfProjects(ctx, store.RequestsOfProjectsParams{ProjectIds: ids, Statuses: []string{requestOpen, requestFilled}})
	if err != nil {
		return gen.PlanningIntake{}, fmt.Errorf("planning: read the planned projects' requests: %w", err)
	}
	allocations, err := q.AllocationsOfProjects(ctx, ids)
	if err != nil {
		return gen.PlanningIntake{}, fmt.Errorf("planning: read the planned projects' allocations: %w", err)
	}
	views, requirements, catalogue, err := s.requestsRead(ctx, c, rows)
	if err != nil {
		return gen.PlanningIntake{}, err
	}
	customers := map[int32]*int32{}
	for _, e := range entries {
		customers[e.ID] = e.CustomerID
	}
	candidates, err := s.intakeCandidates(ctx, c, rows, requirements, catalogue, customers)
	if err != nil {
		return gen.PlanningIntake{}, err
	}
	hours, err := s.rowHoursPerWeek(ctx, c, allocations)
	if err != nil {
		return gen.PlanningIntake{}, err
	}
	names, err := s.customersOf(ctx, entries)
	if err != nil {
		return gen.PlanningIntake{}, err
	}

	byProject := map[int32]*gen.PlanningIntakeProject{}
	for _, e := range entries {
		out.Projects = append(out.Projects, gen.PlanningIntakeProject{Project: projectView(e, names), Requests: []gen.PlanningIntakeRequest{}})
	}
	for i := range out.Projects {
		byProject[out.Projects[i].Project.Id] = &out.Projects[i]
	}
	for i, r := range rows {
		p := byProject[r.ProjectID]
		p.Requests = append(p.Requests, intakeRequest(r, views[i], candidates[r.ID]))
		if r.Status == requestOpen {
			p.RequestedHoursPerWeek += loadOf(r.HoursPerWeek)
		}
	}
	for i, a := range allocations {
		p := byProject[a.ProjectID]
		switch a.Status {
		case statusConfirmed:
			p.ConfirmedHoursPerWeek += hours[i]
		case statusTentative:
			p.TentativeHoursPerWeek += hours[i]
		}
	}
	for i := range out.Projects {
		p := &out.Projects[i]
		p.RequestedHoursPerWeek = roundHours(p.RequestedHoursPerWeek)
		p.TentativeHoursPerWeek = roundHours(p.TentativeHoursPerWeek)
		p.ConfirmedHoursPerWeek = roundHours(p.ConfirmedHoursPerWeek)
	}
	return out, nil
}

// intakeCandidates is each of rows' candidates as the intake judges them
// (judgeCandidates, without includePartial), by request id, from ONE read
// of Staff: the window over the union of the rows' periods cut to the bound
// (loadWindow once), and the competences held on each distinct later of a
// request's first day and today (workforceHeld once per such day — once on
// today when every first day is past) for the people any request of that
// day keeps. Each request is then judged on its own period cut to the
// bound, lo..hi, out of the shared window, exactly as its candidates read
// judges it over a window of its own. The bindings (S17.3–5) are read once
// for every person kept, from the earliest first day of the requests judged,
// and each request judged against its own project's customer (customers, by
// project id). No fit and no current project's name is asked: coverage reads
// neither.
//
// A request with no day inside the Workforce bound has no candidates Staff
// can answer about, so it is judged on none (absent) rather than refusing
// the whole intake, which has no 400; so is every request when Staff
// refuses the shared window as outside its bound (its today another day
// than this module's, at midnight).
func (s *server) intakeCandidates(ctx context.Context, c *callerContext, rows []store.PlanningRequest,
	requirements map[int64][]store.PlanningRequestRequirement, catalogue map[int64]contracts.CompetenceEntry,
	customers map[int32]*int32) (map[int64][]judged, error) {
	out := map[int64][]judged{}
	type cut struct{ lo, hi time.Time }
	cuts := map[int64]cut{}
	var lo, hi time.Time
	for _, r := range rows {
		l, h, ok := clipToBound(civil(r.FromDate), civil(r.ToDate), c.Today)
		if !ok {
			continue
		}
		if len(cuts) == 0 {
			lo, hi = l, h
		}
		lo, hi = earliestOf(lo, l), latestOf(hi, h)
		cuts[r.ID] = cut{l, h}
	}
	if len(cuts) == 0 {
		return out, nil
	}
	w, err := s.loadWindow(ctx, lo, hi, nil)
	if errors.Is(err, contracts.ErrWindowOutOfBounds) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}

	people := map[int64][]contracts.PersonEntry{}
	var days []string // the distinct days held is asked on, in first-seen order
	idsOn := map[string][]int64{}
	var everyone []int64
	var first time.Time
	for _, r := range rows {
		k, ok := cuts[r.ID]
		if !ok {
			continue
		}
		if first.IsZero() || civil(r.FromDate).Before(first) {
			first = civil(r.FromDate)
		}
		people[r.ID] = employedOver(w.People, civil(r.FromDate), civil(r.ToDate), k.lo, k.hi)
		everyone = append(everyone, personIDsOf(people[r.ID])...)
		day := latestOf(civil(r.FromDate), c.Today).Format(time.DateOnly)
		if _, seen := idsOn[day]; !seen {
			days = append(days, day)
		}
		idsOn[day] = append(idsOn[day], personIDsOf(people[r.ID])...)
	}
	heldOn := map[string]map[int64][]contracts.HeldCompetence{}
	for _, day := range days {
		on, _ := time.Parse(time.DateOnly, day)
		held, err := s.workforceHeld(ctx, on, slices.Compact(slices.Sorted(slices.Values(idsOn[day]))))
		if err != nil {
			return nil, err
		}
		heldOn[day] = held
	}
	b, err := s.readBindings(ctx, c.Today, first, slices.Compact(slices.Sorted(slices.Values(everyone))))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		k, ok := cuts[r.ID]
		if !ok {
			continue
		}
		held := heldOn[latestOf(civil(r.FromDate), c.Today).Format(time.DateOnly)]
		b.customerID = customers[r.ProjectID]
		out[r.ID] = judgeCandidates(w, people[r.ID], held, r, k.lo, k.hi, requirements[r.ID], catalogue, b, false)
	}
	return out, nil
}

// intakeRequest judges one request's coverage (S18) from its candidates:
// covered when it is filled or at least one candidate covers it — fits, no
// flag, nothing missing — partly_covered when there are candidates meeting
// every requirement and none covers (each partial or flagged), uncovered
// when nobody employed through the period meets every requirement;
// candidates is how many cover it; key_person flags an open request exactly
// one person covers — a filled request has its person.
func intakeRequest(r store.PlanningRequest, view gen.PlanningRequest, candidates []judged) gen.PlanningIntakeRequest {
	covering := 0
	for _, k := range candidates {
		if k.candidate.Covers {
			covering++
		}
	}
	out := gen.PlanningIntakeRequest{Request: view, Candidates: int32(covering), Flags: []gen.PlanningIntakeRequestFlags{}}
	switch {
	case r.Status == requestFilled || covering > 0:
		out.Coverage = gen.Covered
	case len(candidates) > 0:
		out.Coverage = gen.PartlyCovered
	default:
		out.Coverage = gen.Uncovered
	}
	if r.Status == requestOpen && covering == 1 {
		out.Flags = append(out.Flags, gen.KeyPerson)
	}
	return out
}

// rowHoursPerWeek is each of rows' hours a week, in rows' order (plan
// reading 4): an hours row its load; a percent row its share of the person's
// hours a week in force on its first day — the first day inside the
// Workforce bound for a row that started before it, the earliest day Staff
// answers about — 0 when Staff answers the person no capacity there, or no
// day of the row lies inside the bound. The capacity is read once, through
// Workforce.People and Workforce.Capacity over the percent rows' span cut to
// the bound — the rows' people Staff answers there, no allocation read —
// and nothing is asked when there is none.
func (s *server) rowHoursPerWeek(ctx context.Context, c *callerContext, rows []store.PlanningAllocation) ([]float64, error) {
	out := make([]float64, len(rows))
	var percent []store.PlanningAllocation
	for i, a := range rows {
		if a.LoadUnit == loadUnitHours {
			out[i] = loadOf(a.Load)
			continue
		}
		percent = append(percent, a)
	}
	if len(percent) == 0 {
		return out, nil
	}
	from, to := civil(percent[0].FromDate), civil(percent[0].ToDate)
	personIDs := []int64{}
	for _, a := range percent {
		from, to = earliestOf(from, civil(a.FromDate)), latestOf(to, civil(a.ToDate))
		personIDs = append(personIDs, a.PersonID)
	}
	lo, hi, ok := clipToBound(from, to, c.Today)
	if !ok {
		return out, nil
	}
	people, err := s.workforcePeople(ctx, lo, hi)
	if errors.Is(err, contracts.ErrWindowOutOfBounds) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	answered := []int64{}
	for _, p := range people {
		if slices.Contains(personIDs, p.ID) {
			answered = append(answered, p.ID)
		}
	}
	capacity := map[int64]contracts.PersonCapacity{}
	if len(answered) > 0 {
		capacity, err = s.workforceCapacity(ctx, lo, hi, answered)
		if errors.Is(err, contracts.ErrWindowOutOfBounds) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
	for i, a := range rows {
		if a.LoadUnit != loadUnitPercent {
			continue
		}
		if first, _, inside := clipToBound(civil(a.FromDate), civil(a.ToDate), c.Today); inside {
			out[i] = loadOf(a.Load) / 100 * hoursPerWeekOn(capacity[a.PersonID].Segments, first)
		}
	}
	return out, nil
}
