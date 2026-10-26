package planning

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// The people of a window (design S8), for the pickers: as Staff answers them
// to the caller, never restated.

// peopleDefaultDays is how far the pickers' window reaches by default: today
// and the twelve weeks after it.
const peopleDefaultDays = 12 * 7

// ListPlanningPeople List the people of a window
// (GET /api/v1/planning/people)
//
// planning:view. The people in employment on today or a later day of the
// window, filtered by q on the name or the employee number and by
// department.
func (s *server) ListPlanningPeople(ctx context.Context, req gen.ListPlanningPeopleRequestObject) (gen.ListPlanningPeopleResponseObject, error) {
	out, err := s.listPeople(ctx, req.Params)
	switch status, problem := readProblem(err); status {
	case http.StatusBadRequest:
		return gen.ListPlanningPeople400ApplicationProblemPlusJSONResponse(problem), nil
	case http.StatusServiceUnavailable:
		return gen.ListPlanningPeople503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.ListPlanningPeople200JSONResponse(out), nil
}

// listPeople builds GET /people's answer.
func (s *server) listPeople(ctx context.Context, p gen.ListPlanningPeopleParams) (gen.PlanningPeopleList, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningPeopleList{}, err
	}
	from, to, f := windowOf(p.From, p.To, c.Today, func(from time.Time) time.Time { return addDays(from, peopleDefaultDays) })
	if f.failed() {
		return gen.PlanningPeopleList{}, refuseInvalid(invalidQueryTitle, f)
	}
	people, err := s.workforcePeople(ctx, from, to)
	if err != nil {
		return gen.PlanningPeopleList{}, windowOrInvalid(err, from, c.Today)
	}
	people = slices.DeleteFunc(slices.Clone(people), func(e contracts.PersonEntry) bool {
		return !inDepartment(e, p.DepartmentId) || !matchesPerson(e, p.Q)
	})
	out := gen.PlanningPeopleList{People: make([]gen.PlanningPerson, 0, len(people))}
	for _, e := range people {
		out.People = append(out.People, personView(e))
	}
	return out, nil
}

// inDepartment reports whether e is in the department departmentID names;
// nil is every department.
func inDepartment(e contracts.PersonEntry, departmentID *int64) bool {
	return departmentID == nil || (e.DepartmentID != nil && *e.DepartmentID == *departmentID)
}

// matchesPerson reports whether q matches e's display name or employee
// number, case-insensitively; nil or blank matches everyone.
func matchesPerson(e contracts.PersonEntry, q *string) bool {
	if q == nil {
		return true
	}
	needle := strings.ToLower(strings.TrimSpace(*q))
	return strings.Contains(strings.ToLower(e.DisplayName), needle) || strings.Contains(strings.ToLower(e.EmployeeNumber), needle)
}
