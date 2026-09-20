package expenses

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is the expenses themselves: recording one, reading it, replacing
// it, deleting it and listing them.
//
// One shape runs through every write. Everything another module has to answer
// — who the expense is for, whether they may book on the project, what the
// line is called — is asked *before* the transaction; the transaction takes
// the row's lock, decides on what it reads under it, and writes; and whatever
// the response needs from a neighbour is resolved *after* it. The project and
// user directories read through this module's own connection pool, so a
// transaction that held locks while waiting for one of them could starve the
// pool; contractscalls.go carries the check that none ever does.

// billingNonBillable is the billing type of a project that bills nothing. A
// line on one is never billable, whatever the request asked for, the way time
// resolves it.
const billingNonBillable = "non-billable"

// entryValues is everything the server decides about one save rather than the
// caller: a mileage line's rates and amount, the effective billable, and what
// the customer is billed. All of it is exact decimal until it reaches the
// columns.
type entryValues struct {
	Currency      string
	Gross         *big.Rat
	Vat           *big.Rat
	Rate          *big.Rat
	PassengerRate *big.Rat
	Billable      bool
	MarkupPercent *big.Rat
	BillRatePerKm *big.Rat
	BillAmount    *big.Rat
}

// columns is entryValues in the shape the queries want, each amount written
// through its exact decimal text so the column's own scale never rounds
// anything a second time.
type entryColumns struct {
	Gross         pgtype.Numeric
	Vat           pgtype.Numeric
	Distance      pgtype.Numeric
	Rate          pgtype.Numeric
	PassengerRate pgtype.Numeric
	MarkupPercent pgtype.Numeric
	BillRatePerKm pgtype.Numeric
	BillAmount    pgtype.Numeric
}

// columnsOf converts a parsed body and the values resolved for it into the
// numerics the insert and the update take.
func columnsOf(p parsedEntry, v entryValues) (entryColumns, error) {
	var c entryColumns
	var err error
	for _, conv := range []struct {
		value  *big.Rat
		places int
		into   *pgtype.Numeric
	}{
		{v.Gross, moneyPlaces, &c.Gross},
		{v.Vat, moneyPlaces, &c.Vat},
		{p.DistanceKm, distancePlaces, &c.Distance},
		{v.Rate, moneyPlaces, &c.Rate},
		{v.PassengerRate, moneyPlaces, &c.PassengerRate},
		{v.MarkupPercent, moneyPlaces, &c.MarkupPercent},
		{v.BillRatePerKm, moneyPlaces, &c.BillRatePerKm},
		{v.BillAmount, moneyPlaces, &c.BillAmount},
	} {
		if *conv.into, err = numericFromRatPtr(conv.value, conv.places); err != nil {
			return entryColumns{}, err
		}
	}
	return c, nil
}

// resolveOwner is the person the expense concerns (design §5): the caller
// unless userId names somebody else, which needs expenses:manage and a person
// identity still has as active. It is asked of the user directory before any
// transaction.
func (s *server) resolveOwner(ctx context.Context, c *caller, userID *openapi_types.UUID,
	add func(field, msg string),
) (uuid.UUID, error) {
	if userID == nil || *userID == c.UserID {
		return c.UserID, nil
	}
	if !c.Manage {
		add("userId", "Recording an expense for somebody else needs the Manage expenses permission")
		return c.UserID, nil
	}
	user, err := s.usersUser(ctx, *userID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("expenses: look up the expense's owner: %w", err)
	}
	if user == nil || !user.Active {
		add("userId", "Nobody active has that user id")
		return c.UserID, nil
	}
	return user.ID, nil
}

