package orders

// A subscription's save (orders design O42, O44). A draft's save is an order
// draft's: the header resolved again over the customer's billing profile, the
// plan and the lines replaced whole. An activated subscription — active,
// paused or ended — takes its header's texts, references, contact, address,
// our side, payment terms, notice and final period, and its lines as they
// are to be from a period start on (effective from): a line changed or removed ends the day
// before, a line changed or added starts on it, and a line unchanged is kept
// as it is. The period must have no live allocation, and nor may any after
// it (change_in_billed_period): changes take effect from the next unbilled
// period, never inside a billed one, and are never pro-rated.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// subscriptionHeaderInput reads a save's header fields.
func subscriptionHeaderInput(b gen.OrdersSubscriptionUpdate) headerInput {
	in := headerInput{
		CustomerID: b.CustomerId, ContactID: b.ContactId, DeliveryAddressID: b.DeliveryAddressId, Currency: b.Currency,
		DiscountVisible: b.DiscountVisible, IntroText: b.IntroText, ClosingText: b.ClosingText,
		InternalNote: b.InternalNote, YourReference: b.YourReference, PaymentTermsDays: b.PaymentTermsDays,
		PricesIncludeVat: b.PricesIncludeVat, ProjectID: b.ProjectId, SalespersonID: b.SalespersonId,
	}
	if b.Language != nil {
		l := string(*b.Language)
		in.Language = &l
	}
	return in
}

// updateSubscriptionParams are a header, a plan and a full period's totals as
// UpdateSubscriptionHeader writes them.
func updateSubscriptionParams(id int64, h header, p plan, t docTotals, version int32, now time.Time) store.UpdateSubscriptionHeaderParams {
	hp := insertQuoteParams(h, t)
	return store.UpdateSubscriptionHeaderParams{
		ID: id, Version: version, Now: now,
		BillingInterval: p.interval, Billing: p.billing, StartDate: pgDate(&p.start), EndDate: pgDate(p.end),
		NoticeMonths: p.notice, InvoiceDay: p.invoiceDay, FinalPeriod: p.finalPeriod,
		CustomerID: hp.CustomerID, CustomerNumber: hp.CustomerNumber, CustomerName: hp.CustomerName, CustomerType: hp.CustomerType,
		CustomerOrganisationNumber: hp.CustomerOrganisationNumber, CustomerForeignID: hp.CustomerForeignID,
		CustomerCountry: hp.CustomerCountry, InvoiceAddressLine1: hp.InvoiceAddressLine1, InvoiceAddressLine2: hp.InvoiceAddressLine2,
		InvoicePostalCode: hp.InvoicePostalCode, InvoiceCity: hp.InvoiceCity, InvoiceCountry: hp.InvoiceCountry,
		DeliveryAddressID: hp.DeliveryAddressID, DeliveryName: hp.DeliveryName, DeliveryAddressLine1: hp.DeliveryAddressLine1,
		DeliveryAddressLine2: hp.DeliveryAddressLine2, DeliveryPostalCode: hp.DeliveryPostalCode, DeliveryCity: hp.DeliveryCity,
		DeliveryCountry: hp.DeliveryCountry, ContactID: hp.ContactID, ContactName: hp.ContactName, ContactEmail: hp.ContactEmail,
		ContactPhone: hp.ContactPhone, CustomerInvoiceEmail: hp.CustomerInvoiceEmail, YourReference: hp.YourReference,
		SalespersonID: hp.SalespersonID, SalespersonDisplay: hp.SalespersonDisplay, ProjectID: hp.ProjectID, Currency: hp.Currency,
		PricesIncludeVat: hp.PricesIncludeVat, PaymentTermsDays: hp.PaymentTermsDays, DiscountVisible: hp.DiscountVisible,
		Language: hp.Language, IntroText: hp.IntroText, ClosingText: hp.ClosingText, InternalNote: hp.InternalNote,
		NetTotal: hp.NetTotal, VatTotal: hp.VatTotal, GrossTotal: hp.GrossTotal,
	}
}

