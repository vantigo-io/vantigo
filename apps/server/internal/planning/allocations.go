package planning

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is an allocation as the contract answers it (S1, S5, rule 4) and
// the two reads of allocations: the list and the one row. Nothing of the
// person or the project is stored, so every response names them through the
// contracts, read before any transaction: the person through Workforce.People
// over the rows' window — a person Staff does not answer reads as an id with
// displayName null — and the project through the directory, an unresolved
// one by its id. The note is planning:manage's alone (S5).

// allocationResponse is a as the contract answers it: person and project
// named when Staff and the directory answered them (nil reads by id alone,
// rule 4), and the note only when withNote — the caller holds
// planning:manage. The derived dates and the terms are applyTerms' (S14,
// S15), which every response answering allocations calls after this.
func allocationResponse(a store.PlanningAllocation, person *contracts.PersonEntry, project *contracts.ProjectEntry, withNote bool) gen.PlanningAllocation {
	out := gen.PlanningAllocation{
		Id:       a.ID,
		Person:   gen.PlanningPersonRef{Id: a.PersonID},
		Project:  gen.PlanningProjectRef{Id: a.ProjectID},
		FromDate: apiDate(civil(a.FromDate)),
		ToDate:   apiDate(civil(a.ToDate)),
		// The row's end until applyTerms derives the earliest release from
		// its terms (S15).
		EarliestRelease: apiDate(civil(a.ToDate)),
		LoadUnit:        gen.PlanningLoadUnit(a.LoadUnit),
		Load:            loadOf(a.Load),
		Status:          gen.PlanningAllocationStatus(a.Status),
		Title:           a.Title,
		ConfirmedAt:     a.ConfirmedAt,
		RequestId:       a.RequestID,
		Revision:        a.Revision,
		CreatedAt:       a.CreatedAt,
		UpdatedAt:       a.UpdatedAt,
	}
	if person != nil {
		name := person.DisplayName
		out.Person.DisplayName = &name
		if person.EmployeeNumber != "" {
			number := person.EmployeeNumber
			out.Person.EmployeeNumber = &number
		}
	}
	if project != nil {
		code, name, status := project.Code, project.Name, gen.PlanningProjectStatus(project.Status)
		out.Project.Code, out.Project.Name, out.Project.Status = &code, &name, &status
	}
	if withNote {
		out.Note = a.Note
	}
	return out
}

// allocationNames is the people and the projects of some rows, read before
// any transaction: the people Staff answers over the rows' window, cut to
// the Workforce bound (a window wholly outside it names nobody), and the
// projects the directory resolves.
type allocationNames struct {
	people   map[int64]contracts.PersonEntry
	projects map[int32]contracts.ProjectEntry
}

// person is id's entry, nil when Staff did not answer them.
func (n allocationNames) person(id int64) *contracts.PersonEntry {
	if p, ok := n.people[id]; ok {
		return &p
	}
	return nil
}

// project is id's entry, nil when the directory did not resolve it.
func (n allocationNames) project(id int32) *contracts.ProjectEntry {
	if p, ok := n.projects[id]; ok {
		return &p
	}
	return nil
}

// namesOf reads the names of rows (rule 4).
func (s *server) namesOf(ctx context.Context, c *callerContext, rows []store.PlanningAllocation) (allocationNames, error) {
	names := allocationNames{people: map[int64]contracts.PersonEntry{}, projects: map[int32]contracts.ProjectEntry{}}
	if len(rows) == 0 {
		return names, nil
	}
	people, err := s.peopleOfRows(ctx, c, rows)
	if err != nil {
		return allocationNames{}, err
	}
	names.people = people
	var projectIDs []int32
	for _, r := range rows {
		projectIDs = append(projectIDs, r.ProjectID)
	}
	projects, err := s.projectEntries(ctx, slices.Compact(slices.Sorted(slices.Values(projectIDs))))
	if err != nil {
		return allocationNames{}, err
	}
	names.projects = projects
	return names, nil
}

