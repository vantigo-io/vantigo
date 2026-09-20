package expenses

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is decision X7's arithmetic — what an expense bills its customer —
// and the one door through which the project's side sets it.
//
// The door exists because expenses:manage does not imply financial rights on a
// project, and an employee's expense form cannot carry a markup: design §5 puts
// the markup, the customer rate per kilometre and the bill amount on the
// project's side of the line, which means the project's manager prices it, and
// they do it when the expense reaches them — at approval time — not when it was
// recorded. PUT /entries/{id}/billing is that, and nothing else: it touches no
// amount the owner entered, no status and no receipt.

// billingInput is everything the pricing rule is given about one expense: the
// project it is on (nil when it has none, or when the directory can no longer
// resolve it), what it is, what it is worth, the figures it already carries and
// the figures somebody has just named.
type billingInput struct {
	Project  *contracts.ProjectEntry
	Kind     string
	Date     time.Time
	Billable bool

	Gross *big.Rat
	Vat   *big.Rat
	Km    *big.Rat

	// StoredMarkup and StoredBillRate are the figures on the row as it stands,
	// nil when it bills nothing. They are what a save by somebody who was never
	// shown them keeps rather than resets.
	StoredMarkup   *big.Rat
	StoredBillRate *big.Rat

	// NamedMarkup and NamedBillRate are what this request set, nil when it set
	// neither — a submit never does; only the pricing door can.
	NamedMarkup   *big.Rat
	NamedBillRate *big.Rat

	DefaultMarkup *big.Rat
}

// billingFigures is what an expense bills, as exact decimals.
type billingFigures struct {
	Billable   bool
	Markup     *big.Rat
	BillRate   *big.Rat
	BillAmount *big.Rat

	// RateMissing is billable mileage that carries no customer rate and finds
	// none in force on its date. It is not a refusal in itself — a line may
	// wait billable with nothing billed until whoever sees the project's money
	// fills the figure in — so only a caller who could have named one is told.
	RateMissing bool
}

// resolveBilling is the whole of decision X7's rule, in one place, for both the
// freeze a submit does and the pricing the project's side does:
//
//   - a project that bills nothing bills nothing here either, whatever was
//     asked, and a line nobody bills stores no billing figures at all;
//   - a figure this request named is the one used;
//   - otherwise the figure already on the line is kept, so neither a submit nor
//     a save by somebody whose form was never shown it can silently reset it;
//   - and only a line carrying none falls back to the server's own — the
//     settings' default markup, the mileage_customer rate in force that day.
func resolveBilling(ctx context.Context, q *store.Queries, in billingInput) (billingFigures, error) {
	f := billingFigures{
		Billable: in.Billable && in.Project != nil && in.Project.BillingType != billingNonBillable,
	}
	if !f.Billable {
		return f, nil
	}
	switch in.Kind {
	case kindOutlay:
		f.Markup = firstRat(in.NamedMarkup, in.StoredMarkup, in.DefaultMarkup)
		if f.Markup != nil && in.Gross != nil {
			f.BillAmount = outlayBillAmount(netOf(in.Gross, in.Vat), f.Markup)
		}
	case kindMileage:
		f.BillRate = firstRat(in.NamedBillRate, in.StoredBillRate)
		if f.BillRate == nil {
			rate, err := rateFor(ctx, q, rateKindMileageCustomer, in.Date)
			switch {
			case errors.Is(err, errNoRate):
				f.RateMissing = true
			case err != nil:
				return billingFigures{}, err
			default:
				f.BillRate = rate.Value
			}
		}
		if f.BillRate != nil && in.Km != nil {
			f.BillAmount = mileageBillAmount(in.Km, f.BillRate)
		}
	}
	return f, nil
}

// firstRat is the first of the candidates that is set, nil when none is.
func firstRat(candidates ...*big.Rat) *big.Rat {
	for _, v := range candidates {
		if v != nil {
			return v
		}
	}
	return nil
}

// billingColumns is billingFigures in the shape the queries want.
func billingColumns(f billingFigures) (markup, billRate, amount pgtype.Numeric, err error) {
	if markup, err = numericFromRatPtr(f.Markup, moneyPlaces); err != nil {
		return
	}
	if billRate, err = numericFromRatPtr(f.BillRate, moneyPlaces); err != nil {
		return
	}
	amount, err = numericFromRatPtr(f.BillAmount, moneyPlaces)
	return
}

