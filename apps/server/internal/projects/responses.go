package projects

import (
	"context"
	"encoding/json"
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
func projectResponse(row store.ProjectsProject, a access, customerName *string, managers []gen.ProjectPersonSummary, linesAvailable bool) (gen.ProjectResponse, error) {
	budgetHours, err := floatPtrFromNumeric(row.BudgetHours)
	if err != nil {
		return gen.ProjectResponse{}, err
	}
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
		BudgetHours:           budgetHours,
		Revision:              row.Revision,
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
		Managers:              managers,
		Capabilities:          gen.ProjectCapabilities{CanManage: a.CanManage, CanSeeFinancials: a.CanSeeFinancials},
		BillingLinesAvailable: linesAvailable,
	}
	if a.CanSeeFinancials {
		fixedPrice, err := floatPtrFromNumeric(row.FixedPriceAmount)
		if err != nil {
			return gen.ProjectResponse{}, err
		}
		budgetAmount, err := floatPtrFromNumeric(row.BudgetAmount)
		if err != nil {
			return gen.ProjectResponse{}, err
		}
		resp.Financials = &gen.ProjectFinancials{
			Currency:         row.Currency,
			FixedPriceAmount: fixedPrice,
			BudgetAmount:     budgetAmount,
		}
	}
	return resp, nil
}

// projectSummaryResponse is one list row. It is not projectResponse with
// fields removed: the summary schema has no financial fields at all, so
// there is nothing here to shape and no caller who could be shown a price by
// mistake. capabilities and billingLinesAvailable are left out for the same
// reason — they are answers about one project the caller opened, not about a
// row in a table.
func projectSummaryResponse(row store.ProjectsProject, customerName *string, managers []gen.ProjectPersonSummary) (gen.ProjectSummaryResponse, error) {
	budgetHours, err := floatPtrFromNumeric(row.BudgetHours)
	if err != nil {
		return gen.ProjectSummaryResponse{}, err
	}
	return gen.ProjectSummaryResponse{
		Id:           row.ID,
		Code:         row.Code,
		Name:         row.Name,
		CustomerId:   row.CustomerID,
		CustomerName: customerName,
		Internal:     row.CustomerID == nil,
		Status:       row.Status,
		StartDate:    dateFromPgtype(row.StartDate),
		EndDate:      dateFromPgtype(row.EndDate),
		BillingType:  row.BillingType,
		BudgetHours:  budgetHours,
		Revision:     row.Revision,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		Managers:     managers,
	}, nil
}

// timelineEntryResponse renders one stored entry. The payload is decoded
// from the jsonb column rather than passed through as text, so the contract
// answers a JSON object and not a string containing one; a payload that
// cannot be decoded is an error rather than an empty object, because an
// entry whose payload is silently dropped is worse than no answer.
func timelineEntryResponse(e store.ProjectsTimelineEntry) (gen.TimelineEntryResponse, error) {
	payload := map[string]any{}
	if len(e.Payload) > 0 {
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return gen.TimelineEntryResponse{}, fmt.Errorf("projects: decode timeline payload: %w", err)
		}
	}
	return gen.TimelineEntryResponse{
		Id:           e.ID,
		EventType:    e.EventType,
		Payload:      payload,
		ActorUserId:  e.ActorUserID,
		ActorDisplay: e.ActorDisplay,
		OccurredAt:   e.OccurredAt,
	}, nil
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
	return projectResponse(row, a, customerName, managers, s.billingLinesAvailable())
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
