package orders

// A subscription (abonnement, orders design O42–O44): its own aggregate,
// services only in this phase, billed per period through the allocations an
// order's lines are billed by (O43). This file reads one, lists them, creates
// one — by hand, or by accepting a quote as one (accept.go) — and answers it
// with its periods; subscription_edit.go saves one, and
// subscription_lifecycle.go activates, pauses, resumes, ends and cancels one.
// The period calendar is subscription_calendar.go's, the judgement of what is
// due and billed subscription_schedule.go's.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The subscription's refusal codes (O42–O44).
const (
	codeSubscriptionGoodsNotAllowed = "subscription_goods_not_allowed"
	codeSubscriptionNotDraft        = "subscription_not_draft"
	codeSubscriptionNotActive       = "subscription_not_active"
	codeSubscriptionNotPaused       = "subscription_not_paused"
	codeSubscriptionNotEditable     = "subscription_not_editable"
	codeSubscriptionNotCancellable  = "subscription_not_cancellable"
	codeSubscriptionBilled          = "subscription_billed"
	codeSubscriptionEmpty           = "subscription_empty"
	codeChangeInBilledPeriod        = "change_in_billed_period"
	codeEndBeforeBilled             = "end_before_billed"
	codeNotAPeriodStart             = "not_a_period_start"
	codeFinalPeriodBilled           = "final_period_billed"
)

// How a subscription bills the period its end falls in (owner, 2026-11-06).
const (
	finalPeriodFull     = "full"
	finalPeriodProrated = "prorated"
)

// invalidSubscriptionTitle is the title of a subscription's 400s.
const invalidSubscriptionTitle = "Invalid subscription"

// seriesSubscription is the subscription series (O12, O42). It has no
// setting of its own in this phase: it starts at 1.
const seriesSubscription = "subscription"

// The typed actions a subscription's change log adds to an order's (O44).
const (
	actionActivated     = "activated"
	actionPaused        = "paused"
	actionResumed       = "resumed"
	actionEnded         = "ended"
	actionCancelled     = "cancelled"
	actionEffectiveFrom = "effective_from"
	actionNoticeChanged = "notice_changed"
	// actionFinalPeriod is the final period chosen: in the plan, and at End.
	actionFinalPeriod = "final_period"
)

// productTypeService is the catalog's type of a variant a subscription may
// sell (O42).
const productTypeService = "Service"

// plan is a subscription's plan as a create, a save or an accept hands it in
// (O42), judged.
type plan struct {
	interval, billing string
	start             time.Time
	end               *time.Time
	notice            int16
	invoiceDay        *int16
	// finalPeriod is full or prorated; "" on a create that leaves it to the
	// settings' default.
	finalPeriod string
}

// readPlan judges a plan's fields into f: an interval and a billing of the
// four and two, an end on or after the start, a notice of 0 to 12 months, an
// invoice day of 1 to 28 and a final period full or prorated.
func readPlan(f *fieldErrors, interval, billing string, start time.Time, end *openapi_types.Date, notice, invoiceDay *int32,
	final *gen.OrdersFinalPeriod) plan {
	p := plan{interval: interval, billing: billing, start: civil(start)}
	if final != nil {
		if !final.Valid() {
			f.add("finalPeriod", "The final period is full or prorated")
		}
		p.finalPeriod = string(*final)
	}
	if intervalMonths(interval) == 0 {
		f.add("interval", "The interval is monthly, quarterly, half_yearly or yearly")
	}
	if billing != billingInAdvance && billing != billingInArrears {
		f.add("billing", "Billing is in_advance or in_arrears")
	}
	if end != nil {
		e := civil(end.Time)
		p.end = &e
		if e.Before(p.start) {
			f.add("endDate", "The end is on or after the start")
		}
	}
	if notice != nil {
		if *notice < 0 || *notice > 12 {
			f.add("noticeMonths", "The notice is 0 to 12 months")
		}
		p.notice = int16(*notice)
	}
	if invoiceDay != nil {
		if *invoiceDay < 1 || *invoiceDay > 28 {
			f.add("invoiceDay", "The invoice day is 1 to 28")
		}
		d := int16(*invoiceDay)
		p.invoiceDay = &d
	}
	return p
}