// UpdateSubscription Save a subscription
// (PUT /api/v1/orders/subscriptions/{id})
func (s *server) UpdateSubscription(ctx context.Context, req gen.UpdateSubscriptionRequestObject) (gen.UpdateSubscriptionResponseObject, error) {
	resp := answers[gen.UpdateSubscriptionResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.UpdateSubscriptionResponseObject {
			return gen.UpdateSubscription400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateSubscriptionResponseObject { return gen.UpdateSubscription404Response{} },
		conflict: func(p gen.OrdersProblem) gen.UpdateSubscriptionResponseObject {
			return gen.UpdateSubscription409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.UpdateSubscriptionResponseObject {
			return gen.UpdateSubscription503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, subscriptionUpdateSchema)
	if f.failed() {
		return gen.UpdateSubscription400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, f)), nil
	}
	body := *req.Body
	today, err := s.todayOf(ctx)
	if err != nil {
		return answer(err, resp)
	}
	q := store.New(s.deps.Pool)
	cur, err := q.SubscriptionByID(ctx, req.Id)
	if noRows(err) {
		return gen.UpdateSubscription404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read subscription %d: %w", req.Id, err)
	}
	if err := judgeSubscriptionVersion(cur, body.Version, body.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	sch, err := readSchedule(ctx, q, cur)
	if err != nil {
		return nil, err
	}
	var out gen.OrdersSubscription
	switch cur.Status {
	case subscriptionDraft:
		out, err = s.saveDraftSubscription(ctx, sch, body, today)
	case subscriptionActive, subscriptionPaused, subscriptionEnded:
		out, err = s.changeSubscription(ctx, sch, body, today)
	default:
		err = conflict(codeSubscriptionNotEditable, "The subscription is cancelled",
			fmt.Sprintf("Subscription %d is cancelled; a cancelled subscription is not changed.", cur.Number))
	}
	if err != nil {
		return answer(err, resp)
	}
	return gen.UpdateSubscription200JSONResponse(out), nil
}

// planOfUpdate is a save's plan over the subscription's: each field the body
// leaves out keeps the subscription's, its final period too.
func planOfUpdate(f *fieldErrors, cur store.OrdersSubscription, b gen.OrdersSubscriptionUpdate) plan {
	interval, billing, start := cur.BillingInterval, cur.Billing, cur.StartDate.Time
	if b.Interval != nil {
		interval = string(*b.Interval)
	}
	if b.Billing != nil {
		billing = string(*b.Billing)
	}
	if b.StartDate != nil {
		start = b.StartDate.Time
	}
	end := b.EndDate
	if end == nil && cur.EndDate.Valid {
		end = dateOut(cur.EndDate)
	}
	notice, day := b.NoticeMonths, b.InvoiceDay
	if notice == nil {
		n := int32(cur.NoticeMonths)
		notice = &n
	}
	if day == nil && cur.InvoiceDay != nil {
		d := int32(*cur.InvoiceDay)
		day = &d
	}
	final := b.FinalPeriod
	if final == nil {
		fp := gen.OrdersFinalPeriod(cur.FinalPeriod)
		final = &fp
	}
	return readPlan(f, interval, billing, start, end, notice, day, final)
}

// saveDraftSubscription is a draft's save: every read before the
// transaction, which locks the variants first, then the subscription, judged
// a draft at the version again under it.
func (s *server) saveDraftSubscription(ctx context.Context, sch schedule, body gen.OrdersSubscriptionUpdate, today time.Time) (gen.OrdersSubscription, error) {
	cur := sch.sub
	var f fieldErrors
	if body.EffectiveFrom != nil {
		f.add("effectiveFrom", "A draft's lines take effect from its start; effectiveFrom is for an active subscription")
	}
	base := headerOfSubscription(cur)
	in := subscriptionHeaderInput(body)
	reads, err := s.readHeader(ctx, in, &base)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	h := resolveHeader(&f, in, &base, reads)
	if in.CustomerID != cur.CustomerID {
		customerUsable(&f, reads.profile)
	}
	p := planOfUpdate(&f, cur, body)
	if f.failed() {
		return gen.OrdersSubscription{}, refuseInvalid(invalidSubscriptionTitle, f)
	}
	reprice, err := s.repriceKept(ctx, cur.Currency, h.Currency, cur.UpdatedAt)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	priced, err := s.priceSubscriptionInputs(ctx, lineInputsOf(body.Lines), sch.lines, h, reprice, false)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	var out gen.OrdersSubscription
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidSubscriptionTitle, priced); err != nil {
			return err
		}
		sub, err := lockSubscription(ctx, txq, cur.ID)
		if err != nil {
			return err
		}
		if err := judgeSubscriptionVersion(sub, body.Version, body.ExpectedUpdatedAt); err != nil {
			return err
		}
		if sub.Status != subscriptionDraft {
			return conflict(codeVersionConflict, "The subscription changed",
				fmt.Sprintf("Subscription %d was activated while it was being saved. Reload it and try again.", sub.Number))
		}
		row, err := txq.UpdateSubscriptionHeader(ctx, updateSubscriptionParams(sub.ID, h, p, totalsOfPriced(priced), sub.Version, s.deps.Clock()))
		if err != nil {
			return fmt.Errorf("orders: save subscription %d: %w", sub.ID, err)
		}
		if _, err := writeLines(ctx, txq, ownerSubscription, sub.ID, priced); err != nil {
			return err
		}
		out, err = viewOn(ctx, txq, row, today)
		return err
	})
	return out, err
}

