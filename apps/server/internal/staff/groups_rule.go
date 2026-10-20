package staff

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a derived group's rule (Entra design D9, plan task 3a, reading
// 4): one predicate over the register, judged on the business day against the
// employments and terms in force. Who it yields is the people not erased with
// an employment in force that day — started, and not ended before it — each
// judged on one employment, directoryPlace's choice when they have several:
// the earliest in force. employed yields every one of them; businessUnit
// those whose employment is in the unit; department those whose terms in
// force name the department, or with includeBelow one beneath it, the parent
// chain read with archived departments included; location those whose terms
// name the location; employeeType those whose terms carry the value; reportsTo
// those whose effective manager that day (managerGraph.effectiveManager, the
// module's one manager rule) is the person — one level, never their reports'
// reports.

// placeEmployment is one person's employment the rule judges them on.
type placeEmployment struct {
	ID, PersonID, BusinessUnitID int64
	Start                        time.Time
}

// rulePlaces is the employment each person not erased is judged on today:
// of the employments in force, the earliest by start (then id). A person
// with none in force is not in the map.
func rulePlaces(ctx context.Context, q *store.Queries, today time.Time) (map[int64]placeEmployment, error) {
	rows, err := q.GraphEmployments(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("staff: read the employments for a group's rule: %w", err)
	}
	places := map[int64]placeEmployment{}
	for _, r := range rows {
		start := civil(r.StartDate)
		if start.After(today) || (r.EndDate.Valid && r.EndDate.Time.Before(today)) {
			continue
		}
		cur, ok := places[r.PersonID]
		if ok && (cur.Start.Before(start) || (cur.Start.Equal(start) && cur.ID < r.ID)) {
			continue
		}
		places[r.PersonID] = placeEmployment{ID: r.ID, PersonID: r.PersonID, BusinessUnitID: r.BusinessUnitID, Start: start}
	}
	if len(places) == 0 {
		return places, nil
	}
	ids := make([]int64, 0, len(places))
	for id := range places {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	kept, err := q.PeopleNotErased(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the people not erased for a group's rule: %w", err)
	}
	out := make(map[int64]placeEmployment, len(kept))
	for _, id := range kept {
		out[id] = places[id]
	}
	return out, nil
}

// evaluateRule is the people rule yields on today, ascending.
func evaluateRule(ctx context.Context, q *store.Queries, rule gen.StaffGroupRule, today time.Time) ([]int64, error) {
	places, err := rulePlaces(ctx, q, today)
	if err != nil {
		return nil, err
	}
	var match func(p placeEmployment) bool
	switch rule.Kind {
	case gen.Employed:
		match = func(placeEmployment) bool { return true }
	case gen.BusinessUnit:
		match = func(p placeEmployment) bool { return rule.Id != nil && p.BusinessUnitID == *rule.Id }
	case gen.Department, gen.Location, gen.EmployeeType:
		terms, err := termsInForceOf(ctx, q, places, today)
		if err != nil {
			return nil, err
		}
		match, err = termsMatch(ctx, q, rule, terms)
		if err != nil {
			return nil, err
		}
	case gen.ReportsTo:
		g, err := loadManagerGraph(ctx, q, nil)
		if err != nil {
			return nil, err
		}
		match = func(p placeEmployment) bool {
			if rule.Id == nil {
				return false
			}
			for _, e := range g.employments[p.PersonID] {
				if e.ID == p.ID {
					m := g.effectiveManager(e, today)
					return m != nil && *m == *rule.Id
				}
			}
			return false
		}
	default:
		return nil, fmt.Errorf("staff: a group's rule has the unknown kind %q", rule.Kind)
	}
	out := []int64{}
	for id, p := range places {
		if match(p) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out, nil
}

// termsInForceOf is the terms row in force today of each person's place, by
// person; a person whose employment has no row yet is not in the map.
func termsInForceOf(ctx context.Context, q *store.Queries, places map[int64]placeEmployment, today time.Time) (map[int64]store.StaffEmploymentTerm, error) {
	ids := make([]int64, 0, len(places))
	byEmployment := make(map[int64]int64, len(places))
	for _, p := range places {
		ids = append(ids, p.ID)
		byEmployment[p.ID] = p.PersonID
	}
	slices.Sort(ids)
	rows, err := q.ListTermsOfEmployments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the terms for a group's rule: %w", err)
	}
	chains := map[int64][]store.StaffEmploymentTerm{}
	for _, r := range rows {
		chains[r.EmploymentID] = append(chains[r.EmploymentID], r)
	}
	out := map[int64]store.StaffEmploymentTerm{}
	for employment, chain := range chains {
		if i := rowOn(chain, today); i >= 0 {
			out[byEmployment[employment]] = chain[i]
		}
	}
	return out, nil
}

// termsMatch is the predicate of a rule the terms in force decide.
func termsMatch(ctx context.Context, q *store.Queries, rule gen.StaffGroupRule, terms map[int64]store.StaffEmploymentTerm) (func(placeEmployment) bool, error) {
	switch rule.Kind {
	case gen.Location:
		return func(p placeEmployment) bool {
			t, ok := terms[p.PersonID]
			return ok && rule.Id != nil && t.LocationID != nil && *t.LocationID == *rule.Id
		}, nil
	case gen.EmployeeType:
		return func(p placeEmployment) bool {
			t, ok := terms[p.PersonID]
			return ok && rule.Value != nil && t.EmployeeType != nil && *t.EmployeeType == *rule.Value
		}, nil
	}
	// department
	wanted := map[int64]bool{}
	if rule.Id != nil {
		wanted[*rule.Id] = true
		if rule.IncludeBelow != nil && *rule.IncludeBelow {
			rows, err := q.DepartmentParents(ctx)
			if err != nil {
				return nil, fmt.Errorf("staff: read the departments for a group's rule: %w", err)
			}
			parents := make(map[int64]*int64, len(rows))
			for _, r := range rows {
				parents[r.ID] = r.ParentID
			}
			for id := range parents {
				if departmentBeneath(parents, id, *rule.Id) {
					wanted[id] = true
				}
			}
		}
	}
	return func(p placeEmployment) bool {
		t, ok := terms[p.PersonID]
		return ok && t.DepartmentID != nil && wanted[*t.DepartmentID]
	}, nil
}

// departmentBeneath reports whether department id sits somewhere beneath
// top in parents. The climb carries a visited set, so a loop written past
// this module ends it.
func departmentBeneath(parents map[int64]*int64, id, top int64) bool {
	visited := map[int64]bool{id: true}
	for next := parents[id]; next != nil; next = parents[*next] {
		if *next == top {
			return true
		}
		if visited[*next] {
			return false
		}
		visited[*next] = true
	}
	return false
}
