package orders_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// The tests of subscriptions (orders design O42–O44, §12 S): the lifecycle,
// the periods due in advance and in arrears, the twelve-month limit, changes
// from the next period, pause and resume, billed through forward and back, a
// credited period due again alone, the unique live period index, and
// accepting a quote as a subscription. The harness clock is 12 September
// 2026.

const (
	varSupport int32 = 201 // a service, NOK 310
	varHosting int32 = 202 // a service, NOK 99.90
)

const subscriptionsPath = "/api/v1/orders/subscriptions"

// subscriber holds what the subscription screens ask for.
var subscriber = []string{"orders:access", "orders:subscriptions-manage", "orders:quotes-manage"}

func subscriptionPath(id int64, rest ...string) string {
	p := fmt.Sprintf("%s/%d", subscriptionsPath, id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

// withServices seeds two services beside the harness's goods.
func withServices(h *harness) {
	service := func(id int32, sku, name string) contracts.VariantEntry {
		return contracts.VariantEntry{ID: id, ProductID: id, ProductName: name, SKU: sku, Unit: "mnd", ProductType: "Service",
			ProductStatus: "Active", TaxCategoryID: catStandard}
	}
	h.catalog.seed(service(varSupport, "SUP-1", "Support"), service(varHosting, "HOST-1", "Hosting"))
	h.catalog.setPrice(varSupport, "NOK", "310.00")
	h.catalog.setPrice(varHosting, "NOK", "99.90")
}

func date(y int, m time.Month, d int) openapi_types.Date {
	return openapi_types.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// createSubscription creates a draft subscription for customer on a monthly
// plan from start, billed as billing, and answers it.
func createSubscription(t *testing.T, c *modtest.Client, customer int32, start openapi_types.Date, billing gen.OrdersSubscriptionInputBilling,
	lines ...gen.OrdersLineInput) gen.OrdersSubscription {
	t.Helper()
	if lines == nil {
		lines = []gen.OrdersLineInput{}
	}
	res := c.Do(http.MethodPost, subscriptionsPath, gen.OrdersSubscriptionInput{
		ClientRequestId: uuid.New(), CustomerId: customer, Interval: gen.OrdersSubscriptionInputIntervalMonthly,
		Billing: billing, StartDate: start, Lines: &lines,
	})
	mustStatus(t, res, http.StatusCreated, "create subscription")
	return subscriptionOf(t, res)
}

func subscriptionOf(t *testing.T, res *modtest.Response) gen.OrdersSubscription {
	t.Helper()
	var s gen.OrdersSubscription
	res.JSON(&s)
	return s
}

// activate activates a draft and answers it.
func activate(t *testing.T, c *modtest.Client, s gen.OrdersSubscription) gen.OrdersSubscription {
	t.Helper()
	res := c.Do(http.MethodPost, subscriptionPath(s.Id, "activate"), gen.OrdersVersionInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version})
	mustStatus(t, res, http.StatusOK, "activate")
	return subscriptionOf(t, res)
}

// getSubscription reads one as it now stands.
func getSubscription(t *testing.T, c *modtest.Client, id int64) gen.OrdersSubscription {
	t.Helper()
	res := c.Do(http.MethodGet, subscriptionPath(id), nil)
	mustStatus(t, res, http.StatusOK, "get subscription")
	return subscriptionOf(t, res)
}

// activeSubscription is an active subscription of one support line of qty,
// monthly from start.
func activeSubscription(t *testing.T, c *modtest.Client, start openapi_types.Date, billing gen.OrdersSubscriptionInputBilling, qty float64) gen.OrdersSubscription {
	t.Helper()
	return activate(t, c, createSubscription(t, c, custBusiness, start, billing, productLine(varSupport, qty)))
}

// dueOf is the subscription's due periods through until as DueOrders answers
// them: each period's first day to its lines' amounts.
func dueOf(t *testing.T, p contracts.BillableOrders, id int64, until time.Time) map[string][]contracts.DueOrderLine {
	t.Helper()
	page, err := p.DueOrders(context.Background(), contracts.DueOrdersRequest{SubscriptionIDs: []int64{id}, Until: until})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]contracts.DueOrderLine{}
	for _, d := range page.Orders {
		if d.Kind != contracts.DueKindSubscription || d.ID != id {
			t.Fatalf("due %+v; want subscription %d only", d, id)
		}
		for _, l := range d.Lines {
			k := l.PeriodStart.Format(time.DateOnly)
			out[k] = append(out[k], l)
		}
	}
	return out
}

// allocateThrough allocates the subscription's periods through until.
func allocateThrough(t *testing.T, p contracts.BillableOrders, id int64, until time.Time) []contracts.OrderAllocation {
	t.Helper()
	got, err := p.Allocate(context.Background(), contracts.AllocateRequest{
		Subscriptions: []contracts.AllocateSubscription{{SubscriptionID: id, Through: until}},
	})
	if err != nil {
		t.Fatalf("allocate subscription %d through %s: %v", id, until.Format(time.DateOnly), err)
	}
	return got
}

// markAll stamps every allocation invoiced by one invoice.
func markAll(t *testing.T, h *harness, ref contracts.InvoiceRef, as []contracts.OrderAllocation) {
	t.Helper()
	sources := make([]contracts.WorkSource, 0, len(as))
	for _, a := range as {
		sources = append(sources, sourceOf(a))
	}
	if err := mark(t, h, ref, sources...); err != nil {
		t.Fatalf("mark invoiced: %v", err)
	}
}

func onDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// A subscription sells services: a goods line is refused, a service, a free
// text line, a section and a note are not. Activating a draft publishes
// orders.subscription.started.v1 and logs the step; a second activate, a
// subscription without a product line, and a create by someone without the
// key are refused. It is listed, by customer, and the customer summary
// counts it at its value per year.
func TestSubscription_CreateActivateAndList(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)

	res := c.Do(http.MethodPost, subscriptionsPath, gen.OrdersSubscriptionInput{
		ClientRequestId: uuid.New(), CustomerId: custBusiness, Interval: gen.OrdersSubscriptionInputIntervalMonthly,
		Billing: gen.OrdersSubscriptionInputBillingInAdvance, StartDate: date(2026, 10, 1),
		Lines: &[]gen.OrdersLineInput{productLine(varBolt, 1)},
	})
	expectRefusal(t, res, http.StatusBadRequest, "subscription_goods_not_allowed", "a goods line")

	free := line(gen.OrdersLineInputKindProduct)
	free.Description, free.Quantity, free.UnitPrice = str("Rådgivning"), f64(2), f64(500)
	section := line(gen.OrdersLineInputKindSection)
	section.Description = str("Drift")
	s := createSubscription(t, c, custBusiness, date(2026, 10, 1), gen.OrdersSubscriptionInputBillingInAdvance,
		section, productLine(varSupport, 1), free)
	if s.Status != gen.OrdersSubscriptionStatusDraft || s.Number < 1 || len(s.Lines) != 3 || s.Totals.Net != 1310 ||
		s.YourReference != "PO-77" || s.Currency != "NOK" {
		t.Fatalf("draft %+v; want three lines netting 1 310.00 a period with the customer's reference", s)
	}
	expectRefusal(t, h.SignIn(t, reader...).Do(http.MethodPost, subscriptionsPath, gen.OrdersSubscriptionInput{
		ClientRequestId: uuid.New(), CustomerId: custBusiness, Interval: gen.OrdersSubscriptionInputIntervalMonthly,
		Billing: gen.OrdersSubscriptionInputBillingInAdvance, StartDate: date(2026, 10, 1),
	}), http.StatusForbidden, "", "a reader creating")

	empty := createSubscription(t, c, custBusiness, date(2026, 10, 1), gen.OrdersSubscriptionInputBillingInAdvance)
	expectRefusal(t, c.Do(http.MethodPost, subscriptionPath(empty.Id, "activate"),
		gen.OrdersVersionInput{ExpectedUpdatedAt: empty.UpdatedAt, Version: empty.Version}), http.StatusConflict, "subscription_empty", "an empty one")

	active := activate(t, c, s)
	if active.Status != gen.OrdersSubscriptionStatusActive || active.Version != 2 || active.ActivatedAt == nil {
		t.Fatalf("activated %+v; want active at version 2", active)
	}
	if n := eventsNamed(t, h, "orders.subscription.started", s.Id); n != 1 {
		t.Errorf("%d started events, want 1", n)
	}
	expectRefusal(t, c.Do(http.MethodPost, subscriptionPath(s.Id, "activate"),
		gen.OrdersVersionInput{ExpectedUpdatedAt: active.UpdatedAt, Version: active.Version}), http.StatusConflict, "subscription_not_draft", "again")
	expectRefusal(t, c.Do(http.MethodPost, subscriptionPath(s.Id, "pause"),
		gen.OrdersPauseInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version}), http.StatusConflict, "version_conflict", "a stale pause")

	res = c.Do(http.MethodGet, subscriptionPath(s.Id, "changes"), nil)
	mustStatus(t, res, http.StatusOK, "changes")
	var changes gen.OrdersChangeList
	res.JSON(&changes)
	if len(changes.Items) != 1 || changes.Items[0].Actions[0].Kind != "activated" {
		t.Errorf("changes %+v; want the activation", changes.Items)
	}
	res = c.Do(http.MethodGet, fmt.Sprintf("%s?customerId=%d&status=active", subscriptionsPath, custBusiness), nil)
	mustStatus(t, res, http.StatusOK, "list")
	var page gen.OrdersSubscriptionPage
	res.JSON(&page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Id != s.Id || page.Items[0].Net != 1310 {
		t.Errorf("list %+v; want the active one at 1 310.00", page)
	}
	res = c.Do(http.MethodGet, fmt.Sprintf("/api/v1/orders/customers/%d/summary", custBusiness), nil)
	mustStatus(t, res, http.StatusOK, "summary")
	var sum gen.OrdersCustomerSummary
	res.JSON(&sum)
	if len(sum.ActiveSubscriptions) != 1 || sum.ActiveSubscriptions[0].Count != 1 || sum.ActiveSubscriptions[0].Amount != 15720 {
		t.Errorf("summary's subscriptions %+v; want one at 15 720.00 a year", sum.ActiveSubscriptions)
	}
}

