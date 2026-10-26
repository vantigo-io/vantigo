package planning

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The board (design S10): people against weeks, each cell the week's figures
// of the capacity calculation (S6), with the allocations touching the window
// for the bars.

const (
	// boardDefaultWeeks is the board's window when the request names no to:
	// the week of from and the eleven after it.
	boardDefaultWeeks = 12
	// boardMaxWeeks is the most weeks the board answers (S10).
	boardMaxWeeks = 53
)

// GetPlanningBoard The board
// (GET /api/v1/planning/board)
//
// planning:view. groupBy people (the default) answers a row per person;
// groupBy project a row per project, the sums over its people's rows of it,
// each followed by its people's rows of it.
func (s *server) GetPlanningBoard(ctx context.Context, req gen.GetPlanningBoardRequestObject) (gen.GetPlanningBoardResponseObject, error) {
	out, err := s.board(ctx, req.Params)
	switch status, problem := readProblem(err); status {
	case http.StatusBadRequest:
		return gen.GetPlanningBoard400ApplicationProblemPlusJSONResponse(problem), nil
	case http.StatusServiceUnavailable:
		return gen.GetPlanningBoard503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningBoard200JSONResponse(out), nil
}

// board builds GET /board's answer. Every contract call — the window's
// people and capacity, the competences held, the projects and their
// customers — is made before anything is shaped, and no transaction opens.
func (s *server) board(ctx context.Context, p gen.GetPlanningBoardParams) (gen.PlanningBoard, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningBoard{}, err
	}
	from, to, f := windowOf(p.From, p.To, mondayOf(c.Today), func(from time.Time) time.Time {
		return addDays(mondayOf(from), boardDefaultWeeks*7-1)
	})
	if !f.failed() && len(mondaysOf(from, to)) > boardMaxWeeks {
		f.add("to", fmt.Sprintf("The board answers at most %d weeks", boardMaxWeeks))
	}
	if f.failed() {
		return gen.PlanningBoard{}, refuseInvalid(invalidQueryTitle, f)
	}
	w, err := s.loadWindow(ctx, from, to, nil)
	if err != nil {
		return gen.PlanningBoard{}, windowOrInvalid(err, from, c.Today)
	}

	people := slices.DeleteFunc(slices.Clone(w.People), func(e contracts.PersonEntry) bool {
		return !inDepartment(e, p.DepartmentId)
	})
	if p.CompetenceId != nil {
		// Held is judged on the later of from and today (amendment 33), so a
		// board that begins last Monday keeps who holds it from today.
		held, err := s.workforceHeld(ctx, latestOf(from, c.Today), personIDsOf(people))
		if err != nil {
			return gen.PlanningBoard{}, err
		}
		people = slices.DeleteFunc(people, func(e contracts.PersonEntry) bool {
			return !slices.ContainsFunc(held[e.ID], func(h contracts.HeldCompetence) bool { return h.CompetenceID == *p.CompetenceId })
		})
	}
	projects, err := s.projectsOfRows(ctx, w.Allocations)
	if err != nil {
		return gen.PlanningBoard{}, err
	}

	includeTentative := p.IncludeTentative == nil || *p.IncludeTentative
	includePlanned := p.IncludePlanned == nil || *p.IncludePlanned
	keep := func(a store.PlanningAllocation) bool {
		if p.ProjectId != nil && a.ProjectID != *p.ProjectId {
			return false
		}
		if !includeTentative && a.Status == statusTentative {
			return false
		}
		if e, ok := projects[a.ProjectID]; ok && !includePlanned && e.Status == string(gen.Planned) {
			return false
		}
		return true
	}
	var listed []store.PlanningAllocation
	people = slices.DeleteFunc(people, func(e contracts.PersonEntry) bool {
		rows := slices.DeleteFunc(w.rowsOf(e.ID), func(a store.PlanningAllocation) bool { return !keep(a) })
		listed = append(listed, rows...)
		return p.ProjectId != nil && len(rows) == 0
	})

	out := gen.PlanningBoard{
		From: apiDate(from), To: apiDate(to), Weeks: []gen.PlanningWeek{}, Rows: []gen.PlanningBoardRow{},
		Allocations: allocationsView(listed, peopleByID(people), projects, c.Manage),
	}
	if err := s.applyTerms(ctx, out.Allocations, c.Manage); err != nil {
		return gen.PlanningBoard{}, err
	}
	for _, m := range mondaysOf(from, to) {
		out.Weeks = append(out.Weeks, gen.PlanningWeek{Monday: apiDate(m)})
	}
	if p.GroupBy != nil && *p.GroupBy == gen.Project {
		var entries []contracts.ProjectEntry
		for _, e := range projects {
			entries = append(entries, e)
		}
		customers, err := s.customersOf(ctx, entries)
		if err != nil {
			return gen.PlanningBoard{}, err
		}
		out.Rows = projectRows(w, people, listed, projects, customers, includeTentative, keep)
		return out, nil
	}
	for _, e := range people {
		out.Rows = append(out.Rows, gen.PlanningBoardRow{
			Person: ptrTo(personView(e)), Cells: cellsOf(capacityWeeks(w.inputOf(e.ID, includeTentative, keep))),
		})
	}
	return out, nil
}

