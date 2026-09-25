package expenses

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is GET /projects: the projects the caller may book an expense on,
// with the billing lines they may book it against, so the expense form needs
// no code of the projects module's own (design §6, "Project options"). It is
// the one operation that does not exist at all without that module — decision
// X2's "there is no project field anywhere" taken to its conclusion.
//
// kind=supplier_invoice is the same picker for the one kind that is not booked
// on CanLogTime (supplier invoices design D2): the projects the caller holds
// financial rights on and that are not cancelled.

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
//
// kind=supplier_invoice is judged on the *caller*, as the save of one is
// (checkSupplierInvoiceProject), so naming somebody else beside it is refused
// on kind rather than quietly answered with the caller's projects.
func (s *server) GetExpensesProjects(ctx context.Context, req gen.GetExpensesProjectsRequestObject) (gen.GetExpensesProjectsResponseObject, error) {
	if !s.projectsAvailable() {
		return gen.GetExpensesProjects404ApplicationProblemPlusJSONResponse(projectsNotInstalled()), nil
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	if kind := filterValue(req.Params.Kind); kind != nil {
		switch *kind {
		case kindOutlay, kindMileage:
			// The picker these two kinds have always had.
		case kindSupplierInvoice:
			if id := req.Params.UserId; id != nil && *id != c.UserID {
				return gen.GetExpensesProjects400ApplicationProblemPlusJSONResponse(invalidQueryFields(fieldError("kind",
					"A supplier invoice is booked on the recorder's own financial rights: the right to record one is the recorder's, so its projects are never somebody else's"))), nil
			}
			return s.supplierInvoiceProjects(ctx, c)
		default:
			return gen.GetExpensesProjects400ApplicationProblemPlusJSONResponse(invalidQueryFields(fieldError("kind",
				fmt.Sprintf("'%s' is not a kind this picker narrows by; it takes outlay, mileage or supplier_invoice", *kind)))), nil
		}
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
		option, err := s.projectOption(ctx, project)
		if err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return gen.GetExpensesProjects200JSONResponse(options), nil
}

// supplierInvoiceProjects is the picker for a supplier invoice: the caller's
// own projects on which seesProjectFinancials holds and that are not
// cancelled — exactly the projects checkSupplierInvoiceProject would let this
// caller record one on, so the picker never offers what the save refuses. It
// can only list projects the caller holds a role on, because that is what
// ProjectsForUser answers; a projects:manage-all holder on no team records
// from the project page instead, whose summary hands the project over.
func (s *server) supplierInvoiceProjects(ctx context.Context, c *caller) (gen.GetExpensesProjectsResponseObject, error) {
	projects, err := s.projectsForUser(ctx, c.UserID)
	if err != nil {
		return nil, fmt.Errorf("expenses: list the caller's projects: %w", err)
	}
	options := make([]gen.ExpensesProjectOption, 0, len(projects))
	for _, project := range projects {
		if project.Status == projectCancelled {
			continue
		}
		role, err := c.role(ctx, s, project.ID)
		if err != nil {
			return nil, err
		}
		if !c.seesProjectFinancials(role) {
			continue
		}
		option, err := s.projectOption(ctx, project)
		if err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return gen.GetExpensesProjects200JSONResponse(options), nil
}

// projectOption is one project as a booking picker offers it: its code, its
// name, its currency and its active billing lines — an inactive one is
// refused on a save, so offering it would be offering a mistake.
func (s *server) projectOption(ctx context.Context, project contracts.ProjectEntry) (gen.ExpensesProjectOption, error) {
	lines, err := s.projectsBillingLines(ctx, project.ID)
	if err != nil {
		return gen.ExpensesProjectOption{}, fmt.Errorf("expenses: list a project's billing lines: %w", err)
	}
	offered := make([]gen.ExpensesProjectOptionBillingLine, 0, len(lines))
	for _, line := range lines {
		if line.Active {
			offered = append(offered, gen.ExpensesProjectOptionBillingLine{Id: line.ID, Code: line.Code})
		}
	}
	return gen.ExpensesProjectOption{
		Id: project.ID, Code: project.Code, Name: project.Name,
		Currency: project.Currency, BillingLines: offered,
	}, nil
}