// activatedHeader is an activated subscription's header after a save: the
// body resolved over base, with the customer, its snapshot, the currency and
// whether prices include VAT kept as they are — they are frozen once the
// subscription is active (subscription_not_editable).
func (s *server) activatedHeader(ctx context.Context, f *fieldErrors, base header, body gen.OrdersSubscriptionUpdate) (header, error) {
	frozen := func(field string, differs bool) {
		if differs {
			f.addCoded(field, codeSubscriptionNotEditable, "This is frozen once the subscription is active")
		}
	}
	frozen("customerId", body.CustomerId != base.CustomerID)
	frozen("currency", body.Currency != nil && *body.Currency != base.Currency)
	frozen("pricesIncludeVat", body.PricesIncludeVat != nil && *body.PricesIncludeVat != base.PricesIncludeVat)
	if f.failed() {
		return base, nil
	}
	in := subscriptionHeaderInput(body)
	reads, err := s.readHeader(ctx, in, &base)
	if err != nil {
		return base, err
	}
	h := resolveHeader(f, in, &base, reads)
	h.CustomerNumber, h.CustomerName, h.CustomerType = base.CustomerNumber, base.CustomerName, base.CustomerType
	h.CustomerOrganisationNumber, h.CustomerForeignID, h.CustomerCountry = base.CustomerOrganisationNumber, base.CustomerForeignID, base.CustomerCountry
	h.InvoiceAddressLine1, h.InvoiceAddressLine2, h.InvoicePostalCode = base.InvoiceAddressLine1, base.InvoiceAddressLine2, base.InvoicePostalCode
	h.InvoiceCity, h.InvoiceCountry, h.CustomerInvoiceEmail = base.InvoiceCity, base.InvoiceCountry, base.CustomerInvoiceEmail
	h.Currency, h.PricesIncludeVat = base.Currency, base.PricesIncludeVat
	return h, nil
}

// judgeEffectiveFrom judges a change's first period start against the
// schedule (O44): a period start of the subscription (not_a_period_start),
// with no live allocation in it or after it (change_in_billed_period).
func judgeEffectiveFrom(sch schedule, from time.Time) error {
	if !sch.cal.isPeriodStart(from) {
		return refuseField(invalidSubscriptionTitle, "effectiveFrom", codeNotAPeriodStart,
			fmt.Sprintf("%s is not the first day of one of the subscription's periods", from.Format(time.DateOnly)))
	}
	if open := sch.firstOpenStart(); from.Before(open) {
		return conflict(codeChangeInBilledPeriod, "The period is billed",
			fmt.Sprintf("A period from %s on is on an invoice or a draft invoice; a change takes effect from %s at the earliest. Credit the period first to change it.",
				from.Format(time.DateOnly), open.Format(time.DateOnly)))
	}
	return nil
}