// checkProject is decision X9's rule over another module's data: the *owner* —
// the person the expense concerns, not whoever is recording it — may book on
// the project, and the billing line is one of that project's active ones. A
// project they may not book on answers the one cannotBookOnProject message
// whatever the reason, and stops there: checking a line on it would tell a
// caller which of its ids exist.
//
// A link the save is *keeping* is not judged again — design §8's "a project
// that is no longer active: existing lines stay", which is the same sentence
// that gives checkCategory its grandfathering. A project that has been
// completed, or that the owner has been taken off, or a billing line since
// deactivated, must not make the expense unsaveable: its owner would have to
// drop the link to fix a typo, which is the cost record quietly disappearing.
// A *changed* project or line is judged in full, so nothing new is booked
// somewhere it may not be.
//
// It also answers whether the kept project is one the directory can no longer
// resolve at all. That is not a refusal either — the stored id stays (decision
// X2) — but nothing can be priced against a project that cannot be read, so
// the save carries the project columns through untouched instead.
func (s *server) checkProject(ctx context.Context, ownerID uuid.UUID, p parsedEntry,
	current *store.ExpensesEntry, add func(field, msg string),
) (*contracts.ProjectEntry, bool, error) {
	if p.ProjectID == nil || !s.projectsAvailable() {
		return nil, false, nil
	}
	keptProject := current != nil && current.ProjectID != nil && *current.ProjectID == *p.ProjectID
	if !keptProject {
		allowed, err := s.projectsCanLogTime(ctx, *p.ProjectID, ownerID)
		if err != nil {
			return nil, false, fmt.Errorf("expenses: check the owner may book on the project: %w", err)
		}
		if !allowed {
			add("projectId", cannotBookOnProject)
			return nil, false, nil
		}
	}
	project, err := s.projectsProject(ctx, *p.ProjectID)
	if err != nil {
		return nil, false, fmt.Errorf("expenses: look up the project: %w", err)
	}
	if project == nil {
		if keptProject {
			return nil, true, nil
		}
		add("projectId", cannotBookOnProject)
		return nil, false, nil
	}

	keptLine := keptProject && p.LineID != nil &&
		current.BillingLineID != nil && *current.BillingLineID == *p.LineID
	if p.LineID != nil && !keptLine {
		line, err := s.projectsBillingLine(ctx, *p.ProjectID, *p.LineID)
		if err != nil {
			return nil, false, fmt.Errorf("expenses: look up the billing line: %w", err)
		}
		switch {
		case line == nil:
			add("billingLineId", fmt.Sprintf("Billing line %d is not one of this project's", *p.LineID))
		case !line.Active:
			add("billingLineId", fmt.Sprintf("Billing line %d is inactive", *p.LineID))
		}
	}
	return project, false, nil
}

// checkCategory is design §8's category rule: an outlay's category must be one
// that exists, and a deactivated one may not be put on a line that does not
// already carry it — the lines that do keep it, and stay editable.
func checkCategory(ctx context.Context, q *store.Queries, p parsedEntry, current *store.ExpensesEntry,
	add func(field, msg string),
) error {
	if p.CategoryID == nil {
		return nil
	}
	category, err := q.GetCategory(ctx, *p.CategoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		add("categoryId", fmt.Sprintf("No category has id %d", *p.CategoryID))
		return nil
	}
	if err != nil {
		return fmt.Errorf("expenses: look up the category: %w", err)
	}
	if !category.Active {
		kept := current != nil && current.CategoryID != nil && *current.CategoryID == category.ID
		if !kept {
			add("categoryId", fmt.Sprintf("The category '%s' is no longer in use", category.Name))
		}
	}
	return nil
}

// billingScope is what one save knows about the project side of the expense:
// the project it lands on (nil when it has none, or when the directory could
// not resolve the one it keeps), whether the caller may see — and therefore
// set — the money the project makes on it, and the row being replaced, whose
// figures are preserved for a caller who cannot see them.
type billingScope struct {
	Project   *contracts.ProjectEntry
	Financial bool
	Current   *store.ExpensesEntry
}

// storedBillingFigure is a billing figure already on the row being replaced,
// nil on a create and nil when that row billed nothing — the values a save is
// to carry forward rather than compute again.
func storedBillingFigure(current *store.ExpensesEntry, pick func(store.ExpensesEntry) pgtype.Numeric) (*big.Rat, error) {
	if current == nil || !current.Billable {
		return nil, nil
	}
	return ratPtrFromNumeric(pick(*current))
}

