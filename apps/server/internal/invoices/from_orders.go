package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the From orders wizard (orders design O36–O39): GET
// /invoices/from-orders/due lists the order lines due to be invoiced, as
// Orders' DueOrders answers them; POST /invoices/from-orders allocates the
// lines asked for — at the quantities asked, less than is left being
// delfakturering — through Orders' Allocate, then writes the drafts in one
// transaction, one per order or combined per customer (samlefaktura). Every
// call to Orders is made on the pool, never under this module's locks:
// Allocate writes Orders' own rows in transactions of its own, and should
// anything fail after it the allocations are released again.

// The From orders wizard's refusals, beside the work wizard's (work.go) where
// the rule is the same.
const (
	codeOrdersUnavailable = "orders_unavailable"
	codeOrderBlocked      = "order_blocked"
	codeNoOrdersDue       = "no_orders_due"
	// codeAllocationExceedsOpen and codeLineNotInvoiceable are Orders'
	// AllocationRefusal codes (contracts.AllocationExceedsOpen,
	// contracts.AllocationLineNotInvoiceable), which the wizard answers as
	// they are, and judges itself first from DueOrders.
	codeAllocationExceedsOpen = "allocation_exceeds_open"
	codeLineNotInvoiceable    = "line_not_invoiceable"

	cannotInvoiceOrdersTitle = "The orders cannot be invoiced"
	invalidOrdersTitle       = "Invalid order selection"
)

// warningOrdersTruncated is a due list Orders had more than MaxBillableRows
// lines to answer for: the last orders are not listed.
const warningOrdersTruncated = "orders_truncated"

func ordersUnavailable() gen.InvoicesConflictProblem {
	return conflict(codeOrdersUnavailable, cannotInvoiceOrdersTitle, "The orders module, which holds the orders, is switched off.")
}

// orderLineConflict is one of the wizard's refusals about one order line,
// carrying its id.
func orderLineConflict(code string, lineID int64, detail string) *gen.InvoicesConflictProblem {
	p := conflict(code, cannotInvoiceOrdersTitle, detail)
	p.OrderLineId = &lineID
	return &p
}

// GetInvoicesFromOrdersDue List the order lines due to be invoiced
// (GET /api/v1/invoices/from-orders/due)
//
// The wizard's first two steps (O37, O39): exactly one of customerId,
// orderId and every; Orders' DueOrders for it, the customers named as the
// directory names them. Nothing is written and nothing allocated.
func (s *server) GetInvoicesFromOrdersDue(ctx context.Context, req gen.GetInvoicesFromOrdersDueRequestObject) (gen.GetInvoicesFromOrdersDueResponseObject, error) {
	p := req.Params
	due := contracts.DueOrdersRequest{Every: p.Every != nil && *p.Every}
	if p.CustomerId != nil {
		due.CustomerIDs = *p.CustomerId
	}
	if p.OrderId != nil {
		due.OrderIDs = *p.OrderId
	}
	if p.Until != nil {
		due.Until = utcDay(p.Until.Time)
	}
	if err := due.Validate(); err != nil {
		return gen.GetInvoicesFromOrdersDue400ApplicationProblemPlusJSONResponse(invalid(invalidOrdersTitle,
			fieldError("customerId", "Name customers, orders or every order — one of the three, each at most once"))), nil
	}
	if s.deps.BillableOrders == nil {
		return gen.GetInvoicesFromOrdersDue409ApplicationProblemPlusJSONResponse(ordersUnavailable()), nil
	}
	page, err := s.dueOrders(ctx, due)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the orders due: %w", err)
	}
	names, err := s.customerNames(ctx, page.Orders)
	if err != nil {
		return nil, err
	}
	view := gen.InvoicesDueOrdersResponse{Orders: make([]gen.InvoicesDueOrder, 0, len(page.Orders)), Warnings: []string{}}
	if page.More {
		view.Warnings = append(view.Warnings, warningOrdersTruncated)
	}
	for _, o := range page.Orders {
		out, err := dueOrderView(o)
		if err != nil {
			return nil, err
		}
		if name, ok := names[o.CustomerID]; ok {
			out.CustomerName = &name
		}
		view.Orders = append(view.Orders, out)
	}
	return gen.GetInvoicesFromOrdersDue200JSONResponse(view), nil
}