// In advance a period is due from its first day, in arrears from its last;
// a start in mid-July pro-rates July by its days (O43).
func TestSubscription_DueInAdvanceAndInArrears(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	today := onDay(2026, 9, 12)

	advance := activeSubscription(t, c, date(2026, 7, 15), gen.OrdersSubscriptionInputBillingInAdvance, 1)
	arrears := activeSubscription(t, c, date(2026, 7, 15), gen.OrdersSubscriptionInputBillingInArrears, 1)

	due := dueOf(t, p, advance.Id, today)
	if len(due) != 3 || len(due["2026-07-15"]) != 1 || len(due["2026-09-01"]) != 1 {
		t.Fatalf("in advance due %v; want July (from the 15th), August and September", due)
	}
	july := due["2026-07-15"][0]
	if july.PeriodEnd.Format(time.DateOnly) != "2026-07-31" || july.UnitPrice != "170.00000000" || july.DiscountPercent != "0.00" {
		t.Errorf("July %+v; want 17/31 of 310.00 = 170.00", july)
	}
	if aug := due["2026-08-01"][0]; aug.UnitPrice != "310.00000000" || aug.ToInvoice != "1.000" {
		t.Errorf("August %+v; want the whole month", aug)
	}
	if due := dueOf(t, p, arrears.Id, today); len(due) != 2 || due["2026-09-01"] != nil {
		t.Errorf("in arrears due %v; want July and August, not September", due)
	}
	view := getSubscription(t, c, advance.Id)
	states := map[string]gen.OrdersSubscriptionPeriodState{}
	for _, per := range view.Periods {
		states[per.Start.Format(time.DateOnly)] = per.State
	}
	if states["2026-07-15"] != gen.OrdersSubscriptionPeriodStateDue || states["2026-10-01"] != gen.OrdersSubscriptionPeriodStateUpcoming {
		t.Errorf("periods %v; want July due and October upcoming", states)
	}
	if !view.Periods[0].Partial || view.Periods[0].Amount != 170 {
		t.Errorf("the first period %+v; want partial at 170.00", view.Periods[0])
	}
}

// Nothing starting more than twelve months after today is billed, however
// far ahead the date asked (O43).
func TestSubscription_TwelveMonthLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := activeSubscription(t, c, date(2026, 9, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)
	got := allocateThrough(t, p, s.Id, onDay(2028, 12, 31))
	if len(got) != 13 || got[12].PeriodStart.Format(time.DateOnly) != "2027-09-01" {
		t.Fatalf("%d allocations to %v; want September 2026 to September 2027", len(got), got[len(got)-1].PeriodStart)
	}
	for _, a := range got {
		if a.Kind != contracts.AllocationSubscriptionPeriod || a.OrderID != s.Id || a.OrderNumber != s.Number || a.Amount != "310.00" ||
			a.State != contracts.AllocationDraft || a.Currency != "NOK" || a.VATCodeID != codeStandard {
			t.Fatalf("allocation %+v; want a draft period of the subscription at 310.00", a)
		}
	}
	_, err := p.Allocate(context.Background(), contracts.AllocateRequest{
		Subscriptions: []contracts.AllocateSubscription{{SubscriptionID: s.Id, Through: onDay(2028, 12, 31)}},
	})
	var r *contracts.AllocationRefusal
	if !errors.As(err, &r) || r.Code != contracts.AllocationNothingDue || r.SubscriptionID != s.Id {
		t.Errorf("a second allocate: %v; want nothing_due", err)
	}
}