// billingRefusal is why an expense cannot be priced right now — because of what
// it is, not who is asking. Invoicing is the end of the line: what has been
// sent to a customer is not repriced behind their back.
//
// The period lock is deliberately not one of the reasons (see accessFor's
// CanSetBilling): pricing is bookkeeping done after a period closes, and the
// people who do it hold financial rights on a project rather than
// expenses:manage, whom the lock exempts.
func billingRefusal(_ *caller, row store.ExpensesEntry) (string, string) {
	if row.InvoicedAt != nil {
		return "status", "An expense that has been invoiced can no longer be priced"
	}
	return "", ""
}

// PutExpensesEntriesByIdBilling Price an expense from its project
// (PUT /api/v1/expenses/entries/{id}/billing)
//
// A full replace of the billing fields alone, guarded by the revision the
// expense was read at. The refusals come in the order that is most use to the
// caller and tells them nothing they could not already see: an expense they may
// not see is the unknown id's bare 404; one with no project to price — or an
// installation with no projects module — is a 400 naming that, because whether
// an expense they *can* see carries a project is already in their copy of it;
// and only then is the money itself refused them with the access layer's 403.
func (s *server) PutExpensesEntriesByIdBilling(ctx context.Context, req gen.PutExpensesEntriesByIdBillingRequestObject) (gen.PutExpensesEntriesByIdBillingResponseObject, error) {
	body := gen.ExpensesBillingRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	row, _, a, found, err := s.visibleEntry(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.PutExpensesEntriesByIdBilling404Response{}, nil
	}
	switch {
	case !s.projectsAvailable():
		return gen.PutExpensesEntriesByIdBilling400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError("projectId", withoutProjects("billed on to a customer")))), nil
	case row.ProjectID == nil:
		return gen.PutExpensesEntriesByIdBilling400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError("projectId",
				"This expense is not booked on a project, so there is nothing to bill a customer for"))), nil
	case !a.CanSeeBilling:
		return gen.PutExpensesEntriesByIdBilling403JSONResponse(forbidden()), nil
	}
	if field, msg := billingRefusal(c, row); msg != "" {
		return gen.PutExpensesEntriesByIdBilling400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError(field, msg))), nil
	}

	figures, errs, err := s.priceEntry(ctx, q, c, row, body)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return gen.PutExpensesEntriesByIdBilling400ApplicationProblemPlusJSONResponse(invalidEntry(errs)), nil
	}
	markup, billRate, amount, err := billingColumns(figures)
	if err != nil {
		return nil, err
	}

	var (
		updated  store.ExpensesEntry
		gone     bool
		conflict *int32
		stale    [2]string
	)
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		// The claim's lock first, then the line's — the module's one order
		// (lockEntryUnit). Pricing reads no status of the claim's, but it
		// writes a row a claim-wide write may be holding, so it queues behind
		// the same lock everything else does.
		locked, _, found, err := lockEntryUnit(ctx, txq, req.Id, row.ClaimID)
		if err != nil {
			return err
		}
		if !found {
			gone = true
			return nil
		}
		// Judged again on the row as it stands under the lock: an invoicing
		// that committed since is the state refusal above, arrived a moment
		// later.
		if field, msg := billingRefusal(c, locked); msg != "" {
			stale = [2]string{field, msg}
			return nil
		}
		if locked.Revision != body.Revision {
			conflict = &locked.Revision
			return nil
		}
		updated, err = txq.UpdateEntryBilling(ctx, store.UpdateEntryBillingParams{
			ID: req.Id, Revision: body.Revision, BillingLineID: body.BillingLineId,
			Billable: figures.Billable, MarkupPercent: markup, BillRatePerKm: billRate,
			BillAmount: amount, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("expenses: price an expense: %w", err)
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case gone:
		return gen.PutExpensesEntriesByIdBilling404Response{}, nil
	case stale[1] != "":
		return gen.PutExpensesEntriesByIdBilling400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError(stale[0], stale[1]))), nil
	case conflict != nil:
		return gen.PutExpensesEntriesByIdBilling409ApplicationProblemPlusJSONResponse(
			revisionConflict(*conflict, body.Revision)), nil
	}

	resp, err := s.entryResponseFor(ctx, c, updated)
	if err != nil {
		return nil, err
	}
	return gen.PutExpensesEntriesByIdBilling200JSONResponse(resp), nil
}