// customerNames is the orders' customers' names as the directory answers
// them, in chunks of one read's size; empty when the directory is off.
func (s *server) customerNames(ctx context.Context, orders []contracts.DueOrder) (map[int32]string, error) {
	names := map[int32]string{}
	if s.deps.Directory == nil {
		return names, nil
	}
	var ids []int32
	for _, o := range orders {
		if !slices.Contains(ids, o.CustomerID) {
			ids = append(ids, o.CustomerID)
		}
	}
	for chunk := range slices.Chunk(ids, customerChunk) {
		entries, err := s.customerEntries(ctx, chunk)
		if err != nil {
			return nil, fmt.Errorf("invoices: name the orders' customers: %w", err)
		}
		for _, e := range entries {
			names[e.ID] = e.Name
		}
	}
	return names, nil
}

// dueOrderView is a due order on the wire, its numbers at their own decimals
// and each line's amount docmath's net of its whole quantity left.
func dueOrderView(o contracts.DueOrder) (gen.InvoicesDueOrder, error) {
	out := gen.InvoicesDueOrder{
		Id: o.ID, Number: o.Number, CustomerId: o.CustomerID, Currency: o.Currency, ProjectId: o.ProjectID,
		YourReference: o.YourReference, PaymentTermsDays: o.PaymentTermsDays, Blocked: nonEmpty(o.Blocked),
		Lines: make([]gen.InvoicesDueOrderLine, 0, len(o.Lines)),
	}
	if o.DeliveryDate != nil {
		out.DeliveryDate = ptr(wireDate(utcDay(*o.DeliveryDate)))
	}
	if a := o.DeliveryAddress; a != nil {
		out.DeliveryName = nonEmpty(a.Label)
		out.DeliveryAddress = &gen.InvoicesDeliveryAddress{
			Line1: a.Line1, Line2: nonEmpty(a.Line2), PostalCode: nonEmpty(a.PostalCode), City: a.City, Country: a.Country,
		}
	}
	for _, l := range o.Lines {
		quantity, err := billableAmount(l.ToInvoice)
		if err != nil {
			return out, err
		}
		price, err := billableAmount(l.UnitPrice)
		if err != nil {
			return out, err
		}
		discount, err := billableAmount(l.DiscountPercent)
		if err != nil {
			return out, err
		}
		out.Lines = append(out.Lines, gen.InvoicesDueOrderLine{
			LineId: l.LineID, Position: l.Position, Description: l.Description, Unit: l.Unit,
			ToInvoice: floatFromRat(quantity, 3), UnitPrice: floatFromRat(price, unitPricePlaces),
			DiscountPercent: floatFromRat(discount, 2), VatCodeId: l.VATCodeID, InvoicePolicy: l.InvoicePolicy,
			Amount: floatFromRat(computeLine(quantity, price, discount).Net, 2),
		})
	}
	return out, nil
}

// orderLineInput is one line the wizard is asked to invoice.
type orderLineInput struct {
	orderID, lineID int64
	quantity        *big.Rat
}

// parseFromOrders is the body's 400s: 1 to MaxBillableRows lines, each
// naming a positive order and line, no line twice, and a quantity above zero
// with at most three decimals within the column.
func parseFromOrders(body gen.InvoicesFromOrdersRequest) ([]orderLineInput, map[string][]string) {
	var errs map[string][]string
	switch {
	case len(body.Lines) == 0:
		return nil, fieldError("lines", "Choose the order lines to invoice")
	case len(body.Lines) > contracts.MaxBillableRows:
		return nil, fieldError("lines", fmt.Sprintf("At most %d order lines are invoiced at once", contracts.MaxBillableRows))
	}
	out := make([]orderLineInput, 0, len(body.Lines))
	seen := map[int64]bool{}
	for i, l := range body.Lines {
		field := func(name string) string { return fmt.Sprintf("lines[%d].%s", i, name) }
		if l.OrderId <= 0 {
			errs = withFieldError(errs, field("orderId"), "An order's id is positive")
		}
		switch {
		case l.LineId <= 0:
			errs = withFieldError(errs, field("lineId"), "An order line's id is positive")
		case seen[l.LineId]:
			errs = withFieldError(errs, field("lineId"), "Each order line is named once")
		}
		seen[l.LineId] = true
		quantity, msg := amount("A quantity", l.Quantity, 3, zero, true, maxQuantity)
		if msg != "" {
			errs = withFieldError(errs, field("quantity"), msg)
		}
		out = append(out, orderLineInput{orderID: l.OrderId, lineID: l.LineId, quantity: quantity})
	}
	return out, errs
}

