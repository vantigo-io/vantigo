package expenses

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is GET /projects: the projects the caller may book an expense on,
// with the billing lines they may book it against, so the expense form needs
// no code of the projects module's own (design §6, "Project options"). It is
// the one operation that does not exist at all without that module — decision
// X2's "there is no project field anywhere" taken to its conclusion.

// GetExpensesProjects List the projects an expense may be booked on
// (GET /api/v1/expenses/projects)
//
// The directory is asked for the person's projects, then for each one whether
// they may book on it (the rule a create is held to, so the picker can never
// offer a project the save would refuse), and then for its billing lines. That
// is one call per project on each of two counts: contracts.ProjectDirectory
// offers no bulk form of either, and widening the contract for a picker is a
// worse trade than three small reads of an in-process module. A caller holds
// few enough projects for it.
//
// The person is the caller unless userId names somebody else, which is the
// same rule and the same field the create answers: a save is judged on what
// the expense's *owner* may book on (checkProject), so an administrator
// recording for a colleague has to be offered the colleague's projects or the
// picker would offer what the save then refuses. Naming anybody else needs
// expenses:manage, which is what lets one record for somebody else at all.
func (s *server) GetExpensesProjects(ctx context.Context, req gen.GetExpensesProjectsRequestObject) (gen.GetExpensesProjectsResponseObject, error) {
	if !s.projectsAvailable() {
		return gen.GetExpensesProjects404ApplicationProblemPlusJSONResponse(projectsNotInstalled()), nil
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	userID := c.UserID
	if id := req.Params.UserId; id != nil && *id != c.UserID {
		if !c.Manage {
			return gen.GetExpensesProjects400ApplicationProblemPlusJSONResponse(invalidQueryFields(fieldError(
				"userId", "Listing somebody else's projects needs the Manage expenses permission"))), nil
		}
		user, err := s.usersUser(ctx, *id)
		if err != nil {
			return nil, fmt.Errorf("expenses: look up the person the picker is for: %w", err)
		}
		if user == nil || !user.Active {
			return gen.GetExpensesProjects400ApplicationProblemPlusJSONResponse(
				invalidQueryFields(fieldError("userId", "Nobody active has that user id"))), nil
		}
		userID = user.ID
	}
	projects, err := s.projectsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("expenses: list the person's projects: %w", err)
	}

	options := make([]gen.ExpensesProjectOption, 0, len(projects))
	for _, project := range projects {
		allowed, err := s.projectsCanLogTime(ctx, project.ID, userID)
		if err != nil {
			return nil, fmt.Errorf("expenses: check the person may book on a project: %w", err)
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
