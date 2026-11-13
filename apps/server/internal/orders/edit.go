package orders

// An order's save (orders design O14, O16, O26). A draft's save is a quote
// draft's: the header resolved again over the customer's billing profile —
// the snapshot follows the customer (O14) — and the catalog lines priced
// again when the currency or the day moved (O16). A confirmed order's save
// takes only what O26 allows: lines added, a line's quantity down to its
// progress, its price, discount and text while nothing of it is invoiced, a
// line without progress removed, and the delivery date, the delivery address,
// the contact, your reference and the internal note; anything else of the
// frozen header that differs is refused. It is judged under the order's lock
// against the lines as they are then, logs one change entry, bumps the
// version and publishes orders.sales_order.changed.v1. The save settles each
// line's delivery method and invoice policy (O48), changes the site (O46),
// and moves the order's reservations with it (stock.go).

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// updateOrderParams are a header as UpdateOrderHeader writes it.
func updateOrderParams(id int64, h header, t docTotals, delivery, invoice string, version int32, now time.Time) store.UpdateOrderHeaderParams {
	p := insertQuoteParams(h, t)
	return store.UpdateOrderHeaderParams{
		ID: id, Version: version, Now: now, DeliveryStatus: delivery, InvoiceStatus: invoice,
		CustomerID: p.CustomerID, CustomerNumber: p.CustomerNumber, CustomerName: p.CustomerName, CustomerType: p.CustomerType,
		CustomerOrganisationNumber: p.CustomerOrganisationNumber, CustomerForeignID: p.CustomerForeignID,
		CustomerCountry: p.CustomerCountry, InvoiceAddressLine1: p.InvoiceAddressLine1, InvoiceAddressLine2: p.InvoiceAddressLine2,
		InvoicePostalCode: p.InvoicePostalCode, InvoiceCity: p.InvoiceCity, InvoiceCountry: p.InvoiceCountry,
		DeliveryAddressID: p.DeliveryAddressID, DeliveryName: p.DeliveryName, DeliveryAddressLine1: p.DeliveryAddressLine1,
		DeliveryAddressLine2: p.DeliveryAddressLine2, DeliveryPostalCode: p.DeliveryPostalCode, DeliveryCity: p.DeliveryCity,
		DeliveryCountry: p.DeliveryCountry, ContactID: p.ContactID, ContactName: p.ContactName, ContactEmail: p.ContactEmail,
		ContactPhone: p.ContactPhone, CustomerInvoiceEmail: p.CustomerInvoiceEmail, YourReference: p.YourReference,
		SalespersonID: p.SalespersonID, SalespersonDisplay: p.SalespersonDisplay, ProjectID: p.ProjectID, Currency: p.Currency,
		PricesIncludeVat: p.PricesIncludeVat, PaymentTermsDays: p.PaymentTermsDays, DiscountVisible: p.DiscountVisible,
		Language: p.Language, IntroText: p.IntroText, ClosingText: p.ClosingText, InternalNote: p.InternalNote,
		DeliveryDate: p.DeliveryDate, NetTotal: p.NetTotal, VatTotal: p.VatTotal, GrossTotal: p.GrossTotal,
	}
}