// resolveValues prices one expense (design §4). A mileage line's amount, rate
// and passenger supplement come from the dated rate table for its own date and
// are written again on every save while it is a draft; an outlay stands as it
// was entered. What the customer is billed is added on top when the line is
// billable and the project bills at all.
//
// The two customer-facing figures — the markup on an outlay and the rate per
// kilometre on mileage — belong to whoever may see the project's money (design
// §5), which is what makes their rules here more than a default:
//
//   - a figure the caller sent is theirs, and only a caller with financial
//     rights can have sent one (prepare refuses the field otherwise);
//   - otherwise the figure already on the line is kept, so a save by somebody
//     whose form was never shown it cannot silently reset it, and the amount
//     billed still follows the new net or distance;
//   - and only a line that carries none falls back to the server's own — the
//     settings' default markup, the mileage_customer rate in force that day.
//
// A billable mileage line with no customer rate in force and nobody able to
// name one is saved billable with neither a rate nor an amount, rather than
// refusing an employee over a price they may not know exists; whoever can see
// the project's money fills it in.
func (s *server) resolveValues(ctx context.Context, q *store.Queries, c *caller, p parsedEntry,
	sc billingScope, add func(field, msg string),
) (entryValues, error) {
	project := sc.Project
	v := entryValues{Currency: p.Currency, Gross: p.Gross, Vat: p.Vat}

	if p.Kind == kindMileage {
		v.Currency = c.Settings.DefaultCurrency
		rate, err := rateFor(ctx, q, rateKindMileage, p.Date)
		switch {
		case errors.Is(err, errNoRate):
			add("entryDate", "No mileage rate applies on this date")
		case err != nil:
			return entryValues{}, err
		default:
			v.Rate = rate.Value
		}
		if p.Passengers > 0 {
			passenger, err := rateFor(ctx, q, rateKindMileagePassenger, p.Date)
			switch {
			case errors.Is(err, errNoRate):
				add("passengers", "No passenger rate applies on this date")
			case err != nil:
				return entryValues{}, err
			default:
				v.PassengerRate = passenger.Value
			}
		}
		if v.Rate != nil && p.DistanceKm != nil {
			v.Gross = mileageAmount(p.DistanceKm, v.Rate, v.PassengerRate, int(p.Passengers))
		}
	}

	// Decision X7: a project that bills nothing bills nothing here either,
	// whatever the request asked for, and a line nobody bills stores no
	// billing figures at all.
	v.Billable = p.Billable && project != nil && project.BillingType != billingNonBillable
	if !v.Billable {
		// A figure sent for a line that will bill nothing is refused rather
		// than accepted and quietly dropped: the caller asked for a markup and
		// would otherwise get a 200 with no markup and no explanation.
		refuseFiguresNothingWillUse(p, add)
		return v, nil
	}

	switch p.Kind {
	case kindOutlay:
		v.MarkupPercent = p.MarkupPercent
		if v.MarkupPercent == nil {
			kept, err := storedBillingFigure(sc.Current, func(e store.ExpensesEntry) pgtype.Numeric { return e.MarkupPercent })
			if err != nil {
				return entryValues{}, err
			}
			v.MarkupPercent = kept
		}
		if v.MarkupPercent == nil {
			markup, err := ratFromNumeric(c.Settings.DefaultMarkupPercent)
			if err != nil {
				return entryValues{}, err
			}
			v.MarkupPercent = markup
		}
		if v.Gross != nil {
			v.BillAmount = outlayBillAmount(netOf(v.Gross, v.Vat), v.MarkupPercent)
		}
	case kindMileage:
		v.BillRatePerKm = p.BillRatePerKm
		if v.BillRatePerKm == nil {
			kept, err := storedBillingFigure(sc.Current, func(e store.ExpensesEntry) pgtype.Numeric { return e.BillRatePerKm })
			if err != nil {
				return entryValues{}, err
			}
			v.BillRatePerKm = kept
		}
		if v.BillRatePerKm == nil {
			rate, err := rateFor(ctx, q, rateKindMileageCustomer, p.Date)
			switch {
			case errors.Is(err, errNoRate):
				// Only somebody who could have named one is told there is
				// none: to anybody else the message would be about a price
				// this module deliberately hides from them.
				if sc.Financial {
					add("billRatePerKm", "No customer rate per kilometre applies on this date, so the line needs one of its own")
				}
			case err != nil:
				return entryValues{}, err
			default:
				v.BillRatePerKm = rate.Value
			}
		}
		if v.BillRatePerKm != nil && p.DistanceKm != nil {
			v.BillAmount = mileageBillAmount(p.DistanceKm, v.BillRatePerKm)
		}
	}
	return v, nil
}

// projectBillsNothing is the message the two customer-facing figures carry when
// they are named for a line the project will never bill.
const projectBillsNothing = "This project bills nothing, so there is nothing to charge a customer for"

// refuseFiguresNothingWillUse reports a markup or a customer rate per kilometre
// named on a line that will not be billable — which here can only mean the
// project bills nothing, because parseProjectFields has already refused both on
// a line that did not ask to be billable at all.
func refuseFiguresNothingWillUse(p parsedEntry, add func(field, msg string)) {
	if p.MarkupPercent != nil {
		add("markupPercent", projectBillsNothing)
	}
	if p.BillRatePerKm != nil {
		add("billRatePerKm", projectBillsNothing)
	}
}

// maxMoneyRat is the amount columns' ceiling as an exact decimal, for the
// products validation cannot bound on their own.
var maxMoneyRat = ratFromFloat(maxMoney)

// drivingField is the field a caller can have driven an amount with on this
// kind: what they paid for an outlay, how far they drove on a mileage line.
func drivingField(kind string) string {
	if kind == kindMileage {
		return "distanceKm"
	}
	return "grossAmount"
}

