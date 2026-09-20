package expenses

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is decision X5's second track: what the customer has been billed
// for. It is per billable line on a project, and it belongs to whoever may see
// that project's money — the same financial rights the billing object and the
// pricing door are decided by, and deliberately not expenses:manage, who pays
// the employee. The two tracks never meet: a line can be reimbursed, invoiced,
// both or neither, in any order.
//
// The period lock does not appear here either. It protects what the employee
// submitted and what was approved; an invoice for December goes out in
// January, and a project manager who is not expenses:manage would otherwise be
// shut out of their own books by a lock that was never about them.

// invoicedMark is the difference between marking a line invoiced and taking it
// back: what the state has to be, and what the update is.
type invoicedMark struct {
	undo bool
}

// invoicedRefusal is why a line cannot be marked (or unmarked) right now —
// because of what it is, not who is asking. It answers the field the reason
// belongs to and the message, or "" and "" when nothing refuses.
//
// The order is the order that is most use to the caller: what the line is
// comes before what it carries, because a draft is a thing they can wait for
// and an unpriced line is a thing they can fix.
func invoicedRefusal(row store.ExpensesEntry, unit entryUnit, m invoicedMark) (string, string) {
	if m.undo {
		if row.InvoicedAt == nil {
			return "status", "This expense has not been marked invoiced"
		}
		return "", ""
	}
	switch {
	case row.Kind == kindPerDiem:
		// It bills nobody anything (design §4), so there is nothing to put on an
		// invoice. Named here as well as on the pricing door, so a row that went
		// billable before that door was closed cannot slip out this way.
		return "kind", perDiemNotBillable
	case unit.Status != statusApproved:
		return "status", fmt.Sprintf(
			"Only an approved expense can be marked invoiced; this one is %s", unit.Status)
	case row.InvoicedAt != nil:
		return "status", "This expense has already been marked invoiced"
	case !row.Billable:
		return "billable", "Only a billable expense is invoiced on to a customer"
	case !row.BillAmount.Valid:
		return "billAmount",
			"This expense has no amount to bill yet, so it must be priced before it can be invoiced"
	}
	return "", ""
}

// PostExpensesEntriesByIdInvoiced Mark an expense invoiced
// (POST /api/v1/expenses/entries/{id}/invoiced)
func (s *server) PostExpensesEntriesByIdInvoiced(ctx context.Context, req gen.PostExpensesEntriesByIdInvoicedRequestObject) (gen.PostExpensesEntriesByIdInvoicedResponseObject, error) {
	body := gen.ExpensesInvoicedRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	resp, err := s.markInvoiced(ctx, req.Id, body.Revision, body.Reference, invoicedMark{})
	if err != nil {
		return nil, err
	}
	switch {
	case resp.notFound:
		return gen.PostExpensesEntriesByIdInvoiced404Response{}, nil
	case resp.forbidden:
		return gen.PostExpensesEntriesByIdInvoiced403JSONResponse(forbidden()), nil
	case resp.errs != nil:
		return gen.PostExpensesEntriesByIdInvoiced400ApplicationProblemPlusJSONResponse(invalidEntry(resp.errs)), nil
	case resp.conflict != nil:
		return gen.PostExpensesEntriesByIdInvoiced409ApplicationProblemPlusJSONResponse(
			revisionConflict(*resp.conflict, body.Revision)), nil
	}
	return gen.PostExpensesEntriesByIdInvoiced200JSONResponse(resp.entry), nil
}

// PostExpensesEntriesByIdInvoicedUndo Undo marking an expense invoiced
// (POST /api/v1/expenses/entries/{id}/invoiced/undo)
func (s *server) PostExpensesEntriesByIdInvoicedUndo(ctx context.Context, req gen.PostExpensesEntriesByIdInvoicedUndoRequestObject) (gen.PostExpensesEntriesByIdInvoicedUndoResponseObject, error) {
	body := gen.ExpensesInvoicedUndoRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	resp, err := s.markInvoiced(ctx, req.Id, body.Revision, nil, invoicedMark{undo: true})
	if err != nil {
		return nil, err
	}
	switch {
	case resp.notFound:
		return gen.PostExpensesEntriesByIdInvoicedUndo404Response{}, nil
	case resp.forbidden:
		return gen.PostExpensesEntriesByIdInvoicedUndo403JSONResponse(forbidden()), nil
	case resp.errs != nil:
		return gen.PostExpensesEntriesByIdInvoicedUndo400ApplicationProblemPlusJSONResponse(invalidEntry(resp.errs)), nil
	case resp.conflict != nil:
		return gen.PostExpensesEntriesByIdInvoicedUndo409ApplicationProblemPlusJSONResponse(
			revisionConflict(*resp.conflict, body.Revision)), nil
	}
	return gen.PostExpensesEntriesByIdInvoicedUndo200JSONResponse(resp.entry), nil
}