// headerOfSubscription is a stored subscription's header; it has no
// delivery date.
func headerOfSubscription(s store.OrdersSubscription) header {
	return header{
		CustomerID: s.CustomerID, CustomerNumber: s.CustomerNumber, CustomerName: s.CustomerName, CustomerType: s.CustomerType,
		CustomerOrganisationNumber: s.CustomerOrganisationNumber, CustomerForeignID: s.CustomerForeignID,
		CustomerCountry: s.CustomerCountry, InvoiceAddressLine1: s.InvoiceAddressLine1, InvoiceAddressLine2: s.InvoiceAddressLine2,
		InvoicePostalCode: s.InvoicePostalCode, InvoiceCity: s.InvoiceCity, InvoiceCountry: s.InvoiceCountry,
		DeliveryAddressID: s.DeliveryAddressID, DeliveryName: s.DeliveryName, DeliveryAddressLine1: s.DeliveryAddressLine1,
		DeliveryAddressLine2: s.DeliveryAddressLine2, DeliveryPostalCode: s.DeliveryPostalCode, DeliveryCity: s.DeliveryCity,
		DeliveryCountry: s.DeliveryCountry, ContactID: s.ContactID, ContactName: s.ContactName, ContactEmail: s.ContactEmail,
		ContactPhone: s.ContactPhone, CustomerInvoiceEmail: s.CustomerInvoiceEmail, YourReference: s.YourReference,
		SalespersonID: s.SalespersonID, SalespersonDisplay: s.SalespersonDisplay, ProjectID: s.ProjectID, Currency: s.Currency,
		PricesIncludeVat: s.PricesIncludeVat, PaymentTermsDays: s.PaymentTermsDays, DiscountVisible: s.DiscountVisible,
		Language: s.Language, IntroText: s.IntroText, ClosingText: s.ClosingText, InternalNote: s.InternalNote,
	}
}

// pgDate is a day as a date column, NULL for nil.
func pgDate(d *time.Time) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: civil(*d), Valid: true}
}

// equalDate reports whether a date column holds d (NULL for nil).
func equalDate(col pgtype.Date, d *time.Time) bool {
	if !col.Valid || d == nil {
		return !col.Valid && d == nil
	}
	return civil(col.Time).Equal(civil(*d))
}

// wireDate is a day as the API's date.
func wireDate(d time.Time) openapi_types.Date { return openapi_types.Date{Time: civil(d)} }

// readSchedule reads a subscription's lines, allocations and skips on q and
// answers its schedule.
func readSchedule(ctx context.Context, q *store.Queries, sub store.OrdersSubscription) (schedule, error) {
	lines, err := q.SubscriptionLinesOf(ctx, []int64{sub.ID})
	if err != nil {
		return schedule{}, fmt.Errorf("orders: read subscription %d's lines: %w", sub.ID, err)
	}
	allocations, err := q.SubscriptionAllocations(ctx, &sub.ID)
	if err != nil {
		return schedule{}, fmt.Errorf("orders: read subscription %d's allocations: %w", sub.ID, err)
	}
	skips, err := q.SubscriptionSkips(ctx, sub.ID)
	if err != nil {
		return schedule{}, fmt.Errorf("orders: read subscription %d's skipped periods: %w", sub.ID, err)
	}
	return newSchedule(sub, lines, allocations, skips), nil
}

// currentLines is the lines in force from the latest change on (valid_to
// NULL), in position order: what the editor shows and a full period's totals
// are of.
func currentLines(lines []store.OrdersLine) []store.OrdersLine {
	var out []store.OrdersLine
	for _, l := range lines {
		if !l.ValidTo.Valid {
			out = append(out, l)
		}
	}
	slices.SortStableFunc(out, func(a, b store.OrdersLine) int { return int(a.Position - b.Position) })
	return out
}