// A change takes effect from the next unbilled period by default; a period
// with a live allocation refuses it; the changed line ends the day before
// and its new version bills from the change on (O44).
func TestSubscription_ChangeFromTheNextPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := activeSubscription(t, c, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)
	markAll(t, h, invoiceRef(h, 9001, 1), allocateThrough(t, p, s.Id, onDay(2026, 9, 12)))
	s = getSubscription(t, c, s.Id)
	if s.BilledThrough == nil || s.BilledThrough.Format(time.DateOnly) != "2026-09-30" || s.NextChangeFrom.Format(time.DateOnly) != "2026-10-01" {
		t.Fatalf("billed through %v, next change from %v; want 30 September and 1 October", s.BilledThrough, s.NextChangeFrom)
	}

	support := s.Lines[0]
	changed := gen.OrdersLineInput{Id: &support.Id, Kind: gen.OrdersLineInputKindProduct, VariantId: i32(varSupport), Quantity: f64(2), UnitPrice: f64(350)}
	update := gen.OrdersSubscriptionUpdate{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, CustomerId: custBusiness,
		Lines: []gen.OrdersLineInput{changed}, EffectiveFrom: &openapi_types.Date{Time: onDay(2026, 9, 1)}}
	expectRefusal(t, c.Do(http.MethodPut, subscriptionPath(s.Id), update), http.StatusConflict, "change_in_billed_period", "a billed period")
	update.EffectiveFrom = &openapi_types.Date{Time: onDay(2026, 10, 15)}
	expectRefusal(t, c.Do(http.MethodPut, subscriptionPath(s.Id), update), http.StatusBadRequest, "not_a_period_start", "mid-period")
	update.EffectiveFrom = nil
	res := c.Do(http.MethodPut, subscriptionPath(s.Id), update)
	mustStatus(t, res, http.StatusOK, "change")
	s = subscriptionOf(t, res)
	if s.Version != 3 || len(s.Lines) != 1 || s.Lines[0].ValidFrom == nil || s.Lines[0].ValidFrom.Format(time.DateOnly) != "2026-10-01" ||
		s.Totals.Net != 700 || len(s.EarlierLines) != 1 || s.EarlierLines[0].ValidTo.Format(time.DateOnly) != "2026-09-30" {
		t.Fatalf("changed %+v; want the new line from October at 700.00 and the old one to 30 September", s)
	}
	due := dueOf(t, p, s.Id, onDay(2026, 11, 30))
	if len(due) != 2 || due["2026-10-01"][0].UnitPrice != "350.00000000" || due["2026-10-01"][0].ToInvoice != "2.000" {
		t.Errorf("due %v; want October and November at the new price", due)
	}
	if n := eventsNamed(t, h, "orders.subscription.changed", s.Id); n != 1 {
		t.Errorf("%d changed events, want 1", n)
	}
	res = c.Do(http.MethodGet, subscriptionPath(s.Id, "changes"), nil)
	var changes gen.OrdersChangeList
	res.JSON(&changes)
	last := changes.Items[len(changes.Items)-1]
	if !slices.ContainsFunc(last.Actions, func(a gen.OrdersChangeAction) bool { return a.Kind == "effective_from" && *a.To == "2026-10-01" }) ||
		!slices.ContainsFunc(last.Actions, func(a gen.OrdersChangeAction) bool { return a.Kind == "price_changed" }) {
		t.Errorf("the change's actions %+v; want effective_from and price_changed", last.Actions)
	}
}

