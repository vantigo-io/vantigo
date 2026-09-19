package expenses

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
)

// This file is GET /projects: the projects the caller may book an expense on,
// with the billing lines they may book it against, so the expense form needs
// no code of the projects module's own (design §6, "Project options"). It is
// the one operation that does not exist at all without that module — decision
// X2's "there is no project field anywhere" taken to its conclusion.

// GetExpensesProjects List the projects an expense may be booked on
// (GET /api/v1/expenses/projects)
//
// The directory is asked for the caller's projects, then for each one whether
// they may book on it (the rule a create is held to, so the picker can never
// offer a project the save would refuse), and then for its billing lines. That
// is one call per project on each of two counts: contracts.ProjectDirectory
// offers no bulk form of either, and widening the contract for a picker is a
// worse trade than three small reads of an in-process module. A caller holds
// few enough projects for it.
func (s *server) GetExpensesProjects(ctx context.Context, _ gen.GetExpensesProjectsRequestObject) (gen.GetExpensesProjectsResponseObject, error) {
	if !s.projectsAvailable() {
		return gen.GetExpensesProjects404ApplicationProblemPlusJSONResponse(projectsNotInstalled()), nil
	}
	userID := callerID(ctx)
	projects, err := s.projectsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("expenses: list the caller's projects: %w", err)
	}

	options := make([]gen.ExpensesProjectOption, 0, len(projects))
	for _, project := range projects {
		allowed, err := s.projectsCanLogTime(ctx, project.ID, userID)
		if err != nil {
			return nil, fmt.Errorf("expenses: check the caller may book on a project: %w", err)
		}
		if !allowed {
			continue
		}
		lines, err := s.projectsBillingLines(ctx, project.ID)
		if err != nil {
			return nil, fmt.Errorf("expenses: list a project's billing lines: %w", err)
		}
		// Only the active lines: an inactive one is refused on a save, so
		// offering it would be offering a mistake.
		offered := make([]gen.ExpensesProjectOptionBillingLine, 0, len(lines))
		for _, line := range lines {
			if line.Active {
				offered = append(offered, gen.ExpensesProjectOptionBillingLine{Id: line.ID, Code: line.Code})
			}
		}
		options = append(options, gen.ExpensesProjectOption{
			Id: project.ID, Code: project.Code, Name: project.Name,
			Currency: project.Currency, BillingLines: offered,
		})
	}
	return gen.GetExpensesProjects200JSONResponse(options), nil
}