// carriedBillAmount is what a save whose project columns are carried through
// bills the customer: the markup or the customer rate per kilometre already on
// the row — neither of which this save may judge or change — applied to the
// net or the distance it is writing. nil when the line bills nothing, or when
// it carries no figure to bill with, which is the same nothing the columns
// already hold.
//
// It is arithmetic over this module's own row and asks nobody anything, so it
// is safe under the lock, where the row it reads is the one being written.
func carriedBillAmount(p prepared, row store.ExpensesEntry) (*big.Rat, error) {
	if !row.Billable {
		return nil, nil
	}
	switch p.Parsed.Kind {
	case kindOutlay:
		markup, err := ratPtrFromNumeric(row.MarkupPercent)
		if err != nil || markup == nil || p.Values.Gross == nil {
			return nil, err
		}
		return outlayBillAmount(netOf(p.Values.Gross, p.Values.Vat), markup), nil
	case kindMileage:
		rate, err := ratPtrFromNumeric(row.BillRatePerKm)
		if err != nil || rate == nil || p.Parsed.DistanceKm == nil {
			return nil, err
		}
		return mileageBillAmount(p.Parsed.DistanceKm, rate), nil
	}
	return nil, nil
}

// checkAmountsFit is the bound on what the arithmetic produced. Every field a
// caller sends passes its own rule and only their product can overrun
// numeric(12,2) — 9999.9 kilometres at a rate an administrator was allowed to
// enter, a ten-times markup on the largest storable amount — and a column
// refusing the write would be a 500 for a body that broke no documented rule.
// The refusal names the field that drove the amount.
func checkAmountsFit(p parsedEntry, v entryValues, add func(field, msg string)) {
	grossField := "grossAmount"
	billField := "markupPercent"
	if p.Kind == kindMileage {
		grossField, billField = "distanceKm", "billRatePerKm"
	}
	if overflowsMoney(v.Gross) {
		add(grossField, amountTooBig)
	}
	if overflowsMoney(v.BillAmount) {
		add(billField, amountTooBig)
	}
}

// overflowsMoney reports whether an amount is more than a numeric(12,2) holds.
func overflowsMoney(v *big.Rat) bool { return v != nil && v.Cmp(maxMoneyRat) > 0 }

// amountTooBig is the message an amount that will not fit carries.
var amountTooBig = fmt.Sprintf("This works out to more than %s, which is more than an expense can hold",
	formatNumber(maxMoney))

// prepared is one save judged and priced, ready for the database.
type prepared struct {
	Owner   uuid.UUID
	Parsed  parsedEntry
	Values  entryValues
	Columns entryColumns
	Errors  map[string][]string

	// CarryProject says the six project columns are not this save's to write:
	// the caller could not have named them and nothing can judge them, so an
	// update takes them from the row it locks instead. It is set when this
	// installation has no projects module at all (decision X2 — the stored ids
	// stay), and when the project the line keeps is one the directory can no
	// longer resolve.
	CarryProject bool
}

// onlyFinancialRights is the message the two customer-facing figures carry
// when somebody who may not see the project's money tries to set one.
const onlyFinancialRights = "Only someone who can see the project's financials can set this"

// prepare runs every rule a save is held to, in the order that asks nothing of
// another module twice: the body, then the owner, then the project, then the
// category and the rate table. current is the entry being replaced, nil on a
// create — it is what lets a line keep a category that has since been
// deactivated. Nothing here takes a lock.
func (s *server) prepare(ctx context.Context, q *store.Queries, c *caller, body entryBody,
	userID *openapi_types.UUID, current *store.ExpensesEntry,
) (prepared, error) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}

	parsed, parseErrs := parseEntry(body, c.Settings.DefaultCurrency, s.projectsAvailable())
	for field, messages := range parseErrs {
		for _, msg := range messages {
			add(field, msg)
		}
	}

	owner, err := s.resolveOwner(ctx, c, userID, add)
	if err != nil {
		return prepared{}, err
	}
	if current != nil {
		owner = current.UserID
	}

	if len(errs) > 0 {
		// The body did not stand up, so there is nothing consistent to ask the
		// project directory or the rate table about.
		return prepared{Owner: owner, Errors: errs}, nil
	}

	// Whether the caller may see — and so set — the money the project makes on
	// this expense, decided on the project the save lands on rather than the
	// one it came from. The role is read once and cached beside the ones the
	// response shaping will ask for.
	financial := false
	if s.projectsAvailable() && parsed.ProjectID != nil {
		role, err := c.role(ctx, s, *parsed.ProjectID)
		if err != nil {
			return prepared{}, err
		}
		financial = c.seesProjectFinancials(role)
	}
	if s.projectsAvailable() && !financial {
		// Design §5 puts the markup and the customer rate per kilometre on the
		// project's side of the line, in both directions: a caller who is not
		// sent them may not set them either. Without the projects module
		// parseEntry has already refused both, on the same fields.
		if body.MarkupPercent != nil {
			add("markupPercent", onlyFinancialRights)
		}
		if body.BillRatePerKm != nil {
			add("billRatePerKm", onlyFinancialRights)
		}
	}

	project, projectLost, err := s.checkProject(ctx, owner, parsed, current, add)
	if err != nil {
		return prepared{}, err
	}
	if err := checkCategory(ctx, q, parsed, current, add); err != nil {
		return prepared{}, err
	}
	if current != nil {
		stranded, err := changeStrandsReceipts(ctx, q, *current, parsed.Kind)
		if err != nil {
			return prepared{}, err
		}
		if stranded {
			add("kind", receiptsStranded)
		}
	}
	if !c.mayWritePast(parsed.Date) {
		add("entryDate", lockedBeforeMessage(*lockedBefore(c.Settings)))
	}
	carry := current != nil && (!s.projectsAvailable() || projectLost)
	values, err := s.resolveValues(ctx, q, c, parsed,
		billingScope{Project: project, Financial: financial, Current: current}, add)
	if err != nil {
		return prepared{}, err
	}
	checkAmountsFit(parsed, values, add)
	if len(errs) > 0 {
		return prepared{Owner: owner, Errors: errs}, nil
	}

	columns, err := columnsOf(parsed, values)
	if err != nil {
		return prepared{}, err
	}
	return prepared{
		Owner: owner, Parsed: parsed, Values: values, Columns: columns, CarryProject: carry,
	}, nil
}