// PostInvoicesFromOrders Invoice order lines
// (POST /api/v1/invoices/from-orders)
func (s *server) PostInvoicesFromOrders(ctx context.Context, req gen.PostInvoicesFromOrdersRequestObject) (gen.PostInvoicesFromOrdersResponseObject, error) {
	lines, errs := parseFromOrders(*req.Body)
	if len(errs) > 0 {
		return gen.PostInvoicesFromOrders400ApplicationProblemPlusJSONResponse(invalid(invalidOrdersTitle, errs)), nil
	}
	out, err := s.fromOrders(ctx, lines, req.Body.Combine)
	switch {
	case err != nil:
		return nil, err
	case out.invalid != nil:
		return gen.PostInvoicesFromOrders400ApplicationProblemPlusJSONResponse(invalid(invalidOrdersTitle, out.invalid)), nil
	case out.refusal != nil:
		return gen.PostInvoicesFromOrders409ApplicationProblemPlusJSONResponse(*out.refusal), nil
	}
	return gen.PostInvoicesFromOrders201JSONResponse(gen.InvoicesFromOrdersResponse{Invoices: out.drafts}), nil
}

// ordersOutcome is what the wizard's core made of its lines: the drafts, or
// the refusal (a 409) or the field errors (a 400). Exactly one holds.
type ordersOutcome struct {
	drafts  []gen.InvoicesFromOrdersDraft
	refusal *gen.InvoicesConflictProblem
	invalid map[string][]string
}

// orderDraft is one draft about to be written: its group, lines, sources,
// header, totals and customer facts.
type orderDraft struct {
	group   orderGroup
	lines   []draftLine
	rows    []heldSource
	header  orderHeader
	totals  documentTotals
	profile *contracts.CustomerBillingProfile
}

