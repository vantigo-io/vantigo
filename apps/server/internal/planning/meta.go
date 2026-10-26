package planning

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// GetPlanningMeta Planning meta
// (GET /api/v1/planning/meta)
//
// What the app needs to start (design §5): the caller's two roles beyond
// planning:access, whether Staff answers a person for them (Self, so My
// planning has someone to show), whether Staff answers anyone at all to a
// caller who may see the plan — a planner without staff:view sees an empty
// board, and workforceVisible false lets the app say why rather than show an
// empty plan (reading 14) — the load units, and for planning:manage the
// settings. Every contract call is made before the settings are read.
func (s *server) GetPlanningMeta(ctx context.Context, _ gen.GetPlanningMetaRequestObject) (gen.GetPlanningMetaResponseObject, error) {
	out, err := s.meta(ctx)
	if errors.Is(err, errDependencyUnavailable) {
		return gen.GetPlanningMeta503ApplicationProblemPlusJSONResponse(dependencyUnavailable()), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningMeta200JSONResponse(out), nil
}

// meta builds GET /meta's answer.
func (s *server) meta(ctx context.Context) (gen.PlanningMeta, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningMeta{}, err
	}
	self, err := s.callerSelf(ctx, c)
	if err != nil {
		return gen.PlanningMeta{}, err
	}
	visible := false
	if c.View {
		people, err := s.workforcePeople(ctx, c.Today, c.Today)
		if err != nil {
			return gen.PlanningMeta{}, err
		}
		visible = len(people) > 0
	}
	out := gen.PlanningMeta{
		CanView:          c.View,
		CanManage:        c.Manage,
		HasPerson:        self != nil,
		WorkforceVisible: visible,
		LoadUnits:        []gen.PlanningLoadUnit{gen.Percent, gen.HoursPerWeek},
	}
	if c.Manage {
		row, err := settings(ctx, store.New(s.deps.Pool))
		if err != nil {
			return gen.PlanningMeta{}, err
		}
		answer := settingsResponse(row)
		out.Settings = &answer
	}
	return out, nil
}