// PostExpensesEntries Record an expense
// (POST /api/v1/expenses/entries)
//
// The expense is a draft owned by the caller, or by the person userId names.
// Nothing about it contends with another row — there is no cap and no sequence
// to hold — so it is one insert rather than a locked transaction.
func (s *server) PostExpensesEntries(ctx context.Context, req gen.PostExpensesEntriesRequestObject) (gen.PostExpensesEntriesResponseObject, error) {
	body := gen.ExpensesEntryRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	p, err := s.prepare(ctx, q, c, bodyOfCreate(body), body.UserId, nil)
	if err != nil {
		return nil, err
	}
	if len(p.Errors) > 0 {
		return gen.PostExpensesEntries400ApplicationProblemPlusJSONResponse(invalidEntry(p.Errors)), nil
	}

	created, err := q.InsertEntry(ctx, store.InsertEntryParams{
		UserID:          p.Owner,
		CreatedByUserID: c.UserID,
		Kind:            p.Parsed.Kind,
		EntryDate:       pgDate(p.Parsed.Date),
		Description:     p.Parsed.Description,
		CategoryID:      p.Parsed.CategoryID,
		Supplier:        p.Parsed.Supplier,
		PaidBy:          p.Parsed.PaidBy,
		Currency:        p.Values.Currency,
		GrossAmount:     p.Columns.Gross,
		VatAmount:       p.Columns.Vat,
		DistanceKm:      p.Columns.Distance,
		FromPlace:       p.Parsed.FromPlace,
		ToPlace:         p.Parsed.ToPlace,
		Passengers:      p.Parsed.Passengers,
		Rate:            p.Columns.Rate,
		PassengerRate:   p.Columns.PassengerRate,
		ProjectID:       p.Parsed.ProjectID,
		BillingLineID:   p.Parsed.LineID,
		Billable:        p.Values.Billable,
		MarkupPercent:   p.Columns.MarkupPercent,
		BillRatePerKm:   p.Columns.BillRatePerKm,
		BillAmount:      p.Columns.BillAmount,
		Now:             s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: record an expense: %w", err)
	}

	resp, err := s.entryResponseFor(ctx, c, created)
	if err != nil {
		return nil, err
	}
	return gen.PostExpensesEntries201JSONResponse(resp), nil
}

// GetExpensesEntriesById Get an expense by id
// (GET /api/v1/expenses/entries/{id})
//
// A caller who may not see the expense gets the same bare 404 an unknown id
// gets, which is why the row is loaded before the caller's access to it is
// resolved and then discarded.
func (s *server) GetExpensesEntriesById(ctx context.Context, req gen.GetExpensesEntriesByIdRequestObject) (gen.GetExpensesEntriesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	row, _, found, err := s.visibleEntry(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.GetExpensesEntriesById404Response{}, nil
	}
	resp, err := s.entryResponseFor(ctx, c, row)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesEntriesById200JSONResponse(resp), nil
}

// visibleEntry loads one expense and the caller's access to it, answering
// found=false both for an unknown id and for an expense the caller may not
// see, so the two can never be told apart.
func (s *server) visibleEntry(ctx context.Context, q *store.Queries, c *caller, id int64) (store.ExpensesEntry, entryAccess, bool, error) {
	row, err := q.GetEntry(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ExpensesEntry{}, entryAccess{}, false, nil
	}
	if err != nil {
		return store.ExpensesEntry{}, entryAccess{}, false, fmt.Errorf("expenses: get an expense: %w", err)
	}
	a, err := s.entryAccess(ctx, c, row)
	if err != nil {
		return store.ExpensesEntry{}, entryAccess{}, false, err
	}
	if !a.CanSee {
		return store.ExpensesEntry{}, entryAccess{}, false, nil
	}
	return row, a, true, nil
}

// PutExpensesEntriesById Change an expense
// (PUT /api/v1/expenses/entries/{id})
//
// A full replace, held to every rule a create is held to and guarded by the
// revision the entry was read at. The refusals come in the order that tells
// the caller least about what they may not touch: an expense they may not see
// is the unknown id's 404, one they may see but not change the access layer's
// 403, and only then is the body judged (400). Inside the transaction the row
// is locked and read again, and judged by exactly the rule that judged it
// before the transaction: a submit that committed since is entryStateRefusal's
// 400 naming the status, arrived a moment later, and a save that only moved the
// revision on is a 409 — the state first, because a caller holding a stale
// revision of an expense that has been submitted can do nothing with a fresher
// one either.
func (s *server) PutExpensesEntriesById(ctx context.Context, req gen.PutExpensesEntriesByIdRequestObject) (gen.PutExpensesEntriesByIdResponseObject, error) {
	body := gen.ExpensesEntryUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	current, a, found, err := s.visibleEntry(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.PutExpensesEntriesById404Response{}, nil
	}
	if !a.IsWriter {
		return gen.PutExpensesEntriesById403JSONResponse(forbidden()), nil
	}
	// What the expense is right now — settled, or dated inside a closed period
	// — is a fact about the expense rather than about the caller, so it is a
	// 400 naming the reason. The lock is judged here on the day the expense
	// *has*, and again in prepare on the day it is being given, so a locked
	// line can be edited neither into nor out of the lock.
	if field, msg := entryStateRefusal(c, current); msg != "" {
		return gen.PutExpensesEntriesById400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError(field, msg))), nil
	}

	p, err := s.prepare(ctx, q, c, bodyOfUpdate(body), nil, &current)
	if err != nil {
		return nil, err
	}
	// Revisions start at 1, and an absent one decodes as 0: refused on the
	// field rather than answered with a conflict against a revision nobody
	// read.
	if body.Revision < 1 {
		p.Errors = withFieldError(p.Errors, "revision", "The revision the expense was read at is required")
	}
	if len(p.Errors) > 0 {
		return gen.PutExpensesEntriesById400ApplicationProblemPlusJSONResponse(invalidEntry(p.Errors)), nil
	}

	var (
		updated    store.ExpensesEntry
		gone       bool
		staleField string
		staleMsg   string
		conflict   *int32
	)
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		row, err := txq.LockEntry(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			gone = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("expenses: lock an expense: %w", err)
		}
		if staleField, staleMsg = entryStateRefusal(c, row); staleMsg != "" {
			return nil
		}
		// And judged again on the receipts as they stand under the lock: an
		// upload that committed since must not be left on a line that stopped
		// carrying receipts, which is the one state nothing here can undo.
		stranded, err := changeStrandsReceipts(ctx, txq, row, p.Parsed.Kind)
		if err != nil {
			return err
		}
		if stranded {
			staleField, staleMsg = "kind", receiptsStranded
			return nil
		}
		if row.Revision != body.Revision {
			conflict = &row.Revision
			return nil
		}
		params := store.UpdateEntryParams{
			ID:            row.ID,
			Revision:      body.Revision,
			AnyOwner:      c.Manage,
			UserID:        c.UserID,
			Kind:          p.Parsed.Kind,
			EntryDate:     pgDate(p.Parsed.Date),
			Description:   p.Parsed.Description,
			CategoryID:    p.Parsed.CategoryID,
			Supplier:      p.Parsed.Supplier,
			PaidBy:        p.Parsed.PaidBy,
			Currency:      p.Values.Currency,
			GrossAmount:   p.Columns.Gross,
			VatAmount:     p.Columns.Vat,
			DistanceKm:    p.Columns.Distance,
			FromPlace:     p.Parsed.FromPlace,
			ToPlace:       p.Parsed.ToPlace,
			Passengers:    p.Parsed.Passengers,
			Rate:          p.Columns.Rate,
			PassengerRate: p.Columns.PassengerRate,
			ProjectID:     p.Parsed.ProjectID,
			BillingLineID: p.Parsed.LineID,
			Billable:      p.Values.Billable,
			MarkupPercent: p.Columns.MarkupPercent,
			BillRatePerKm: p.Columns.BillRatePerKm,
			BillAmount:    p.Columns.BillAmount,
			Now:           s.deps.Clock(),
		}
		if p.CarryProject {
			// Decision X2: an installation that no longer has the projects
			// module — or a project the directory can no longer resolve —
			// leaves what was booked exactly as it was booked. The values come
			// off the row this transaction holds, not the one the handler read
			// before it, so a write that committed in between is carried
			// forward rather than undone.
			params.ProjectID = row.ProjectID
			params.BillingLineID = row.BillingLineID
			params.Billable = row.Billable
			params.MarkupPercent = row.MarkupPercent
			params.BillRatePerKm = row.BillRatePerKm
			// What it bills is not carried, though: it is worked out again
			// from those same carried figures and the net or the distance this
			// save is writing. Copying the stored amount would leave the
			// customer figure standing against a gross that has been
			// rewritten, which is a number that follows nothing.
			amount, err := carriedBillAmount(p, row)
			if err != nil {
				return err
			}
			if overflowsMoney(amount) {
				staleField, staleMsg = drivingField(p.Parsed.Kind), amountTooBig
				return nil
			}
			if params.BillAmount, err = numericFromRatPtr(amount, moneyPlaces); err != nil {
				return err
			}
		}
		updated, err = txq.UpdateEntry(ctx, params)
		return err
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("expenses: change an expense: %w", err)
	case gone:
		return gen.PutExpensesEntriesById404Response{}, nil
	case staleMsg != "":
		return gen.PutExpensesEntriesById400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError(staleField, staleMsg))), nil
	case conflict != nil:
		return gen.PutExpensesEntriesById409ApplicationProblemPlusJSONResponse(
			revisionConflict(*conflict, body.Revision)), nil
	}

	resp, err := s.entryResponseFor(ctx, c, updated)
	if err != nil {
		return nil, err
	}
	return gen.PutExpensesEntriesById200JSONResponse(resp), nil
}