// invoicedOutcome is what one of the two operations answers: the four
// refusals they share, or the expense as it now stands.
type invoicedOutcome struct {
	notFound  bool
	forbidden bool
	errs      map[string][]string
	conflict  *int32
	entry     gen.ExpensesEntryResponse
}

// markInvoiced is both operations, because they differ only in their state
// rule and their update. The refusals come in the pricing door's own order and
// tell the caller nothing they could not already see: an expense they may not
// see is the unknown id's bare 404; one with no project to invoice — or an
// installation with no projects module — is a 400 naming that, because whether
// an expense they *can* see carries a project is already in their copy of it;
// and only then is the money itself refused them with the access layer's 403.
func (s *server) markInvoiced(ctx context.Context, id int64, revision int32, reference *string,
	m invoicedMark,
) (invoicedOutcome, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return invoicedOutcome{}, err
	}
	row, unit, a, found, err := s.visibleEntry(ctx, q, c, id)
	if err != nil {
		return invoicedOutcome{}, err
	}
	switch {
	case !found:
		return invoicedOutcome{notFound: true}, nil
	case !s.projectsAvailable():
		return invoicedOutcome{errs: fieldError("projectId", withoutProjects("invoiced on to a customer"))}, nil
	case row.ProjectID == nil:
		return invoicedOutcome{errs: fieldError("projectId",
			"This expense is not booked on a project, so there is no customer to invoice it to")}, nil
	case !a.CanSeeBilling:
		return invoicedOutcome{forbidden: true}, nil
	}
	if field, msg := invoicedRefusal(row, unit, m); msg != "" {
		return invoicedOutcome{errs: fieldError(field, msg)}, nil
	}
	var trimmed *string
	if msg := optionalText(reference, "A reference", referenceMaxLength, &trimmed); msg != "" {
		return invoicedOutcome{errs: fieldError("reference", msg)}, nil
	}
	if revision < 1 {
		return invoicedOutcome{errs: fieldError("revision",
			"The revision the expense was read at is required")}, nil
	}

	var (
		updated store.ExpensesEntry
		out     invoicedOutcome
	)
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, lockedUnit, _, found, err := lockEntryUnit(ctx, txq, id, row.ClaimID)
		if err != nil {
			return err
		}
		if !found {
			out = invoicedOutcome{notFound: true}
			return nil
		}
		// Judged again on the row as it stands under the lock: an unapproval
		// or another invoicing that committed since is the state refusal
		// above, arrived a moment later.
		if field, msg := invoicedRefusal(locked, lockedUnit, m); msg != "" {
			out = invoicedOutcome{errs: fieldError(field, msg)}
			return nil
		}
		if locked.Revision != revision {
			current := locked.Revision
			out = invoicedOutcome{conflict: &current}
			return nil
		}
		if m.undo {
			updated, err = txq.UnmarkEntryInvoiced(ctx, store.UnmarkEntryInvoicedParams{
				ID: id, Revision: revision, Now: s.deps.Clock(),
			})
		} else {
			updated, err = txq.MarkEntryInvoiced(ctx, store.MarkEntryInvoicedParams{
				ID: id, Revision: revision, InvoicedBy: c.UserID, Reference: trimmed, Now: s.deps.Clock(),
			})
		}
		if err != nil {
			return fmt.Errorf("expenses: mark an expense invoiced: %w", err)
		}
		return nil
	})
	switch {
	case err != nil:
		return invoicedOutcome{}, err
	case out.notFound || out.errs != nil || out.conflict != nil:
		return out, nil
	}

	resp, err := s.entryResponseFor(ctx, c, updated)
	if err != nil {
		return invoicedOutcome{}, err
	}
	return invoicedOutcome{entry: resp}, nil
}
