package planning

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// A project's plan (design S11): the entry, its allocations in the window,
// and per week its booked and tentative hours — each person's rows of it over
// that person's capacity (S6) — and its open demand, 0 until the requests of
// delivery B.

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
		Weeks:       make([]gen.PlanningProjectPlanWeek, 0, len(mondays)),
	}
	for i, m := range mondays {
		out.Weeks = append(out.Weeks, gen.PlanningProjectPlanWeek{
			Monday: apiDate(m), Booked: roundHours(booked[i]), Tentative: roundHours(tentative[i]), Demand: 0,
		})
	}
	return out, nil
}