// earlierLines is the dated lines a change ended, by position then validity.
func earlierLines(lines []store.OrdersLine) []store.OrdersLine {
	var out []store.OrdersLine
	for _, l := range lines {
		if l.ValidTo.Valid {
			out = append(out, l)
		}
	}
	return out
}

// subscriptionLinesOut are a subscription's lines as the API answers them,
// with their validity.
func subscriptionLinesOut(lines []store.OrdersLine, pricesIncludeVat bool) []gen.OrdersLine {
	out := make([]gen.OrdersLine, 0, len(lines))
	for _, l := range lines {
		o := lineOut(l, pricesIncludeVat)
		o.ValidFrom, o.ValidTo = dateOut(l.ValidFrom), dateOut(l.ValidTo)
		out = append(out, o)
	}
	return out
}

// todayOf is today in the business zone, read before any transaction.
func (s *server) todayOf(ctx context.Context) (time.Time, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return dayOf(s.deps.Clock(), zone), nil
}

// subscriptionView is a subscription as the API answers it (O42, O43): its
// header and plan, its lines in force from the latest change on and its
// earlier ones, and its periods as they stand on today.
func subscriptionView(sch schedule, today time.Time) gen.OrdersSubscription {
	sub := sch.sub
	h := headerOfSubscription(sub)
	current := currentLines(sch.lines)
	out := gen.OrdersSubscription{
		Id: sub.ID, Number: sub.Number, Status: gen.OrdersSubscriptionStatus(sub.Status),
		Interval: gen.OrdersSubscriptionInterval(sub.BillingInterval), Billing: gen.OrdersSubscriptionBilling(sub.Billing),
		StartDate: wireDate(sub.StartDate.Time), EndDate: dateOut(sub.EndDate), NoticeMonths: int32(sub.NoticeMonths),
		FinalPeriod: gen.OrdersFinalPeriod(sub.FinalPeriod),
		PausedFrom:  dateOut(sub.PausedFrom), BilledThrough: dateOut(sub.BilledThrough), Version: sub.Version,
		NextChangeFrom: wireDate(sch.firstOpenStart()),
		Customer:       snapshotOut(h), YourReference: sub.YourReference, Currency: sub.Currency,
		PricesIncludeVat: sub.PricesIncludeVat, PaymentTermsDays: sub.PaymentTermsDays, DiscountVisible: sub.DiscountVisible,
		Language: gen.OrdersSubscriptionLanguage(sub.Language), IntroText: sub.IntroText, ClosingText: sub.ClosingText,
		InternalNote: sub.InternalNote,
		Origin: gen.OrdersOrigin{
			Channel: sub.Channel, SourceModule: sub.SourceModule, SourceKind: sub.SourceKind, SourceId: sub.SourceID,
			ExternalId: sub.ExternalID, ExternalNumber: sub.ExternalNumber, PaymentReference: sub.PaymentReference,
			PrepaidAmount: wirePtr(ratOf(sub.PrepaidAmount), amountPlaces),
		},
		Totals: totalsOut(totalsOfRows(current)), Lines: subscriptionLinesOut(current, sub.PricesIncludeVat),
		EarlierLines: subscriptionLinesOut(earlierLines(sch.lines), sub.PricesIncludeVat),
		Periods:      periodsOut(sch, today),
		ProjectId:    sub.ProjectID, Salesperson: personOut(sub.SalespersonID, sub.SalespersonDisplay),
		ActivatedAt: sub.ActivatedAt, ActivatedBy: personOut(sub.ActivatedByUserID, sub.ActivatedByDisplay),
		EndedAt: sub.EndedAt, CancelledAt: sub.CancelledAt, CreatedAt: sub.CreatedAt, UpdatedAt: sub.UpdatedAt,
		SourceQuoteId: sourceQuoteOf(sub.Channel, sub.SourceKind, sub.SourceID),
	}
	if sub.InvoiceDay != nil {
		d := int32(*sub.InvoiceDay)
		out.InvoiceDay = &d
	}
	if sub.Status == subscriptionDraft {
		out.NextChangeFrom = wireDate(sub.StartDate.Time)
	}
	return out
}