// changeSubscription is an activated subscription's save (O44).
func (s *server) changeSubscription(ctx context.Context, sch schedule, body gen.OrdersSubscriptionUpdate, today time.Time) (gen.OrdersSubscription, error) {
	cur := sch.sub
	var f fieldErrors
	frozenPlan := func(field string, differs bool) {
		if differs {
			f.addCoded(field, codeSubscriptionNotEditable, "This is fixed once the subscription is active; end it with End")
		}
	}
	frozenPlan("interval", body.Interval != nil && string(*body.Interval) != cur.BillingInterval)
	frozenPlan("billing", body.Billing != nil && string(*body.Billing) != cur.Billing)
	frozenPlan("startDate", body.StartDate != nil && !civil(body.StartDate.Time).Equal(civil(cur.StartDate.Time)))
	frozenPlan("endDate", body.EndDate != nil && !equalDate(cur.EndDate, ptr(civil(body.EndDate.Time))))
	base := headerOfSubscription(cur)
	h, err := s.activatedHeader(ctx, &f, base, body)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	p := planOfUpdate(&f, cur, body)
	if f.failed() {
		return gen.OrdersSubscription{}, refuseInvalid(invalidSubscriptionTitle, f)
	}
	from := sch.firstOpenStart()
	if body.EffectiveFrom != nil {
		from = civil(body.EffectiveFrom.Time)
	}
	current := currentLines(sch.lines)
	priced, err := s.priceSubscriptionInputs(ctx, lineInputsOf(body.Lines), current, h, false, true)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	locking := append([]pricedLine{}, priced...)
	for _, l := range pricedLinesOf(current) {
		l.frozen = true
		locking = append(locking, l)
	}
	var out gen.OrdersSubscription
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidSubscriptionTitle, locking); err != nil {
			return err
		}
		sub, err := lockSubscription(ctx, txq, cur.ID)
		if err != nil {
			return err
		}
		if err := judgeSubscriptionVersion(sub, body.Version, body.ExpectedUpdatedAt); err != nil {
			return err
		}
		locked, err := readSchedule(ctx, txq, sub)
		if err != nil {
			return err
		}
		lineActions, err := applyLineChanges(ctx, txq, locked, priced, from)
		if err != nil {
			return err
		}
		actions := headerActions(base, h)
		if p.notice != cur.NoticeMonths {
			actions = append(actions, changeAction{Kind: actionNoticeChanged, From: ptr(fmt.Sprint(cur.NoticeMonths)), To: ptr(fmt.Sprint(p.notice))})
		}
		if p.finalPeriod != sub.FinalPeriod {
			if sub.EndDate.Valid {
				if err := judgeFinalPeriod(locked, civil(sub.EndDate.Time), p.finalPeriod); err != nil {
					return err
				}
			}
			actions = append(actions, changeAction{Kind: actionFinalPeriod, From: ptr(sub.FinalPeriod), To: ptr(p.finalPeriod)})
		}
		if len(lineActions) > 0 {
			actions = append(append(actions, changeAction{Kind: actionEffectiveFrom, To: ptr(from.Format(time.DateOnly))}), lineActions...)
		}
		now := s.deps.Clock()
		version := sub.Version + 1
		afterLines, err := txq.SubscriptionLinesOf(ctx, []int64{sub.ID})
		if err != nil {
			return fmt.Errorf("orders: read subscription %d's lines: %w", sub.ID, err)
		}
		row, err := txq.UpdateSubscriptionHeader(ctx, updateSubscriptionParams(sub.ID, h, p, totalsOfRows(currentLines(afterLines)), version, now))
		if err != nil {
			return fmt.Errorf("orders: save subscription %d: %w", sub.ID, err)
		}
		if err := logSubscriptionChange(ctx, txq, sub.ID, version, by, now, actions); err != nil {
			return err
		}
		if err := events.Publish(ctx, tx, moduleName, contracts.SubscriptionChanged{
			SubscriptionID: row.ID, Number: row.Number, Version: row.Version, CustomerID: row.CustomerID, Status: row.Status, ChangedAt: now,
		}, now); err != nil {
			return err
		}
		out, err = viewOn(ctx, txq, row, today)
		return err
	})
	return out, err
}