// DeleteExpensesEntriesById Delete an expense
// (DELETE /api/v1/expenses/entries/{id})
//
// Delete is for what is still editable: a draft or a rejected line, its owner's
// or expenses:manage's, and not before the period lock — exactly
// capabilities.canDelete. A caller who may see the expense but not delete it
// gets the access layer's own 403; one who may not see it, the unknown id's
// 404.
//
// The receipt rows go with the expense (the foreign key cascades). Their object
// keys are read under the expense's own row lock, in the transaction that
// deletes it, because an upload takes that same lock: read outside it, a
// receipt committing between the read and the delete would have its row
// cascaded away and its object left behind. The objects themselves are removed
// once the delete has committed, and never before — a delete that turns out not
// to have happened must not have taken anything with it. A removal the store
// refuses is logged and does not fail the request: the row is what made it a
// receipt, and what is left is a stray object.
func (s *server) DeleteExpensesEntriesById(ctx context.Context, req gen.DeleteExpensesEntriesByIdRequestObject) (gen.DeleteExpensesEntriesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	current, a, found, err := s.visibleEntry(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.DeleteExpensesEntriesById404Response{}, nil
	}
	if !a.IsWriter {
		return gen.DeleteExpensesEntriesById403JSONResponse(forbidden()), nil
	}
	if field, msg := entryStateRefusal(c, current); msg != "" {
		return gen.DeleteExpensesEntriesById400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError(field, msg))), nil
	}

	var (
		keys       []string
		deleted    int64
		gone       bool
		staleField string
		staleMsg   string
	)
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, err := txq.LockEntry(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			// A concurrent delete won; it is gone, which is the unknown id.
			gone = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("expenses: lock an expense: %w", err)
		}
		// Judged again on the row as it is under the lock: a submit that
		// committed since is the state refusal above, arrived a moment later.
		if staleField, staleMsg = entryStateRefusal(c, locked); staleMsg != "" {
			return nil
		}
		if keys, err = txq.ListAttachmentKeysForEntry(ctx, req.Id); err != nil {
			return fmt.Errorf("expenses: read an expense's receipt keys: %w", err)
		}
		if deleted, err = txq.DeleteEntry(ctx, store.DeleteEntryParams{
			ID: req.Id, AnyOwner: c.Manage, UserID: c.UserID,
		}); err != nil {
			return fmt.Errorf("expenses: delete an expense: %w", err)
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case gone:
		return gen.DeleteExpensesEntriesById404Response{}, nil
	case staleMsg != "":
		return gen.DeleteExpensesEntriesById400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError(staleField, staleMsg))), nil
	case deleted == 0:
		// Unreachable: the row was locked, the caller is its writer (checked
		// above) and its state passed under the lock, which is every guard
		// DeleteEntry itself applies. Answered rather than asserted, because a
		// future guard added to the query should not become a silent 204.
		return gen.DeleteExpensesEntriesById403JSONResponse(forbidden()), nil
	}
	for _, key := range keys {
		s.removeReceiptObject(ctx, req.Id, key)
	}
	return gen.DeleteExpensesEntriesById204Response{}, nil
}

