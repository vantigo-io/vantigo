package planning

import (
	"context"
	"errors"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is what the reads share (design S8–S13, S19): the window a
// request asks for, the 400 a window outside Staff's bound answers, and the
// shapes the contract answers people, projects, allocations and a week's
// figures in.

// windowOf is a request's window: from and to as asked, each defaulting —
// from to defaultFrom, to to defaultTo of the from in force — and a field
// error on to when it is before from.
func windowOf(from, to *openapi_types.Date, defaultFrom time.Time, defaultTo func(from time.Time) time.Time) (time.Time, time.Time, fieldErrors) {
	var f fieldErrors
	start := defaultFrom
	if from != nil {
		start = dayOf(*from)
	}
	end := defaultTo(start)
	if to != nil {
		end = dayOf(*to)
	}
	if end.Before(start) {
		f.add("to", "to must be on or after from")
	}
	return start, end, f
}

// windowOrInvalid is err as a read answers it: Staff's refusal of a window
// outside its bound (contracts.ErrWindowOutOfBounds) as the 400
// window_out_of_bounds (§13 amendment 8), on from when from is before the
// bound's near end — the Monday of the week thirteen weeks before today
// (amendment 14), named here only to say which field to blame — else on to.
// Any other error is answered as it is.
func windowOrInvalid(err error, from, today time.Time) error {
	if !errors.Is(err, contracts.ErrWindowOutOfBounds) {
		return err
	}
	field := "to"
	if from.Before(addDays(mondayOf(today), -13*7)) {
		field = "from"
	}
	var f fieldErrors
	f.addCoded(field, codeWindowOutOfBounds,
		"The window must lie between the Monday thirteen weeks before today and three years after today")
	return refuseInvalid(invalidQueryTitle, f)
}

// readProblem sorts a read's error into what its handler answers: the
// refusal's status and problem (400 or 404), 503 for a contract not composed,
// or 0 for a fault the handler returns as it is.
func readProblem(err error) (int, gen.PlanningProblem) {
	if r, ok := asRefusal(err); ok {
		return r.status, r.problem
	}
	if errors.Is(err, errDependencyUnavailable) {
		return http.StatusServiceUnavailable, dependencyUnavailable()
	}
	return 0, gen.PlanningProblem{}
}

// spansView is spans as the contract answers them: never null.
func spansView(spans []contracts.DateSpan) []gen.PlanningDateSpan {
	out := make([]gen.PlanningDateSpan, 0, len(spans))
	for _, s := range spans {
		out = append(out, gen.PlanningDateSpan{From: apiDate(s.From), To: apiDate(s.To)})
	}
	return out
}

// personView is a person as Staff answered them (S8): the work group, the
// department when there is one, and the days of the window in employment.
func personView(p contracts.PersonEntry) gen.PlanningPerson {
	out := gen.PlanningPerson{
		Id: p.ID, DisplayName: p.DisplayName, EmployeeNumber: p.EmployeeNumber, Employed: spansView(p.Employed),
	}
	if p.DepartmentID != nil {
		out.Department = &gen.PlanningDepartmentRef{Id: *p.DepartmentID, Name: p.DepartmentName}
	}
	return out
}

// projectView is a directory entry as the contract answers it (S9): never
// its currency or bill rate, the customer named when the customer directory
// resolves it.
func projectView(e contracts.ProjectEntry, customers map[int32]string) gen.PlanningProject {
	out := gen.PlanningProject{Id: e.ID, Code: e.Code, Name: e.Name, Status: gen.PlanningProjectStatus(e.Status)}
	if e.CustomerID != nil {
		ref := gen.PlanningCustomerRef{Id: *e.CustomerID}
		if name, ok := customers[*e.CustomerID]; ok {
			ref.Name = &name
		}
		out.Customer = &ref
	}
	if e.StartDate != nil {
		d := apiDate(*e.StartDate)
		out.StartDate = &d
	}
	if e.EndDate != nil {
		d := apiDate(*e.EndDate)
		out.EndDate = &d
	}
	return out
}

// customersOf is the names of the customers of entries (S9), read through
// the customer directory.
func (s *server) customersOf(ctx context.Context, entries []contracts.ProjectEntry) (map[int32]string, error) {
	var ids []int32
	for _, e := range entries {
		if e.CustomerID != nil {
			ids = append(ids, *e.CustomerID)
		}
	}
	return s.customerNames(ctx, ids)
}

// projectsOfRows is the directory entries of the projects rows are on, by
// id; a project the directory does not resolve is absent (rule 4).
func (s *server) projectsOfRows(ctx context.Context, rows []store.PlanningAllocation) (map[int32]contracts.ProjectEntry, error) {
	seen := map[int32]bool{}
	var ids []int32
	for _, a := range rows {
		if !seen[a.ProjectID] {
			seen[a.ProjectID] = true
			ids = append(ids, a.ProjectID)
		}
	}
	return s.projectEntries(ctx, ids)
}

// peopleByID is people by id, for naming the rows' persons.
func peopleByID(people []contracts.PersonEntry) map[int64]*contracts.PersonEntry {
	out := make(map[int64]*contracts.PersonEntry, len(people))
	for i := range people {
		out[people[i].ID] = &people[i]
	}
	return out
}

// allocationsView is rows as the contract answers them, each person and
// project named when Staff and the directory answer them (rule 4), the note
// for planning:manage alone (S5), through allocations.go's allocationResponse.
// Never null.
func allocationsView(rows []store.PlanningAllocation, people map[int64]*contracts.PersonEntry, projects map[int32]contracts.ProjectEntry, withNote bool) []gen.PlanningAllocation {
	out := make([]gen.PlanningAllocation, 0, len(rows))
	for _, a := range rows {
		var project *contracts.ProjectEntry
		if e, ok := projects[a.ProjectID]; ok {
			project = &e
		}
		out = append(out, allocationResponse(a, people[a.PersonID], project, withNote))
	}
	return out
}

// weekFiguresView is one week of the capacity calculation (S6) as the
// contract answers it.
func weekFiguresView(w weekFigures) gen.PlanningWeekFigures {
	return gen.PlanningWeekFigures{
		Monday: apiDate(w.Monday), Capacity: w.Capacity, Available: w.Available, Booked: w.Booked,
		Tentative: w.Tentative, Free: w.Free, LoadPercent: w.LoadPercent, Away: w.Away,
	}
}