// sourceQuoteOf is the quote a document was accepted from, when its origin
// names one ("<id>@<revision>", O27); nil otherwise.
func sourceQuoteOf(channel string, kind, id *string) *int64 {
	return sourceQuoteID(store.OrdersOrder{Channel: channel, SourceKind: kind, SourceID: id})
}

// maxShownPeriods bounds the periods a subscription answers, newest kept.
const maxShownPeriods = 120

// periodsOut is a subscription's periods as its page shows them (O43): from
// the first through the later of today and the last period billed or
// skipped, and the next three after, bounded by the end; each with its state
// on today and what it bills.
func periodsOut(sch schedule, today time.Time) []gen.OrdersSubscriptionPeriod {
	out := []gen.OrdersSubscriptionPeriod{}
	if sch.cal.months == 0 {
		return out
	}
	last := civil(today)
	if sch.hasLive && sch.maxLive.After(last) {
		last = sch.maxLive
	}
	if sch.hasSkips && sch.maxSkip.After(last) {
		last = sch.maxSkip
	}
	if p, ok := sch.cal.periodOn(last); ok {
		last = sch.cal.period(p.index + 3).start
	} else if last.Before(sch.cal.start) {
		last = sch.cal.period(3).start
	}
	periods := sch.cal.periodsThrough(last)
	if len(periods) > maxShownPeriods {
		periods = periods[len(periods)-maxShownPeriods:]
	}
	for _, p := range periods {
		amount := new(big.Rat)
		var invoices []int64
		state := periodUpcoming
		lines := sch.linesOn(p.start)
		drafted, invoicedAll := false, len(lines) > 0
		for _, l := range lines {
			if a, ok := sch.live[liveKey{l.ID, p.start}]; ok {
				amount.Add(amount, ratOrZero(a.Amount))
				if a.State == contracts.AllocationDraft {
					drafted, invoicedAll = true, false
				} else if a.InvoiceNumber != nil && !slices.Contains(invoices, *a.InvoiceNumber) {
					invoices = append(invoices, *a.InvoiceNumber)
				}
				continue
			}
			invoicedAll = false
			amount.Add(amount, periodAmountOf(p, ratOf(l.Quantity), ratOf(l.UnitPrice), ratOrZero(l.DiscountPercent)).amount)
		}
		switch {
		case sch.skips[p.start]:
			state = periodSkipped
		case invoicedAll:
			state = periodInvoiced
		case drafted:
			state = periodDraft
		case sch.sub.Status == subscriptionPaused && sch.sub.PausedFrom.Valid && !p.start.Before(civil(sch.sub.PausedFrom.Time)):
			state = periodPaused
		case sch.billsFrom(p) && dueThrough(sch.sub.Billing, p, today) && !p.start.After(latestBillableStart(today)):
			state = periodDue
		}
		if invoices == nil {
			invoices = []int64{}
		}
		out = append(out, gen.OrdersSubscriptionPeriod{
			Start: wireDate(p.start), End: wireDate(p.end), Partial: p.partial(), Amount: wire(amount, amountPlaces),
			State: gen.OrdersSubscriptionPeriodState(state), InvoiceNumbers: invoices,
		})
	}
	return out
}

// getSubscriptionView reads a subscription and answers it, in one snapshot.
func (s *server) getSubscriptionView(ctx context.Context, id int64) (gen.OrdersSubscription, error) {
	today, err := s.todayOf(ctx)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	var out gen.OrdersSubscription
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		sub, err := q.SubscriptionByID(ctx, id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("orders: read subscription %d: %w", id, err)
		}
		sch, err := readSchedule(ctx, q, sub)
		if err != nil {
			return err
		}
		out = subscriptionView(sch, today)
		return nil
	})
	return out, err
}

// viewOn answers a subscription written on txq, its rows read again there.
func viewOn(ctx context.Context, txq *store.Queries, sub store.OrdersSubscription, today time.Time) (gen.OrdersSubscription, error) {
	sch, err := readSchedule(ctx, txq, sub)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	return subscriptionView(sch, today), nil
}

