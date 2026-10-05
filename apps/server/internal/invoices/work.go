package invoices

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the uninvoiced view and the wizard (invoices work design D3,
// D4, D6, D10-D12, D14, D15): GET /invoices/work lists a customer's or a
// project's work not yet invoiced, as the source modules' billable reads
// answer it, with what live documents hold of it; POST /invoices/from-work
// makes a draft of the chosen work — or adds it to one — grouped into lines
// in the buyer's language and held under the draft's lock. Every read of a
// directory or a billable read is made before the wizard's transaction, never
// in it.

// The wizard's and the view's refusals, beside the issue's (writeback.go).
const (
	codeWorkUnavailable      = "work_unavailable"
	codeTooManySources       = "too_many_sources"
	codeSourceNotForCustomer = "source_not_for_customer"
	codeMixedCurrency        = "mixed_currency"
	codeCurrencyNotNOK       = "currency_not_nok"
	codeTooManyLines         = "too_many_lines"

	cannotInvoiceWorkTitle = "The work cannot be invoiced"
	invalidWorkTitle       = "Invalid work selection"
)

// The view's warnings (D11, D12, D15): never refusals.
const (
	warningWorkOverdue             = "work_overdue_to_invoice"
	warningCurrencyNotNOK          = "currency_not_nok"
	warningSupplierInvoiceRebilled = "supplier_invoice_rebilled"
	warningWorkTruncated           = "work_truncated"
)

// Why a row of the view is not selectable (D3, D11, D14).
const (
	reasonHeld        = "held"
	reasonNoCustomer  = "no_customer"
	reasonNonBillable = "non_billable"
	reasonFixedPrice  = "fixed_price"
	reasonCurrency    = "currency"
)

// vatCodeNotRegistered is the seeded category-O code (id 9, code 7) the
// wizard pre-fills for every kind while the seller is not VAT-registered (D6):
// the issue refuses any other category then.
const vatCodeNotRegistered = 9

// fromWorkBeforeInsert is called inside the wizard's transaction after
// LiveSourcesElsewhere found the work free and before the holds are inserted,
// so a race test can park one wizard there while another commits (plan
// reading 36). nil in production.
var fromWorkBeforeInsert func(ctx context.Context, invoiceID int64)

// workAvailable is whether any billable read is composed (D3).
func (s *server) workAvailable() bool {
	return s.deps.BillableHours != nil || s.deps.BillableExpenses != nil || s.deps.BillableMilestones != nil
}

func workUnavailable() gen.InvoicesConflictProblem {
	return conflict(codeWorkUnavailable, cannotInvoiceWorkTitle,
		"No module that records billable work — time, expenses or projects — is switched on.")
}

func projectsUnavailable() gen.InvoicesConflictProblem {
	return conflict(codeProjectsUnavailable, cannotInvoiceWorkTitle,
		"Work is invoiced per project, and the projects module is switched off.")
}

// workPage is what the billable reads answered: the rows of each kind and
// whether any read had more than one page.
type workPage struct {
	workSelection
	more bool
}

// readWork reads the work of projects, dated on or before until (zero: no
// bound), through every composed billable read, on the pool, one after the
// other.
func (s *server) readWork(ctx context.Context, projects []int32, until time.Time) (workPage, error) {
	var page workPage
	if len(projects) == 0 {
		return page, nil
	}
	req := contracts.BillableRequest{ProjectIDs: projects, Until: until}
	if s.deps.BillableHours != nil {
		p, err := s.billableHours(ctx, req)
		if err != nil {
			return workPage{}, fmt.Errorf("invoices: read the billable hours: %w", err)
		}
		page.hours, page.more = p.Hours, page.more || p.More
	}
	if s.deps.BillableExpenses != nil {
		p, err := s.billableExpenses(ctx, req)
		if err != nil {
			return workPage{}, fmt.Errorf("invoices: read the billable expenses: %w", err)
		}
		page.expenses, page.more = p.Expenses, page.more || p.More
	}
	if s.deps.BillableMilestones != nil {
		p, err := s.billableMilestones(ctx, req)
		if err != nil {
			return workPage{}, fmt.Errorf("invoices: read the billable milestones: %w", err)
		}
		page.milestones, page.more = p.Milestones, page.more || p.More
	}
	return page, nil
}

// decimalFloat is a contract's decimal text onto the wire.
func decimalFloat(text string, places int) (float64, error) {
	r, err := billableAmount(text)
	if err != nil {
		return 0, err
	}
	return floatFromRat(r, places), nil
}

func optionalDecimal(text *string, places int) (*float64, error) {
	if text == nil {
		return nil, nil
	}
	f, err := decimalFloat(*text, places)
	return &f, err
}

func nonEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// workRow is what judges one row of the view: whether it is selectable, and
// why not.
type workRow struct {
	held     *gen.InvoicesWorkHeldBy
	project  contracts.ProjectEntry
	billed   bool // the project bills a customer
	kind     contracts.WorkSourceKind
	currency string
}

// reason is why a row is not selectable, "" when it is: held by a live
// document, its project billing no customer, its project non-billable, a
// fixed-price project's hours (shown as information, D14), another currency
// than the module's (D11).
func (r workRow) reason(currency string) string {
	switch {
	case r.held != nil:
		return reasonHeld
	case !r.billed:
		return reasonNoCustomer
	case r.project.BillingType == billingNonBillable:
		return reasonNonBillable
	case r.project.BillingType == billingFixedPrice && r.kind == contracts.WorkSourceHours:
		return reasonFixedPrice
	case r.currency != currency:
		return reasonCurrency
	}
	return ""
}

// GetInvoicesWork List the uninvoiced work
// (GET /api/v1/invoices/work)
//
// D3's view: exactly one of customerId and projectId; work_unavailable before
// any directory read when no billable read is composed; the projects, then
// the composed billable reads over them one after the other, on the pool;
// then, from this module's own rows, what live documents hold of that work —
// listed, never selectable, out of the totals.
func (s *server) GetInvoicesWork(ctx context.Context, req gen.GetInvoicesWorkRequestObject) (gen.GetInvoicesWorkResponseObject, error) {
	p := req.Params
	if (p.CustomerId == nil) == (p.ProjectId == nil) {
		return gen.GetInvoicesWork400ApplicationProblemPlusJSONResponse(invalid(invalidWorkTitle,
			fieldError("customerId", "Name a customer or a project, one of the two"))), nil
	}
	if !s.workAvailable() {
		return gen.GetInvoicesWork409ApplicationProblemPlusJSONResponse(workUnavailable()), nil
	}
	if s.deps.Projects == nil {
		return gen.GetInvoicesWork409ApplicationProblemPlusJSONResponse(projectsUnavailable()), nil
	}
	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	var customerID *int32
	var projects []contracts.ProjectEntry
	if p.CustomerId != nil {
		customerID = p.CustomerId
		if projects, err = s.projectsForCustomer(ctx, *p.CustomerId); err != nil {
			return nil, fmt.Errorf("invoices: read customer %d's projects: %w", *p.CustomerId, err)
		}
	} else {
		project, err := s.projectEntry(ctx, *p.ProjectId)
		if err != nil {
			return nil, fmt.Errorf("invoices: read project %d: %w", *p.ProjectId, err)
		}
		if project == nil {
			return gen.GetInvoicesWork404Response{}, nil
		}
		projects, customerID = []contracts.ProjectEntry{*project}, project.CustomerID
	}
	slices.SortFunc(projects, func(a, b contracts.ProjectEntry) int {
		return cmp.Or(cmp.Compare(a.Code, b.Code), cmp.Compare(a.ID, b.ID))
	})
	ids := make([]int32, 0, len(projects))
	for _, pr := range projects {
		ids = append(ids, pr.ID)
	}
	var until time.Time
	if p.Until != nil {
		until = utcDay(p.Until.Time)
	}
	page, err := s.readWork(ctx, ids, until)
	if err != nil {
		return nil, err
	}
	var userIDs []uuid.UUID
	for _, h := range page.hours {
		if !slices.Contains(userIDs, h.UserID) {
			userIDs = append(userIDs, h.UserID)
		}
	}
	users, err := s.userEntries(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("invoices: name the people of the hours: %w", err)
	}
	view, err := s.workView(ctx, q, settings.DefaultCurrency, customerID, projects, page)
	if err != nil {
		return nil, err
	}
	// The directory answers at most MaxActualsRequests projects; exactly that
	// many may mean more, whose work is not here.
	if len(projects) == contracts.MaxActualsRequests && !slices.Contains(view.Warnings, warningWorkTruncated) {
		view.Warnings = append(view.Warnings, warningWorkTruncated)
	}
	view.Users = make([]gen.InvoicesWorkUser, 0, len(users))
	for _, u := range users {
		view.Users = append(view.Users, gen.InvoicesWorkUser{Id: u.ID, DisplayName: u.DisplayName})
	}
	slices.SortFunc(view.Users, func(a, b gen.InvoicesWorkUser) int {
		return cmp.Or(cmp.Compare(a.DisplayName, b.DisplayName), cmp.Compare(a.Id.String(), b.Id.String()))
	})
	return gen.GetInvoicesWork200JSONResponse(view), nil
}

// workView renders the view from what was read, judging each row against
// what this module's own rows hold of it.
func (s *server) workView(ctx context.Context, q *store.Queries, currency string, customerID *int32,
	projects []contracts.ProjectEntry, page workPage,
) (gen.InvoicesWorkResponse, error) {
	view := gen.InvoicesWorkResponse{
		CustomerId: customerID, Projects: []gen.InvoicesWorkProject{}, Totals: []gen.InvoicesWorkTotal{}, Warnings: []string{},
	}
	if page.more {
		view.Warnings = append(view.Warnings, warningWorkTruncated)
	}
	// What live documents hold of this work: the rows the view lists held.
	var refs store.LiveSourcesForParams
	addRef := func(kind contracts.WorkSourceKind, id int64) {
		refs.Kinds, refs.Ids = append(refs.Kinds, string(kind)), append(refs.Ids, id)
	}
	for _, h := range page.hours {
		addRef(contracts.WorkSourceHours, h.ID)
	}
	for _, e := range page.expenses {
		addRef(contracts.WorkSourceExpense, e.ID)
	}
	for _, m := range page.milestones {
		addRef(contracts.WorkSourceMilestone, m.ID)
	}
	held := map[sourceRef]gen.InvoicesWorkHeldBy{}
	if len(refs.Ids) > 0 {
		live, err := q.LiveSourcesFor(ctx, refs)
		if err != nil {
			return view, fmt.Errorf("invoices: read what holds the work: %w", err)
		}
		for _, l := range live {
			ref := sourceRef{kind: contracts.WorkSourceKind(l.SourceKind), id: l.SourceID}
			if _, ok := held[ref]; !ok {
				held[ref] = gen.InvoicesWorkHeldBy{InvoiceId: l.InvoiceID, Number: l.Number, Status: l.Status}
			}
		}
	}
	ids := make([]int32, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	onDrafts := map[int32][]gen.InvoicesWorkHeldOnDraft{}
	if len(ids) > 0 {
		rows, err := q.HeldOnDrafts(ctx, ids)
		if err != nil {
			return view, fmt.Errorf("invoices: read what drafts hold: %w", err)
		}
		for _, r := range rows {
			onDrafts[r.ProjectID] = append(onDrafts[r.ProjectID], gen.InvoicesWorkHeldOnDraft{InvoiceId: r.InvoiceID, Kind: r.SourceKind, Count: r.Held})
		}
	}
	// D15: a supplier invoice whose supplier and number another row of this
	// answer repeats warns on both, as one Expenses already invoiced does.
	duplicates := map[string]int{}
	supplierKey := func(e contracts.BillableExpense) string {
		if e.Kind != expenseSupplierInvoice || strings.TrimSpace(e.SupplierInvoiceNumber) == "" {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(e.Supplier)) + "\x00" + strings.TrimSpace(e.SupplierInvoiceNumber)
	}
	for _, e := range page.expenses {
		if k := supplierKey(e); k != "" {
			duplicates[k]++
		}
	}

	today := businessDay(s.deps.Clock())
	deadline := monthBefore(today)
	totals := map[string]*big.Rat{}
	addTotal := func(cur string, amount *big.Rat) {
		if totals[cur] == nil {
			totals[cur] = new(big.Rat)
		}
		totals[cur].Add(totals[cur], amount)
	}
	byID := map[int32]*gen.InvoicesWorkProject{}
	oldest := map[int32]time.Time{}
	noteSelectable := func(project int32, date time.Time, cur string, amount *big.Rat) {
		addTotal(cur, amount)
		if o, ok := oldest[project]; !ok || date.Before(o) {
			oldest[project] = date
		}
	}
	entries := map[int32]contracts.ProjectEntry{}
	for _, p := range projects {
		entries[p.ID] = p
		drafts := onDrafts[p.ID]
		if drafts == nil {
			drafts = []gen.InvoicesWorkHeldOnDraft{}
		}
		view.Projects = append(view.Projects, gen.InvoicesWorkProject{
			Id: p.ID, Code: p.Code, Name: p.Name, BillingType: p.BillingType, Currency: p.Currency,
			Hours: []gen.InvoicesWorkHour{}, Expenses: []gen.InvoicesWorkExpense{}, Milestones: []gen.InvoicesWorkMilestone{},
			HeldOnDrafts: drafts, Warnings: []string{},
		})
	}
	for i := range view.Projects {
		byID[view.Projects[i].Id] = &view.Projects[i]
	}
	row := func(kind contracts.WorkSourceKind, id int64, project int32, cur string) (workRow, *gen.InvoicesWorkProject) {
		r := workRow{project: entries[project], kind: kind, currency: cur}
		r.billed = r.project.CustomerID != nil && customerID != nil && *r.project.CustomerID == *customerID
		if h, ok := held[sourceRef{kind: kind, id: id}]; ok {
			r.held = &h
		}
		return r, byID[project]
	}
	rowWarnings := func(cur string, extra ...string) *[]string {
		var w []string
		if cur != currency {
			w = append(w, warningCurrencyNotNOK)
		}
		w = append(w, extra...)
		if len(w) == 0 {
			return nil
		}
		return &w
	}

	for _, h := range page.hours {
		r, p := row(contracts.WorkSourceHours, h.ID, h.ProjectID, h.Currency)
		if p == nil {
			continue
		}
		amount, err := billableAmount(h.Amount)
		if err != nil {
			return view, err
		}
		rate, err := effectiveRate(h)
		if err != nil {
			return view, err
		}
		billRate, err := decimalFloat(h.BillRate, 4)
		if err != nil {
			return view, err
		}
		multiplier, err := optionalDecimal(h.BillMultiplierPercent, 2)
		if err != nil {
			return view, err
		}
		reason := r.reason(currency)
		out := gen.InvoicesWorkHour{
			Id: h.ID, Revision: h.Revision, Date: wireDate(utcDay(h.Date)), UserId: h.UserID,
			Hours: floatFromRat(big.NewRat(h.HoursHundredths, 100), 2), BillRate: billRate, BillMultiplierPercent: multiplier,
			Rate: floatFromRat(rate, 4), Amount: floatFromRat(amount, 8), Currency: h.Currency,
			WorkTypeId: h.WorkTypeID, WorkTypeName: nonEmpty(h.WorkTypeName), TaskTitle: nonEmpty(h.TaskTitle),
			BillingLineId: h.BillingLineID, Selectable: reason == "", Reason: nonEmpty(reason), HeldBy: r.held,
			Warnings: rowWarnings(h.Currency),
		}
		if out.Selectable {
			noteSelectable(h.ProjectID, utcDay(h.Date), h.Currency, amount)
		}
		p.Hours = append(p.Hours, out)
	}
	for _, e := range page.expenses {
		r, p := row(contracts.WorkSourceExpense, e.ID, e.ProjectID, e.Currency)
		if p == nil {
			continue
		}
		amount, err := billableAmount(e.BillAmount)
		if err != nil {
			return view, err
		}
		net, err := decimalFloat(e.NetAmount, 2)
		if err != nil {
			return view, err
		}
		markup, err := optionalDecimal(e.MarkupPercent, 2)
		if err != nil {
			return view, err
		}
		distance, err := optionalDecimal(e.DistanceKm, 3)
		if err != nil {
			return view, err
		}
		perKm, err := optionalDecimal(e.BillRatePerKm, 4)
		if err != nil {
			return view, err
		}
		var extra []string
		if k := supplierKey(e); e.SupplierInvoiceRebilled || (k != "" && duplicates[k] > 1) {
			extra = append(extra, warningSupplierInvoiceRebilled)
		}
		reason := r.reason(currency)
		out := gen.InvoicesWorkExpense{
			Id: e.ID, Revision: e.Revision, Kind: e.Kind, Date: wireDate(utcDay(e.Date)), Description: e.Description,
			Supplier: nonEmpty(e.Supplier), SupplierInvoiceNumber: nonEmpty(e.SupplierInvoiceNumber),
			NetAmount: net, MarkupPercent: markup, DistanceKm: distance, BillRatePerKm: perKm,
			BillAmount: floatFromRat(amount, 2), Currency: e.Currency, ClaimId: e.ClaimID,
			Selectable: reason == "", Reason: nonEmpty(reason), HeldBy: r.held, Warnings: rowWarnings(e.Currency, extra...),
		}
		if out.Selectable {
			noteSelectable(e.ProjectID, utcDay(e.Date), e.Currency, amount)
		}
		p.Expenses = append(p.Expenses, out)
	}
	for _, m := range page.milestones {
		r, p := row(contracts.WorkSourceMilestone, m.ID, m.ProjectID, m.Currency)
		if p == nil {
			continue
		}
		amount, err := billableAmount(m.Amount)
		if err != nil {
			return view, err
		}
		var planned *openapi_types.Date
		if m.PlannedDate != nil {
			planned = ptr(wireDate(utcDay(*m.PlannedDate)))
		}
		reason := r.reason(currency)
		out := gen.InvoicesWorkMilestone{
			Id: m.ID, Revision: m.Revision, Name: m.Name, Description: m.Description, PlannedDate: planned,
			ReadyAt: m.ReadyAt, Date: wireDate(businessDay(m.ReadyAt)), Amount: floatFromRat(amount, 2), Currency: m.Currency,
			Selectable: reason == "", Reason: nonEmpty(reason), HeldBy: r.held, Warnings: rowWarnings(m.Currency),
		}
		if out.Selectable {
			noteSelectable(m.ProjectID, businessDay(m.ReadyAt), m.Currency, amount)
		}
		p.Milestones = append(p.Milestones, out)
	}
	// D12: the oldest selectable work more than a calendar month old.
	for id, o := range oldest {
		if o.Before(deadline) {
			byID[id].Warnings = append(byID[id].Warnings, warningWorkOverdue)
		}
	}
	for _, cur := range slices.Sorted(maps.Keys(totals)) {
		view.Totals = append(view.Totals, gen.InvoicesWorkTotal{Currency: cur, Amount: floatFromRat(totals[cur], 2)})
	}
	return view, nil
}

// fromWorkRef is one source the wizard's body names.
type fromWorkRef struct {
	sourceRef
	revision int32
}

// parseFromWork is step 1's 400s (D3): a customer, at least one source, each
// of a known kind and named once, a known grouping, an append's id and
// revision together, and a delivery period of both days in order.
func parseFromWork(body gen.InvoicesFromWorkRequest) ([]fromWorkRef, string, map[string][]string) {
	var errs map[string][]string
	if body.CustomerId <= 0 {
		errs = withFieldError(errs, "customerId", "Work is invoiced to a customer")
	}
	if len(body.Sources) == 0 {
		errs = withFieldError(errs, "sources", "Choose the work to invoice")
	}
	if msg := maxLength("The note", optionalText(body.Note), 1000); msg != "" {
		errs = withFieldError(errs, "note", msg)
	}
	refs := make([]fromWorkRef, 0, len(body.Sources))
	seen := make(map[sourceRef]bool, len(body.Sources))
	for i, src := range body.Sources {
		field := fmt.Sprintf("sources[%d]", i)
		ref := sourceRef{kind: contracts.WorkSourceKind(src.Kind), id: src.Id}
		switch {
		case !slices.Contains(contracts.InvoicedWorkOrder, ref.kind) || src.Id <= 0:
			errs = withFieldError(errs, field, msgSourceKind)
		case seen[ref]:
			errs = withFieldError(errs, field, msgSourceTwice)
		default:
			seen[ref] = true
			refs = append(refs, fromWorkRef{sourceRef: ref, revision: src.Revision})
		}
	}
	grouping := groupingProject
	if body.Grouping != nil {
		grouping = *body.Grouping
		if !slices.Contains(groupings, grouping) {
			errs = withFieldError(errs, "grouping", "A grouping is project, work_type, person, date or itemised")
		}
	}
	if (body.InvoiceId == nil) != (body.Revision == nil) {
		errs = withFieldError(errs, "revision", "Adding to a draft names the draft and the revision it was read at")
	}
	switch from, to := body.DeliveryFrom, body.DeliveryTo; {
	case (from == nil) != (to == nil):
		errs = withFieldError(errs, "deliveryTo", "A delivery period has both a first and a last day")
	case from != nil && from.After(to.Time):
		errs = withFieldError(errs, "deliveryTo", "A delivery period ends on or after the day it starts")
	}
	return refs, grouping, errs
}

// foundWork is what the billable reads answer for the wizard's sources, by
// identity, and which kinds were read at all.
type foundWork struct {
	hours      map[int64]contracts.BillableHour
	expenses   map[int64]contracts.BillableExpense
	milestones map[int64]contracts.BillableMilestone
	read       map[contracts.WorkSourceKind]bool
}

// readSources is step 3: the sources by id through each composed billable
// read, on the pool, one after the other. A kind whose module is off is not
// read; its sources are not found.
func (s *server) readSources(ctx context.Context, refs []fromWorkRef) (foundWork, error) {
	f := foundWork{
		hours: map[int64]contracts.BillableHour{}, expenses: map[int64]contracts.BillableExpense{},
		milestones: map[int64]contracts.BillableMilestone{}, read: map[contracts.WorkSourceKind]bool{},
	}
	ids := map[contracts.WorkSourceKind][]int64{}
	for _, r := range refs {
		ids[r.kind] = append(ids[r.kind], r.id)
	}
	if want := ids[contracts.WorkSourceMilestone]; len(want) > 0 && s.deps.BillableMilestones != nil {
		page, err := s.billableMilestones(ctx, contracts.BillableRequest{IDs: want})
		if err != nil {
			return f, fmt.Errorf("invoices: read the billable milestones: %w", err)
		}
		for _, m := range page.Milestones {
			f.milestones[m.ID] = m
		}
		f.read[contracts.WorkSourceMilestone] = true
	}
	if want := ids[contracts.WorkSourceExpense]; len(want) > 0 && s.deps.BillableExpenses != nil {
		page, err := s.billableExpenses(ctx, contracts.BillableRequest{IDs: want})
		if err != nil {
			return f, fmt.Errorf("invoices: read the billable expenses: %w", err)
		}
		for _, e := range page.Expenses {
			f.expenses[e.ID] = e
		}
		f.read[contracts.WorkSourceExpense] = true
	}
	if want := ids[contracts.WorkSourceHours]; len(want) > 0 && s.deps.BillableHours != nil {
		page, err := s.billableHours(ctx, contracts.BillableRequest{IDs: want})
		if err != nil {
			return f, fmt.Errorf("invoices: read the billable hours: %w", err)
		}
		for _, h := range page.Hours {
			f.hours[h.ID] = h
		}
		f.read[contracts.WorkSourceHours] = true
	}
	return f, nil
}

// sourceFacts is what step 5 judges of one found source: its project,
// currency and revision.
type sourceFacts struct {
	project  int32
	currency string
	revision int32
}

func (f foundWork) facts(r sourceRef) (sourceFacts, bool) {
	switch r.kind {
	case contracts.WorkSourceHours:
		h, ok := f.hours[r.id]
		return sourceFacts{h.ProjectID, h.Currency, h.Revision}, ok
	case contracts.WorkSourceExpense:
		e, ok := f.expenses[r.id]
		return sourceFacts{e.ProjectID, e.Currency, e.Revision}, ok
	case contracts.WorkSourceMilestone:
		m, ok := f.milestones[r.id]
		return sourceFacts{m.ProjectID, m.Currency, m.Revision}, ok
	}
	return sourceFacts{}, false
}

// selection is the found sources in the body's order, as groupLines takes them.
func (f foundWork) selection(refs []fromWorkRef) workSelection {
	var sel workSelection
	for _, r := range refs {
		switch r.kind {
		case contracts.WorkSourceHours:
			sel.hours = append(sel.hours, f.hours[r.id])
		case contracts.WorkSourceExpense:
			sel.expenses = append(sel.expenses, f.expenses[r.id])
		case contracts.WorkSourceMilestone:
			sel.milestones = append(sel.milestones, f.milestones[r.id])
		}
	}
	return sel
}

// sourceConflict is one of step 5's refusals about a source.
func sourceConflict(code string, r sourceRef, detail string) *gen.InvoicesConflictProblem {
	p := conflict(code, cannotInvoiceWorkTitle, detail)
	p.SourceKind, p.SourceId = ptr(string(r.kind)), ptr(r.id)
	return &p
}

// judgeSources is step 5, in its order, each judgment over every source
// before the next (D3, D11, D14): a project billing another customer, or
// gone; fixed-price hours or non-billable work; work no longer billable, or
// of a module that is off; another revision than the body's, for an hour
// entry or a milestone — an expense's revision is display-only, its billing
// facts taken as read now (D1); then more than one currency, and one that is
// not the module's.
func judgeSources(refs []fromWorkRef, found foundWork, projects map[int32]contracts.ProjectEntry, customerID int32, currency string) *gen.InvoicesConflictProblem {
	for _, r := range refs {
		f, ok := found.facts(r.sourceRef)
		if !ok {
			continue
		}
		p, known := projects[f.project]
		if !known || p.CustomerID == nil || *p.CustomerID != customerID {
			return sourceConflict(codeSourceNotForCustomer, r.sourceRef, fmt.Sprintf(
				"%s %d belongs to project %d, which does not bill this customer.", r.kind, r.id, f.project))
		}
	}
	for _, r := range refs {
		f, ok := found.facts(r.sourceRef)
		if !ok {
			continue
		}
		switch billing := projects[f.project].BillingType; {
		case billing == billingNonBillable, billing == billingFixedPrice && r.kind == contracts.WorkSourceHours:
			return sourceConflict(codeSourceNotSelectable, r.sourceRef, fmt.Sprintf(
				"%s %d belongs to project %d, which is %s.", r.kind, r.id, f.project, billing))
		}
	}
	for _, r := range refs {
		if _, ok := found.facts(r.sourceRef); !ok {
			return sourceConflict(codeSourceNotInvoiceable, r.sourceRef, fmt.Sprintf(
				"%s %d can no longer be invoiced: it is not approved, ready or billable, it is invoiced already, or its module is off.", r.kind, r.id))
		}
	}
	for _, r := range refs {
		if f, _ := found.facts(r.sourceRef); r.kind != contracts.WorkSourceExpense && f.revision != r.revision {
			return sourceConflict(codeSourceChanged, r.sourceRef, fmt.Sprintf(
				"%s %d changed since it was shown: it is at revision %d, not %d.", r.kind, r.id, f.revision, r.revision))
		}
	}
	var currencies []string
	for _, r := range refs {
		if f, _ := found.facts(r.sourceRef); !slices.Contains(currencies, f.currency) {
			currencies = append(currencies, f.currency)
		}
	}
	switch {
	case len(currencies) > 1:
		slices.Sort(currencies)
		return ptr(conflict(codeMixedCurrency, cannotInvoiceWorkTitle, fmt.Sprintf(
			"The work is in %s; one invoice is in one currency.", strings.Join(currencies, " and "))))
	case len(currencies) == 1 && currencies[0] != currency:
		return ptr(conflict(codeCurrencyNotNOK, cannotInvoiceWorkTitle, fmt.Sprintf(
			"The work is in %s, and this module invoices in %s only.", currencies[0], currency)))
	}
	return nil
}

// kindWords names a kind of work in a 400.
var kindWords = map[string]string{"hours": "hours", "expenses": "expenses", "milestones": "milestones"}

// resolveVatCodes is D6: per kind of work in the selection, the request's code
// or else the default — the settings', or id 9 for every kind while the
// seller is not VAT-registered — each one known and active, else a 400 on
// vatCodes.<kind>.
func resolveVatCodes(settings store.InvoicesSetting, req *gen.InvoicesFromWorkVatCodes, sel workSelection,
	codes map[int32]vatCodeOnDay,
) (workVatCodes, map[string][]string) {
	defaults := workVatCodes{settings.WorkVatCodeHours, settings.WorkVatCodeExpenses, settings.WorkVatCodeMilestones}
	if !settings.VatRegistered {
		defaults = workVatCodes{vatCodeNotRegistered, vatCodeNotRegistered, vatCodeNotRegistered}
	}
	var given gen.InvoicesFromWorkVatCodes
	if req != nil {
		given = *req
	}
	var errs map[string][]string
	resolve := func(kind string, present bool, override *int32, fallback int32) int32 {
		if !present {
			return fallback
		}
		field := "vatCodes." + kind
		if override != nil {
			switch c, ok := codes[*override]; {
			case !ok:
				errs = withFieldError(errs, field, "No VAT code has this id")
			case !c.active:
				errs = withFieldError(errs, field, "This VAT code is no longer offered for new lines")
			}
			return *override
		}
		if c, ok := codes[fallback]; !ok || !c.active {
			errs = withFieldError(errs, field, fmt.Sprintf(
				"The default VAT code for %s, code %s, is no longer offered for new lines: choose another, or change the default in the settings",
				kindWords[kind], c.code))
		}
		return fallback
	}
	return workVatCodes{
		hours:      resolve("hours", len(sel.hours) > 0, given.Hours, defaults.hours),
		expenses:   resolve("expenses", len(sel.expenses) > 0, given.Expenses, defaults.expenses),
		milestones: resolve("milestones", len(sel.milestones) > 0, given.Milestones, defaults.milestones),
	}, errs
}

// checkWorkLines is the column bounds of the lines the work made: a quantity,
// a unit price or a line too large for its column is a 400 on sources.
func checkWorkLines(lines []draftLine, errs map[string][]string) map[string][]string {
	for _, l := range lines {
		if l.quantity.Cmp(maxQuantity) > 0 || l.unitPrice.Cmp(maxUnitPrice) > 0 || l.amounts.gross.Cmp(maxLineGross) > 0 {
			return withFieldError(errs, "sources", "The work makes a line too large for one invoice line; choose a finer grouping")
		}
	}
	return errs
}

// storedLines are a draft's stored lines as a save writes them again: their
// fields as stored — a settlement's deduction line with the invoice it
// deducts, its quantity of -1 and its positive price (invoices work design
// D7) — their amounts computed again. A deduction's snapshot is the
// caller's to add (withDeductionSnapshots) before it is taxed.
func storedLines(rows []store.InvoicesLine) ([]draftLine, error) {
	out := make([]draftLine, 0, len(rows))
	for _, r := range rows {
		l := draftLine{
			description: r.Description, unit: r.Unit, vatCodeID: r.VatCodeID, creditsLineID: r.CreditsLineID,
			deductsInvoiceID: r.DeductsInvoiceID, sourcesGiven: true,
		}
		var err error
		if l.quantity, err = ratFromNumeric(r.Quantity); err != nil {
			return nil, err
		}
		if l.unitPrice, err = ratFromNumeric(r.UnitPrice); err != nil {
			return nil, err
		}
		if l.discount, err = ratFromNumeric(r.DiscountPercent); err != nil {
			return nil, err
		}
		l.amounts = computeLine(l.quantity, l.unitPrice, l.discount)
		out = append(out, l)
	}
	return out, nil
}

// workSpan is the first and last day of rows' work.
func workSpan(rows []heldSource) (first, last pgtype.Date) {
	for _, r := range rows {
		if !first.Valid || r.date.Before(first.Time) {
			first = pgDate(r.date)
		}
		if !last.Valid || r.date.After(last.Time) {
			last = pgDate(r.date)
		}
	}
	return first, last
}

// widenDelivery is an append's delivery (D3): the target's — its period, or
// its day as both ends — widened to cover the added work's first and last
// day, as a period; a target whose day the work does not leave stays that
// day. A target with no delivery at all takes the span of all the work it
// will hold, its own carried and the added. It answers the day, or the
// period, the draft is written with.
func widenDelivery(target store.InvoicesInvoice, carried, added []heldSource) (day, from, to pgtype.Date) {
	first, last := workSpan(added)
	from, to = target.DeliveryFrom, target.DeliveryTo
	if target.DeliveryDate.Valid {
		from, to = target.DeliveryDate, target.DeliveryDate
	}
	if !from.Valid || !to.Valid {
		from, to = workSpan(append(slices.Clone(carried), added...))
		return pgtype.Date{}, from, to
	}
	if first.Valid && first.Time.Before(from.Time) {
		from = first
	}
	if last.Valid && last.Time.After(to.Time) {
		to = last
	}
	if target.DeliveryDate.Valid && from.Time.Equal(to.Time) {
		return target.DeliveryDate, pgtype.Date{}, pgtype.Date{}
	}
	return pgtype.Date{}, from, to
}

// monthBefore is the same day a calendar month before day, clamped to that
// month's last day: 31 March gives 28 (or 29) February, never 3 March (D12).
func monthBefore(day time.Time) time.Time {
	y, m := day.Year(), day.Month()-1
	last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(y, m, min(day.Day(), last), 0, 0, 0, 0, time.UTC)
}

// fromWorkResult is the wizard's transaction's outcome.
type fromWorkResult struct {
	doc     store.InvoicesInvoice
	refusal *gen.InvoicesConflictProblem
	invalid map[string][]string
}

// PostInvoicesFromWork Invoice work
// (POST /api/v1/invoices/from-work)
//
// The wizard (D3): every judgment in the order the design gives, each read of
// a directory or a billable read before the transaction; then one
// withLockedTx that inserts the draft — or locks the target, still a draft at
// the revision the body names — writes the lines (a target's own first, their
// work carried), refuses work another live document holds, and holds the
// work in one statement. Two wizards racing for one source end in one: the
// second either finds the first's hold, or fails on ux_line_sources_live,
// answered the same way.
func (s *server) PostInvoicesFromWork(ctx context.Context, req gen.PostInvoicesFromWorkRequestObject) (gen.PostInvoicesFromWorkResponseObject, error) {
	body := *req.Body
	q := store.New(s.deps.Pool)
	// (1) The body, then an append's target, then the bound — before any
	// read of another module.
	refs, grouping, errs := parseFromWork(body)
	if len(errs) > 0 {
		return gen.PostInvoicesFromWork400ApplicationProblemPlusJSONResponse(invalid(invalidWorkTitle, errs)), nil
	}
	var target *store.InvoicesInvoice
	var targetHeld []heldSource
	if body.InvoiceId != nil {
		t, err := q.GetInvoice(ctx, *body.InvoiceId)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PostInvoicesFromWork404Response{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("invoices: read document %d: %w", *body.InvoiceId, err)
		}
		switch {
		case t.Status != statusDraft:
			return gen.PostInvoicesFromWork409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
		case t.Kind != kindInvoice:
			return gen.PostInvoicesFromWork400ApplicationProblemPlusJSONResponse(invalid(invalidWorkTitle,
				fieldError("invoiceId", msgSourcesOnCredit))), nil
		case t.CustomerID != body.CustomerId:
			return gen.PostInvoicesFromWork400ApplicationProblemPlusJSONResponse(invalid(invalidWorkTitle,
				fieldError("invoiceId", "This draft invoices another customer"))), nil
		}
		target = &t
		if targetHeld, err = sourcesOf(ctx, q, t.ID); err != nil {
			return nil, err
		}
	}
	if n := len(refs) + len(targetHeld); n > contracts.MaxBillableRows {
		return gen.PostInvoicesFromWork409ApplicationProblemPlusJSONResponse(conflict(codeTooManySources, cannotInvoiceWorkTitle,
			fmt.Sprintf("One document holds at most %d sources; this would hold %d.", contracts.MaxBillableRows, n))), nil
	}

	// (2) The settings, the profile and the customer's gates.
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	profile, err := s.customerProfile(ctx, body.CustomerId)
	if err != nil {
		return nil, err
	}
	if refusal := customerGate(profile); refusal != nil {
		return gen.PostInvoicesFromWork409ApplicationProblemPlusJSONResponse(*refusal), nil
	}

	// (3) The sources, by id. (4) Their projects.
	found, err := s.readSources(ctx, refs)
	if err != nil {
		return nil, err
	}
	if s.deps.Projects == nil {
		return gen.PostInvoicesFromWork409ApplicationProblemPlusJSONResponse(projectsUnavailable()), nil
	}
	// The projects of the new work, and on an append of the target's own —
	// every project the draft will hold, which its derived project needs.
	var projectIDs []int32
	for _, r := range refs {
		if f, ok := found.facts(r.sourceRef); ok && !slices.Contains(projectIDs, f.project) {
			projectIDs = append(projectIDs, f.project)
		}
	}
	for _, h := range targetHeld {
		if !slices.Contains(projectIDs, h.projectID) {
			projectIDs = append(projectIDs, h.projectID)
		}
	}
	slices.Sort(projectIDs)
	entries, err := s.projectEntries(ctx, projectIDs)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the work's projects: %w", err)
	}
	projects := make(map[int32]contracts.ProjectEntry, len(entries))
	for _, e := range entries {
		projects[e.ID] = e
	}

	// (5) The judgments.
	if refusal := judgeSources(refs, found, projects, body.CustomerId, settings.DefaultCurrency); refusal != nil {
		return gen.PostInvoicesFromWork409ApplicationProblemPlusJSONResponse(*refusal), nil
	}

	// (6) The lines: the VAT codes, the people a line names, the grouping.
	sel := found.selection(refs)
	codes, err := vatCodesOn(ctx, q, pgDate(businessDay(s.deps.Clock())))
	if err != nil {
		return nil, err
	}
	vat, errs := resolveVatCodes(settings, body.VatCodes, sel, codes)
	if len(errs) > 0 {
		return gen.PostInvoicesFromWork400ApplicationProblemPlusJSONResponse(invalid(invalidWorkTitle, errs)), nil
	}
	// The timesheet (D5): the request's flag, else an append target's own,
	// else the settings' default. On, its rows are written for every hour the
	// draft will hold — the added ones as just read, the target's read again
	// by id — each person named through the user directory, here before the
	// transaction; that one read names the people a line names too.
	timesheetOn := settings.TimesheetDefault
	if target != nil {
		timesheetOn = target.Timesheet
	}
	if body.Timesheet != nil {
		timesheetOn = *body.Timesheet
	}
	var sheet *timesheetRead
	if timesheetOn && s.deps.BillableHours != nil {
		hours := slices.Clone(sel.hours)
		carried, err := s.heldHours(ctx, targetHeld)
		if err != nil {
			return nil, err
		}
		names := make(map[int32]string, len(projects))
		for id, p := range projects {
			names[id] = p.Name
		}
		if sheet, err = s.readTimesheet(ctx, settings.TimesheetPersonLabel, append(hours, carried...), targetHeld, names); err != nil {
			return nil, err
		}
	}
	people := map[uuid.UUID]string{}
	switch {
	case grouping != groupingPerson && grouping != groupingItemised:
	case sheet != nil:
		people = sheet.names
	default:
		var ids []uuid.UUID
		for _, h := range sel.hours {
			if !slices.Contains(ids, h.UserID) {
				ids = append(ids, h.UserID)
			}
		}
		users, err := s.userEntries(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("invoices: name the people of the hours: %w", err)
		}
		for _, u := range users {
			people[u.ID] = u.DisplayName
		}
	}
	language := lineLanguage(profile.Language)
	lines, held, err := groupLines(language, grouping, projects, people, sel, vat)
	if err != nil {
		return nil, err
	}
	existing := 0
	if target != nil {
		n, err := q.Lines(ctx, target.ID)
		if err != nil {
			return nil, fmt.Errorf("invoices: read document %d's lines: %w", target.ID, err)
		}
		existing = len(n) // every line of the target, a settlement's deduction lines included
	}
	if existing+len(lines) > maxLines {
		refusal := conflict(codeTooManyLines, cannotInvoiceWorkTitle, fmt.Sprintf(
			"Grouped by %s the work makes %d lines, and a document holds at most %d.", grouping, existing+len(lines), maxLines))
		for g := suggestCoarser(grouping); g != ""; g = suggestCoarser(g) {
			coarser, _, err := groupLines(language, g, projects, people, sel, vat)
			if err != nil {
				return nil, err
			}
			if existing+len(coarser) <= maxLines {
				refusal.SuggestedGrouping = &g
				break
			}
		}
		return gen.PostInvoicesFromWork409ApplicationProblemPlusJSONResponse(refusal), nil
	}
	if errs := checkWorkLines(lines, nil); len(errs) > 0 {
		return gen.PostInvoicesFromWork400ApplicationProblemPlusJSONResponse(invalid(invalidWorkTitle, errs)), nil
	}

	// The delivery period: the request's, else the work's first and last day
	// — on an append widened from the target's, under its lock — so every
	// line's period sits inside the header's (D3).
	var from, to pgtype.Date
	periodGiven := body.DeliveryFrom != nil
	if periodGiven {
		from, to = pgDate(utcDay(body.DeliveryFrom.Time)), pgDate(utcDay(body.DeliveryTo.Time))
	} else {
		from, to = workSpan(slices.Concat(held...))
	}

	// The note: the request's, else — when the work was released by a
	// credit note before — the note naming the invoice it replaces (D8), in
	// the buyer's language, read on the pool. An append writes one only on a
	// target whose note is empty.
	note := optionalText(body.Note)
	if note == "" && (target == nil || target.Note == "") {
		pulled := make([]sourceRef, 0, len(refs))
		for _, r := range refs {
			pulled = append(pulled, r.sourceRef)
		}
		if note, err = rePullNote(ctx, q, language, pulled); err != nil {
			return nil, err
		}
	}

	var out fromWorkResult
	var rows []heldSource
	var invoiceID int64
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		all, carried := lines, []heldSource(nil)
		var doc store.InvoicesInvoice
		if target == nil {
			_, totals, _ := summarize(taxedLines(all, codes), big.NewRat(1, 1))
			if e := checkTotal(totals, nil); e != nil {
				out.invalid = e
				return errRefused
			}
			net, vatTotal, gross, vatNOK, err := numerics(totals)
			if err != nil {
				return err
			}
			terms := profile.PaymentTermsDays
			if terms == nil {
				terms = &settings.DefaultPaymentTermsDays
			}
			doc, err = txq.InsertInvoiceDraft(ctx, store.InsertInvoiceDraftParams{
				CustomerID: body.CustomerId, DeliveryFrom: from, DeliveryTo: to, PaymentTermsDays: terms,
				Currency: settings.DefaultCurrency, YourReference: profile.BuyerReference, Note: note,
				NetTotal: net, VatTotal: vatTotal, GrossTotal: gross, VatTotalNok: vatNOK, Timesheet: timesheetOn,
				CreatedByUserID: callerID(ctx), Now: s.deps.Clock(),
			})
			if err != nil {
				return fmt.Errorf("invoices: create a draft from work: %w", err)
			}
		} else {
			locked, err := txq.LockInvoice(ctx, target.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				return errDocumentGone
			}
			if err != nil {
				return fmt.Errorf("invoices: lock document %d: %w", target.ID, err)
			}
			if locked.Status != statusDraft {
				out.refusal = ptr(invoiceIssued())
				return errRefused
			}
			if locked.Revision != *body.Revision {
				out.refusal = ptr(revisionConflict("Invoice", locked.Revision, *body.Revision))
				return errRefused
			}
			if carried, err = sourcesOf(ctx, txq, locked.ID); err != nil {
				return err
			}
			// Work the target holds already is held by this very draft: the
			// index would refuse it, and the view lists it held by it.
			own := make(map[sourceRef]bool, len(carried))
			for _, h := range carried {
				own[h.ref()] = true
			}
			for _, r := range refs {
				if own[r.sourceRef] {
					out.refusal = heldByProblem(string(r.kind), r.id,
						gen.InvoicesWorkHeldBy{InvoiceId: locked.ID, Number: locked.Number, Status: locked.Status})
					return errRefused
				}
			}
			stored, err := txq.Lines(ctx, locked.ID)
			if err != nil {
				return fmt.Errorf("invoices: read document %d's lines: %w", locked.ID, err)
			}
			kept, err := storedLines(stored)
			if err != nil {
				return err
			}
			// A settlement's deductions keep their a-kontos' snapshots
			// (D7), read from the documents' own rows and never locked.
			if err := withDeductionSnapshots(ctx, txq, kept); err != nil {
				return err
			}
			all = append(kept, lines...)
			rate, err := ratFromNumeric(locked.ExchangeRate)
			if err != nil {
				return err
			}
			_, totals, _ := summarize(taxedLines(all, codes), rate)
			if e := checkTotal(totals, nil); e != nil {
				out.invalid = e
				return errRefused
			}
			net, vatTotal, gross, vatNOK, err := numerics(totals)
			if err != nil {
				return err
			}
			var deliveryDate pgtype.Date
			deliveryFrom, deliveryTo := from, to
			if !periodGiven {
				deliveryDate, deliveryFrom, deliveryTo = widenDelivery(locked, carried, slices.Concat(held...))
			}
			targetNote := locked.Note
			if targetNote == "" {
				targetNote = note
			}
			doc, err = txq.UpdateDraft(ctx, store.UpdateDraftParams{
				ID: locked.ID, CustomerID: locked.CustomerID, DeliveryDate: deliveryDate, DeliveryFrom: deliveryFrom, DeliveryTo: deliveryTo,
				DeliveryAddressLine1: locked.DeliveryAddressLine1, DeliveryAddressLine2: locked.DeliveryAddressLine2,
				DeliveryPostalCode: locked.DeliveryPostalCode, DeliveryCity: locked.DeliveryCity, DeliveryCountry: locked.DeliveryCountry,
				PaymentTermsDays: locked.PaymentTermsDays, YourReference: locked.YourReference, OurReference: locked.OurReference,
				OrderReference: locked.OrderReference, Note: targetNote, InternalNote: locked.InternalNote,
				NetTotal: net, VatTotal: vatTotal, GrossTotal: gross, VatTotalNok: vatNOK, Timesheet: timesheetOn, Now: s.deps.Clock(),
			})
			if err != nil {
				return fmt.Errorf("invoices: add work to draft %d: %w", locked.ID, err)
			}
		}
		invoiceID = doc.ID
		lineIDs, err := writeLines(ctx, txq, doc.ID, all)
		if err != nil {
			return err
		}
		// The new work goes under the lines after the target's own.
		offset := int32(len(all) - len(lines))
		var added []heldSource
		for _, line := range held {
			for _, h := range line {
				h.linePosition += offset
				added = append(added, h)
			}
		}
		if refusal, err := liveElsewhere(ctx, txq, doc.ID, added); refusal != nil || err != nil {
			out.refusal = refusal
			return cmp.Or(err, errRefused)
		}
		if fromWorkBeforeInsert != nil {
			fromWorkBeforeInsert(ctx, doc.ID)
		}
		rows = append(carried, added...)
		if err := insertSources(ctx, txq, doc.ID, lineIDs, rows); err != nil {
			rows = added
			return err
		}
		// The draft's project, derived from every row it now holds like any
		// save's (D9), its code from the directory read before the lock.
		if doc, err = setDocumentProject(ctx, txq, doc, rows, projectCodes(entries)); err != nil {
			return err
		}
		// The timesheet of every hour the draft now holds, written anew
		// after the holds (D5); a target's turned off is deleted.
		if timesheetOn || (target != nil && target.Timesheet) {
			if err := saveTimesheet(ctx, txq, doc.ID, timesheetOn, sheet, rows); err != nil {
				return err
			}
		}
		out.doc = doc
		return nil
	})
	switch {
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesFromWork404Response{}, nil
	case errors.Is(err, errSourceHeldElsewhere):
		refusal, err := heldElsewhere(ctx, q, invoiceID, rows)
		if err != nil {
			return nil, err
		}
		return gen.PostInvoicesFromWork409ApplicationProblemPlusJSONResponse(refusal), nil
	case out.refusal != nil:
		return gen.PostInvoicesFromWork409ApplicationProblemPlusJSONResponse(*out.refusal), nil
	case out.invalid != nil:
		return gen.PostInvoicesFromWork400ApplicationProblemPlusJSONResponse(invalid(invalidWorkTitle, out.invalid)), nil
	case err != nil:
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, out.doc, profile)
	if err != nil {
		return nil, err
	}
	if target != nil {
		return gen.PostInvoicesFromWork200JSONResponse(resp), nil
	}
	return gen.PostInvoicesFromWork201JSONResponse(resp), nil
}
