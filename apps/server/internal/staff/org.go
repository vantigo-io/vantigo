package staff

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is who manages whom, read from the database (design S6, §4
// Managers): the manager graph loaded for terms_derived.go's pure walks, the
// cycle check every write of an edge ends with, and GET /people/{id}/org —
// the chain up and the direct reports, as the work group names them.

// loadManagerGraph is the manager graph of personIDs — their employments and
// terms — with every department, which a climb may cross, and the
// employments of everyone their terms or a department name as manager, whom
// effectiveManager asks whether they are in employment that day; nil
// personIDs is everyone's. Read under the graph's lock it is the graph a cycle check
// judges; read outside it, a snapshot for the audience and the org view.
func loadManagerGraph(ctx context.Context, q *store.Queries, personIDs []int64) (managerGraph, error) {
	g, _, err := loadManagerGraphAndDepartments(ctx, q, personIDs)
	return g, err
}

// loadManagerGraphAndDepartments is loadManagerGraph with the departments'
// rows it read, for a caller that names them too.
func loadManagerGraphAndDepartments(ctx context.Context, q *store.Queries, personIDs []int64) (managerGraph, []store.StaffDepartment, error) {
	employments, err := q.GraphEmployments(ctx, personIDs)
	if err != nil {
		return managerGraph{}, nil, fmt.Errorf("staff: read the employments of the manager graph: %w", err)
	}
	terms, err := q.GraphTerms(ctx, personIDs)
	if err != nil {
		return managerGraph{}, nil, fmt.Errorf("staff: read the terms of the manager graph: %w", err)
	}
	departments, err := q.ListDepartments(ctx)
	if err != nil {
		return managerGraph{}, nil, fmt.Errorf("staff: read the departments of the manager graph: %w", err)
	}
	if personIDs != nil {
		var named []int64
		add := func(m *int64) {
			if m != nil && !slices.Contains(personIDs, *m) && !slices.Contains(named, *m) {
				named = append(named, *m)
			}
		}
		for _, t := range terms {
			add(t.ManagerPersonID)
		}
		for _, d := range departments {
			add(d.ManagerPersonID)
		}
		if len(named) > 0 {
			more, err := q.GraphEmployments(ctx, named)
			if err != nil {
				return managerGraph{}, nil, fmt.Errorf("staff: read the employments of the managers in the graph: %w", err)
			}
			employments = append(employments, more...)
		}
	}
	ge := make([]graphEmployment, 0, len(employments))
	for _, e := range employments {
		ge = append(ge, graphEmployment{ID: e.ID, PersonID: e.PersonID, BusinessUnitID: e.BusinessUnitID, Start: civil(e.StartDate), End: civilPtr(e.EndDate)})
	}
	gt := make([]graphTerms, 0, len(terms))
	for _, t := range terms {
		gt = append(gt, graphTerms{
			EmploymentID: t.EmploymentID, ValidFrom: civil(t.ValidFrom), DepartmentID: t.DepartmentID, Manager: t.ManagerPersonID,
			Title: t.Title, LocationID: t.LocationID,
		})
	}
	gd := make([]graphDepartment, 0, len(departments))
	for _, d := range departments {
		gd = append(gd, graphDepartment{ID: d.ID, Manager: d.ManagerPersonID, ParentID: d.ParentID})
	}
	return newManagerGraph(ge, gt, gd), departments, nil
}

// checkManagerCycle is the rule every write of an edge ends with (design §4
// Managers): with the write applied, personID is on no loop of the manager
// graph on from or on any later day the graph changes. It runs under the
// manager graph's lock, which the write took after the person's, so the
// graph it reads is the one every other such write will see.
func checkManagerCycle(ctx context.Context, txq *store.Queries, personID int64, from time.Time) error {
	g, err := loadManagerGraph(ctx, txq, nil)
	if err != nil {
		return err
	}
	if day, found := g.cycleFrom(personID, from); found {
		return managerCycle(day)
	}
	return nil
}

// checkDepartmentManagerCycle is checkManagerCycle for a department's write:
// a new manager or parent changes the manager of everyone whose terms name
// the department or one below it, so each of them is judged, from the
// earliest day any row names them in it — every day they can be there.
func checkDepartmentManagerCycle(ctx context.Context, txq *store.Queries, departmentID int64) error {
	g, err := loadManagerGraph(ctx, txq, nil)
	if err != nil {
		return err
	}
	for _, person := range g.peopleUnder(departmentID) {
		for _, e := range g.employments[person] {
			if day, found := g.cycleFrom(person, e.Start); found {
				return managerCycle(day)
			}
		}
	}
	return nil
}

// primaryManagerOf is the manager the org view's chain follows: the
// effectiveManager of the person's earliest employment in force on date that
// has one, nil when none does.
func (g managerGraph) primaryManagerOf(person int64, date time.Time) *int64 {
	for _, e := range g.employments[person] {
		if m := g.effectiveManager(e, date); m != nil {
			return m
		}
	}
	return nil
}

// activeOn reports whether person has an employment in force on date.
func (g managerGraph) activeOn(person int64, date time.Time) bool {
	for _, e := range g.employments[person] {
		if !date.Before(e.Start) && (e.End == nil || !date.After(*e.End)) {
			return true
		}
	}
	return false
}

// reportsOf is everyone whose manager person is on date by managersOf,
// sorted.
func (g managerGraph) reportsOf(person int64, date time.Time) []int64 {
	var out []int64
	for p := range g.employments {
		if slices.Contains(g.managersOf(p, date), person) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// personSummaries is people as the work group names them, by id.
func personSummaries(ctx context.Context, q *store.Queries, ids []int64) (map[int64]gen.StaffPersonSummary, error) {
	rows, err := q.PeopleSummaries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: name people: %w", err)
	}
	out := make(map[int64]gen.StaffPersonSummary, len(rows))
	for _, r := range rows {
		out[r.ID] = gen.StaffPersonSummary{Id: r.ID, DisplayName: r.DisplayName, EmployeeNumber: r.EmployeeNumber}
	}
	return out, nil
}

// GetStaffPeopleByIdOrg Get a person's place in the organisation
// (GET /api/v1/staff/people/{id}/org)
//
// Read in one snapshot: the chain up — each person's manager by their
// earliest employment in force today that has one, by effectiveManager, with
// a visited set, so a graph that loops (written past this module) ends the
// walk — and the people whose manager the person is today by the same rule.
// Everyone in the chain is in employment today, so a colleague, who sees
// only the people active today (plan reading 15), reads it whole.
func (s *server) GetStaffPeopleByIdOrg(ctx context.Context, req gen.GetStaffPeopleByIdOrgRequestObject) (gen.GetStaffPeopleByIdOrgResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out gen.StaffOrg
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.seesPerson() {
			return notFound()
		}
		g, err := loadManagerGraph(ctx, q, nil)
		if err != nil {
			return err
		}
		var chain []int64
		visited := map[int64]bool{req.Id: true}
		for m := g.primaryManagerOf(req.Id, caller.Today); m != nil && !visited[*m]; m = g.primaryManagerOf(*m, caller.Today) {
			visited[*m] = true
			chain = append(chain, *m)
		}
		reports := g.reportsOf(req.Id, caller.Today)
		names, err := personSummaries(ctx, q, append(append([]int64{req.Id}, chain...), reports...))
		if err != nil {
			return err
		}
		out = gen.StaffOrg{
			Person:  names[req.Id],
			Chain:   mapRows(chain, func(id int64) gen.StaffPersonSummary { return names[id] }),
			Reports: mapRows(reports, func(id int64) gen.StaffPersonSummary { return names[id] }),
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdOrg404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdOrg200JSONResponse(out), nil
}