// lockSubscription reads a subscription under its row lock on the caller's
// transaction, after any variants' locks (MB rule 21); notFound when there is
// none.
func lockSubscription(ctx context.Context, txq *store.Queries, id int64) (store.OrdersSubscription, error) {
	sub, err := txq.LockSubscription(ctx, id)
	if noRows(err) {
		return sub, notFound()
	}
	if err != nil {
		return sub, fmt.Errorf("orders: lock subscription %d: %w", id, err)
	}
	return sub, nil
}

// judgeSubscriptionVersion refuses a write made from a version of the
// subscription that is not its current one, or from a read older than its
// last write (version_conflict).
func judgeSubscriptionVersion(sub store.OrdersSubscription, version int32, expected time.Time) error {
	if sub.Version != version {
		return versionConflict("subscription's version", sub.Version, version)
	}
	return judgeFresh("subscription", sub.UpdatedAt, expected)
}

// logSubscriptionChange writes one change log entry of a subscription on the
// caller's transaction, which holds its row (O44).
func logSubscriptionChange(ctx context.Context, txq *store.Queries, id int64, version int32, by actor, at time.Time, actions []changeAction) error {
	if len(actions) == 0 {
		return nil
	}
	body, err := json.Marshal(actions)
	if err != nil {
		return fmt.Errorf("orders: encode subscription %d's change: %w", id, err)
	}
	kind := actorUser
	if by.id == nil {
		kind = actorSystem
	}
	if _, err := txq.InsertSubscriptionChange(ctx, store.InsertSubscriptionChangeParams{
		OwnerID: id, Version: version, ActorKind: kind, ActorUserID: by.id, ActorDisplay: by.display, At: at, Actions: body,
	}); err != nil {
		return fmt.Errorf("orders: log subscription %d's change: %w", id, err)
	}
	return nil
}

// judgeServices refuses a subscription's catalog line whose variant is not a
// service (subscription_goods_not_allowed, O42); variants are the catalog's
// answers, read before any transaction. A free-text line is always allowed.
func judgeServices(lines []pricedLine, variants map[int32]contracts.VariantEntry) []lineFailure {
	var failures []lineFailure
	for i, l := range lines {
		if l.kind != kindProduct || l.variantID == nil {
			continue
		}
		if v, ok := variants[*l.variantID]; ok && v.ProductType != productTypeService {
			failures = append(failures, lineFailure{index: i, code: codeSubscriptionGoodsNotAllowed,
				detail: fmt.Sprintf("%s is goods; a subscription sells services only.", v.SKU)})
		}
	}
	return failures
}

// priceSubscriptionInputs prices a subscription's lines for header h over
// existing (a save's current lines, nil on a create), before any
// transaction: no line is optional (optional_not_allowed), a kept line keeps
// its kind (line_kind_changed), and every catalog line is a service
// (subscription_goods_not_allowed). frozen prices an active subscription's
// kept lines from their own rows, as a confirmed order's are.
func (s *server) priceSubscriptionInputs(ctx context.Context, inputs []lineInput, existing []store.OrdersLine, h header, reprice, frozen bool) ([]pricedLine, error) {
	if err := judgeKinds(inputs, existing); err != nil {
		return nil, err
	}
	var failures []lineFailure
	for i, l := range inputs {
		if l.optional {
			failures = append(failures, lineFailure{index: i, code: codeOptionalNotAllowed, detail: "A subscription has no optional lines."})
		}
	}
	if len(failures) > 0 {
		return nil, refuseLines(invalidSubscriptionTitle, failures)
	}
	old := byID(existing)
	reading := inputs
	if frozen {
		reading = nil
		for _, l := range inputs {
			if l.id != nil {
				if o, ok := old[*l.id]; ok && keptFrozen(l, o) {
					continue
				}
			}
			reading = append(reading, l)
		}
	}
	reads, err := s.readPricing(ctx, reading, h.Currency, s.deps.Clock())
	if err != nil {
		return nil, err
	}
	priced, failures := priceLines(inputs, old, reads, priceOptions{
		pricesIncludeVat: h.PricesIncludeVat, allowOptional: false, repriceKept: reprice, frozenKept: frozen,
	})
	if len(failures) > 0 {
		return nil, refuseLines(invalidSubscriptionTitle, failures)
	}
	if failures := judgeServices(priced, reads.variants); len(failures) > 0 {
		return nil, refuseLines(invalidSubscriptionTitle, failures)
	}
	return priced, nil
}