// applyLineChanges writes an activated subscription's lines from from on, on
// the caller's transaction, which holds the subscription (O44), and answers
// the change log's actions. from is judged against the schedule as it stands
// under the lock, once a line is to change: a save that changes no line —
// an ended subscription's texts, say — has no period to judge. A kept line unchanged keeps its row and takes its new
// place; a line changed ends the day before from and a new row of its chain
// starts on it in its place, with what it was valid to; a line removed ends;
// a line added starts on from. A changed or removed line's other versions
// still in force on or after from — an earlier version a scheduled change
// ends later than from — end the day before from too, so a change made from
// a date before one already scheduled replaces it, and at any date exactly
// one version of a line is in force.
func applyLineChanges(ctx context.Context, txq *store.Queries, sch schedule, priced []pricedLine, from time.Time) ([]changeAction, error) {
	judged := false
	judge := func() error {
		if judged {
			return nil
		}
		judged = true
		return judgeEffectiveFrom(sch, from)
	}
	current := currentLines(sch.lines)
	old := byID(current)
	kept := map[int64]bool{}
	var actions []changeAction
	end := func(o store.OrdersLine) error {
		if err := judge(); err != nil {
			return err
		}
		if err := endLine(ctx, txq, o, from); err != nil {
			return err
		}
		for _, v := range sch.lines {
			if v.ID != o.ID && chainOf(v) == chainOf(o) && scheduledPast(v, from) {
				if err := endLine(ctx, txq, v, from); err != nil {
					return err
				}
			}
		}
		return nil
	}
	insert := func(p pricedLine, position int32, validTo pgtype.Date, chain *int64) error {
		if err := judge(); err != nil {
			return err
		}
		p.id = nil
		p.validFrom, p.validTo, p.chainID = pgDate(&from), validTo, chain
		if _, err := insertLine(ctx, txq, ownerSubscription, sch.sub.ID, position, p, nil); err != nil {
			return fmt.Errorf("orders: write subscription %d's line: %w", sch.sub.ID, err)
		}
		return nil
	}
	for i, p := range priced {
		position := int32(i + 1)
		if p.id == nil {
			if err := insert(p, position, pgtype.Date{}, nil); err != nil {
				return nil, err
			}
			actions = append(actions, changeAction{Kind: actionLineAdded, To: ptr(lineName(p))})
			continue
		}
		o, ok := old[*p.id]
		if !ok {
			return nil, conflict(codeVersionConflict, "The subscription changed",
				"A line of the subscription changed while it was being saved. Reload it and try again.")
		}
		kept[o.ID] = true
		id := o.ID
		changes := lineChanges(o, p)
		if len(changes) == 0 {
			if o.Position != position {
				if err := txq.SetSubscriptionLinePosition(ctx, store.SetSubscriptionLinePositionParams{ID: o.ID, Position: position}); err != nil {
					return nil, fmt.Errorf("orders: place subscription line %d: %w", o.ID, err)
				}
			}
			continue
		}
		if err := end(o); err != nil {
			return nil, err
		}
		chain := chainOf(o)
		if err := insert(p, position, o.ValidTo, &chain); err != nil {
			return nil, err
		}
		for _, c := range changes {
			c.LineID = &id
			actions = append(actions, c)
		}
	}
	for _, o := range current {
		if kept[o.ID] {
			continue
		}
		if err := end(o); err != nil {
			return nil, err
		}
		id := o.ID
		actions = append(actions, changeAction{Kind: actionLineRemoved, LineID: &id, From: ptr(lineName(pricedLineOf(o)))})
	}
	return actions, nil
}

