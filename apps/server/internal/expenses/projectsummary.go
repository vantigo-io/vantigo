package expenses

import (
	"context"
	"fmt"
	"math/big"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is the Expenses tab on the project page, from this module's side:
// what one project's expenses cost and bill, in sum.
//
// It is *not* a second summation. The figures come from projectExpenseTotals,
// the very function contracts.ProjectExpenses answers the projects module
// with, so the Expenses tab and the Economy tab read one implementation and
// can never show a project two different numbers. All this handler adds is who
// may ask, what the caller may do about it, and the rendering.
//
// Who may ask is seesProjectFinancials — the project's manager,
// projects:manage-all, or projects:view-financials on a project they can see —
// which is the rule pricing a line and marking one invoiced are already held
// to. expenses:manage, expenses:approve and expenses:view-all are not in it:
// they open other people's *expenses*, and a project's takings are not that.
//
// The refusal is one bare 404 for all three reasons (no projects module, no
// such project, no financial rights), byte for byte the answer an unknown id
// gets everywhere else in this module, so it tells nobody which projects exist
// or who manages them.
//
// Every directory call it makes — the project, the caller's role on it and
// whether they may book on it — happens before the summation's query and
// outside any transaction, which is this module's standing rule
// (contractscalls.go) and also the only order that can serve one bare 404: the
// authorization has to be settled before anything is read.

// GetExpensesProjectsByProjectIdSummary Sum up a project's expenses
// (GET /api/v1/expenses/projects/{projectId}/summary)
func (s *server) GetExpensesProjectsByProjectIdSummary(ctx context.Context, req gen.GetExpensesProjectsByProjectIdSummaryRequestObject) (gen.GetExpensesProjectsByProjectIdSummaryResponseObject, error) {
	if !s.projectsAvailable() {
		return gen.GetExpensesProjectsByProjectIdSummary404Response{}, nil
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}

	// One question, asked of the directory before anything is read: may this
	// caller see what this project makes? A project the directory does not
	// know answers the same "no" a caller without the right does, which is
	// what makes the three causes one refusal. It is the very question
	// GET /entries?toInvoice=true is gated on, through the same function.
	project, allowed, err := s.projectFinancials(ctx, c, req.ProjectId)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return gen.GetExpensesProjectsByProjectIdSummary404Response{}, nil
	}
	// Projects' own rule for whether this caller may book a cost on the
	// project (decision X9), asked here rather than guessed from the role, so
	// a "Record a cost" button can never offer what the save would refuse.
	canRecord, err := s.projectsCanLogTime(ctx, req.ProjectId, c.UserID)
	if err != nil {
		return nil, fmt.Errorf("expenses: check the caller may book on the project: %w", err)
	}
	// A supplier invoice is recorded by whoever holds the project's financial
	// rights — which this caller does, or the summary would already have
	// answered 404 — on any project but a cancelled one (supplier invoices
	// design D2). The project comes with it as a booking option, because the
	// caller the button exists for is often on no project team, and
	// GET /projects, a picker of what the caller may log time on, offers them
	// nothing to open the form with.
	canRecordSupplierInvoice := project.Status != projectCancelled
	var option *gen.ExpensesProjectOption
	if canRecordSupplierInvoice {
		one, err := s.projectOption(ctx, *project)
		if err != nil {
			return nil, err
		}
		option = &one
	}

	totals, err := projectExpenseTotals(ctx, q, []int32{req.ProjectId})
	if err != nil {
		return nil, err
	}
	// A project with nothing recorded is absent from that map by contract, and
	// here it is a project with no expenses yet: an empty list of currencies
	// and no last entry date, never a 404 — which would say the caller may not
	// see it — and never a zeroed currency this module has no business naming.
	response, err := projectSummaryResponse(totals[req.ProjectId], project.Currency,
		gen.ExpensesProjectSummaryCapabilities{CanRecord: canRecord, CanRecordSupplierInvoice: &canRecordSupplierInvoice},
		option)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesProjectsByProjectIdSummary200JSONResponse(response), nil
}

