package expenses

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file renders stored rows onto the wire. The shaping rule throughout:
// what is not there is absent, never null — a nil pointer on an omitempty
// field, so a client can tell "no lock is set" from "the lock is null".

// settingsResponse renders the installation's settings row, shaped for the
// caller: the default markup is expenses:manage's alone. What the company adds
// to a supplier cost before it invoices it on is a commercial figure, which
// design §5 keeps on the same side as the markup, the customer rate and the
// bill amount on an expense — and no form needs the number, because the server
// applies the default itself when a billable outlay names no markup. Meta
// carries it under exactly the same rule, so the two reads never disagree.
func settingsResponse(row store.ExpensesSetting, canManage bool) (gen.ExpensesSettingsResponse, error) {
	threshold, err := floatPtrFromNumeric(row.ReceiptRequiredOver)
	if err != nil {
		return gen.ExpensesSettingsResponse{}, err
	}
	resp := gen.ExpensesSettingsResponse{
		DefaultCurrency:     row.DefaultCurrency,
		ReceiptRequiredOver: threshold,
		// Read by everyone: a client that labels a trip's days has to label
		// them in the installation's zone, or it will disagree with the server
		// about which day a save lands on.
		TimeZone: row.TimeZone,
	}
	if canManage {
		markup, err := floatFromNumeric(row.DefaultMarkupPercent)
		if err != nil {
			return gen.ExpensesSettingsResponse{}, err
		}
		resp.DefaultMarkupPercent = &markup
	}
	if row.LockedBefore.Valid {
		resp.LockedBefore = &openapi_types.Date{Time: row.LockedBefore.Time}
	}
	return resp, nil
}

// rateResponse renders one dated rate row.
func rateResponse(row store.ExpensesRate) (gen.ExpensesRateResponse, error) {
	value, err := floatFromNumeric(row.Value)
	if err != nil {
		return gen.ExpensesRateResponse{}, err
	}
	return gen.ExpensesRateResponse{
		Id:        row.ID,
		Kind:      row.Kind,
		ValidFrom: openapi_types.Date{Time: row.ValidFrom.Time},
		Value:     value,
		Currency:  row.Currency,
		Source:    row.Source,
	}, nil
}

