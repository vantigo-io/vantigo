package expenses

import (
	"context"
	"fmt"
	"math/big"

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
}

// namesFor resolves rows' names: one user-directory call for every owner, one
// project-directory call for every project, one billing-lines call per project
// that any row has a line on, one category read and one receipt read for the
// whole set.
func (s *server) namesFor(ctx context.Context, rows []store.ExpensesEntry) (entryNames, error) {
	names := entryNames{
		users:       map[uuid.UUID]contracts.UserEntry{},
		projects:    map[int32]contracts.ProjectEntry{},
		lines:       map[int32]contracts.BillingLineEntry{},
		categories:  map[int32]store.ExpensesCategory{},
		attachments: map[int64][]gen.ExpensesAttachmentResponse{},
	}
	if len(rows) == 0 {
		return names, nil
	}

	var userIDs []uuid.UUID
	var projectIDs []int32
	entryIDs := make([]int64, 0, len(rows))
	seenUsers := map[uuid.UUID]bool{}
	seenProjects := map[int32]bool{}
	lineProjects := map[int32]bool{}
	for _, row := range rows {
		entryIDs = append(entryIDs, row.ID)
		if !seenUsers[row.UserID] {
			seenUsers[row.UserID] = true
			userIDs = append(userIDs, row.UserID)
		}
		if row.ProjectID != nil && !seenProjects[*row.ProjectID] {
			seenProjects[*row.ProjectID] = true
			projectIDs = append(projectIDs, *row.ProjectID)
		}
		if row.ProjectID != nil && row.BillingLineID != nil {
			lineProjects[*row.ProjectID] = true
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

	q := store.New(s.deps.Pool)
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
func entryResponse(row store.ExpensesEntry, a entryAccess, names entryNames) (gen.ExpensesEntryResponse, error) {
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
		Status:          row.Status,
		SubmittedAt:     row.SubmittedAt,
		DecidedAt:       row.DecidedAt,
		RejectionReason: row.RejectionReason,
		AttachmentCount: int32(len(names.attachments[row.ID])),
		Attachments:     attachmentsOf(names, row.ID),
		Owner:           ownerResponse(row.UserID, names),
		Revision:        row.Revision,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		Capabilities: gen.ExpensesEntryCapabilities{
			CanEdit:         a.CanEdit,
			CanDelete:       a.CanDelete,
			CanSubmit:       a.CanSubmit,
			CanApprove:      a.CanApprove,
			CanOverrideRate: a.CanOverrideRate,
			CanMarkInvoiced: a.CanMarkInvoiced,
			CanSeeBilling:   a.CanSeeBilling,
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
	if a.CanSeeBilling {
		billing, err := billingResponse(row)
		if err != nil {
			return gen.ExpensesEntryResponse{}, err
		}
		resp.Billing = billing
	}
	return resp, nil
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
// all for an outlay the company paid.
func owedToEmployee(row store.ExpensesEntry, gross *big.Rat) *big.Rat {
	if row.Kind == kindOutlay && (row.PaidBy == nil || *row.PaidBy != paidByEmployee) {
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
	user, ok := names.users[userID]
	if !ok {
		return gen.ExpensesEntryOwner{UserId: userID, DisplayName: unknownUser, Active: false}
	}
	return gen.ExpensesEntryOwner{UserId: userID, DisplayName: user.DisplayName, Active: user.Active}
}

// entryResponseFor renders the single expense a create, a read or a replace
// answers with, through exactly the code that renders a list of them.
func (s *server) entryResponseFor(ctx context.Context, c *caller, row store.ExpensesEntry) (gen.ExpensesEntryResponse, error) {
	names, err := s.namesFor(ctx, []store.ExpensesEntry{row})
	if err != nil {
		return gen.ExpensesEntryResponse{}, err
	}
	a, err := s.entryAccess(ctx, c, row)
	if err != nil {
		return gen.ExpensesEntryResponse{}, err
	}
	return entryResponse(row, a, names)
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
	out := make([]gen.ExpensesEntryResponse, 0, len(rows))
	for _, row := range rows {
		a, err := s.entryAccess(ctx, c, row)
		if err != nil {
			return nil, err
		}
		resp, err := entryResponse(row, a, names)
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