// projectSummaryResponse renders the contract's totals as the API publishes
// them. The contract carries money as exact decimal text, because that is what
// crosses a module boundary without a float ever touching it; this module's
// own API publishes every amount as a JSON number, as grossAmount and
// billAmount already do, so the text is parsed back to the exact decimal it
// came from and rendered once. Nothing is recomputed and nothing is rounded a
// second time: each figure was rounded once, where it was summed.
//
// projectCurrency is the project's own, straight off the ProjectEntry the
// handler already held to decide the 404. It is answered rather than left to
// the reader to fetch because the caller this endpoint exists for may hold no
// role on the project at all, and the only other place this module publishes a
// project's currency — GET /projects, the booking picker — answers exactly
// that caller nothing. Without it the tab could not say which of the
// currencies is the project's and which are "in another currency", which is
// the one thing the per-currency shape exists to let it say.
func projectSummaryResponse(totals contracts.ProjectExpenseTotals, projectCurrency *string,
	capabilities gen.ExpensesProjectSummaryCapabilities, project *gen.ExpensesProjectOption,
) (gen.ExpensesProjectSummaryResponse, error) {
	currencies := make([]gen.ExpensesProjectSummaryCurrency, 0, len(totals.Currencies))
	for _, currency := range totals.Currencies {
		published := gen.ExpensesProjectSummaryCurrency{
			Currency:      currency.Currency,
			ReadyCount:    int32(currency.ReadyCount),
			InvoicedCount: int32(currency.InvoicedCount),
			UnpricedCount: int32(currency.UnpricedCount),
		}
		var err error
		for _, bucket := range []struct {
			from contracts.ExpenseBucket
			into *gen.ExpensesProjectSummaryBucket
		}{
			{currency.Approved, &published.Approved},
			{currency.Submitted, &published.Submitted},
			{currency.Draft, &published.Draft},
			{currency.Total, &published.Total},
		} {
			if *bucket.into, err = summaryBucket(bucket.from); err != nil {
				return gen.ExpensesProjectSummaryResponse{}, err
			}
		}
		if published.ReadyAmount, err = summaryAmount(currency.ReadyAmount); err != nil {
			return gen.ExpensesProjectSummaryResponse{}, err
		}
		if published.InvoicedAmount, err = summaryAmount(currency.InvoicedAmount); err != nil {
			return gen.ExpensesProjectSummaryResponse{}, err
		}
		currencies = append(currencies, published)
	}

	response := gen.ExpensesProjectSummaryResponse{
		Currencies:      currencies,
		ProjectCurrency: projectCurrency,
		Capabilities:    capabilities,
		Project:         project,
	}
	if totals.LastEntryDate != nil {
		day, err := time.Parse(time.DateOnly, *totals.LastEntryDate)
		if err != nil {
			return gen.ExpensesProjectSummaryResponse{}, fmt.Errorf(
				"expenses: the last entry date %q is not a date: %w", *totals.LastEntryDate, err)
		}
		response.LastEntryDate = &openapi_types.Date{Time: day}
	}
	return response, nil
}

// summaryBucket renders one bucket's count and two amounts.
func summaryBucket(b contracts.ExpenseBucket) (gen.ExpensesProjectSummaryBucket, error) {
	cost, err := summaryAmount(b.CostAmount)
	if err != nil {
		return gen.ExpensesProjectSummaryBucket{}, err
	}
	bill, err := summaryAmount(b.BillAmount)
	if err != nil {
		return gen.ExpensesProjectSummaryBucket{}, err
	}
	return gen.ExpensesProjectSummaryBucket{Count: int32(b.Count), Cost: cost, BillAmount: bill}, nil
}

// summaryAmount is one of the contract's decimal amounts as the JSON number
// this module publishes money as. Text that is not a decimal is an
// infrastructure failure and fails the read: a zero where an amount belongs is
// the one answer a figure somebody invoices from must never invent.
func summaryAmount(text string) (float64, error) {
	amount, ok := new(big.Rat).SetString(text)
	if !ok {
		return 0, fmt.Errorf("expenses: %q is not a decimal amount", text)
	}
	return floatOfRat(amount), nil
}