// A pause stops billing from the next period; a resume from a later period
// start skips the periods between, never billed; billed through runs over
// the skipped periods once the ones before are invoiced (O43, O44).
func TestSubscription_PauseAndResumeSkip(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := activeSubscription(t, c, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)

	res := c.Do(http.MethodPost, subscriptionPath(s.Id, "pause"), gen.OrdersPauseInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version})
	mustStatus(t, res, http.StatusOK, "pause")
	s = subscriptionOf(t, res)
	if s.Status != gen.OrdersSubscriptionStatusPaused || s.PausedFrom == nil || s.PausedFrom.Format(time.DateOnly) != "2026-10-01" {
		t.Fatalf("paused %+v; want paused from 1 October", s)
	}
	if due := dueOf(t, p, s.Id, onDay(2026, 12, 31)); len(due) != 9 || due["2026-10-01"] != nil {
		t.Errorf("due while paused: %d periods; want January to September only", len(due))
	}
	expectRefusal(t, c.Do(http.MethodPost, subscriptionPath(s.Id, "resume"),
		gen.OrdersResumeInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, From: date(2026, 9, 1)}), http.StatusBadRequest, "", "before the pause")
	res = c.Do(http.MethodPost, subscriptionPath(s.Id, "resume"), gen.OrdersResumeInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, From: date(2027, 1, 1)})
	mustStatus(t, res, http.StatusOK, "resume")
	s = subscriptionOf(t, res)
	if s.Status != gen.OrdersSubscriptionStatusActive || s.PausedFrom != nil {
		t.Fatalf("resumed %+v; want active", s)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.subscription_skips WHERE subscription_id = $1`, s.Id); n != 3 {
		t.Errorf("%d skipped periods, want October to December", n)
	}
	due := dueOf(t, p, s.Id, onDay(2027, 1, 31))
	if len(due) != 10 || due["2026-11-01"] != nil || due["2027-01-01"] == nil {
		t.Errorf("due after the resume %d periods; want January to September and January 2027", len(due))
	}
	markAll(t, h, invoiceRef(h, 9002, 2), allocateThrough(t, p, s.Id, onDay(2026, 9, 30)))
	if s = getSubscription(t, c, s.Id); s.BilledThrough == nil || s.BilledThrough.Format(time.DateOnly) != "2026-12-31" {
		t.Errorf("billed through %v; want 31 December, over the skipped periods", s.BilledThrough)
	}
	skipped := 0
	for _, per := range s.Periods {
		if per.State == gen.OrdersSubscriptionPeriodStateSkipped {
			skipped++
		}
	}
	if skipped != 3 {
		t.Errorf("%d periods shown skipped, want 3", skipped)
	}
}

// A full credit of January makes January due again and nothing else;
// billed through falls back to before it and comes forward again with its
// new invoice (O43).
func TestSubscription_ACreditReDuesOnlyItsPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	holder := orders.Module().InvoicedWork(h.Deps())
	s := activeSubscription(t, c, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)

	jan := allocateThrough(t, p, s.Id, onDay(2026, 1, 31))
	feb := allocateThrough(t, p, s.Id, onDay(2026, 2, 28))
	if len(jan) != 1 || len(feb) != 1 {
		t.Fatalf("January %d, February %d allocations; want one each", len(jan), len(feb))
	}
	janRef, febRef := invoiceRef(h, 9101, 101), invoiceRef(h, 9102, 102)
	markAll(t, h, janRef, jan)
	if s = getSubscription(t, c, s.Id); s.BilledThrough.Format(time.DateOnly) != "2026-01-31" {
		t.Errorf("billed through %v; want January's end", s.BilledThrough)
	}
	markAll(t, h, febRef, feb)
	if s = getSubscription(t, c, s.Id); s.BilledThrough.Format(time.DateOnly) != "2026-02-28" {
		t.Errorf("billed through %v; want February's end", s.BilledThrough)
	}

	ctx := context.Background()
	if err := pgx.BeginFunc(ctx, h.Pool(), func(tx pgx.Tx) error {
		return holder.ReleaseInvoiced(ctx, tx, janRef, []contracts.WorkSource{sourceOf(jan[0])})
	}); err != nil {
		t.Fatal(err)
	}
	if s = getSubscription(t, c, s.Id); s.BilledThrough != nil {
		t.Errorf("billed through %v after January's credit; want none", s.BilledThrough)
	}
	due := dueOf(t, p, s.Id, onDay(2026, 2, 28))
	if len(due) != 1 || due["2026-01-01"] == nil {
		t.Fatalf("due after the credit %v; want January alone", due)
	}
	markAll(t, h, invoiceRef(h, 9103, 103), allocateThrough(t, p, s.Id, onDay(2026, 2, 28)))
	if s = getSubscription(t, c, s.Id); s.BilledThrough.Format(time.DateOnly) != "2026-02-28" {
		t.Errorf("billed through %v after January's new invoice; want February's end", s.BilledThrough)
	}
	if s.Periods[0].State != gen.OrdersSubscriptionPeriodStateInvoiced || !slices.Equal(s.Periods[0].InvoiceNumbers, []int64{103}) {
		t.Errorf("January %+v; want invoiced by 103", s.Periods[0])
	}
}

// Two Allocates of one subscription at once bill each period once: the
// subscription's lock serialises them, and the unique live period index
// refuses a second live allocation of a line's period whatever writes it.
func TestSubscription_AllocateBillsAPeriodOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := activeSubscription(t, c, date(2026, 6, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)

	var wg sync.WaitGroup
	results := make([]error, 2)
	counts := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := p.Allocate(context.Background(), contracts.AllocateRequest{
				Subscriptions: []contracts.AllocateSubscription{{SubscriptionID: s.Id, Through: onDay(2026, 9, 12)}},
			})
			results[i], counts[i] = err, len(got)
		}()
	}
	wg.Wait()
	ok, refused := 0, 0
	for i, err := range results {
		var r *contracts.AllocationRefusal
		switch {
		case err == nil && counts[i] == 4:
			ok++
		case errors.As(err, &r) && r.Code == contracts.AllocationNothingDue:
			refused++
		default:
			t.Errorf("allocate %d: %d allocations, %v", i, counts[i], err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Errorf("%d allocated and %d refused; want one of each", ok, refused)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.invoice_allocations WHERE subscription_id = $1`, s.Id); n != 4 {
		t.Errorf("%d allocations; want June to September once", n)
	}
	_, err := h.Pool().Exec(context.Background(), `INSERT INTO orders.invoice_allocations
		(line_id, subscription_id, kind, period_start, period_end, quantity, unit_price, amount, vat_code_id, created_at, updated_at)
		SELECT line_id, subscription_id, kind, period_start, period_end, quantity, unit_price, amount, vat_code_id, now(), now()
		FROM orders.invoice_allocations WHERE subscription_id = $1 LIMIT 1`, s.Id)
	if err == nil {
		t.Error("a second live allocation of a period was written")
	}
}

// Ending sets the end — not before the last billed period (end_before_billed)
// — and publishes ended; a period after the end is never due. Cancelling a
// subscription with live allocations is refused, a draft's is not.
func TestSubscription_EndAndCancel(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := activeSubscription(t, c, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)
	allocateThrough(t, p, s.Id, onDay(2026, 3, 31))
	s = getSubscription(t, c, s.Id)

	expectRefusal(t, c.Do(http.MethodPost, subscriptionPath(s.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version}),
		http.StatusConflict, "subscription_billed", "a billed one cancelled")
	expectRefusal(t, c.Do(http.MethodPost, subscriptionPath(s.Id, "end"), gen.OrdersEndInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, EndDate: date(2026, 2, 15)}),
		http.StatusConflict, "end_before_billed", "before a billed period")
	res := c.Do(http.MethodPost, subscriptionPath(s.Id, "end"), gen.OrdersEndInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, EndDate: date(2026, 5, 31)})
	mustStatus(t, res, http.StatusOK, "end")
	s = subscriptionOf(t, res)
	if s.Status != gen.OrdersSubscriptionStatusEnded || s.EndDate == nil || s.EndDate.Format(time.DateOnly) != "2026-05-31" {
		t.Fatalf("ended %+v; want ended on 31 May", s)
	}
	if n := eventsNamed(t, h, "orders.subscription.ended", s.Id); n != 1 {
		t.Errorf("%d ended events, want 1", n)
	}
	if due := dueOf(t, p, s.Id, onDay(2026, 9, 12)); len(due) != 2 || due["2026-06-01"] != nil {
		t.Errorf("due after the end %v; want April and May only", due)
	}

	draft := createSubscription(t, c, custBusiness, date(2026, 10, 1), gen.OrdersSubscriptionInputBillingInArrears, productLine(varHosting, 1))
	res = c.Do(http.MethodPost, subscriptionPath(draft.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: draft.UpdatedAt, Version: draft.Version})
	mustStatus(t, res, http.StatusOK, "cancel a draft")
	if got := subscriptionOf(t, res); got.Status != gen.OrdersSubscriptionStatusCancelled || got.CancelledAt == nil {
		t.Errorf("cancelled %+v", got)
	}
	if n := eventsNamed(t, h, "orders.subscription.ended", draft.Id); n != 0 {
		t.Errorf("a draft's cancel published %d ended events, want none", n)
	}
}