// validateListParams is the list's query rules: the paging, and a status and a
// kind from their enumerations — a typo is a mistake worth reporting, not a
// filter that matches nothing.
func validateListParams(p gen.GetExpensesEntriesParams) []string {
	errs := validatePageParams(p.Page, p.PageSize)
	if value := filterValue(p.Status); value != nil && !slices.Contains(entryStatuses, *value) {
		errs = append(errs, fmt.Sprintf("'status' must be one of %s, but was '%s'.",
			strings.Join(entryStatuses, ", "), *value))
	}
	if value := filterValue(p.Kind); value != nil && !slices.Contains(entryKinds, *value) {
		errs = append(errs, fmt.Sprintf("'kind' must be one of %s, but was '%s'.",
			strings.Join(entryKinds, ", "), *value))
	}
	return errs
}

// filterValue is an optional query filter, nil when it was left out or sent
// empty — a frontend that clears its dropdown sends `status=`, which is no
// filter rather than a filter for the empty string.
func filterValue(raw *string) *string {
	if raw == nil || *raw == "" {
		return nil
	}
	return raw
}

// GetExpensesEntries List expenses
// (GET /api/v1/expenses/entries)
//
// Visibility is a predicate of the query itself — the same rule entryAccess
// applies to one expense: everything for expenses:view-all, expenses:approve
// and expenses:manage, the caller's own, and everything on the projects the
// caller manages. So the total is the number of expenses the caller may see,
// every page is full but the last, and the list never holds an expense its own
// read answers 404 for.
//
// The projects the caller manages are resolved through the project directory
// before the query rather than filtered after it, because a filter after the
// fetch would page through rows the caller never sees. A userId filter narrows
// that set and never widens it: naming somebody the caller may see nothing of
// is an empty page, not a wider one.
func (s *server) GetExpensesEntries(ctx context.Context, req gen.GetExpensesEntriesRequestObject) (gen.GetExpensesEntriesResponseObject, error) {
	p := req.Params
	if msgs := validateListParams(p); len(msgs) > 0 {
		return gen.GetExpensesEntries400ApplicationProblemPlusJSONResponse(invalidQuery(msgs)), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	managed := []int32{}
	if !c.seesEveryone() {
		if managed, err = c.managedProjects(ctx, s); err != nil {
			return nil, err
		}
	}

	filter := store.CountEntriesParams{
		SeeAll:            c.seesEveryone(),
		CallerID:          c.UserID,
		ManagedProjectIds: managed,
		UserID:            p.UserId,
		ProjectID:         p.ProjectId,
		Status:            filterValue(p.Status),
		Kind:              filterValue(p.Kind),
		FromDate:          optionalDate(p.From),
		ToDate:            optionalDate(p.To),
		Reimbursed:        p.Reimbursed,
	}
	total, err := q.CountEntries(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("expenses: count expenses: %w", err)
	}
	rows, err := q.ListEntries(ctx, store.ListEntriesParams{
		SeeAll:            filter.SeeAll,
		CallerID:          filter.CallerID,
		ManagedProjectIds: filter.ManagedProjectIds,
		UserID:            filter.UserID,
		ProjectID:         filter.ProjectID,
		Status:            filter.Status,
		Kind:              filter.Kind,
		FromDate:          filter.FromDate,
		ToDate:            filter.ToDate,
		Reimbursed:        filter.Reimbursed,
		PageSize:          pageSize,
		PageOffset:        (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: list expenses: %w", err)
	}

	data, err := s.entryResponses(ctx, c, rows)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesEntries200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}

// optionalDate is a date filter as the column wants it, invalid (no filter)
// when it was left out.
func optionalDate(d *openapi_types.Date) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgDate(utcDay(d.Time))
}

// listDefaultPageSize and listMaxPageSize are the list paging bounds every
// other module's list uses. listMaxPage is this module's own: the offset is
// computed and sent as an int32, so page x pageSize has to fit one. Without
// the bound ?page=21474838&pageSize=100 overflows into a negative offset,
// which the database refuses — a failure for a request that broke no
// documented rule — or, where the query pages a subselect, quietly answers an
// empty page for a number that means nothing.
const (
	listDefaultPageSize = 25
	listMaxPageSize     = 100
	listMaxPage         = math.MaxInt32 / listMaxPageSize
)

// validatePageParams is the paging rule, in customers' and projects' own
// words: every failure is collected rather than the first one reported.
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

// pageParams is the validated paging as numbers: page 1 and
// listDefaultPageSize rows unless the caller asked otherwise.
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