// ListPlanningAllocations List allocations
// (GET /api/v1/planning/allocations)
//
// planning:view's (the contract's rule): the rows of a person, a project, a
// status and touching from..to, each filter optional, at most 500 by from
// date and id. The window filters rows and nothing else — the people are
// read over the rows' own window — so a past window lists history.
func (s *server) ListPlanningAllocations(ctx context.Context, req gen.ListPlanningAllocationsRequestObject) (gen.ListPlanningAllocationsResponseObject, error) {
	p := req.Params
	params := store.ListAllocationsFilteredParams{PersonID: p.PersonId, ProjectID: p.ProjectId, MaxRows: maxAllocationsListed}
	if p.From != nil {
		params.FromDate = dayParam(dayOf(*p.From))
	}
	if p.To != nil {
		params.ToDate = dayParam(dayOf(*p.To))
	}
	if p.From != nil && p.To != nil && dayOf(*p.To).Before(dayOf(*p.From)) {
		return gen.ListPlanningAllocations400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle,
			fieldErrors{errs: fieldError("to", "to must be on or after from")})), nil
	}
	if p.Status != nil {
		status := string(*p.Status)
		params.Status = &status
	}
	c, err := s.callerFor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.deps.Pool).ListAllocationsFiltered(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("planning: list allocations: %w", err)
	}
	names, err := s.namesOf(ctx, c, rows)
	if errors.Is(err, errDependencyUnavailable) {
		return gen.ListPlanningAllocations503ApplicationProblemPlusJSONResponse(dependencyUnavailable()), nil
	}
	if err != nil {
		return nil, err
	}
	out := gen.PlanningAllocationList{Allocations: make([]gen.PlanningAllocation, len(rows))}
	for i, r := range rows {
		out.Allocations[i] = allocationResponse(r, names.person(r.PersonID), names.project(r.ProjectID), c.Manage)
	}
	if err := s.applyTerms(ctx, out.Allocations, c.Manage); err != nil {
		return nil, err
	}
	return gen.ListPlanningAllocations200JSONResponse(out), nil
}

// GetPlanningAllocation Get an allocation
// (GET /api/v1/planning/allocations/{id})
//
// planning:view's; an unknown id is the bare 404.
func (s *server) GetPlanningAllocation(ctx context.Context, req gen.GetPlanningAllocationRequestObject) (gen.GetPlanningAllocationResponseObject, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return nil, err
	}
	row, found, err := s.allocation(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.GetPlanningAllocation404Response{}, nil
	}
	names, err := s.namesOf(ctx, c, []store.PlanningAllocation{row})
	if errors.Is(err, errDependencyUnavailable) {
		return gen.GetPlanningAllocation503ApplicationProblemPlusJSONResponse(dependencyUnavailable()), nil
	}
	if err != nil {
		return nil, err
	}
	out := []gen.PlanningAllocation{allocationResponse(row, names.person(row.PersonID), names.project(row.ProjectID), c.Manage)}
	if err := s.applyTerms(ctx, out, c.Manage); err != nil {
		return nil, err
	}
	return gen.GetPlanningAllocation200JSONResponse(out[0]), nil
}

// allocation reads one row outside any transaction; false when there is
// none.
func (s *server) allocation(ctx context.Context, id int64) (store.PlanningAllocation, bool, error) {
	row, err := store.New(s.deps.Pool).GetAllocation(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.PlanningAllocation{}, false, nil
	}
	if err != nil {
		return store.PlanningAllocation{}, false, fmt.Errorf("planning: read allocation %d: %w", id, err)
	}
	return row, true, nil
}

// lockedAllocation reads one row FOR UPDATE inside a write's transaction;
// an unknown id is the 404 refusal.
func lockedAllocation(ctx context.Context, txq *store.Queries, id int64) (store.PlanningAllocation, error) {
	row, err := txq.LockAllocation(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.PlanningAllocation{}, notFound()
	}
	if err != nil {
		return store.PlanningAllocation{}, fmt.Errorf("planning: lock allocation %d: %w", id, err)
	}
	return row, nil
}

// rowWindow is the whole weeks a row's period touches, Monday to Sunday.
func rowWindow(from, to time.Time) (time.Time, time.Time) { return mondayOf(from), sundayOf(to) }
