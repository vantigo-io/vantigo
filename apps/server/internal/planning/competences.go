package planning

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// ListPlanningCompetences The competence catalogue
// (GET /api/v1/planning/competences)
//
// planning:view's (plan reading 6, amendment 38): Staff's catalogue as
// Workforce.Competences answers the caller — every competence not archived,
// hidden ones flagged so the request sheet offers none for a new
// requirement — for a request's requirements and the board's filter. Empty,
// not an error, when Staff answers the caller nothing; the module never
// restates Staff's rule.
func (s *server) ListPlanningCompetences(ctx context.Context, _ gen.ListPlanningCompetencesRequestObject) (gen.ListPlanningCompetencesResponseObject, error) {
	entries, err := s.workforceCompetences(ctx)
	if errors.Is(err, errDependencyUnavailable) {
		return gen.ListPlanningCompetences503ApplicationProblemPlusJSONResponse(dependencyUnavailable()), nil
	}
	if err != nil {
		return nil, err
	}
	out := gen.PlanningCompetenceList{Competences: make([]gen.PlanningCompetence, 0, len(entries))}
	for _, e := range entries {
		out.Competences = append(out.Competences, gen.PlanningCompetence{
			Id: e.ID, Key: e.Key, NameEn: e.NameEn, NameNb: e.NameNb, CategoryKey: e.CategoryKey,
			Kind: gen.PlanningCompetenceKind(e.Kind), HasLevel: e.HasLevel, Hidden: e.Hidden,
		})
	}
	return gen.ListPlanningCompetences200JSONResponse(out), nil
}
