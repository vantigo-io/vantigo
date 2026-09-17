package projects

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file turns a stored project row into the contract's ProjectResponse,
// including D12's financial shaping: the money is not forbidden to a caller
// who may not see it, it is simply not in their copy of the resource.

// projectResponse projects one row for one caller. financials is set exactly
// when the caller may see the project's money — and then always set, even
// with nothing in it, so a client can tell "may see, nothing entered" from
// "may not see" (D12). budgetHours is deliberately outside it: hours are
// planning data everyone who sees the project may see.
func projectResponse(row store.ProjectsProject, a access, customerName *string, managers []gen.ProjectPersonSummary, linesAvailable bool) gen.ProjectResponse {
	resp := gen.ProjectResponse{
		Id:                    row.ID,
		Code:                  row.Code,
		Name:                  row.Name,
		Description:           row.Description,
		CustomerId:            row.CustomerID,
		CustomerName:          customerName,
		Internal:              row.CustomerID == nil,
		Status:                row.Status,
		StartDate:             dateFromPgtype(row.StartDate),
		EndDate:               dateFromPgtype(row.EndDate),
		BillingType:           row.BillingType,
		BudgetHours:           floatPtrFromNumeric(row.BudgetHours),
		Revision:              row.Revision,
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
		Managers:              managers,
		Capabilities:          gen.ProjectCapabilities{CanManage: a.CanManage, CanSeeFinancials: a.CanSeeFinancials},
		BillingLinesAvailable: linesAvailable,
	}
	if a.CanSeeFinancials {
		resp.Financials = &gen.ProjectFinancials{
			Currency:         row.Currency,
			FixedPriceAmount: floatPtrFromNumeric(row.FixedPriceAmount),
			BudgetAmount:     floatPtrFromNumeric(row.BudgetAmount),
		}
	}
	return resp
}

// projectResponseFor is projectResponse with the two lookups it needs made
// first: the project's customer name, through contracts.CustomerDirectory,
// and its managers' display names, through contracts.UserDirectory. Both
// live in other modules' schemas, so neither can be a join.
func (s *server) projectResponseFor(ctx context.Context, q *store.Queries, row store.ProjectsProject, a access) (gen.ProjectResponse, error) {
	customerName, err := s.customerName(ctx, row.CustomerID)
	if err != nil {
		return gen.ProjectResponse{}, err
	}
	managers, err := s.managers(ctx, q, row.ID)
	if err != nil {
		return gen.ProjectResponse{}, err
	}
	return projectResponse(row, a, customerName, managers, s.billingLinesAvailable()), nil
}

// customerName resolves a project's customer for display. An internal
// project has none; so, as far as this module can tell, does a project whose
// customer the directory no longer knows — a name it cannot resolve is left
// out rather than invented. Deps.Directory is never nil here: config refuses
// to start with projects enabled and customers disabled.
func (s *server) customerName(ctx context.Context, customerID *int32) (*string, error) {
	if customerID == nil {
		return nil, nil
	}
	customer, err := s.deps.Directory.Customer(ctx, *customerID)
	if err != nil {
		return nil, fmt.Errorf("projects: look up customer: %w", err)
	}
	if customer == nil {
		return nil, nil
	}
	return &customer.Name, nil
}

// managers lists a project's managers, oldest assignment first, each named
// through the user directory. The order is the query's, not the display
// names', so the person who created the project — its first manager — is
// always first.
func (s *server) managers(ctx context.Context, q *store.Queries, projectID int32) ([]gen.ProjectPersonSummary, error) {
	ids, err := q.ListProjectManagers(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("projects: list managers: %w", err)
	}
	names, err := s.displayNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	return personSummaries(ids, names), nil
}

// personSummaries pairs ids with their display names, in ids' order. The
// slice is never nil: `managers` is a required array in the contract, and an
// omitted one would decode as null.
func personSummaries(ids []uuid.UUID, names map[uuid.UUID]string) []gen.ProjectPersonSummary {
	out := make([]gen.ProjectPersonSummary, 0, len(ids))
	for _, id := range ids {
		out = append(out, gen.ProjectPersonSummary{UserId: id, DisplayName: names[id]})
	}
	return out
}