// chainOf is the chain a subscription line's version belongs to: the id of
// its first version (O44).
func chainOf(l store.OrdersLine) int64 {
	if l.ChainID != nil {
		return *l.ChainID
	}
	return l.ID
}

// scheduledPast reports whether a dated line is still in force on or after
// from: it ends on or after from and is not a version that never took
// effect (valid_to before valid_from).
func scheduledPast(l store.OrdersLine, from time.Time) bool {
	if !l.ValidTo.Valid || civil(l.ValidTo.Time).Before(from) {
		return false
	}
	return !l.ValidFrom.Valid || !civil(l.ValidTo.Time).Before(civil(l.ValidFrom.Time))
}

// lineName is a line's name for the change log: its catalog name, else its
// description.
func lineName(p pricedLine) string {
	if n := deref(p.name); n != "" {
		return n
	}
	return p.description
}

// lineChanges are what a save changes of a kept line: its quantity, price,
// discount and text, and its item; none when it is unchanged.
func lineChanges(o store.OrdersLine, p pricedLine) []changeAction {
	var out []changeAction
	if !equalInt32(o.VariantID, p.variantID) || o.Unit != p.unit || !equalInt32(o.VatCodeID, p.vatCodeID) {
		out = append(out, changeAction{Kind: actionTextChanged, From: ptr(deref(o.Name)), To: ptr(lineName(p))})
	}
	if q := ratOf(o.Quantity); !equalRat(q, p.quantity) {
		out = append(out, changeAction{Kind: actionQuantityChanged, From: lineText(q, quantityPlaces), To: lineText(p.quantity, quantityPlaces)})
	}
	if price := ratOf(o.UnitPrice); !equalRat(price, p.unitPrice) {
		out = append(out, changeAction{Kind: actionPriceChanged, From: lineText(price, entryPlaces), To: lineText(p.unitPrice, entryPlaces)})
	}
	if d := ratOrZero(o.DiscountPercent); !equalRat(d, orZero(p.discount)) {
		out = append(out, changeAction{Kind: actionDiscountChanged, From: lineText(d, discountPlaces), To: lineText(orZero(p.discount), discountPlaces)})
	}
	if o.Description != p.description || deref(o.Name) != deref(p.name) {
		out = append(out, changeAction{Kind: actionTextChanged})
	}
	return out
}

// endLine ends a dated line before from (O44): the day before, when it was
// in force before from; otherwise it never took effect, and goes — or, when
// an allocation once named it, is kept with an empty validity, the day
// before it would have started.
func endLine(ctx context.Context, txq *store.Queries, l store.OrdersLine, from time.Time) error {
	if !l.ValidFrom.Valid || civil(l.ValidFrom.Time).Before(from) {
		before := from.AddDate(0, 0, -1)
		if err := txq.CloseSubscriptionLine(ctx, store.CloseSubscriptionLineParams{ID: l.ID, ValidTo: pgDate(&before)}); err != nil {
			return fmt.Errorf("orders: end subscription line %d: %w", l.ID, err)
		}
		return nil
	}
	held, err := txq.LineHasAllocations(ctx, l.ID)
	if err != nil {
		return fmt.Errorf("orders: read subscription line %d's allocations: %w", l.ID, err)
	}
	if held {
		before := civil(l.ValidFrom.Time).AddDate(0, 0, -1)
		if err := txq.CloseSubscriptionLine(ctx, store.CloseSubscriptionLineParams{ID: l.ID, ValidTo: pgDate(&before)}); err != nil {
			return fmt.Errorf("orders: end subscription line %d: %w", l.ID, err)
		}
		return nil
	}
	if _, err := txq.DeleteLineByID(ctx, l.ID); err != nil {
		return fmt.Errorf("orders: remove subscription line %d: %w", l.ID, err)
	}
	return nil
}