// subscriptionOrigin is where a new subscription comes from (O27).
type subscriptionOrigin = OrderOrigin

// createSubscriptionRow writes a new draft subscription and its lines on the
// caller's transaction, which has locked the lines' variants first: the
// subscription series' next number, the header, the plan — its final period
// the settings' default when the plan leaves it — and a full period's
// totals, the lines as given (too_many_lines).
func (s *server) createSubscriptionRow(ctx context.Context, txq *store.Queries, origin subscriptionOrigin, h header, p plan,
	lines []pricedLine, by actor) (store.OrdersSubscription, []store.OrdersLine, error) {
	if len(lines) > maxLines {
		return store.OrdersSubscription{}, nil, refuseLines(invalidSubscriptionTitle, []lineFailure{{index: maxLines, code: codeTooManyLines,
			detail: fmt.Sprintf("A subscription has at most %d lines.", maxLines)}})
	}
	number, err := allocateNumber(ctx, txq, seriesSubscription, 1)
	if err != nil {
		return store.OrdersSubscription{}, nil, err
	}
	if p.finalPeriod == "" {
		settings, err := readSettings(ctx, txq)
		if err != nil {
			return store.OrdersSubscription{}, nil, err
		}
		p.finalPeriod = settings.subscriptionFinalPeriodDefault
	}
	hp, t := insertQuoteParams(h, totalsOfPriced(lines)), totalsOfPriced(lines)
	row, err := txq.InsertSubscription(ctx, store.InsertSubscriptionParams{
		Number: number, BillingInterval: p.interval, Billing: p.billing, StartDate: pgDate(&p.start), EndDate: pgDate(p.end),
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
		NetTotal: numeric(t.net, amountPlaces), VatTotal: numeric(t.vat, amountPlaces), GrossTotal: numeric(t.gross, amountPlaces),
		Channel: origin.Channel, SourceModule: origin.SourceModule, SourceKind: origin.SourceKind, SourceID: origin.SourceID,
		ExternalID: origin.ExternalID, ExternalNumber: origin.ExternalNumber, ClientRequestID: origin.ClientRequestID,
		CreatedByUserID: by.id, CreatedByDisplay: by.display, Now: s.deps.Clock(),
	})
	if err != nil {
		return store.OrdersSubscription{}, nil, fmt.Errorf("orders: insert subscription: %w", err)
	}
	written, err := writeLines(ctx, txq, ownerSubscription, row.ID, lines)
	if err != nil {
		return store.OrdersSubscription{}, nil, err
	}
	return row, written, nil
}