// A draft's save replaces its plan and lines; a period's issue and credit
// through the holder are a subscription's stamp, as an order line's are.
func TestSubscription_SaveADraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	s := createSubscription(t, c, custBusiness, date(2026, 10, 1), gen.OrdersSubscriptionInputBillingInAdvance, productLine(varSupport, 1))
	quarterly := gen.OrdersSubscriptionUpdateIntervalQuarterly
	res := c.Do(http.MethodPut, subscriptionPath(s.Id), gen.OrdersSubscriptionUpdate{
		ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, CustomerId: custBusiness, Interval: &quarterly,
		Lines: []gen.OrdersLineInput{{Id: &s.Lines[0].Id, Kind: gen.OrdersLineInputKindProduct, VariantId: i32(varSupport), Quantity: f64(3)}, productLine(varHosting, 1)},
	})
	mustStatus(t, res, http.StatusOK, "save a draft")
	got := subscriptionOf(t, res)
	if got.Interval != gen.OrdersSubscriptionIntervalQuarterly || len(got.Lines) != 2 || got.Lines[0].Id != s.Lines[0].Id || got.Totals.Net != 1029.9 {
		t.Errorf("saved %+v; want quarterly with two lines netting 1 029.90", got)
	}
}

// A quote of services is accepted as a subscription on a plan (O20): the
// subscription is a draft made from the quote's lines, its origin the quote;
// a quote of goods is refused, a plan is required, and the key is.
func TestSubscription_AcceptAQuoteAsOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	plan := &gen.OrdersSubscriptionPlan{Interval: gen.OrdersSubscriptionPlanIntervalYearly, Billing: gen.OrdersSubscriptionPlanBillingInAdvance,
		StartDate: date(2027, 1, 1), NoticeMonths: func() *int32 { v := int32(3); return &v }()}
	accept := func(q gen.OrdersQuote, into gen.OrdersAcceptInputInto, plan *gen.OrdersSubscriptionPlan, as *modtest.Client) *modtest.Response {
		return as.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{
			ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: q.Revision, Into: into, Plan: plan,
		})
	}

	goods := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	expectRefusal(t, accept(goods, gen.OrdersAcceptInputIntoSubscription, plan, c), http.StatusBadRequest, "subscription_goods_not_allowed", "goods")

	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varSupport, 2)))
	expectRefusal(t, accept(q, gen.OrdersAcceptInputIntoSubscription, nil, c), http.StatusBadRequest, "", "no plan")
	mustStatus(t, accept(q, gen.OrdersAcceptInputIntoSubscription, plan, h.SignIn(t, quoter...)), http.StatusForbidden, "without the key")
	res := accept(q, gen.OrdersAcceptInputIntoSubscription, plan, c)
	mustStatus(t, res, http.StatusOK, "accept")
	var out gen.OrdersAccepted
	res.JSON(&out)
	if out.Subscription == nil || out.Order != nil {
		t.Fatalf("accepted %+v; want a subscription and no order", out)
	}
	sub := *out.Subscription
	if sub.Status != gen.OrdersSubscriptionStatusDraft || sub.Interval != gen.OrdersSubscriptionIntervalYearly || sub.NoticeMonths != 3 ||
		len(sub.Lines) != 1 || sub.Totals.Net != 620 || sub.SourceQuoteId == nil || *sub.SourceQuoteId != q.Id || sub.Origin.Channel != "quote" {
		t.Errorf("subscription %+v; want a yearly draft of the quote's line at 620.00, its origin the quote", sub)
	}
	if out.Quote.Status != gen.OrdersQuoteStatusAccepted || out.Quote.AcceptedInto == nil ||
		out.Quote.AcceptedInto.Kind != gen.OrdersDocumentRefKindSubscription || out.Quote.AcceptedInto.Number != sub.Number {
		t.Errorf("quote %+v; want accepted into the subscription", out.Quote.AcceptedInto)
	}
	if n := eventsNamed(t, h, "orders.quote.accepted", q.Id); n != 1 {
		t.Errorf("%d accepted events, want 1", n)
	}
}