// projectRows is the board grouped by project: per project of the listed
// rows, by code, a row with the sums of its people's figures on it, then a
// row per person on it — capacity and available the person's, booked and
// tentative their rows of that project, free what that leaves. A project the
// directory does not resolve has no row: the contract names a project row by
// its code, name and status, which it does not have.
func projectRows(w *window, people []contracts.PersonEntry, listed []store.PlanningAllocation, projects map[int32]contracts.ProjectEntry,
	customers map[int32]string, includeTentative bool, keep func(store.PlanningAllocation) bool,
) []gen.PlanningBoardRow {
	var ids []int32
	for _, a := range listed {
		if _, ok := projects[a.ProjectID]; ok && !slices.Contains(ids, a.ProjectID) {
			ids = append(ids, a.ProjectID)
		}
	}
	slices.SortFunc(ids, func(a, b int32) int {
		return cmp.Or(strings.Compare(projects[a].Code, projects[b].Code), cmp.Compare(a, b))
	})
	rows := []gen.PlanningBoardRow{}
	for _, id := range ids {
		project := projectView(projects[id], customers)
		onProject := func(a store.PlanningAllocation) bool { return a.ProjectID == id && keep(a) }
		var under []gen.PlanningBoardRow
		var weeks [][]weekFigures
		for _, e := range people {
			if !slices.ContainsFunc(w.rowsOf(e.ID), onProject) {
				continue
			}
			figures := capacityWeeks(w.inputOf(e.ID, includeTentative, onProject))
			weeks = append(weeks, figures)
			under = append(under, gen.PlanningBoardRow{Person: ptrTo(personView(e)), Project: &project, Cells: cellsOf(figures)})
		}
		rows = append(rows, gen.PlanningBoardRow{Project: &project, Cells: cellsOf(sumWeeks(weeks))})
		rows = append(rows, under...)
	}
	return rows
}

// sumWeeks is the week-by-week sums of several people's figures over one
// window — each the same weeks in the same order — loadPercent recomputed
// over the sums and away whether anyone is away that week.
func sumWeeks(people [][]weekFigures) []weekFigures {
	if len(people) == 0 {
		return nil
	}
	out := slices.Clone(people[0])
	for _, figures := range people[1:] {
		for i, w := range figures {
			s := &out[i]
			s.Capacity += w.Capacity
			s.Available += w.Available
			s.Booked += w.Booked
			s.Tentative += w.Tentative
			s.Free += w.Free
			s.Away = s.Away || w.Away
		}
	}
	for i := range out {
		s := &out[i]
		s.Capacity, s.Available, s.Booked = roundHours(s.Capacity), roundHours(s.Available), roundHours(s.Booked)
		s.Tentative, s.Free = roundHours(s.Tentative), roundHours(s.Free)
		s.LoadPercent = nil
		if s.Capacity > 0 {
			load := roundHours(s.Booked / s.Capacity * 100)
			s.LoadPercent = &load
		}
	}
	return out
}

// cellsOf is weeks as the board's cells: never null.
func cellsOf(weeks []weekFigures) []gen.PlanningWeekFigures {
	out := make([]gen.PlanningWeekFigures, 0, len(weeks))
	for _, w := range weeks {
		out = append(out, weekFiguresView(w))
	}
	return out
}

// personIDsOf is people's ids, in order.
func personIDsOf(people []contracts.PersonEntry) []int64 {
	ids := make([]int64, len(people))
	for i, e := range people {
		ids[i] = e.ID
	}
	return ids
}

// ptrTo is a pointer to a copy of v.
func ptrTo[T any](v T) *T { return &v }
