package planning

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// A project's plan (design S11): the entry, its allocations and its open and
// filled requests in the window, and per week its booked and tentative
// hours — each person's rows of it over that person's capacity (S6) — and
// its open demand, the hours its open requests ask for (demandOf).

// GetPlanningProjectPlan A project's plan
// (GET /api/v1/planning/projects/{projectId}/plan)
//
// planning:view. An id the directory does not resolve is the bare 404.
func (s *server) GetPlanningProjectPlan(ctx context.Context, req gen.GetPlanningProjectPlanRequestObject) (gen.GetPlanningProjectPlanResponseObject, error) {
	out, err := s.projectPlan(ctx, req.ProjectId, req.Params)
	switch status, problem := readProblem(err); status {
	case http.StatusBadRequest:
		return gen.GetPlanningProjectPlan400ApplicationProblemPlusJSONResponse(problem), nil
	case http.StatusNotFound:
		return gen.GetPlanningProjectPlan404Response{}, nil
	case http.StatusServiceUnavailable:
		return gen.GetPlanningProjectPlan503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningProjectPlan200JSONResponse(out), nil
}

// projectPlan builds GET /projects/{projectId}/plan's answer: the project's
// rows read first, from the module's own table, then the window of the
// people on them — so a person Staff does not answer keeps their row, by id,
// weighing nothing in the weeks (rule 4).
func (s *server) projectPlan(ctx context.Context, projectID int32, p gen.GetPlanningProjectPlanParams) (gen.PlanningProjectPlan, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningProjectPlan{}, err
	}
	from, to, f := windowOf(p.From, p.To, mondayOf(c.Today), func(from time.Time) time.Time {
		return addDays(mondayOf(from), boardDefaultWeeks*7-1)
	})
	if f.failed() {
		return gen.PlanningProjectPlan{}, refuseInvalid(invalidQueryTitle, f)
	}
	entry, err := s.projectEntry(ctx, projectID)
	if err != nil {
		return gen.PlanningProjectPlan{}, err
	}
	if entry == nil {
		return gen.PlanningProjectPlan{}, notFound()
	}
	rows, err := store.New(s.deps.Pool).ProjectAllocationsInWindow(ctx, store.ProjectAllocationsInWindowParams{
		ProjectID: projectID, FromDate: dayParam(from), ToDate: dayParam(to),
	})
	if err != nil {
		return gen.PlanningProjectPlan{}, fmt.Errorf("planning: read the plan of project %d: %w", projectID, err)
	}
	personIDs := []int64{}
	for _, a := range rows {
		personIDs = append(personIDs, a.PersonID)
	}
	w, err := s.loadWindow(ctx, from, to, personIDs)
	if err != nil {
		return gen.PlanningProjectPlan{}, windowOrInvalid(err, from, c.Today)
	}
	customers, err := s.customersOf(ctx, []contracts.ProjectEntry{*entry})
	if err != nil {
		return gen.PlanningProjectPlan{}, err
	}
	requestRows, err := store.New(s.deps.Pool).RequestsOfProjectInWindow(ctx, store.RequestsOfProjectInWindowParams{
		ProjectID: projectID, FromDate: dayParam(from), ToDate: dayParam(to),
	})
	if err != nil {
		return gen.PlanningProjectPlan{}, fmt.Errorf("planning: read the requests of project %d: %w", projectID, err)
	}
	requests, err := s.requestsOf(ctx, c, requestRows)
	if err != nil {
		return gen.PlanningProjectPlan{}, err
	}

	mondays := mondaysOf(from, to)
	booked, tentative := make([]float64, len(mondays)), make([]float64, len(mondays))
	onProject := func(a store.PlanningAllocation) bool { return a.ProjectID == projectID }
	for _, e := range w.People {
		for i, week := range capacityWeeks(w.inputOf(e.ID, true, onProject)) {
			booked[i] += week.Booked
			tentative[i] += week.Tentative
		}
	}
	out := gen.PlanningProjectPlan{
		Project:     projectView(*entry, customers),
		Allocations: allocationsView(rows, peopleByID(w.People), map[int32]contracts.ProjectEntry{projectID: *entry}, c.Manage),
		Requests:    requests,
		Weeks:       make([]gen.PlanningProjectPlanWeek, 0, len(mondays)),
	}
	if err := s.applyTerms(ctx, out.Allocations, c.Manage); err != nil {
		return gen.PlanningProjectPlan{}, err
	}
	for i, m := range mondays {
		out.Weeks = append(out.Weeks, gen.PlanningProjectPlanWeek{
			Monday: apiDate(m), Booked: roundHours(booked[i]), Tentative: roundHours(tentative[i]),
			Demand: roundHours(demandOf(requestRows, latestOf(m, from), earliestOf(addDays(m, 6), to))),
		})
	}
	return out, nil
}

// demandOf is the open demand of one week's days from..to (S11, S16, plan
// reading 5): over the open requests, each one's hours a week × the
// business days of from..to inside its period / 5 — Monday to Friday less
// the public holidays (holidays.BusinessDay), the days an hours row books on
// (capacity.go's rawDays) — so a request starting on a Wednesday weighs 3/5
// of its hours in its first week, and one over the week of Christmas Day
// 4/5, exactly as an hours row books. A filled request is booked through its
// row, and a withdrawn one is no demand. from..to is the week cut to the
// plan's window, as the week's booked and tentative are.
func demandOf(requests []store.PlanningRequest, from, to time.Time) float64 {
	total := 0.0
	for _, r := range requests {
		if r.Status != requestOpen {
			continue
		}
		days := 0
		for day := latestOf(from, civil(r.FromDate)); !day.After(earliestOf(to, civil(r.ToDate))); day = addDays(day, 1) {
			if holidays.BusinessDay(day) {
				days++
			}
		}
		total += loadOf(r.HoursPerWeek) * float64(days) / workdaysPerWeek
	}
	return total
}