// The holders count a subscription: a variant on an active subscription's
// lines holds a delete (O30), a merge re-points it, an erase anonymises a
// draft one at once and keeps an activated one, and the export carries it.
func TestSubscription_Holders(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	activeSubscription(t, c, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)
	createSubscription(t, c, custBusiness, date(2026, 10, 1), gen.OrdersSubscriptionInputBillingInAdvance, productLine(varHosting, 1))
	ctx := context.Background()

	var hold *contracts.VariantHold
	if err := pgx.BeginFunc(ctx, h.Pool(), func(tx pgx.Tx) error {
		var err error
		hold, err = orders.Module().VariantReferences(h.Deps()).VariantHeld(ctx, tx, varSupport, contracts.VariantHoldDelete)
		return err
	}); err != nil || hold == nil || hold.Documents != 1 {
		t.Errorf("the support variant's hold %+v, %v; want one document", hold, err)
	}
	data := orders.Module().CustomerPersonalData(h.Deps())
	exported, err := data.ExportCustomerData(ctx, custBusiness)
	if err != nil || exported == nil || !strings.Contains(fmt.Sprintf("%+v", exported), "monthly in_advance from 2026-01-01") {
		t.Errorf("export %+v, %v; want the subscriptions with their plans", exported, err)
	}
	var erased []contracts.ErasedData
	if err := pgx.BeginFunc(ctx, h.Pool(), func(tx pgx.Tx) error {
		erased, err = data.EraseCustomerData(ctx, tx, custBusiness)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if last := erased[len(erased)-1]; last != (contracts.ErasedData{Kind: "orders.subscriptions", Count: 1}) {
		t.Errorf("erased %+v; want the draft subscription", erased)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.subscriptions WHERE anonymised_at IS NULL`); n != 1 {
		t.Errorf("%d subscriptions still hold the person; want the activated one", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.erasures WHERE customer_id = $1`, custBusiness); n != 1 {
		t.Errorf("the erasure was not recorded for the kept subscription")
	}
}

// keptLine is an active subscription's line l as a save names it again, at
// qty and its own price.
func keptLine(l gen.OrdersLine, qty float64) gen.OrdersLineInput {
	in := productLine(*l.VariantId, qty)
	in.Id, in.UnitPrice = &l.Id, l.UnitPrice
	return in
}

// subLineOf is the subscription's current line of variant.
func subLineOf(t *testing.T, s gen.OrdersSubscription, variant int32) gen.OrdersLine {
	t.Helper()
	for _, l := range s.Lines {
		if l.VariantId != nil && *l.VariantId == variant {
			return l
		}
	}
	t.Fatalf("subscription %d has no line of variant %d: %+v", s.Id, variant, s.Lines)
	return gen.OrdersLine{}
}

// changeLines saves an active subscription's lines from from on.
func changeLines(t *testing.T, c *modtest.Client, s gen.OrdersSubscription, from openapi_types.Date, lines ...gen.OrdersLineInput) gen.OrdersSubscription {
	t.Helper()
	if lines == nil {
		lines = []gen.OrdersLineInput{}
	}
	res := c.Do(http.MethodPut, subscriptionPath(s.Id), gen.OrdersSubscriptionUpdate{
		ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, CustomerId: custBusiness, Lines: lines, EffectiveFrom: &from,
	})
	mustStatus(t, res, http.StatusOK, "change from "+from.Format(time.DateOnly))
	return subscriptionOf(t, res)
}

// supportAndHosting is an active subscription of one support and one
// hosting line from 1 January 2026, invoiced through September.
func supportAndHosting(t *testing.T, h *harness, c *modtest.Client, p contracts.BillableOrders) gen.OrdersSubscription {
	t.Helper()
	s := activate(t, c, createSubscription(t, c, custBusiness, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance,
		productLine(varSupport, 1), productLine(varHosting, 1)))
	markAll(t, h, invoiceRef(h, 9101, 1), allocateThrough(t, p, s.Id, onDay(2026, 9, 12)))
	return getSubscription(t, c, s.Id)
}

// supportQuantities is each due period's support quantities and how many
// lines it bills.
func supportQuantities(due map[string][]contracts.DueOrderLine) map[string]string {
	out := map[string]string{}
	for start, lines := range due {
		var qs []string
		for _, l := range lines {
			if l.UnitPrice == "310.00000000" {
				qs = append(qs, l.ToInvoice)
			}
		}
		out[start] = fmt.Sprintf("%d lines, support %v", len(lines), qs)
	}
	return out
}

// A change made from a date before one already scheduled replaces it: the
// line's first version ends the day before the second change, the scheduled
// version never takes effect, and every period bills one version of the
// line — never two (O44).
func TestSubscription_AnEarlierChangeReplacesAScheduledOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := supportAndHosting(t, h, c, p)

	s = changeLines(t, c, s, date(2026, 12, 1), keptLine(subLineOf(t, s, varSupport), 2), keptLine(subLineOf(t, s, varHosting), 1))
	s = changeLines(t, c, s, date(2026, 11, 1), keptLine(subLineOf(t, s, varSupport), 3), keptLine(subLineOf(t, s, varHosting), 1))

	support := subLineOf(t, s, varSupport)
	if support.ValidFrom == nil || support.ValidFrom.Format(time.DateOnly) != "2026-11-01" || support.Quantity == nil || *support.Quantity != 3 {
		t.Fatalf("support %+v; want 3 from 1 November", support)
	}
	if len(s.EarlierLines) != 1 || s.EarlierLines[0].ValidTo.Format(time.DateOnly) != "2026-10-31" {
		t.Fatalf("earlier lines %+v; want the first support line to 31 October, the scheduled one gone", s.EarlierLines)
	}
	want := map[string]string{
		"2026-10-01": "2 lines, support [1.000]", "2026-11-01": "2 lines, support [3.000]",
		"2026-12-01": "2 lines, support [3.000]", "2027-01-01": "2 lines, support [3.000]",
	}
	if got := supportQuantities(dueOf(t, p, s.Id, onDay(2027, 1, 31))); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("due %v; want %v", got, want)
	}
	made := allocateThrough(t, p, s.Id, onDay(2027, 1, 31))
	perPeriod := map[string]int{}
	for _, a := range made {
		perPeriod[a.PeriodStart.Format(time.DateOnly)]++
	}
	if len(made) != 8 || perPeriod["2026-11-01"] != 2 || perPeriod["2026-12-01"] != 2 {
		t.Errorf("allocated %v; want each period's support and hosting once", perPeriod)
	}
}

// A removal made from a date before a scheduled change ends the line there:
// neither its first version nor the scheduled one bills after it (O44).
func TestSubscription_AnEarlierRemovalEndsAScheduledChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := supportAndHosting(t, h, c, p)

	s = changeLines(t, c, s, date(2026, 12, 1), keptLine(subLineOf(t, s, varSupport), 2), keptLine(subLineOf(t, s, varHosting), 1))
	s = changeLines(t, c, s, date(2026, 11, 1), keptLine(subLineOf(t, s, varHosting), 1))

	if len(s.Lines) != 1 || len(s.EarlierLines) != 1 || s.EarlierLines[0].ValidTo.Format(time.DateOnly) != "2026-10-31" {
		t.Fatalf("lines %+v, earlier %+v; want hosting alone and support to 31 October", s.Lines, s.EarlierLines)
	}
	want := map[string]string{
		"2026-10-01": "2 lines, support [1.000]", "2026-11-01": "1 lines, support []", "2026-12-01": "1 lines, support []",
	}
	if got := supportQuantities(dueOf(t, p, s.Id, onDay(2026, 12, 31))); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("due %v; want %v", got, want)
	}
}

// A subscription's today is the business zone's (O43): at 00:30 on
// 1 October in Oslo — still 30 September in UTC — October is due in advance
// with no date asked, and the twelve-month limit reaches October 2027.
func TestSubscription_TodayInTheBusinessZone(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	h.SetBusinessTimeZone(t, "Europe/Oslo")
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := activeSubscription(t, c, date(2026, 9, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)
	h.Advance(time.Date(2026, 9, 30, 22, 30, 0, 0, time.UTC).Sub(h.Now()))

	if due := dueOf(t, p, s.Id, time.Time{}); len(due) != 2 || due["2026-10-01"] == nil {
		t.Errorf("due today %v; want September and October", due)
	}
	got := allocateThrough(t, p, s.Id, onDay(2028, 12, 31))
	if len(got) != 14 || got[13].PeriodStart.Format(time.DateOnly) != "2027-10-01" {
		t.Errorf("%d allocations; want September 2026 to October 2027", len(got))
	}
}

// Ending a paused subscription skips its paused periods up to the end, and
// billed through runs over them at once (O43).
func TestSubscription_EndingAPausedOneMovesBilledThrough(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := activeSubscription(t, c, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)
	markAll(t, h, invoiceRef(h, 9102, 1), allocateThrough(t, p, s.Id, onDay(2026, 9, 12)))
	s = getSubscription(t, c, s.Id)
	res := c.Do(http.MethodPost, subscriptionPath(s.Id, "pause"), gen.OrdersPauseInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version})
	mustStatus(t, res, http.StatusOK, "pause")
	s = subscriptionOf(t, res)
	res = c.Do(http.MethodPost, subscriptionPath(s.Id, "end"), gen.OrdersEndInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, EndDate: date(2026, 12, 31)})
	mustStatus(t, res, http.StatusOK, "end")
	if s = subscriptionOf(t, res); s.BilledThrough == nil || s.BilledThrough.Format(time.DateOnly) != "2026-12-31" {
		t.Errorf("billed through %v; want 31 December, over the skipped periods", s.BilledThrough)
	}
}

