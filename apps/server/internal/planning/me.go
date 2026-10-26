package planning

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// My planning (design S19): the caller's own person, as Staff's Self rule
// answers it, and their confirmed rows from today on — never a tentative
// row, never the note.

// GetPlanningMe My planning
// (GET /api/v1/planning/me)
//
// planning:access. A caller Staff answers no person for is the 404
// no_person.
func (s *server) GetPlanningMe(ctx context.Context, _ gen.GetPlanningMeRequestObject) (gen.GetPlanningMeResponseObject, error) {
	out, err := s.me(ctx)
	switch status, problem := readProblem(err); status {
	case http.StatusNotFound:
		return gen.GetPlanningMe404ApplicationProblemPlusJSONResponse(problem), nil
	case http.StatusServiceUnavailable:
		return gen.GetPlanningMe503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningMe200JSONResponse(out), nil
}

// me builds GET /me's answer.
func (s *server) me(ctx context.Context) (gen.PlanningMe, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningMe{}, err
	}
	self, err := s.callerSelf(ctx, c)
	if err != nil {
		return gen.PlanningMe{}, err
	}
	if self == nil {
		return gen.PlanningMe{}, &refusal{status: http.StatusNotFound, problem: noPerson()}
	}
	rows, err := store.New(s.deps.Pool).PersonConfirmedAllocationsFrom(ctx, store.PersonConfirmedAllocationsFromParams{
		PersonID: self.ID, FromDate: dayParam(c.Today),
	})
	if err != nil {
		return gen.PlanningMe{}, fmt.Errorf("planning: read the confirmed rows of person %d: %w", self.ID, err)
	}
	projects, err := s.projectsOfRows(ctx, rows)
	if err != nil {
		return gen.PlanningMe{}, err
	}
	person := *self
	return gen.PlanningMe{
		Person:      personView(person),
		Allocations: allocationsView(rows, map[int64]*contracts.PersonEntry{person.ID: &person}, projects, false),
	}, nil
}
