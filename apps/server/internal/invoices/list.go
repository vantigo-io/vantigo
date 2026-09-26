package invoices

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is GET /invoices (D4): every module list's page/pageSize
// convention, drafts first and then by number descending. There is no keyset
// cursor: number is NULL on every draft.

// The paging bounds every module's list uses; listMaxPage keeps page ×
// pageSize inside the int32 offset.
const (
	listDefaultPageSize = 25
	listMaxPageSize     = 100
	listMaxPage         = math.MaxInt32 / listMaxPageSize
)

const invalidQueryTitle = "Invalid query parameters"

// validatePageParams is the paging rule, in the other modules' own words.
func validatePageParams(page, pageSize *int32) []string {
	var errs []string
	switch {
	case page == nil:
	case *page < 1:
		errs = append(errs, fmt.Sprintf("'page' must be 1 or greater, but was %d.", *page))
	case *page > listMaxPage:
		errs = append(errs, fmt.Sprintf("'page' must be at most %d, but was %d.", listMaxPage, *page))
	}
	if pageSize != nil && (*pageSize < 1 || *pageSize > listMaxPageSize) {
		errs = append(errs, fmt.Sprintf("'pageSize' must be between 1 and %d, but was %d.", listMaxPageSize, *pageSize))
	}
	return errs
}

// pageParams is the validated paging as numbers.
func pageParams(page, pageSize *int32) (int32, int32) {
	p, size := int32(1), int32(listDefaultPageSize)
	if page != nil {
		p = *page
	}
	if pageSize != nil {
		size = *pageSize
	}
	return p, size
}

// likePattern is a case-insensitive substring pattern with LIKE's own
// wildcards escaped.
func likePattern(s string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s) + "%"
}

// GetInvoices List invoices and credit notes
// (GET /api/v1/invoices)
func (s *server) GetInvoices(ctx context.Context, req gen.GetInvoicesRequestObject) (gen.GetInvoicesResponseObject, error) {
	p := req.Params
	errs := validatePageParams(p.Page, p.PageSize)
	if p.Status != nil && *p.Status != statusDraft && *p.Status != statusIssued {
		errs = append(errs, fmt.Sprintf("'status' must be 'draft' or 'issued', but was '%s'.", *p.Status))
	}
	if p.Kind != nil && *p.Kind != kindInvoice && *p.Kind != kindCreditNote {
		errs = append(errs, fmt.Sprintf("'kind' must be 'invoice' or 'credit_note', but was '%s'.", *p.Kind))
	}
	if p.From != nil && p.To != nil && p.From.After(p.To.Time) {
		errs = append(errs, "'from' must be on or before 'to'.")
	}
	if len(errs) > 0 {
		return gen.GetInvoices400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	params := store.ListInvoicesParams{
		Status: p.Status, Kind: p.Kind, CustomerID: p.CustomerId,
		PageOffset: (page - 1) * pageSize, PageSize: pageSize,
	}
	if p.From != nil {
		params.IssuedFrom = pgDate(utcDay(p.From.Time))
	}
	if p.To != nil {
		params.IssuedTo = pgDate(utcDay(p.To.Time))
	}
	if p.Search != nil {
		if term := strings.TrimSpace(*p.Search); term != "" {
			params.SearchPattern = ptr(likePattern(term))
			if n, err := strconv.ParseInt(term, 10, 64); err == nil {
				params.SearchNumber = &n
			}
		}
	}
	q := store.New(s.deps.Pool)
	rows, err := q.ListInvoices(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("invoices: list: %w", err)
	}
	total, err := q.CountInvoices(ctx, store.CountInvoicesParams{
		Status: params.Status, Kind: params.Kind, CustomerID: params.CustomerID,
		SearchNumber: params.SearchNumber, SearchPattern: params.SearchPattern,
		IssuedFrom: params.IssuedFrom, IssuedTo: params.IssuedTo,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: count: %w", err)
	}

	// An invoice draft has no buyer snapshot (a credit-note draft carries the
	// one it copied): the page's invoice drafts are named by their customers'
	// current names, in one directory round trip.
	var draftCustomers []int32
	for _, r := range rows {
		if r.BuyerName == nil {
			draftCustomers = append(draftCustomers, r.CustomerID)
		}
	}
	entries, err := s.customerEntries(ctx, draftCustomers)
	if err != nil {
		return nil, err
	}
	names := make(map[int32]string, len(entries))
	for _, e := range entries {
		names[e.ID] = e.Name
	}

	data := make([]gen.InvoicesInvoiceListItem, 0, len(rows))
	for _, r := range rows {
		gross, err := floatFromNumeric(r.GrossTotal)
		if err != nil {
			return nil, err
		}
		item := gen.InvoicesInvoiceListItem{
			Id: r.ID, Kind: r.Kind, Status: r.Status, Number: r.Number, CustomerId: r.CustomerID,
			IssueDate: wireDateOf(r.IssueDate), DueDate: wireDateOf(r.DueDate), GrossTotal: gross,
			Currency: r.Currency, CreditsInvoiceId: r.CreditsInvoiceID, CustomerName: r.BuyerName,
		}
		if item.CustomerName == nil {
			if name, ok := names[r.CustomerID]; ok {
				item.CustomerName = &name
			}
		}
		data = append(data, item)
	}
	return gen.GetInvoices200JSONResponse(gen.PaginatedResponseOfInvoicesInvoiceListItem{
		Data: data, Pagination: apicommon.Pagination(page, pageSize, total),
	}), nil
}