// rateResponses renders rows in the order they were read.
func rateResponses(rows []store.ExpensesRate) ([]gen.ExpensesRateResponse, error) {
	out := make([]gen.ExpensesRateResponse, 0, len(rows))
	for _, row := range rows {
		resp, err := rateResponse(row)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

// categoryResponse renders one category row.
func categoryResponse(row store.ExpensesCategory) gen.ExpensesCategoryResponse {
	return gen.ExpensesCategoryResponse{
		Id:       row.ID,
		Name:     row.Name,
		Active:   row.Active,
		Position: row.Position,
	}
}

// categoryResponses renders rows in the order they were read.
func categoryResponses(rows []store.ExpensesCategory) []gen.ExpensesCategoryResponse {
	out := make([]gen.ExpensesCategoryResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryResponse(row))
	}
	return out
}

// unknownUser is the name an owner the user directory no longer knows is
// rendered under, so an expense still renders rather than failing the read.
const unknownUser = "Unknown user"

// entryNames is everything an expense is rendered with that this module does
// not own: its owner's name (identity, through contracts.UserDirectory), its
// project's code and name and its billing line's code (projects, through
// contracts.ProjectDirectory), and its category's name (this module's own
// table, but a join the entry query deliberately does not make). None of it
// may be read inside a locked transaction, so it is resolved in bulk for a set
// of rows, after any write has committed.
type entryNames struct {
	users      map[uuid.UUID]contracts.UserEntry
	projects   map[int32]contracts.ProjectEntry
	lines      map[int32]contracts.BillingLineEntry
	categories map[int32]store.ExpensesCategory
	// attachments is each entry's receipts, oldest first. The count an entry
	// is rendered with is the length of its list rather than a second query, so
	// attachmentCount and attachments can never disagree.
	attachments map[int64][]gen.ExpensesAttachmentResponse
	// claims is the travel claim of every line among the rows, read once for
	// the whole set. It is what unitFor answers from, so a page of a claim's
	// lines resolves the claim once rather than once per line.
	claims map[int64]store.ExpensesClaim
}

// unitFor is the unit one of the rendered rows belongs to (authorize.go): the
// expense itself, or the claim resolved for the whole set.
func (n entryNames) unitFor(row store.ExpensesEntry, loc *time.Location) entryUnit {
	if row.ClaimID == nil {
		return unitOf(row, nil, loc)
	}
	claim, ok := n.claims[*row.ClaimID]
	if !ok {
		return unitOf(row, nil, loc)
	}
	return unitOf(row, &claim, loc)
}

// namesFor resolves rows' names: one user-directory call for every owner, one
// project-directory call for every project, one billing-lines call per project
// that any row has a line on, one category read and one receipt read for the
// whole set.
//
// claims are the travel claims to name as well as the rows' own — the claim a
// read or a list is *about*, whose owner, project and deciders have to be named
// even when it holds no lines at all. Passing it here rather than resolving it
// separately is what keeps a claim and its lines to one directory call each.
func (s *server) namesFor(ctx context.Context, rows []store.ExpensesEntry, claims ...store.ExpensesClaim) (entryNames, error) {
	names := entryNames{
		users:       map[uuid.UUID]contracts.UserEntry{},
		projects:    map[int32]contracts.ProjectEntry{},
		lines:       map[int32]contracts.BillingLineEntry{},
		categories:  map[int32]store.ExpensesCategory{},
		attachments: map[int64][]gen.ExpensesAttachmentResponse{},
		claims:      map[int64]store.ExpensesClaim{},
	}
	if len(rows) == 0 && len(claims) == 0 {
		return names, nil
	}

	var userIDs []uuid.UUID
	var projectIDs []int32
	var claimIDs []int64
	entryIDs := make([]int64, 0, len(rows))
	seenUsers := map[uuid.UUID]bool{}
	seenProjects := map[int32]bool{}
	seenClaims := map[int64]bool{}
	lineProjects := map[int32]bool{}
	addUser := func(id uuid.UUID) {
		if !seenUsers[id] {
			seenUsers[id] = true
			userIDs = append(userIDs, id)
		}
	}
	for _, row := range rows {
		entryIDs = append(entryIDs, row.ID)
		addUser(row.UserID)
		// Whoever overrode a rate, decided the expense, paid it back or
		// invoiced it is named on it too, in the same directory call as its
		// owner — never one of their own, and never inside a transaction.
		if row.RateOverriddenByUserID != nil {
			addUser(*row.RateOverriddenByUserID)
		}
		if row.DecidedByUserID != nil {
			addUser(*row.DecidedByUserID)
		}
		if row.ReimbursedByUserID != nil {
			addUser(*row.ReimbursedByUserID)
		}
		if row.InvoicedByUserID != nil {
			addUser(*row.InvoicedByUserID)
		}
		if row.ProjectID != nil && !seenProjects[*row.ProjectID] {
			seenProjects[*row.ProjectID] = true
			projectIDs = append(projectIDs, *row.ProjectID)
		}
		if row.ProjectID != nil && row.BillingLineID != nil {
			lineProjects[*row.ProjectID] = true
		}
		// The claim a line belongs to is what its status, its decision and its
		// reimbursement are read off, so it is resolved with the names rather
		// than one query per line.
		if row.ClaimID != nil && !seenClaims[*row.ClaimID] {
			seenClaims[*row.ClaimID] = true
			claimIDs = append(claimIDs, *row.ClaimID)
		}
	}
	// A claim named here is one the caller already has in hand, so it is not
	// read again; its own people and its project join the same two calls the
	// lines' do.
	for _, claim := range claims {
		seenClaims[claim.ID] = true
		names.claims[claim.ID] = claim
		addUser(claim.UserID)
		for _, id := range []*uuid.UUID{claim.DecidedByUserID, claim.ReimbursedByUserID} {
			if id != nil {
				addUser(*id)
			}
		}
		if claim.ProjectID != nil && !seenProjects[*claim.ProjectID] {
			seenProjects[*claim.ProjectID] = true
			projectIDs = append(projectIDs, *claim.ProjectID)
		}
	}

	// The lines' claims are read **before** the directory call, because a line's
	// decision and reimbursement are its claim's: the people behind them are on
	// the claim row and never on the line's own columns, so a claim read after
	// the names were resolved would leave a line naming "Unknown user" for the
	// approver its claim page shows by name. The caller that hands claims in
	// (claimResponseFor) has already put them in seenClaims, so this reads only
	// what nobody has.
	q := store.New(s.deps.Pool)
	if len(claimIDs) > 0 {
		rows, err := q.GetClaims(ctx, claimIDs)
		if err != nil {
			return entryNames{}, fmt.Errorf("expenses: resolve the expenses' travel claims: %w", err)
		}
		for _, claim := range rows {
			names.claims[claim.ID] = claim
			for _, id := range []*uuid.UUID{claim.DecidedByUserID, claim.ReimbursedByUserID} {
				if id != nil {
					addUser(*id)
				}
			}
		}
	}

	users, err := s.usersUsers(ctx, userIDs)
	if err != nil {
		return entryNames{}, fmt.Errorf("expenses: resolve the expenses' owners: %w", err)
	}
	for _, u := range users {
		names.users[u.ID] = u
	}

	// Decision X2: without the projects module a stored project id is simply
	// not shown, and nothing fails for it.
	if s.projectsAvailable() && len(projectIDs) > 0 {
		found, err := s.projectsProjects(ctx, projectIDs)
		if err != nil {
			return entryNames{}, fmt.Errorf("expenses: resolve the expenses' projects: %w", err)
		}
		for _, p := range found {
			names.projects[p.ID] = p
		}
		for _, projectID := range projectIDs {
			if !lineProjects[projectID] {
				continue
			}
			lines, err := s.projectsBillingLines(ctx, projectID)
			if err != nil {
				return entryNames{}, fmt.Errorf("expenses: resolve the expenses' billing lines: %w", err)
			}
			for _, l := range lines {
				names.lines[l.ID] = l
			}
		}
	}

	categories, err := listCategoryRows(ctx, q)
	if err != nil {
		return entryNames{}, err
	}
	for _, category := range categories {
		names.categories[category.ID] = category
	}
	attachments, err := q.ListAttachmentsForEntries(ctx, entryIDs)
	if err != nil {
		return entryNames{}, fmt.Errorf("expenses: read the expenses' receipts: %w", err)
	}
	for _, attachment := range attachments {
		names.attachments[attachment.EntryID] = append(names.attachments[attachment.EntryID],
			attachmentResponse(attachment))
	}
	return names, nil
}

// entryResponse projects one row for one caller. The billing object is set
// exactly when the caller may see the project's money on it, and then always
// set, even with nothing in it, so a client can tell "may see, nothing billed"
// from "may not see".
//
// The status, the submission stamp, the decision and the reimbursement are the
// **unit's** (authorize.go): a line inside a travel claim shows its claim's,
// because that is what the line actually is — its own columns stay at their
// defaults and would say "draft" about a trip that has been approved and paid.
func entryResponse(row store.ExpensesEntry, unit entryUnit, a entryAccess, names entryNames) (gen.ExpensesEntryResponse, error) {
	gross, err := ratFromNumeric(row.GrossAmount)
	if err != nil {
		return gen.ExpensesEntryResponse{}, err
	}
	vat, err := ratPtrFromNumeric(row.VatAmount)
	if err != nil {
		return gen.ExpensesEntryResponse{}, err
	}
	net := netOf(gross, vat)

	resp := gen.ExpensesEntryResponse{
		Id:              row.ID,
		ClaimId:         row.ClaimID,
		Kind:            row.Kind,
		EntryDate:       openapi_types.Date{Time: row.EntryDate.Time},
		Description:     row.Description,
		Supplier:        row.Supplier,
		PaidBy:          row.PaidBy,
		Currency:        row.Currency,
		GrossAmount:     floatOfRat(gross),
		NetAmount:       floatOfRat(net),
		OwedToEmployee:  floatOfRat(owedToEmployee(row, gross)),
		FromPlace:       row.FromPlace,
		ToPlace:         row.ToPlace,
		Billable:        row.Billable,
		Status:          unit.Status,
		SubmittedAt:     unit.SubmittedAt,
		Decision:        decisionResponse(unit, names),
		AttachmentCount: int32(len(names.attachments[row.ID])),
		Attachments:     attachmentsOf(names, row.ID),
		Owner:           ownerResponse(row.UserID, names),
		Revision:        row.Revision,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		Capabilities: gen.ExpensesEntryCapabilities{
			CanEdit:           a.CanEdit,
			CanDelete:         a.CanDelete,
			CanSubmit:         a.CanSubmit,
			CanApprove:        a.CanApprove,
			CanUnapprove:      a.CanUnapprove,
			CanOverrideRate:   a.CanOverrideRate,
			CanMarkInvoiced:   a.CanMarkInvoiced,
			CanUndoInvoiced:   a.CanUndoInvoiced,
			CanMarkReimbursed: a.CanMarkReimbursed,
			CanUndoReimbursed: a.CanUndoReimbursed,
			CanSeeBilling:     a.CanSeeBilling,
			CanSetBilling:     a.CanSetBilling,
		},
	}
	if vat != nil {
		resp.VatAmount = ptrTo(floatOfRat(vat))
	}
	if row.Kind == kindMileage {
		if resp.DistanceKm, err = floatPtrFromNumeric(row.DistanceKm); err != nil {
			return gen.ExpensesEntryResponse{}, err
		}
		if resp.Rate, err = floatPtrFromNumeric(row.Rate); err != nil {
			return gen.ExpensesEntryResponse{}, err
		}
		if resp.PassengerRate, err = floatPtrFromNumeric(row.PassengerRate); err != nil {
			return gen.ExpensesEntryResponse{}, err
		}
		resp.Passengers = ptrTo(int32(row.Passengers))
	}
	if row.Kind == kindPerDiem {
		if resp.Rate, err = floatPtrFromNumeric(row.Rate); err != nil {
			return gen.ExpensesEntryResponse{}, err
		}
		if resp.PerDiem, err = perDiemResponse(row); err != nil {
			return gen.ExpensesEntryResponse{}, err
		}
	}
	// Decision X8: a rate somebody replaced is shown to everyone who may see
	// the expense, its owner included — what they are paid was decided by a
	// person rather than by the table, and they are entitled to know.
	if row.RateOverriddenByUserID != nil {
		tableValue, err := floatPtrFromNumeric(row.RateTableValue)
		if err != nil {
			return gen.ExpensesEntryResponse{}, err
		}
		// Absent rather than repeated when the supplement was never overridden,
		// so a reader can tell a changed supplement from an untouched one.
		passengerTableValue, err := floatPtrFromNumeric(row.PassengerRateTableValue)
		if err != nil {
			return gen.ExpensesEntryResponse{}, err
		}
		resp.RateOverride = &gen.ExpensesEntryRateOverride{
			ByUser:              userRef(*row.RateOverriddenByUserID, names),
			TableValue:          tableValue,
			PassengerTableValue: passengerTableValue,
		}
	}
	if row.CategoryID != nil {
		if category, ok := names.categories[*row.CategoryID]; ok {
			resp.Category = &gen.ExpensesEntryCategory{Id: category.ID, Name: category.Name}
		}
	}
	// A project or a line the directory no longer lists — or an installation
	// with no projects module at all — leaves the stored id where it is and
	// simply shows nothing for it (decision X2).
	if row.ProjectID != nil {
		if p, ok := names.projects[*row.ProjectID]; ok {
			resp.Project = &gen.ExpensesEntryProject{Id: p.ID, Code: p.Code, Name: p.Name}
		}
	}
	if row.BillingLineID != nil {
		if l, ok := names.lines[*row.BillingLineID]; ok {
			resp.BillingLine = &gen.ExpensesEntryBillingLine{Id: l.ID, Code: l.Code}
		}
	}
	// Decision X5: that somebody has been paid back is shown to everyone who
	// may see the expense, its owner first of all. It is not a privilege to be
	// told that the money has gone out — and the figure it names is the one
	// the owner could already see.
	if reimbursement := reimbursementResponse(unit, a.SeesPayrollReference, names); reimbursement != nil {
		resp.Reimbursement = reimbursement
	}
	if a.CanSeeBilling {
		billing, err := billingResponse(row)
		if err != nil {
			return gen.ExpensesEntryResponse{}, err
		}
		// The invoice sits inside billing rather than beside the
		// reimbursement: what the customer was charged, and on which invoice,
		// is the project's business and not the employee's.
		if row.InvoicedAt != nil {
			billing.Invoice = &gen.ExpensesEntryInvoice{
				At:        *row.InvoicedAt,
				By:        userRef(invoicedBy(row), names),
				Reference: row.InvoiceReference,
			}
		}
		resp.Billing = billing
	}
	return resp, nil
}

// reimbursementResponse is the payroll stamp of one unit — a standalone
// expense's own, or the travel claim's, which every one of its lines is paid
// through. Decision X5: that somebody has been paid back is shown to everyone
// who may see the expense, its owner first of all. It is not a privilege to be
// told that the money has gone out — and the figure it names is the one the
// owner could already see.
//
// The batch it went with is narrower than the fact that it went: the reference
// is the payroll clerk's record of their own run, so it goes to the person it
// paid and to whoever reads everybody's expenses, and not to a project
// manager. seesReference is accessFor's SeesPayrollReference, or the claim
// reader's own copy of the same rule.
func reimbursementResponse(unit entryUnit, seesReference bool, names entryNames) *gen.ExpensesEntryReimbursement {
	if unit.ReimbursedAt == nil {
		return nil
	}
	stamp := &gen.ExpensesEntryReimbursement{
		At:   *unit.ReimbursedAt,
		By:   userRef(reimbursedBy(unit), names),
		Date: openapi_types.Date{Time: unit.ReimbursementDate.Time},
	}
	if seesReference {
		stamp.Reference = unit.ReimbursementReference
	}
	return stamp
}

// reimbursedBy and invoicedBy are who made the mark, falling back to the nil
// uuid — which userRef renders as the unknown user — rather than failing a
// read over a row whose stamp somehow lost its person.
func reimbursedBy(unit entryUnit) uuid.UUID {
	if unit.ReimbursedByUserID == nil {
		return uuid.Nil
	}
	return *unit.ReimbursedByUserID
}

func invoicedBy(row store.ExpensesEntry) uuid.UUID {
	if row.InvoicedByUserID == nil {
		return uuid.Nil
	}
	return *row.InvoicedByUserID
}

// decisionResponse is what was decided about the expense, by whom and when —
// nil until a decision stands, and nil again the moment one is undone (a submit
// and an unapprove both clear the three columns it reads).
//
// It is shown to **everyone who may see the expense**, its owner first of all:
// being told who rejected you, and why, is the point of a rejection, and the
// name it carries is one the owner could read off the approval queue anyway. It
// is the same shape as the two stamps the tracks after approval leave
// (reimbursement, invoice), so a client reads all three the same way. The
// status is the decision's own word rather than a second reading of the row's,
// because the columns are only ever written together.
//
// by is absent rather than empty when the row carries a decision and no
// decider. The three columns are only ever written together, so nothing here
// can produce that; a restore or a support script could, and a person made of
// a nil uuid and an empty name would be worse than none at all.
func decisionResponse(unit entryUnit, names entryNames) *gen.ExpensesEntryDecision {
	if unit.DecidedAt == nil {
		return nil
	}
	decision := &gen.ExpensesEntryDecision{
		Status: unit.Status,
		At:     *unit.DecidedAt,
		Reason: unit.RejectionReason,
	}
	if unit.DecidedByUserID != nil {
		decision.By = ptrTo(userRef(*unit.DecidedByUserID, names))
	}
	return decision
}

// attachmentsOf is an entry's receipts, always a list and never null: an
// expense with none carries an empty array, so a client never has to tell
// "none" from "not answered".
func attachmentsOf(names entryNames, entryID int64) []gen.ExpensesAttachmentResponse {
	if list, ok := names.attachments[entryID]; ok {
		return list
	}
	return []gen.ExpensesAttachmentResponse{}
}

// owedToEmployee is what the owner gets back (design §3.1): the gross of an
// outlay they paid themselves, a mileage line's whole amount, and nothing at
// all for an outlay the company paid. owesEmployee (authorize.go) is the same
// rule as a yes or no, and the reimbursement queries are that rule in SQL.
func owedToEmployee(row store.ExpensesEntry, gross *big.Rat) *big.Rat {
	if !owesEmployee(row) {
		return new(big.Rat)
	}
	return gross
}

// billingResponse is the project's money on one expense.
func billingResponse(row store.ExpensesEntry) (*gen.ExpensesEntryBilling, error) {
	markup, err := floatPtrFromNumeric(row.MarkupPercent)
	if err != nil {
		return nil, err
	}
	billRate, err := floatPtrFromNumeric(row.BillRatePerKm)
	if err != nil {
		return nil, err
	}
	amount, err := floatFromNumeric(row.BillAmount)
	if err != nil {
		return nil, err
	}
	return &gen.ExpensesEntryBilling{MarkupPercent: markup, BillRatePerKm: billRate, BillAmount: amount}, nil
}

// ownerResponse is the person an expense concerns, named through identity.
func ownerResponse(userID uuid.UUID, names entryNames) gen.ExpensesEntryOwner {
	return gen.ExpensesEntryOwner(userRef(userID, names))
}

// userRef names one person the response points at — an approval group's owner,
// or whoever overrode a rate — falling back to a name rather than failing the
// read when the directory no longer knows them.
func userRef(userID uuid.UUID, names entryNames) gen.ExpensesUserRef {
	user, ok := names.users[userID]
	if !ok {
		return gen.ExpensesUserRef{UserId: userID, DisplayName: unknownUser, Active: false}
	}
	return gen.ExpensesUserRef{UserId: userID, DisplayName: user.DisplayName, Active: user.Active}
}

// entryResponseFor renders the single expense a create, a read or a replace
// answers with, through exactly the code that renders a list of them.
func (s *server) entryResponseFor(ctx context.Context, c *caller, row store.ExpensesEntry) (gen.ExpensesEntryResponse, error) {
	names, err := s.namesFor(ctx, []store.ExpensesEntry{row})
	if err != nil {
		return gen.ExpensesEntryResponse{}, err
	}
	unit := names.unitFor(row, c.zone())
	a, err := s.entryAccess(ctx, c, row, unit)
	if err != nil {
		return gen.ExpensesEntryResponse{}, err
	}
	return entryResponse(row, unit, a, names)
}

// entryResponses renders a set of rows for one caller: the names resolved once
// for all of them, the access resolved per row through the caller's cached
// roles, so a page of expenses on one project asks the directory about it
// once.
func (s *server) entryResponses(ctx context.Context, c *caller, rows []store.ExpensesEntry) ([]gen.ExpensesEntryResponse, error) {
	names, err := s.namesFor(ctx, rows)
	if err != nil {
		return nil, err
	}
	return s.entryResponsesWith(ctx, c, rows, names)
}

// entryResponsesWith is entryResponses over names already resolved — what a
// travel claim renders its lines through, so the claim and a list of expenses
// share one renderer and cannot drift apart.
func (s *server) entryResponsesWith(ctx context.Context, c *caller, rows []store.ExpensesEntry,
	names entryNames,
) ([]gen.ExpensesEntryResponse, error) {
	out := make([]gen.ExpensesEntryResponse, 0, len(rows))
	for _, row := range rows {
		unit := names.unitFor(row, c.zone())
		a, err := s.entryAccess(ctx, c, row, unit)
		if err != nil {
			return nil, err
		}
		resp, err := entryResponse(row, unit, a, names)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

// floatOfRat is an exact decimal on the wire as the JSON number it renders to,
// at the scale the columns hold. Every amount has already been rounded once,
// in money.go, so this is a rendering step and never an arithmetic one.
func floatOfRat(v *big.Rat) float64 {
	f, _ := new(big.Rat).SetString(decimalText(v, moneyPlaces))
	if f == nil {
		return 0
	}
	value, _ := f.Float64()
	return value
}