// priceEntry judges a pricing body and works out the figures it asks for. Every
// call into the project directory it makes is before any transaction.
func (s *server) priceEntry(ctx context.Context, q *store.Queries, c *caller, row store.ExpensesEntry,
	body gen.ExpensesBillingRequest,
) (billingFigures, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) { errs = withFieldError(errs, field, msg) }

	project, err := s.projectsProject(ctx, *row.ProjectID)
	if err != nil {
		return billingFigures{}, nil, fmt.Errorf("expenses: look up the project: %w", err)
	}
	if project == nil {
		add("projectId", "The project this expense is booked on is no longer one this installation knows")
		return billingFigures{}, errs, nil
	}
	// A billing line the expense already carries is not judged again (design
	// §8's "existing lines stay"); a different one is judged in full.
	if body.BillingLineId != nil &&
		(row.BillingLineID == nil || *row.BillingLineID != *body.BillingLineId) {
		line, err := s.projectsBillingLine(ctx, *row.ProjectID, *body.BillingLineId)
		if err != nil {
			return billingFigures{}, nil, fmt.Errorf("expenses: look up the billing line: %w", err)
		}
		switch {
		case line == nil:
			add("billingLineId", fmt.Sprintf("Billing line %d is not one of this project's", *body.BillingLineId))
		case !line.Active:
			add("billingLineId", fmt.Sprintf("Billing line %d is inactive", *body.BillingLineId))
		}
	}

	var named, namedRate *big.Rat
	if body.MarkupPercent != nil {
		if msg := validateDecimal("A markup", *body.MarkupPercent, 0, maxMarkupPercent); msg != "" {
			add("markupPercent", msg)
		} else if row.Kind != kindOutlay || !body.Billable {
			add("markupPercent", "A markup belongs to a billable outlay")
		} else {
			named = ratFromFloat(*body.MarkupPercent)
		}
	}
	if body.BillRatePerKm != nil {
		if msg := validateAboveZero("A customer rate", *body.BillRatePerKm, maxRateValue); msg != "" {
			add("billRatePerKm", msg)
		} else if row.Kind != kindMileage || !body.Billable {
			add("billRatePerKm", "A rate per kilometre belongs to billable mileage")
		} else {
			namedRate = ratFromFloat(*body.BillRatePerKm)
		}
	}
	if body.Revision < 1 {
		add("revision", "The revision the expense was read at is required")
	}
	if len(errs) > 0 {
		return billingFigures{}, errs, nil
	}

	in, err := billingInputFor(row, project)
	if err != nil {
		return billingFigures{}, nil, err
	}
	in.Billable = body.Billable
	in.NamedMarkup, in.NamedBillRate = named, namedRate
	if in.DefaultMarkup, err = ratFromNumeric(c.Settings.DefaultMarkupPercent); err != nil {
		return billingFigures{}, nil, err
	}

	figures, err := resolveBilling(ctx, q, in)
	if err != nil {
		return billingFigures{}, nil, err
	}
	if !figures.Billable {
		// The request asked for a figure on a line the project will never bill.
		// Refused on its own field rather than answered 200 with everything
		// cleared and nothing said.
		if named != nil {
			add("markupPercent", projectBillsNothing)
		}
		if namedRate != nil {
			add("billRatePerKm", projectBillsNothing)
		}
		return figures, errs, nil
	}
	if figures.RateMissing {
		// This caller can see the project's money, so they are the one who can
		// name the figure that is missing.
		add("billRatePerKm", "No customer rate per kilometre applies on this date, so the line needs one of its own")
	}
	if overflowsMoney(figures.BillAmount) {
		field := "markupPercent"
		if row.Kind == kindMileage {
			field = "billRatePerKm"
		}
		add(field, amountTooBig)
	}
	return figures, errs, nil
}

