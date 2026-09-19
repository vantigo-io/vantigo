package expenses

import (
	"context"
	"errors"
	"fmt"
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
func (s *server) checkProject(ctx context.Context, ownerID uuid.UUID, p parsedEntry,
	add func(field, msg string),
) (*contracts.ProjectEntry, error) {
	if p.ProjectID == nil || !s.projectsAvailable() {
		return nil, nil
	}
	allowed, err := s.projectsCanLogTime(ctx, *p.ProjectID, ownerID)
	if err != nil {
		return nil, fmt.Errorf("expenses: check the owner may book on the project: %w", err)
	}
	var project *contracts.ProjectEntry
	if allowed {
		if project, err = s.projectsProject(ctx, *p.ProjectID); err != nil {
			return nil, fmt.Errorf("expenses: look up the project: %w", err)
		}
	}
	if project == nil {
		add("projectId", cannotBookOnProject)
		return nil, nil
	}

	if p.LineID != nil {
		line, err := s.projectsBillingLine(ctx, *p.ProjectID, *p.LineID)
		if err != nil {
			return nil, fmt.Errorf("expenses: look up the billing line: %w", err)
		}
		switch {
		case line == nil:
			add("billingLineId", fmt.Sprintf("Billing line %d is not one of this project's", *p.LineID))
		case !line.Active:
			add("billingLineId", fmt.Sprintf("Billing line %d is inactive", *p.LineID))
		}
	}
	return project, nil
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

// resolveValues prices one expense (design §4). A mileage line's amount, rate
// and passenger supplement come from the dated rate table for its own date and
// are written again on every save while it is a draft; an outlay stands as it
// was entered. What the customer is billed is added on top when the line is
// billable and the project bills at all.
func (s *server) resolveValues(ctx context.Context, q *store.Queries, c *caller, p parsedEntry,
	project *contracts.ProjectEntry, add func(field, msg string),
) (entryValues, error) {
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
		return v, nil
	}

	switch p.Kind {
	case kindOutlay:
		v.MarkupPercent = p.MarkupPercent
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
			rate, err := rateFor(ctx, q, rateKindMileageCustomer, p.Date)
			switch {
			case errors.Is(err, errNoRate):
				add("billRatePerKm", "No customer rate per kilometre applies on this date, so the line needs one of its own")
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

// prepared is one save judged and priced, ready for the database.
type prepared struct {
	Owner   uuid.UUID
	Parsed  parsedEntry
	Values  entryValues
	Columns entryColumns
	Errors  map[string][]string
}

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

	project, err := s.checkProject(ctx, owner, parsed, add)
	if err != nil {
		return prepared{}, err
	}
	if err := checkCategory(ctx, q, parsed, current, add); err != nil {
		return prepared{}, err
	}
	if !c.mayWritePast(parsed.Date) {
		add("entryDate", lockedBeforeMessage(*lockedBefore(c.Settings)))
	}
	values, err := s.resolveValues(ctx, q, c, parsed, project, add)
	if err != nil {
		return prepared{}, err
	}
	if len(errs) > 0 {
		return prepared{Owner: owner, Errors: errs}, nil
	}

	columns, err := columnsOf(parsed, values)
	if err != nil {
		return prepared{}, err
	}
	return prepared{Owner: owner, Parsed: parsed, Values: values, Columns: columns}, nil
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
// is locked and read again: a save that committed since answers 403 if it
// settled the expense and 409 if it only moved the revision on — the status
// first, because a caller holding a stale revision of an expense that has been
// submitted can do nothing with a fresher one either.
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
	if !a.CanEdit {
		return gen.PutExpensesEntriesById403JSONResponse(forbidden()), nil
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
	// The period lock is judged on the day the expense had as well as the day
	// it is being given, so a locked line cannot be edited out of the lock.
	if !c.mayWritePast(current.EntryDate.Time) {
		p.Errors = withFieldError(p.Errors, "entryDate", lockedBeforeMessage(*lockedBefore(c.Settings)))
	}
	if len(p.Errors) > 0 {
		return gen.PutExpensesEntriesById400ApplicationProblemPlusJSONResponse(invalidEntry(p.Errors)), nil
	}

	var (
		updated  store.ExpensesEntry
		gone     bool
		settled  bool
		conflict *int32
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
		switch {
		case !slices.Contains(editableStatuses, row.Status):
			settled = true
			return nil
		case row.Revision != body.Revision:
			conflict = &row.Revision
			return nil
		}
		updated, err = txq.UpdateEntry(ctx, store.UpdateEntryParams{
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
		})
		return err
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("expenses: change an expense: %w", err)
	case gone:
		return gen.PutExpensesEntriesById404Response{}, nil
	case settled:
		return gen.PutExpensesEntriesById403JSONResponse(forbidden()), nil
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
func (s *server) DeleteExpensesEntriesById(ctx context.Context, req gen.DeleteExpensesEntriesByIdRequestObject) (gen.DeleteExpensesEntriesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	_, a, found, err := s.visibleEntry(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.DeleteExpensesEntriesById404Response{}, nil
	}
	if !a.CanDelete {
		return gen.DeleteExpensesEntriesById403JSONResponse(forbidden()), nil
	}

	deleted, err := q.DeleteEntry(ctx, store.DeleteEntryParams{ID: req.Id, AnyOwner: c.Manage, UserID: c.UserID})
	if err != nil {
		return nil, fmt.Errorf("expenses: delete an expense: %w", err)
	}
	if deleted == 0 {
		// Something committed between the read and the delete: a concurrent
		// delete (it is gone) or a submit (it is no longer a draft).
		if _, err := q.GetEntry(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
			return gen.DeleteExpensesEntriesById404Response{}, nil
		} else if err != nil {
			return nil, fmt.Errorf("expenses: re-read an expense after a delete that removed nothing: %w", err)
		}
		return gen.DeleteExpensesEntriesById403JSONResponse(forbidden()), nil
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
// other module's list uses.
const (
	listDefaultPageSize = 25
	listMaxPageSize     = 100
)

// validatePageParams is the paging rule, in customers' and projects' own
// words: every failure is collected rather than the first one reported.
func validatePageParams(page, pageSize *int32) []string {
	var errs []string
	if page != nil && *page < 1 {
		errs = append(errs, fmt.Sprintf("'page' must be 1 or greater, but was %d.", *page))
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