// UpdateSalesOrder Save an order
// (PUT /api/v1/orders/orders/{id})
func (s *server) UpdateSalesOrder(ctx context.Context, req gen.UpdateSalesOrderRequestObject) (gen.UpdateSalesOrderResponseObject, error) {
	resp := answers[gen.UpdateSalesOrderResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.UpdateSalesOrderResponseObject {
			return gen.UpdateSalesOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateSalesOrderResponseObject { return gen.UpdateSalesOrder404Response{} },
		conflict: func(p gen.OrdersProblem) gen.UpdateSalesOrderResponseObject {
			return gen.UpdateSalesOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.UpdateSalesOrderResponseObject {
			return gen.UpdateSalesOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, orderUpdateSchema)
	if f.failed() {
		return gen.UpdateSalesOrder400ApplicationProblemPlusJSONResponse(invalid(invalidOrderTitle, f)), nil
	}
	body := *req.Body
	cur, curLines, err := readOrder(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return answer(err, resp)
	}
	if err := judgeVersion(cur, body.Version, body.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	var out gen.OrdersOrder
	switch cur.Status {
	case orderDraft:
		out, err = s.saveDraftOrder(ctx, cur, curLines, body)
	case orderConfirmed:
		out, err = s.saveConfirmedOrder(ctx, cur, curLines, body)
	default:
		err = conflict(codeOrderNotConfirmed, "The order is cancelled",
			fmt.Sprintf("Order %d is cancelled; a cancelled order is not changed.", cur.Number))
	}
	if err != nil {
		return answer(err, resp)
	}
	return gen.UpdateSalesOrder200JSONResponse(out), nil
}

// saveDraftOrder is a draft's save: every read before the transaction, which
// locks the variants first, then the order, judged a draft at the version
// again under it.
func (s *server) saveDraftOrder(ctx context.Context, cur store.OrdersOrder, curLines []store.OrdersLine, body gen.OrdersOrderUpdate) (gen.OrdersOrder, error) {
	var f fieldErrors
	base := headerOfOrder(cur)
	in := headerInputOfOrderUpdate(body)
	reads, err := s.readHeader(ctx, in, &base)
	if err != nil {
		return gen.OrdersOrder{}, err
	}
	h := resolveHeader(&f, in, &base, reads)
	if in.CustomerID != cur.CustomerID {
		customerUsable(&f, reads.profile)
	}
	if f.failed() {
		return gen.OrdersOrder{}, refuseInvalid(invalidOrderTitle, f)
	}
	reprice, err := s.repriceKept(ctx, cur.Currency, h.Currency, cur.UpdatedAt)
	if err != nil {
		return gen.OrdersOrder{}, err
	}
	priced, err := s.priceOrderInputs(ctx, lineInputsOf(body.Lines), curLines, h, reprice, false)
	if err != nil {
		return gen.OrdersOrder{}, err
	}
	// A draft's site is the body's, none when it names none (O46).
	site := body.SiteId
	if site != nil && !equalInt64(site, cur.SiteID) {
		if err := s.judgeSite(ctx, &f, "siteId", *site); err != nil {
			return gen.OrdersOrder{}, err
		}
		if f.failed() {
			return gen.OrdersOrder{}, refuseInvalid(invalidOrderTitle, f)
		}
	}
	var out gen.OrdersOrder
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidOrderTitle, priced); err != nil {
			return err
		}
		o, err := lockOrder(ctx, txq, cur.ID)
		if err != nil {
			return err
		}
		if err := judgeVersion(o, body.Version, body.ExpectedUpdatedAt); err != nil {
			return err
		}
		if o.Status != orderDraft {
			return conflict(codeVersionConflict, "The order changed",
				fmt.Sprintf("Order %d was confirmed while it was being saved. Reload it and try again.", o.Number))
		}
		current, err := linesOf(ctx, txq, ownerOrder, o.ID)
		if err != nil {
			return err
		}
		siteChanged := !equalInt64(site, o.SiteID)
		settlePolicies(ctx, priced, byID(current), s.atSite(site), siteChanged)
		if siteChanged {
			if _, err := txq.SetOrderSite(ctx, store.SetOrderSiteParams{ID: o.ID, SiteID: site, Now: s.deps.Clock()}); err != nil {
				return fmt.Errorf("orders: set order %d's site: %w", o.ID, err)
			}
		}
		row, err := txq.UpdateOrderHeader(ctx, updateOrderParams(o.ID, h, totalsOfPriced(priced), o.DeliveryStatus, o.InvoiceStatus, o.Version, s.deps.Clock()))
		if err != nil {
			return fmt.Errorf("orders: save order %d: %w", o.ID, err)
		}
		lines, err := writeLines(ctx, txq, ownerOrder, o.ID, priced)
		if err != nil {
			return err
		}
		warnings, err := documentWarnings(ctx, txq, row.CustomerID, lines)
		if err != nil {
			return err
		}
		out = orderView(row, lines, warnings, nil)
		return nil
	})
	// A refused line is named by the request's row, a kit's child by its
	// kit's (M145).
	return out, atInputs(err, priced)
}

// frozenHeader judges a confirmed order's header in body against base: the
// fields O26 allows are taken — the delivery date, the delivery address and
// the contact (snapshotted afresh from the customer's billing profile when
// they change), your reference and the internal note — and any other field
// that differs from the order's is refused.
func (s *server) frozenHeader(ctx context.Context, f *fieldErrors, base header, body gen.OrdersOrderUpdate) (header, error) {
	h := base
	frozen := func(field string, differs bool) {
		if differs {
			f.add(field, "This is frozen once the order is confirmed")
		}
	}
	frozen("customerId", body.CustomerId != base.CustomerID)
	frozen("currency", body.Currency != nil && *body.Currency != base.Currency)
	frozen("pricesIncludeVat", body.PricesIncludeVat != nil && *body.PricesIncludeVat != base.PricesIncludeVat)
	frozen("paymentTermsDays", body.PaymentTermsDays != nil && *body.PaymentTermsDays != base.PaymentTermsDays)
	frozen("discountVisible", body.DiscountVisible != nil && *body.DiscountVisible != base.DiscountVisible)
	frozen("language", body.Language != nil && string(*body.Language) != base.Language)
	frozen("introText", body.IntroText != nil && *body.IntroText != base.IntroText)
	frozen("closingText", body.ClosingText != nil && *body.ClosingText != base.ClosingText)
	frozen("projectId", body.ProjectId != nil && (base.ProjectID == nil || *body.ProjectId != *base.ProjectID))
	frozen("salespersonId", body.SalespersonId != nil && (base.SalespersonID == nil || *body.SalespersonId != *base.SalespersonID))
	if f.failed() {
		return h, nil
	}
	changesAddress := body.DeliveryAddressId != nil && (base.DeliveryAddressID == nil || *body.DeliveryAddressId != *base.DeliveryAddressID)
	changesContact := body.ContactId != nil && (base.ContactID == nil || *body.ContactId != *base.ContactID)
	if changesAddress || changesContact {
		in := headerInput{CustomerID: base.CustomerID, DeliveryAddressID: body.DeliveryAddressId, ContactID: body.ContactId}
		reads, err := s.readHeader(ctx, in, &base)
		if err != nil {
			return h, err
		}
		resolved := resolveHeader(f, in, &base, reads)
		if f.failed() {
			return h, nil
		}
		if changesAddress {
			h.DeliveryAddressID, h.DeliveryName = resolved.DeliveryAddressID, resolved.DeliveryName
			h.DeliveryAddressLine1, h.DeliveryAddressLine2 = resolved.DeliveryAddressLine1, resolved.DeliveryAddressLine2
			h.DeliveryPostalCode, h.DeliveryCity, h.DeliveryCountry = resolved.DeliveryPostalCode, resolved.DeliveryCity, resolved.DeliveryCountry
		}
		if changesContact {
			h.ContactID, h.ContactName, h.ContactEmail, h.ContactPhone = resolved.ContactID, resolved.ContactName, resolved.ContactEmail, resolved.ContactPhone
		}
	}
	if body.YourReference != nil {
		h.YourReference = trimmed(*body.YourReference)
		if len([]rune(h.YourReference)) > 100 {
			f.add("yourReference", "Your reference is at most 100 characters")
		}
	}
	if body.InternalNote != nil {
		h.InternalNote = *body.InternalNote
		if len([]rune(h.InternalNote)) > 4000 {
			f.add("internalNote", "A text is at most 4000 characters")
		}
	}
	if body.DeliveryDate != nil {
		h.DeliveryDate = dateIn(body.DeliveryDate)
	}
	return h, nil
}

// trimmed is s without surrounding space.
func trimmed(s string) string { return deref(textPtr(s)) }

// headerActions are what a confirmed save changed of the header.
func headerActions(from, to header) []changeAction {
	var out []changeAction
	text := func(p *string) *string {
		if p == nil {
			return ptr("")
		}
		return p
	}
	idText := func(p *int32) *string {
		if p == nil {
			return ptr("")
		}
		return ptr(fmt.Sprint(*p))
	}
	dateText := func(d pgtype.Date) *string {
		if !d.Valid {
			return ptr("")
		}
		return ptr(d.Time.Format("2006-01-02"))
	}
	if !equalInt32(from.DeliveryAddressID, to.DeliveryAddressID) {
		out = append(out, changeAction{Kind: actionDeliveryChanged, From: idText(from.DeliveryAddressID), To: idText(to.DeliveryAddressID)})
	}
	if from.DeliveryDate != to.DeliveryDate {
		out = append(out, changeAction{Kind: actionDeliveryChanged, From: dateText(from.DeliveryDate), To: dateText(to.DeliveryDate)})
	}
	if !equalInt32(from.ContactID, to.ContactID) {
		out = append(out, changeAction{Kind: actionContactChanged, From: text(from.ContactName), To: text(to.ContactName)})
	}
	if from.YourReference != to.YourReference {
		out = append(out, changeAction{Kind: actionReferenceChanged, From: &from.YourReference, To: &to.YourReference})
	}
	if from.InternalNote != to.InternalNote {
		out = append(out, changeAction{Kind: actionTextChanged})
	}
	return out
}

func equalInt32(a, b *int32) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func equalInt64(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func equalRat(a, b *big.Rat) bool {
	switch {
	case a == nil || b == nil:
		return a == nil && b == nil
	}
	return a.Cmp(b) == 0
}

// lineText is a line's quantity, price or discount as a change log value.
func lineText(v *big.Rat, places int) *string {
	if v == nil {
		return ptr("")
	}
	return ptr(v.FloatString(places))
}

// judgeConfirmedLines judges a confirmed order's new lines against its lines
// and its invoice allocations as they are under the lock (O26), and answers
// the change log's actions. A draft allocation counts as progress (B): a
// line's quantity goes no lower than the larger of delivered and invoiced +
// draft-allocated, plus cancelled; its price and discount do not change
// while a draft allocation holds it (line_in_draft_invoice); and a line an
// invoice allocation holds — draft, invoiced, or released with an invoice's
// stamp — is not removed (line_has_progress).
func judgeConfirmedLines(priced []pricedLine, current []store.OrdersLine, allocs lineAllocations) ([]changeAction, error) {
	old := byID(current)
	var actions []changeAction
	var failures []lineFailure
	kept := map[int64]bool{}
	for i, p := range priced {
		if p.id == nil {
			actions = append(actions, changeAction{Kind: actionLineAdded, To: ptr(deref(p.name))})
			continue
		}
		o, ok := old[*p.id]
		if !ok {
			// A line removed meanwhile: priced from a read before the lock.
			return nil, conflict(codeVersionConflict, "The order changed",
				"A line of the order was removed while it was being saved. Reload it and try again.")
		}
		if o.Kind != p.kind {
			return nil, lineKindChanged(o)
		}
		kept[o.ID] = true
		id := o.ID
		delivered, invoiced, cancelled := ratOrZero(o.QtyDelivered), ratOrZero(o.QtyInvoiced), ratOrZero(o.QtyCancelled)
		drafted := allocs.drafted(o.ID)
		progressed := delivered.Sign() > 0 || invoiced.Sign() > 0 || cancelled.Sign() > 0 || drafted.Sign() > 0
		if !equalInt32(o.VariantID, p.variantID) && progressed {
			return nil, conflict(codeLineHasProgress, "The line has progress",
				fmt.Sprintf("Line %d is delivered, invoiced, in a draft invoice or cancelled in part; its item cannot change. Add a new line instead.", o.Position))
		}
		if pricedKind(p.kind) && p.quantity != nil {
			floor := new(big.Rat).Set(delivered)
			if billed := new(big.Rat).Add(invoiced, drafted); billed.Cmp(floor) > 0 {
				floor.Set(billed)
			}
			floor.Add(floor, cancelled)
			if p.quantity.Cmp(floor) < 0 {
				failures = append(failures, lineFailure{index: i, code: codeQuantityBelowProgress,
					detail: fmt.Sprintf("Line %d cannot go below %s: what is delivered, invoiced or in a draft invoice, and cancelled.", o.Position, floor.FloatString(quantityPlaces))})
				continue
			}
		}
		oldQty, oldPrice, oldDiscount := ratOf(o.Quantity), ratOf(o.UnitPrice), ratOrZero(o.DiscountPercent)
		priceChanged := !equalRat(oldPrice, p.unitPrice)
		discountChanged := !equalRat(oldDiscount, orZero(p.discount))
		textChanged := o.Description != p.description || deref(o.Name) != deref(p.name)
		if (priceChanged || discountChanged || textChanged) && invoiced.Sign() > 0 {
			return nil, conflict(codeLineInvoiced, "The line is invoiced",
				fmt.Sprintf("Something of line %d is invoiced; its price, discount and text no longer change.", o.Position))
		}
		if (priceChanged || discountChanged) && drafted.Sign() > 0 {
			return nil, conflict(codeLineInDraftInvoice, "The line is in a draft invoice",
				fmt.Sprintf("Part of line %d is in a draft invoice; its price and discount do not change until the draft is issued or deleted, or its allocations released.", o.Position))
		}
		if !equalRat(oldQty, p.quantity) {
			actions = append(actions, changeAction{Kind: actionQuantityChanged, LineID: &id,
				From: lineText(oldQty, quantityPlaces), To: lineText(p.quantity, quantityPlaces)})
		}
		if priceChanged {
			actions = append(actions, changeAction{Kind: actionPriceChanged, LineID: &id,
				From: lineText(oldPrice, entryPlaces), To: lineText(p.unitPrice, entryPlaces)})
		}
		if discountChanged {
			actions = append(actions, changeAction{Kind: actionDiscountChanged, LineID: &id,
				From: lineText(oldDiscount, discountPlaces), To: lineText(orZero(p.discount), discountPlaces)})
		}
		if textChanged {
			actions = append(actions, changeAction{Kind: actionTextChanged, LineID: &id})
		}
	}
	if len(failures) > 0 {
		return nil, refuseLines(invalidOrderTitle, failures)
	}
	for _, o := range current {
		if kept[o.ID] {
			continue
		}
		if ratOrZero(o.QtyDelivered).Sign() > 0 || ratOrZero(o.QtyInvoiced).Sign() > 0 || ratOrZero(o.QtyCancelled).Sign() > 0 ||
			allocs[o.ID].held {
			return nil, conflict(codeLineHasProgress, "The line has progress",
				fmt.Sprintf("Line %d is delivered, invoiced, in a draft invoice or cancelled in part, and is kept.", o.Position))
		}
		id := o.ID
		actions = append(actions, changeAction{Kind: actionLineRemoved, LineID: &id, From: ptr(deref(o.Name))})
	}
	return actions, nil
}

// saveConfirmedOrder is a confirmed order's save (O26).
func (s *server) saveConfirmedOrder(ctx context.Context, cur store.OrdersOrder, curLines []store.OrdersLine, body gen.OrdersOrderUpdate) (gen.OrdersOrder, error) {
	if err := judgeUnlocked(cur); err != nil {
		return gen.OrdersOrder{}, err
	}
	var f fieldErrors
	base := headerOfOrder(cur)
	h, err := s.frozenHeader(ctx, &f, base, body)
	if err != nil {
		return gen.OrdersOrder{}, err
	}
	if f.failed() {
		return gen.OrdersOrder{}, refuseInvalid(invalidOrderTitle, f)
	}
	// Kept lines keep their frozen item, name, unit, VAT and price (O16,
	// keptFrozen) unless the save names a new price; a new line takes
	// today's list price.
	priced, err := s.priceOrderInputs(ctx, lineInputsOf(body.Lines), curLines, h, false, true)
	if err != nil {
		return gen.OrdersOrder{}, err
	}
	// A confirmed order's site changes to another site, never to none; an
	// absent site keeps it (O46).
	site := cur.SiteID
	if body.SiteId != nil && !equalInt64(body.SiteId, cur.SiteID) {
		if err := s.judgeSite(ctx, &f, "siteId", *body.SiteId); err != nil {
			return gen.OrdersOrder{}, err
		}
		if f.failed() {
			return gen.OrdersOrder{}, refuseInvalid(invalidOrderTitle, f)
		}
		site = body.SiteId
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return gen.OrdersOrder{}, err
	}
	// The day the ledger judges a lot's expiry on, read on the pool (rule 24).
	today, err := s.todayOf(ctx)
	if err != nil {
		return gen.OrdersOrder{}, err
	}
	// The variants of the lines kept, added and removed, all locked first;
	// only a new line's, or a line's whose item changed, must still exist.
	locking := slices.Clone(priced)
	for _, l := range pricedLinesOf(curLines) {
		l.frozen = true
		locking = append(locking, l)
	}
	var out gen.OrdersOrder
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidOrderTitle, locking); err != nil {
			return err
		}
		o, err := lockOrder(ctx, txq, cur.ID)
		if err != nil {
			return err
		}
		if err := judgeVersion(o, body.Version, body.ExpectedUpdatedAt); err != nil {
			return err
		}
		if err := judgeConfirmed(o); err != nil {
			return err
		}
		if err := judgeUnlocked(o); err != nil {
			return err
		}
		current, err := linesOf(ctx, txq, ownerOrder, o.ID)
		if err != nil {
			return err
		}
		allocations, err := orderAllocations(ctx, txq, o.ID)
		if err != nil {
			return err
		}
		actions, err := judgeConfirmedLines(priced, current, allocationsByLine(allocations))
		if err != nil {
			return err
		}
		if err := judgeDraftDelivered(ctx, txq, o, priced, current); err != nil {
			return err
		}
		siteChanged := !equalInt64(site, o.SiteID)
		settlePolicies(ctx, priced, byID(current), s.atSite(site), false)
		// Inventory off says nothing of a kept line's goods (O45): it keeps
		// how it is delivered, and so its reservation, unless its item
		// changed.
		if !s.stockKept() {
			old := byID(current)
			for i := range priced {
				p := &priced[i]
				if p.id == nil {
					continue
				}
				if was, ok := old[*p.id]; ok && equalInt32(was.VariantID, p.variantID) {
					p.deliveryMethod = was.DeliveryMethod
				}
			}
		}
		if err := judgePolicies(priced, current, allocationsByLine(allocations)); err != nil {
			return err
		}
		actions = append(headerActions(headerOfOrder(o), h), actions...)
		actions = append(actions, policyActions(priced, current)...)
		if siteChanged {
			has, err := txq.OrderHasDeliveries(ctx, o.ID)
			if err != nil {
				return fmt.Errorf("orders: read order %d's deliveries: %w", o.ID, err)
			}
			if has {
				return conflict(codeOrderHasDeliveries, "The order has deliveries",
					fmt.Sprintf("Order %d has a delivery; its site no longer changes.", o.Number))
			}
			actions = append(actions, changeAction{Kind: actionSiteChanged, From: idText64(o.SiteID), To: idText64(site)})
		}
		if len(actions) == 0 {
			warnings, err := documentWarnings(ctx, txq, o.CustomerID, current)
			if err != nil {
				return err
			}
			out = orderView(o, current, warnings, allocations)
			return nil
		}
		now := s.deps.Clock()
		if _, err := txq.UpdateOrderHeader(ctx, updateOrderParams(o.ID, h, totalsOfPriced(priced), o.DeliveryStatus, o.InvoiceStatus, o.Version, now)); err != nil {
			return fmt.Errorf("orders: save order %d: %w", o.ID, err)
		}
		// A removed line's released allocations that no invoice stamped are
		// forgotten with it; any other allocation kept it (judged above).
		for _, a := range actions {
			if a.Kind != actionLineRemoved {
				continue
			}
			if err := txq.DeleteForgottenLineAllocations(ctx, *a.LineID); err != nil {
				return fmt.Errorf("orders: forget removed line %d's released allocations: %w", *a.LineID, err)
			}
		}
		if _, err := writeLines(ctx, txq, ownerOrder, o.ID, priced); err != nil {
			return err
		}
		// The kept lines' buckets, as they were under the lock.
		old := byID(current)
		for _, p := range priced {
			if p.id == nil {
				continue
			}
			l := old[*p.id]
			if err := txq.SetLineBuckets(ctx, store.SetLineBucketsParams{
				ID: l.ID, QtyCancelled: l.QtyCancelled, QtyDelivered: l.QtyDelivered, QtyInvoiced: l.QtyInvoiced,
			}); err != nil {
				return fmt.Errorf("orders: keep line %d's progress: %w", l.ID, err)
			}
		}
		lines, err := linesOf(ctx, txq, ownerOrder, o.ID)
		if err != nil {
			return err
		}
		// The reservations follow the edit (O26, O46): a new site takes
		// them all over, at what is open now; otherwise each line's grows or
		// shrinks with its open quantity.
		if siteChanged {
			reserved, err := s.reservedByLine(ctx, tx, o.ID)
			if err != nil {
				return err
			}
			if err := s.releaseAll(ctx, tx, o.ID); err != nil {
				return err
			}
			if o, err = txq.SetOrderSite(ctx, store.SetOrderSiteParams{ID: o.ID, SiteID: site, Now: now}); err != nil {
				return fmt.Errorf("orders: set order %d's site: %w", o.ID, err)
			}
			if holdsReservations(reserved) {
				if _, err := s.reserveOpen(ctx, tx, o, lines, today, invalidOrderTitle); err != nil {
					return err
				}
			}
		} else if err := s.adjustReservations(ctx, tx, o, current, lines, today, invalidOrderTitle); err != nil {
			return err
		}
		row, err := s.bumpProgress(ctx, tx, txq, o, lines, o.Version+1)
		if err != nil {
			return err
		}
		if err := logChange(ctx, txq, row.ID, row.Version, by, now, actions); err != nil {
			return err
		}
		warnings, err := documentWarnings(ctx, txq, row.CustomerID, lines)
		if err != nil {
			return err
		}
		out = orderView(row, lines, warnings, allocations)
		return nil
	})
	if err != nil {
		// A refused line is named by the request's row, a kit's child by
		// its kit's (M145).
		return out, atInputs(err, priced)
	}
	return out, s.withStockFigures(ctx, &out)
}