// ListSubscriptions List subscriptions
// (GET /api/v1/orders/subscriptions)
func (s *server) ListSubscriptions(ctx context.Context, req gen.ListSubscriptionsRequestObject) (gen.ListSubscriptionsResponseObject, error) {
	p := req.Params
	page, size := int32(1), int32(50)
	if p.Page != nil {
		page = *p.Page
	}
	if p.PageSize != nil {
		size = *p.PageSize
	}
	if page < 1 || size < 1 || size > 200 {
		var f fieldErrors
		f.add("page", "page is from 1, and pageSize 1 to 200")
		return gen.ListSubscriptions400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	statuses := []string{}
	if p.Status != nil {
		for _, st := range *p.Status {
			statuses = append(statuses, string(st))
		}
	}
	search := ""
	if p.Q != nil {
		search = strings.TrimSpace(*p.Q)
	}
	out := gen.OrdersSubscriptionPage{Items: []gen.OrdersSubscriptionSummary{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.ListSubscriptions(ctx, store.ListSubscriptionsParams{
			CustomerID: p.CustomerId, Statuses: statuses, Q: search, PageSize: size, PageOffset: (page - 1) * size,
		})
		if err != nil {
			return fmt.Errorf("orders: list subscriptions: %w", err)
		}
		if out.Total, err = q.CountSubscriptions(ctx, store.CountSubscriptionsParams{CustomerID: p.CustomerId, Statuses: statuses, Q: search}); err != nil {
			return fmt.Errorf("orders: count subscriptions: %w", err)
		}
		for _, r := range rows {
			out.Items = append(out.Items, gen.OrdersSubscriptionSummary{
				Id: r.ID, Number: r.Number, Status: gen.OrdersSubscriptionSummaryStatus(r.Status),
				Interval: gen.OrdersSubscriptionSummaryInterval(r.BillingInterval), Billing: gen.OrdersSubscriptionSummaryBilling(r.Billing),
				StartDate: wireDate(r.StartDate.Time), EndDate: dateOut(r.EndDate), BilledThrough: dateOut(r.BilledThrough),
				CustomerId: r.CustomerID, CustomerName: r.CustomerName, Currency: r.Currency,
				Net: wire(ratOrZero(r.NetTotal), amountPlaces), Gross: wire(ratOrZero(r.GrossTotal), amountPlaces), UpdatedAt: r.UpdatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.ListSubscriptions200JSONResponse(out), nil
}

// GetSubscription Get a subscription
// (GET /api/v1/orders/subscriptions/{id})
func (s *server) GetSubscription(ctx context.Context, req gen.GetSubscriptionRequestObject) (gen.GetSubscriptionResponseObject, error) {
	out, err := s.getSubscriptionView(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetSubscriptionResponseObject]{notFound: func() gen.GetSubscriptionResponseObject { return gen.GetSubscription404Response{} }})
	}
	return gen.GetSubscription200JSONResponse(out), nil
}

// CreateSubscription Create a subscription
// (POST /api/v1/orders/subscriptions)
//
// A draft by hand (O42), its origin channel manual: everything it is made
// from is read before its transaction, which locks the variants first (MB
// rule 21), takes the number and writes it. A repeated clientRequestId
// answers the first subscription with 200 (O27).
func (s *server) CreateSubscription(ctx context.Context, req gen.CreateSubscriptionRequestObject) (gen.CreateSubscriptionResponseObject, error) {
	resp := answers[gen.CreateSubscriptionResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.CreateSubscriptionResponseObject {
			return gen.CreateSubscription400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.OrdersProblem) gen.CreateSubscriptionResponseObject {
			return gen.CreateSubscription409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.CreateSubscriptionResponseObject {
			return gen.CreateSubscription503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, subscriptionInputSchema)
	if f.failed() {
		return gen.CreateSubscription400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, f)), nil
	}
	body := *req.Body
	requestID := uuid.UUID(body.ClientRequestId)
	if repeat, err := s.repeatedSubscription(ctx, requestID, body.CustomerId); repeat != nil || err != nil {
		if err != nil {
			return answer(err, resp)
		}
		return gen.CreateSubscription200JSONResponse(*repeat), nil
	}
	pl := readPlan(&f, string(body.Interval), string(body.Billing), body.StartDate.Time, body.EndDate, body.NoticeMonths, body.InvoiceDay,
		body.FinalPeriod)
	in := headerInput{
		CustomerID: body.CustomerId, ContactID: body.ContactId, DeliveryAddressID: body.DeliveryAddressId, Currency: body.Currency,
		DiscountVisible: body.DiscountVisible, IntroText: body.IntroText, ClosingText: body.ClosingText,
		InternalNote: body.InternalNote, YourReference: body.YourReference, PaymentTermsDays: body.PaymentTermsDays,
		PricesIncludeVat: body.PricesIncludeVat, ProjectID: body.ProjectId, SalespersonID: body.SalespersonId,
	}
	if body.Language != nil {
		l := string(*body.Language)
		in.Language = &l
	}
	reads, err := s.readHeader(ctx, in, nil)
	if err != nil {
		return answer(err, resp)
	}
	h := resolveHeader(&f, in, nil, reads)
	customerUsable(&f, reads.profile)
	if f.failed() {
		return gen.CreateSubscription400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, f)), nil
	}
	var inputs []lineInput
	if body.Lines != nil {
		inputs = lineInputsOf(*body.Lines)
	}
	priced, err := s.priceSubscriptionInputs(ctx, inputs, nil, h, false, false)
	if err != nil {
		return answer(err, resp)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	today, err := s.todayOf(ctx)
	if err != nil {
		return answer(err, resp)
	}
	var out gen.OrdersSubscription
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidSubscriptionTitle, priced); err != nil {
			return err
		}
		row, _, err := s.createSubscriptionRow(ctx, txq, subscriptionOrigin{Channel: channelManual, ClientRequestID: &requestID}, h, pl, priced, by)
		if err != nil {
			return err
		}
		out, err = viewOn(ctx, txq, row, today)
		return err
	})
	if db.IsUniqueViolation(err, "ux_subscriptions_client_request") {
		if repeat, err := s.repeatedSubscription(ctx, requestID, body.CustomerId); repeat != nil || err != nil {
			if err != nil {
				return answer(err, resp)
			}
			return gen.CreateSubscription200JSONResponse(*repeat), nil
		}
	}
	if err != nil {
		return answer(err, resp)
	}
	return gen.CreateSubscription201JSONResponse(out), nil
}