// fromOrders is the wizard's core (O39): the handler's and the billing run's
// one code path. Its actor is the principal on ctx. In order, every read
// before any write: Orders off; DueOrders for the orders named, and each
// line judged against them — its order blocked, or not due on it; the
// settings, each customer's profile and gates; the currency; the VAT codes;
// the grouping and its line bound; the projects' codes. Then Allocate, and
// from there every refusal and failure releases the allocations again; then
// one transaction writes every draft.
func (s *server) fromOrders(ctx context.Context, lines []orderLineInput, combine bool) (ordersOutcome, error) {
	if s.deps.BillableOrders == nil {
		return ordersOutcome{refusal: ptr(ordersUnavailable())}, nil
	}
	var orderIDs []int64
	for _, l := range lines {
		if !slices.Contains(orderIDs, l.orderID) {
			orderIDs = append(orderIDs, l.orderID)
		}
	}
	slices.Sort(orderIDs)
	page, err := s.dueOrders(ctx, contracts.DueOrdersRequest{OrderIDs: orderIDs})
	if err != nil {
		return ordersOutcome{}, fmt.Errorf("invoices: read the orders to invoice: %w", err)
	}
	if page.More {
		return ordersOutcome{refusal: ptr(conflict(codeTooManySources, cannotInvoiceOrdersTitle,
			fmt.Sprintf("The orders have more than %d lines to invoice; choose fewer orders.", contracts.MaxBillableRows)))}, nil
	}
	due := make(map[int64]contracts.DueOrder, len(page.Orders))
	for _, o := range page.Orders {
		due[o.ID] = o
	}
	positions := map[int64]int32{}
	vatCodes := map[int64]int32{}
	var taken []contracts.DueOrder
	for _, l := range lines {
		o, ok := due[l.orderID]
		if !ok {
			return ordersOutcome{refusal: orderLineConflict(codeLineNotInvoiceable, l.lineID, fmt.Sprintf(
				"Order %d has nothing left to invoice, or is gone.", l.orderID))}, nil
		}
		if o.Blocked != "" {
			return ordersOutcome{refusal: orderLineConflict(codeOrderBlocked, l.lineID, fmt.Sprintf(
				"Order %d cannot be invoiced now (%s).", o.Number, o.Blocked))}, nil
		}
		i := slices.IndexFunc(o.Lines, func(dl contracts.DueOrderLine) bool { return dl.LineID == l.lineID })
		if i < 0 {
			return ordersOutcome{refusal: orderLineConflict(codeLineNotInvoiceable, l.lineID, fmt.Sprintf(
				"Order line %d is not a line of order %d left to invoice.", l.lineID, o.Number))}, nil
		}
		left, err := billableAmount(o.Lines[i].ToInvoice)
		if err != nil {
			return ordersOutcome{}, err
		}
		if l.quantity.Cmp(left) > 0 {
			return ordersOutcome{refusal: orderLineConflict(codeAllocationExceedsOpen, l.lineID, fmt.Sprintf(
				"Order %d's line %d has %s left to invoice.", o.Number, o.Lines[i].Position, left.FloatString(3)))}, nil
		}
		positions[l.lineID], vatCodes[l.lineID] = o.Lines[i].Position, o.Lines[i].VATCodeID
		if !slices.ContainsFunc(taken, func(t contracts.DueOrder) bool { return t.ID == o.ID }) {
			taken = append(taken, o)
		}
	}

	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return ordersOutcome{}, fmt.Errorf("invoices: read the settings: %w", err)
	}
	profiles := map[int32]*contracts.CustomerBillingProfile{}
	for _, o := range taken {
		if _, ok := profiles[o.CustomerID]; ok {
			continue
		}
		profile, err := s.customerProfile(ctx, o.CustomerID)
		if err != nil {
			return ordersOutcome{}, err
		}
		if refusal := customerGate(profile); refusal != nil {
			return ordersOutcome{refusal: refusal}, nil
		}
		profiles[o.CustomerID] = profile
	}
	for _, o := range taken {
		if o.Currency != settings.DefaultCurrency {
			return ordersOutcome{refusal: ptr(conflict(codeCurrencyNotNOK, cannotInvoiceOrdersTitle, fmt.Sprintf(
				"Order %d is in %s, and this module invoices in %s only.", o.Number, o.Currency, settings.DefaultCurrency)))}, nil
		}
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return ordersOutcome{}, err
	}
	today := holidays.DayIn(s.deps.Clock(), loc)
	codes, err := vatCodesOn(ctx, q, pgDate(today))
	if err != nil {
		return ordersOutcome{}, err
	}
	for _, l := range lines {
		if c, ok := codes[vatCodes[l.lineID]]; !ok || !c.active {
			return ordersOutcome{refusal: orderLineConflict(codeVatCodeInactive, l.lineID, fmt.Sprintf(
				"Order line %d's VAT code is no longer offered for new lines.", l.lineID))}, nil
		}
	}
	groups := groupOrders(taken, combine)
	perOrder := map[int64]int{}
	for _, l := range lines {
		perOrder[l.orderID]++
	}
	for _, g := range groups {
		n := 0
		for _, o := range g.orders {
			n += perOrder[o.ID]
			if len(g.orders) > 1 {
				n++ // the section line
			}
		}
		if n > maxLines {
			return ordersOutcome{refusal: ptr(conflict(codeTooManyLines, cannotInvoiceOrdersTitle, fmt.Sprintf(
				"An invoice would have %d lines, and a document holds at most %d; choose fewer lines or one invoice per order.", n, maxLines)))}, nil
		}
	}
	var projectIDs []int32
	for _, o := range taken {
		if o.ProjectID != nil && !slices.Contains(projectIDs, *o.ProjectID) {
			projectIDs = append(projectIDs, *o.ProjectID)
		}
	}
	var entries []contracts.ProjectEntry
	if len(projectIDs) > 0 && s.deps.Projects != nil {
		slices.Sort(projectIDs)
		if entries, err = s.projectEntries(ctx, projectIDs); err != nil {
			return ordersOutcome{}, fmt.Errorf("invoices: read the orders' projects: %w", err)
		}
	}

	// Allocate, on the pool. From here every way out but success gives the
	// allocations back.
	req := contracts.AllocateRequest{Lines: make([]contracts.AllocateLine, 0, len(lines))}
	for _, l := range lines {
		req.Lines = append(req.Lines, contracts.AllocateLine{LineID: l.lineID, Quantity: l.quantity.FloatString(3)})
	}
	allocations, err := s.allocate(ctx, req)
	var refused *contracts.AllocationRefusal
	if errors.As(err, &refused) {
		return ordersOutcome{refusal: orderLineConflict(refused.Code, refused.LineID, fmt.Sprintf(
			"Order line %d cannot be invoiced: %s", refused.LineID, refused.Detail))}, nil
	}
	if err != nil {
		return ordersOutcome{}, fmt.Errorf("invoices: allocate the order lines: %w", err)
	}
	ids := make([]int64, 0, len(allocations))
	for _, a := range allocations {
		ids = append(ids, a.ID)
	}
	done := false
	defer func() {
		if !done {
			s.releaseOrderAllocations(ctx, 0, ids)
		}
	}()

	byOrder := map[int64][]contracts.OrderAllocation{}
	for _, a := range allocations {
		if _, ok := due[a.OrderID]; !ok {
			return ordersOutcome{}, fmt.Errorf("invoices: Orders allocated line %d of order %d, which was not asked for", a.LineID, a.OrderID)
		}
		byOrder[a.OrderID] = append(byOrder[a.OrderID], a)
	}
	dates := map[int64]time.Time{}
	for _, o := range taken {
		dates[o.ID] = today
		if o.DeliveryDate != nil {
			dates[o.ID] = utcDay(*o.DeliveryDate)
		}
	}
	drafts := make([]orderDraft, 0, len(groups))
	for _, g := range groups {
		profile := profiles[g.customerID]
		language := lineLanguage(profile.Language)
		made, rows, err := orderDraftLines(language, g, byOrder, positions, dates)
		if err != nil {
			return ordersOutcome{}, err
		}
		for _, l := range made {
			if l.quantity.Cmp(maxQuantity) > 0 || l.unitPrice.Cmp(maxUnitPrice) > 0 || l.amounts.Gross.Cmp(maxLineGross) > 0 {
				return ordersOutcome{invalid: fieldError("lines", "An order line makes a line too large for one invoice line")}, nil
			}
		}
		_, totals, _ := summarize(taxedLines(made, codes), big.NewRat(1, 1))
		if e := checkTotal(totals, nil); e != nil {
			return ordersOutcome{invalid: e}, nil
		}
		terms := profile.PaymentTermsDays
		if terms == nil {
			terms = &settings.DefaultPaymentTermsDays
		}
		drafts = append(drafts, orderDraft{
			group: g, lines: made, rows: rows, totals: totals, profile: profile,
			header: headerOf(g, rows, profile.BuyerReference, terms, today),
		})
	}

	var out []gen.InvoicesFromOrdersDraft
	var failed []heldSource
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		out = make([]gen.InvoicesFromOrdersDraft, 0, len(drafts))
		for _, d := range drafts {
			net, vatTotal, gross, vatNOK, err := numerics(d.totals)
			if err != nil {
				return err
			}
			h := d.header
			line1, line2, postal, city, country := addressColumns(h.address)
			doc, err := txq.InsertInvoiceDraft(ctx, store.InsertInvoiceDraftParams{
				CustomerID: d.group.customerID, DeliveryDate: h.deliveryDate, DeliveryFrom: h.deliveryFrom, DeliveryTo: h.deliveryTo,
				DeliveryAddressLine1: line1, DeliveryAddressLine2: line2, DeliveryPostalCode: postal, DeliveryCity: city, DeliveryCountry: country,
				PaymentTermsDays: h.terms, Currency: settings.DefaultCurrency, YourReference: h.yourReference,
				OrderReference: h.orderReference, NetTotal: net, VatTotal: vatTotal, GrossTotal: gross, VatTotalNok: vatNOK,
				CreatedByUserID: callerID(ctx), Now: s.deps.Clock(),
			})
			if err != nil {
				return fmt.Errorf("invoices: create a draft from orders: %w", err)
			}
			lineIDs, err := writeLines(ctx, txq, doc.ID, d.lines)
			if err != nil {
				return err
			}
			if err := insertSources(ctx, txq, doc.ID, lineIDs, d.rows); err != nil {
				failed = d.rows
				return err
			}
			if _, err := setDocumentProject(ctx, txq, doc, d.rows, projectCodes(entries)); err != nil {
				return err
			}
			entry := gen.InvoicesFromOrdersDraft{
				InvoiceId: doc.ID, CustomerId: d.group.customerID, SeparatedBy: d.group.separatedBy,
				OrderIds: []int64{}, OrderNumbers: []int64{},
			}
			for _, o := range d.group.orders {
				entry.OrderIds, entry.OrderNumbers = append(entry.OrderIds, o.ID), append(entry.OrderNumbers, o.Number)
			}
			out = append(out, entry)
		}
		return nil
	})
	if errors.Is(err, errSourceHeldElsewhere) {
		refusal, err := heldElsewhere(ctx, q, 0, failed)
		if err != nil {
			return ordersOutcome{}, err
		}
		return ordersOutcome{refusal: &refusal}, nil
	}
	if err != nil {
		return ordersOutcome{}, err
	}
	done = true
	return ordersOutcome{drafts: out}, nil
}

// releaseOrderAllocations gives ids back to Orders (O36), after the
// transaction that let go of them has committed — a draft deleted, a save
// that dropped them, or a wizard that failed after Allocate: best effort, on
// a context the request's end does not cancel, logged on failure. Orders'
// sweep releases what a failed call leaves.
func (s *server) releaseOrderAllocations(ctx context.Context, invoiceID int64, ids []int64) {
	if len(ids) == 0 || s.deps.BillableOrders == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for chunk := range slices.Chunk(ids, contracts.MaxBillableRows) {
		if err := s.releaseAllocations(ctx, chunk); err != nil {
			s.deps.Logger.WarnContext(ctx, "invoices: order allocations could not be released; Orders' sweep releases them",
				"invoiceId", invoiceID, "allocations", chunk, "error", err.Error())
		}
	}
}