// idText64 is an id as a change log value.
func idText64(p *int64) *string {
	if p == nil {
		return ptr("")
	}
	return ptr(fmt.Sprint(*p))
}

// policyActions are the invoice policies a confirmed save changed (O48).
func policyActions(priced []pricedLine, current []store.OrdersLine) []changeAction {
	old := byID(current)
	var out []changeAction
	for _, p := range priced {
		if p.id == nil {
			continue
		}
		if o, ok := old[*p.id]; ok && o.InvoicePolicy != p.invoicePolicy {
			id := o.ID
			out = append(out, changeAction{Kind: actionPolicyChanged, LineID: &id, From: ptr(o.InvoicePolicy), To: ptr(p.invoicePolicy)})
		}
	}
	return out
}

// judgeDraftDelivered refuses a confirmed order's save that removes a line a
// draft delivery names, or changes its item (line_has_progress, O47): the
// draft is saved without it, or cancelled, first.
func judgeDraftDelivered(ctx context.Context, txq *store.Queries, o store.OrdersOrder, priced []pricedLine, current []store.OrdersLine) error {
	ids, err := txq.LinesInDraftDeliveries(ctx, o.ID)
	if err != nil {
		return fmt.Errorf("orders: read order %d's draft deliveries: %w", o.ID, err)
	}
	if len(ids) == 0 {
		return nil
	}
	kept := map[int64]pricedLine{}
	for _, p := range priced {
		if p.id != nil {
			kept[*p.id] = p
		}
	}
	old := byID(current)
	for _, id := range ids {
		p, ok := kept[id]
		if ok && equalInt32(p.variantID, old[id].VariantID) {
			continue
		}
		return conflict(codeLineHasProgress, "The line is in a draft delivery",
			fmt.Sprintf("Line %d is in a draft delivery; save the delivery without it, or cancel it, first.", old[id].Position))
	}
	return nil
}
