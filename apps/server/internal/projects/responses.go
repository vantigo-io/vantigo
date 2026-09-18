package projects

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
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

// trackableCode is the pair a later module quotes a line by — the project's
// code and the line's, joined with a hyphen (D2/D9). It is computed from the
// project as it stands rather than stored beside the line, so renaming a
// project (D1) moves every one of its lines' trackable codes with it; neither
// code may contain a hyphen, so the pair always splits cleanly.
func trackableCode(projectCode, lineCode string) string {
	return projectCode + "-" + lineCode
}

// billingLineResponses renders a project's lines for one caller. The variant
// details every line embeds (D15) are resolved in one catalog call for the
// whole list rather than one per line; a variant the catalog no longer knows
// is simply absent from its answer, which is what variantMissing reports.
// Deps.Products is never nil here: the handlers answer 409 first (D10).
func (s *server) billingLineResponses(ctx context.Context, project store.ProjectsProject, a access, rows []store.ProjectsBillingLine) ([]gen.BillingLineResponse, error) {
	ids := make([]int32, 0, len(rows))
	seen := make(map[int32]bool, len(rows))
	for _, row := range rows {
		if !seen[row.VariantID] {
			seen[row.VariantID] = true
			ids = append(ids, row.VariantID)
		}
	}
	variants := make(map[int32]contracts.VariantEntry, len(ids))
	if len(ids) > 0 {
		found, err := s.deps.Products.Variants(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("projects: resolve the lines' variants: %w", err)
		}
		for _, v := range found {
			variants[v.ID] = v
		}
	}

	data := make([]gen.BillingLineResponse, 0, len(rows))
	for _, row := range rows {
		line, err := s.billingLineResponse(ctx, project, a, row, variants)
		if err != nil {
			return nil, err
		}
		data = append(data, line)
	}
	return data, nil
}

// billingLineResponseFor is billingLineResponses for the single line a create
// or a change answers with, so one line is rendered by exactly the code that
// renders a list of them.
func (s *server) billingLineResponseFor(ctx context.Context, project store.ProjectsProject, a access, row store.ProjectsBillingLine) (gen.BillingLineResponse, error) {
	data, err := s.billingLineResponses(ctx, project, a, []store.ProjectsBillingLine{row})
	if err != nil {
		return gen.BillingLineResponse{}, err
	}
	return data[0], nil
}

// billingLineResponse projects one line for one caller. pricing is set
// exactly when the caller may see the project's money (D12) — the same
// shaping the project's own financials get, for the same reason: the rates a
// project bills at are not a member's business.
func (s *server) billingLineResponse(ctx context.Context, project store.ProjectsProject, a access, row store.ProjectsBillingLine, variants map[int32]contracts.VariantEntry) (gen.BillingLineResponse, error) {
	resp := gen.BillingLineResponse{
		Id:            row.ID,
		Code:          row.Code,
		TrackableCode: trackableCode(project.Code, row.Code),
		VariantId:     row.VariantID,
		Active:        row.Active,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	// A line whose variant products has since dropped keeps resolving, so
	// that work already billed against it stays readable; it says its variant
	// is gone rather than inventing a name for it.
	if variant, ok := variants[row.VariantID]; ok {
		resp.ProductName = &variant.ProductName
		resp.Sku = &variant.SKU
		resp.Unit = &variant.Unit
	} else {
		resp.VariantMissing = true
	}
	if a.CanSeeFinancials {
		pricing, err := s.linePricing(ctx, project, row)
		if err != nil {
			return gen.BillingLineResponse{}, err
		}
		resp.Pricing = pricing
	}
	return resp, nil
}

// linePricing is one line's pricing block: the stored rule, plus the
// variant's list price in the project's currency at this moment. The list
// price is resolved rather than stored — a price list the products module
// changes tomorrow must show through here — and it is simply absent when the
// project has no currency (D13) or the variant has no price in it. Projects
// never applies the rule to the price: that is the invoice's arithmetic, not
// this module's.
func (s *server) linePricing(ctx context.Context, project store.ProjectsProject, row store.ProjectsBillingLine) (*gen.BillingLinePricing, error) {
	fixedAmount, err := floatPtrFromNumeric(row.FixedAmount)
	if err != nil {
		return nil, err
	}
	discountPercent, err := floatPtrFromNumeric(row.DiscountPercent)
	if err != nil {
		return nil, err
	}
	pricing := &gen.BillingLinePricing{
		Mode:            row.PricingMode,
		FixedAmount:     fixedAmount,
		DiscountPercent: discountPercent,
	}
	if project.Currency == nil {
		return pricing, nil
	}
	price, err := s.deps.Products.ListPrice(ctx, row.VariantID, *project.Currency, s.deps.Clock())
	if err != nil {
		return nil, fmt.Errorf("projects: resolve a variant's list price: %w", err)
	}
	if price != nil {
		pricing.ListPrice = &gen.BillingLineListPrice{Amount: price.Amount, Currency: price.Currency}
	}
	return pricing, nil
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