// billingInputFor is the part of a pricing that comes off the row itself: what
// the expense is, what it is worth, and the figures it already carries.
func billingInputFor(row store.ExpensesEntry, project *contracts.ProjectEntry) (billingInput, error) {
	in := billingInput{Project: project, Kind: row.Kind, Date: row.EntryDate.Time}
	var err error
	if in.Gross, err = ratFromNumeric(row.GrossAmount); err != nil {
		return billingInput{}, err
	}
	if in.Vat, err = ratPtrFromNumeric(row.VatAmount); err != nil {
		return billingInput{}, err
	}
	if in.Km, err = ratPtrFromNumeric(row.DistanceKm); err != nil {
		return billingInput{}, err
	}
	if !row.Billable {
		return in, nil
	}
	if in.StoredMarkup, err = ratPtrFromNumeric(row.MarkupPercent); err != nil {
		return billingInput{}, err
	}
	if in.StoredBillRate, err = ratPtrFromNumeric(row.BillRatePerKm); err != nil {
		return billingInput{}, err
	}
	return in, nil
}

// GetExpensesEntriesByIdBillingLines List the billing lines an expense may be priced against
// (GET /api/v1/expenses/entries/{id}/billing-lines)
//
// The pricing dialog's own read, and the reason it is not GET /projects: that
// list answers the projects the *caller* may book an expense on, which is
// projects' CanLogTime — a member or a manager of a project still open for
// work. Pricing is a different right (accessFor's CanSeeBilling: the project's
// manager, projects:manage-all, or projects:view-financials on a project they
// can see), so a finance person on no project team is offered nothing there,
// and neither is anybody pricing a line on a project that has been completed.
// This read is keyed on the expense and judged by exactly the rule PUT
// /entries/{id}/billing is judged by, in the same order, so the dialog can
// never offer a line the save then refuses and the two doors cannot drift.
//
// It is a read: the period lock does not reach it, as it does not reach the
// pricing it serves. One directory call, outside any transaction.
func (s *server) GetExpensesEntriesByIdBillingLines(ctx context.Context, req gen.GetExpensesEntriesByIdBillingLinesRequestObject) (gen.GetExpensesEntriesByIdBillingLinesResponseObject, error) {
	// Without the projects module the operation is not there at all — the
	// answer GET /projects gives for the same reason (decision X2), rather than
	// the pricing door's 400 on a field a read has no body to carry.
	if !s.projectsAvailable() {
		return gen.GetExpensesEntriesByIdBillingLines404ApplicationProblemPlusJSONResponse(
			projectsNotInstalled()), nil
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	row, _, a, found, err := s.visibleEntry(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	switch {
	case !found:
		return billingLinesNotFound{}, nil
	case row.ProjectID == nil:
		return gen.GetExpensesEntriesByIdBillingLines400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError("projectId",
				"This expense is not booked on a project, so there is nothing to bill a customer for"))), nil
	case !a.CanSeeBilling:
		return gen.GetExpensesEntriesByIdBillingLines403JSONResponse(forbidden()), nil
	}

	lines, err := s.projectsBillingLines(ctx, *row.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("expenses: list a project's billing lines: %w", err)
	}
	out := make([]gen.ExpensesBillingLineOption, 0, len(lines))
	for _, line := range lines {
		// Only the lines still in use — an inactive one is refused on a save,
		// so offering it would be offering a mistake — and, whatever its state,
		// the one this expense already carries. A save keeps a line it is not
		// changing (design §8's "existing lines stay"), so the dialog has to be
		// able to show it; active:false is what keeps it out of the choices.
		kept := row.BillingLineID != nil && line.ID == *row.BillingLineID
		if !line.Active && !kept {
			continue
		}
		out = append(out, gen.ExpensesBillingLineOption{Id: line.ID, Code: line.Code, Active: line.Active})
	}
	// By code, which is what the dialog shows: the directory's own order is its
	// business and not a contract.
	slices.SortFunc(out, func(a, b gen.ExpensesBillingLineOption) int {
		return cmp.Or(strings.Compare(a.Code, b.Code), cmp.Compare(a.Id, b.Id))
	})
	return gen.GetExpensesEntriesByIdBillingLines200JSONResponse(out), nil
}

// billingLinesNotFound is the bare 404 an expense the caller may not see
// answers with — byte for byte an unknown id's, so the two cannot be told
// apart (decision X10). The generated response type for this operation's 404
// carries a problem body, which is the *other* thing that status means here
// (this installation has no projects module), so the empty one is written by
// hand rather than by declaring a second 404 the contract has no way to hold.
type billingLinesNotFound struct{}

func (billingLinesNotFound) VisitGetExpensesEntriesByIdBillingLinesResponse(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNotFound)
	return nil
}