// A subscription that still bills holds its variants against a delete: one
// ended on a later day, and a line removed from a period still to come; one
// ended in the past does not (O30, O42).
func TestSubscription_HoldsWhatItStillBills(t *testing.T) {
	t.Parallel()
	held := func(t *testing.T, h *harness, variant int32) int {
		t.Helper()
		ctx := context.Background()
		var hold *contracts.VariantHold
		if err := pgx.BeginFunc(ctx, h.Pool(), func(tx pgx.Tx) error {
			var err error
			hold, err = orders.Module().VariantReferences(h.Deps()).VariantHeld(ctx, tx, variant, contracts.VariantHoldDelete)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if hold == nil {
			return 0
		}
		return hold.Documents
	}
	end := func(t *testing.T, c *modtest.Client, s gen.OrdersSubscription, on openapi_types.Date) {
		t.Helper()
		mustStatus(t, c.Do(http.MethodPost, subscriptionPath(s.Id, "end"), gen.OrdersEndInput{ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, EndDate: on}),
			http.StatusOK, "end")
	}

	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	end(t, c, activeSubscription(t, c, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1), date(2026, 12, 31))
	past := activate(t, c, createSubscription(t, c, custBusiness, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance, productLine(varHosting, 1)))
	end(t, c, past, date(2026, 6, 30))
	if n := held(t, h, varSupport); n != 1 {
		t.Errorf("support held by %d; want the subscription ending 31 December", n)
	}
	if n := held(t, h, varHosting); n != 0 {
		t.Errorf("hosting held by %d; want none, its subscription ended in June", n)
	}

	h = newHarness(t, harnessOptions{})
	withServices(h)
	c = h.SignIn(t, subscriber...)
	s := activate(t, c, createSubscription(t, c, custBusiness, date(2026, 1, 1), gen.OrdersSubscriptionInputBillingInAdvance,
		productLine(varSupport, 1), productLine(varHosting, 1)))
	changeLines(t, c, s, date(2026, 10, 1), keptLine(subLineOf(t, s, varSupport), 1))
	if n := held(t, h, varHosting); n != 1 {
		t.Errorf("hosting held by %d; want the subscription billing it through September", n)
	}
}

// endSubscription ends s on end, with final when it is not nil.
func endSubscription(c *modtest.Client, s gen.OrdersSubscription, end openapi_types.Date, final *gen.OrdersFinalPeriod) *modtest.Response {
	return c.Do(http.MethodPost, subscriptionPath(s.Id, "end"), gen.OrdersEndInput{
		ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, EndDate: end, FinalPeriod: final,
	})
}

func finalPeriod(v gen.OrdersFinalPeriod) *gen.OrdersFinalPeriod { return &v }

// periodOf is the subscription's period starting on start.
func periodOf(t *testing.T, s gen.OrdersSubscription, start string) gen.OrdersSubscriptionPeriod {
	t.Helper()
	for _, p := range s.Periods {
		if p.Start.Format(time.DateOnly) == start {
			return p
		}
	}
	t.Fatalf("subscription %d has no period from %s: %+v", s.Id, start, s.Periods)
	return gen.OrdersSubscriptionPeriod{}
}

// A new subscription takes its final period from the settings, full until
// they say otherwise; a create may name its own (owner, 2026-11-06).
func TestSubscription_FinalPeriodDefaultsFromTheSettings(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	if s := createSubscription(t, c, custBusiness, date(2026, 10, 1), gen.OrdersSubscriptionInputBillingInAdvance); s.FinalPeriod != gen.Full {
		t.Fatalf("final period %q before any setting; want full", s.FinalPeriod)
	}
	admin := h.SignIn(t, settingsKey...)
	res := admin.Do(http.MethodPut, settingsPath, gen.OrdersSettingsInput{
		Revision: 0, QuoteSeriesStart: 1, OrderSeriesStart: 1, QuoteValidityDays: 30, DefaultPaymentTermsDays: 14,
		MailTexts: []gen.OrdersMailText{}, SubscriptionFinalPeriodDefault: finalPeriod(gen.Prorated),
	})
	mustStatus(t, res, http.StatusOK, "settings")
	var st gen.OrdersSettings
	res.JSON(&st)
	if st.SubscriptionFinalPeriodDefault != gen.Prorated {
		t.Fatalf("settings %+v; want prorated by default", st)
	}
	if s := createSubscription(t, c, custBusiness, date(2026, 10, 1), gen.OrdersSubscriptionInputBillingInAdvance); s.FinalPeriod != gen.Prorated {
		t.Errorf("final period %q; want the settings' prorated", s.FinalPeriod)
	}
	res = c.Do(http.MethodPost, subscriptionsPath, gen.OrdersSubscriptionInput{
		ClientRequestId: uuid.New(), CustomerId: custBusiness, Interval: gen.OrdersSubscriptionInputIntervalMonthly,
		Billing: gen.OrdersSubscriptionInputBillingInAdvance, StartDate: date(2026, 10, 1), FinalPeriod: finalPeriod(gen.Full),
	})
	mustStatus(t, res, http.StatusCreated, "create in full")
	if s := subscriptionOf(t, res); s.FinalPeriod != gen.Full {
		t.Errorf("final period %q; want the create's full", s.FinalPeriod)
	}
	res = admin.Do(http.MethodPut, settingsPath, gen.OrdersSettingsInput{
		Revision: st.Revision, QuoteSeriesStart: 1, OrderSeriesStart: 1, QuoteValidityDays: 30, DefaultPaymentTermsDays: 14,
		MailTexts: []gen.OrdersMailText{},
	})
	mustStatus(t, res, http.StatusOK, "settings without the default")
	res.JSON(&st)
	if st.SubscriptionFinalPeriodDefault != gen.Prorated {
		t.Errorf("a save leaving the default out made it %q; want it kept", st.SubscriptionFinalPeriodDefault)
	}
}

// Ending mid-period bills the period in full by default; prorated — the
// subscription's own setting when End names none — ends it on the end at
// its days over the month's, to øre, at a unit price docmath reproduces, and
// the choice is stored and logged (owner, 2026-11-06).
func TestSubscription_TheFinalPeriodFullOrProrated(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	hosting := func(final *gen.OrdersFinalPeriod) gen.OrdersSubscription {
		res := c.Do(http.MethodPost, subscriptionsPath, gen.OrdersSubscriptionInput{
			ClientRequestId: uuid.New(), CustomerId: custBusiness, Interval: gen.OrdersSubscriptionInputIntervalMonthly,
			Billing: gen.OrdersSubscriptionInputBillingInAdvance, StartDate: date(2026, 9, 1), FinalPeriod: final,
			Lines: &[]gen.OrdersLineInput{productLine(varHosting, 3)},
		})
		mustStatus(t, res, http.StatusCreated, "create")
		return activate(t, c, subscriptionOf(t, res))
	}

	full := hosting(nil)
	res := endSubscription(c, full, date(2026, 10, 15), nil)
	mustStatus(t, res, http.StatusOK, "end in full")
	full = subscriptionOf(t, res)
	if oct := dueOf(t, p, full.Id, onDay(2026, 10, 31))["2026-10-01"]; len(oct) != 1 || oct[0].PeriodEnd.Format(time.DateOnly) != "2026-10-31" ||
		oct[0].UnitPrice != "99.90000000" || oct[0].ToInvoice != "3.000" {
		t.Errorf("October in full %+v; want the whole month at 3 × 99.90", oct)
	}

	prorated := hosting(finalPeriod(gen.Prorated))
	res = endSubscription(c, prorated, date(2026, 10, 15), nil)
	mustStatus(t, res, http.StatusOK, "end on the subscription's own prorated")
	prorated = subscriptionOf(t, res)
	if prorated.FinalPeriod != gen.Prorated {
		t.Fatalf("final period %q; want prorated kept", prorated.FinalPeriod)
	}
	oct := dueOf(t, p, prorated.Id, onDay(2026, 10, 31))["2026-10-01"]
	if len(oct) != 1 || oct[0].PeriodEnd.Format(time.DateOnly) != "2026-10-15" || oct[0].UnitPrice != "48.34000000" ||
		oct[0].ToInvoice != "3.000" || oct[0].DiscountPercent != "0.00" {
		t.Fatalf("October pro-rated %+v; want 15/31 of 299.70 = 145.02 as 3 × 48.34 to the 15th", oct)
	}
	if per := periodOf(t, prorated, "2026-10-01"); !per.Partial || per.Amount != 145.02 || per.End.Format(time.DateOnly) != "2026-10-15" {
		t.Errorf("October as shown %+v; want partial at 145.02 to the 15th", per)
	}
	made := allocateThrough(t, p, prorated.Id, onDay(2026, 10, 31))
	if last := made[len(made)-1]; last.Amount != "145.02" || last.PeriodEnd.Format(time.DateOnly) != "2026-10-15" {
		t.Errorf("October's allocation %+v; want 145.02 to the 15th", last)
	}

	// Named on End, the choice is stored and logged.
	named := hosting(nil)
	res = endSubscription(c, named, date(2026, 10, 15), finalPeriod(gen.Prorated))
	mustStatus(t, res, http.StatusOK, "end prorated")
	if named = subscriptionOf(t, res); named.FinalPeriod != gen.Prorated {
		t.Errorf("final period %q; want End's prorated stored", named.FinalPeriod)
	}
	res = c.Do(http.MethodGet, subscriptionPath(named.Id, "changes"), nil)
	var changes gen.OrdersChangeList
	res.JSON(&changes)
	last := changes.Items[len(changes.Items)-1]
	if !slices.ContainsFunc(last.Actions, func(a gen.OrdersChangeAction) bool {
		return a.Kind == "final_period" && *a.From == "full" && *a.To == "prorated"
	}) {
		t.Errorf("End's actions %+v; want final_period from full to prorated", last.Actions)
	}
}

// A period both first and final pro-rates over its actual span: from the
// start to the end, over the month's days (owner, 2026-11-06).
func TestSubscription_AFirstAndFinalPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	end := date(2026, 10, 20)
	res := c.Do(http.MethodPost, subscriptionsPath, gen.OrdersSubscriptionInput{
		ClientRequestId: uuid.New(), CustomerId: custBusiness, Interval: gen.OrdersSubscriptionInputIntervalMonthly,
		Billing: gen.OrdersSubscriptionInputBillingInAdvance, StartDate: date(2026, 10, 10), EndDate: &end,
		FinalPeriod: finalPeriod(gen.Prorated), Lines: &[]gen.OrdersLineInput{productLine(varHosting, 1)},
	})
	mustStatus(t, res, http.StatusCreated, "create")
	s := activate(t, c, subscriptionOf(t, res))
	due := dueOf(t, p, s.Id, onDay(2026, 12, 31))
	oct := due["2026-10-10"]
	if len(due) != 1 || len(oct) != 1 || oct[0].PeriodEnd.Format(time.DateOnly) != "2026-10-20" || oct[0].UnitPrice != "35.45000000" {
		t.Errorf("due %v; want one period, 10 to 20 October, at 11/31 of 99.90 = 35.45", due)
	}
}

// A final period with a live allocation is not pro-rated (final_period_billed):
// End refuses it, a save of the plan refuses it, and End in full is allowed —
// so is an end inside the billed period, but not before a period billed
// after it (end_before_billed).
func TestSubscription_ABilledFinalPeriodIsNotProrated(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, subscriber...)
	p := orders.Module().BillableOrders(h.Deps())
	s := activeSubscription(t, c, date(2026, 9, 1), gen.OrdersSubscriptionInputBillingInAdvance, 1)
	allocateThrough(t, p, s.Id, onDay(2026, 10, 31))
	s = getSubscription(t, c, s.Id)

	expectRefusal(t, endSubscription(c, s, date(2026, 9, 20), nil), http.StatusConflict, "end_before_billed", "before October")
	expectRefusal(t, endSubscription(c, s, date(2026, 10, 15), finalPeriod(gen.Prorated)), http.StatusConflict, "final_period_billed", "prorated")
	res := endSubscription(c, s, date(2026, 10, 15), finalPeriod(gen.Full))
	mustStatus(t, res, http.StatusOK, "end in full")
	s = subscriptionOf(t, res)
	if per := periodOf(t, s, "2026-10-01"); per.Partial || per.End.Format(time.DateOnly) != "2026-10-31" || per.State != gen.OrdersSubscriptionPeriodStateDraft {
		t.Errorf("October %+v; want the whole month on its draft", per)
	}
	expectRefusal(t, c.Do(http.MethodPut, subscriptionPath(s.Id), gen.OrdersSubscriptionUpdate{
		ExpectedUpdatedAt: s.UpdatedAt, Version: s.Version, CustomerId: custBusiness,
		Lines: []gen.OrdersLineInput{keptLine(s.Lines[0], 1)}, FinalPeriod: finalPeriod(gen.Prorated),
	}), http.StatusConflict, "final_period_billed", "the plan made prorated")
}
