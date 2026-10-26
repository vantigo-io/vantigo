package planning

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// The plannable projects (design S9): the directory's listing by status,
// consumer-gated — planning:view, the operation's own rule, decides who sees
// it, bids included.

// defaultProjectStatuses is the listing's statuses when the request names
// none: what can still be planned.
var defaultProjectStatuses = []string{string(gen.Planned), string(gen.Active), string(gen.OnHold)}

// projectStatuses is every status a project is in, as the contract names them.
var projectStatuses = []string{string(gen.Planned), string(gen.Active), string(gen.OnHold), string(gen.Completed), string(gen.Cancelled)}

// ListPlanningProjects List plannable projects
// (GET /api/v1/planning/projects)
//
// planning:view. Every project in the statuses asked, by code, filtered by q
// on the code or the name, each with its customer's name.
func (s *server) ListPlanningProjects(ctx context.Context, req gen.ListPlanningProjectsRequestObject) (gen.ListPlanningProjectsResponseObject, error) {
	out, err := s.listProjects(ctx, req.Params)
	switch status, problem := readProblem(err); status {
	case http.StatusBadRequest:
		return gen.ListPlanningProjects400ApplicationProblemPlusJSONResponse(problem), nil
	case http.StatusServiceUnavailable:
		return gen.ListPlanningProjects503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.ListPlanningProjects200JSONResponse(out), nil
}

// listProjects builds GET /projects' answer.
func (s *server) listProjects(ctx context.Context, p gen.ListPlanningProjectsParams) (gen.PlanningProjectList, error) {
	statuses := defaultProjectStatuses
	if p.Status != nil {
		var f fieldErrors
		statuses = nil
		for _, raw := range strings.Split(*p.Status, ",") {
			status := strings.TrimSpace(raw)
			if status == "" {
				continue
			}
			if !slices.Contains(projectStatuses, status) {
				f.add("status", fmt.Sprintf("status must be a comma list of %s, but '%s' is none of them", strings.Join(projectStatuses, ", "), status))
				continue
			}
			statuses = append(statuses, status)
		}
		if f.failed() {
			return gen.PlanningProjectList{}, refuseInvalid(invalidQueryTitle, f)
		}
	}
	entries, err := s.projectsByStatus(ctx, statuses)
	if err != nil {
		return gen.PlanningProjectList{}, err
	}
	if len(entries) == contracts.MaxDirectoryProjects {
		// The search below runs on what the directory answered, so a project
		// past the cap by code cannot be found (amendment 31).
		s.deps.Logger.InfoContext(ctx, "planning: the project directory answered its cap; the picker's search may miss projects past it",
			"cap", contracts.MaxDirectoryProjects, "statuses", statuses)
	}
	if p.Q != nil {
		q := strings.ToLower(strings.TrimSpace(*p.Q))
		entries = slices.DeleteFunc(entries, func(e contracts.ProjectEntry) bool {
			return !strings.Contains(strings.ToLower(e.Code), q) && !strings.Contains(strings.ToLower(e.Name), q)
		})
	}
	customers, err := s.customersOf(ctx, entries)
	if err != nil {
		return gen.PlanningProjectList{}, err
	}
	out := gen.PlanningProjectList{Projects: make([]gen.PlanningProject, 0, len(entries))}
	for _, e := range entries {
		out.Projects = append(out.Projects, projectView(e, customers))
	}
	return out, nil
}