// repeatedSubscription is the subscription an earlier create with requestID
// made, nil when none did; a repeat for another customer is
// client_request_reused.
func (s *server) repeatedSubscription(ctx context.Context, requestID uuid.UUID, customerID int32) (*gen.OrdersSubscription, error) {
	row, err := store.New(s.deps.Pool).SubscriptionByClientRequest(ctx, &requestID)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read the subscription of request %s: %w", requestID, err)
	}
	if row.CustomerID != customerID {
		return nil, conflict(codeClientRequestReused, "The request id was used before",
			"An earlier create with this clientRequestId made a subscription for another customer.")
	}
	out, err := s.getSubscriptionView(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSubscriptionChanges List a subscription's changes
// (GET /api/v1/orders/subscriptions/{id}/changes)
func (s *server) ListSubscriptionChanges(ctx context.Context, req gen.ListSubscriptionChangesRequestObject) (gen.ListSubscriptionChangesResponseObject, error) {
	out := gen.OrdersChangeList{Items: []gen.OrdersChange{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.SubscriptionByID(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("orders: read subscription %d: %w", req.Id, err)
		}
		rows, err := q.SubscriptionChangesOf(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("orders: read subscription %d's changes: %w", req.Id, err)
		}
		for _, r := range rows {
			var actions []changeAction
			if err := json.Unmarshal(r.Actions, &actions); err != nil {
				return fmt.Errorf("orders: decode change %d: %w", r.ID, err)
			}
			items := make([]gen.OrdersChangeAction, 0, len(actions))
			for _, a := range actions {
				items = append(items, gen.OrdersChangeAction{Kind: a.Kind, LineId: a.LineID, From: a.From, To: a.To})
			}
			who := personOut(r.ActorUserID, r.ActorDisplay)
			if who == nil {
				who = &gen.OrdersPerson{DisplayName: ""}
			}
			out.Items = append(out.Items, gen.OrdersChange{Id: r.ID, Version: r.Version, Actor: *who, At: r.At, Actions: items})
		}
		return nil
	})
	if err != nil {
		return answer(err, answers[gen.ListSubscriptionChangesResponseObject]{
			notFound: func() gen.ListSubscriptionChangesResponseObject { return gen.ListSubscriptionChanges404Response{} },
		})
	}
	return gen.ListSubscriptionChanges200JSONResponse(out), nil
}
