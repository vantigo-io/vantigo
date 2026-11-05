package integration_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	invgen "github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	ordgen "github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// A subscription is billed per period (orders design O42–O44, delivery S),
// end to end against the real modules: Products sells the service, Orders
// computes its periods and allocates each due one through BillableOrders,
// Invoices' billing run drafts them through the date — one line per period,
// the header spanning them — the issue stamps them through Orders' holder,
// which moves billed through, and a full credit makes the periods due
// again. The harness clock is 12 September 2026.

const subscriptionsAPIPath = "/api/v1/orders/subscriptions"

// activeSubscription creates a monthly subscription of the variant for the
// customer from start, billed in advance, and activates it.
func (o ordersInstallation) activeSubscription(t *testing.T, variant int32, start time.Time) ordgen.OrdersSubscription {
	t.Helper()
	lines := []ordgen.OrdersLineInput{orderLine(variant, 1, 0)}
	var draft ordgen.OrdersSubscription
	okJSON(t, o.c, http.MethodPost, subscriptionsAPIPath, ordgen.OrdersSubscriptionInput{
		ClientRequestId: uuid.New(), CustomerId: o.customer, Interval: ordgen.OrdersSubscriptionInputIntervalMonthly,
		Billing: ordgen.OrdersSubscriptionInputBillingInAdvance, StartDate: openapi_types.Date{Time: start}, Lines: &lines,
	}, &draft)
	var active ordgen.OrdersSubscription
	okJSON(t, o.c, http.MethodPost, fmt.Sprintf("%s/%d/activate", subscriptionsAPIPath, draft.Id),
		ordgen.OrdersVersionInput{ExpectedUpdatedAt: draft.UpdatedAt, Version: draft.Version}, &active)
	if active.Status != ordgen.OrdersSubscriptionStatusActive {
		t.Fatalf("subscription %d = %s, want active", draft.Id, active.Status)
	}
	return active
}

// subscription is a subscription as it now stands.
func (o ordersInstallation) subscription(t *testing.T, id int64) ordgen.OrdersSubscription {
	t.Helper()
	var got ordgen.OrdersSubscription
	okJSON(t, o.c, http.MethodGet, fmt.Sprintf("%s/%d", subscriptionsAPIPath, id), nil, &got)
	return got
}

// duePeriods is the wizard's due list's periods of the subscription, by
// first day.
func (o ordersInstallation) duePeriods(t *testing.T, id int64) []string {
	t.Helper()
	var due invgen.InvoicesDueOrdersResponse
	okJSON(t, o.c, http.MethodGet, fmt.Sprintf("%s/due?customerId=%d", fromOrdersPath, o.customer), nil, &due)
	var out []string
	for _, d := range due.Orders {
		if d.Kind != invgen.Subscription || d.Id != id {
			continue
		}
		for _, l := range d.Lines {
			out = append(out, l.PeriodStart.Format(time.DateOnly))
		}
	}
	return out
}

func TestSubscriptionsInvoicing_ABillingRunIssuesAndACreditReDues(t *testing.T) {
	o := newOrdersInstallation(t)
	standard := taxCategory(t, o.c, "Tjenester", 0.25, 1)
	support := pricedVariantOf(t, o.c, "Service", standard, "Support", "SUP-1", 310)
	sub := o.activeSubscription(t, support, time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC))

	if got := o.duePeriods(t, sub.Id); !slices.Equal(got, []string{"2026-07-01", "2026-08-01", "2026-09-01"}) {
		t.Fatalf("the due list's periods = %v, want July to September", got)
	}

	// A billing run of orders through August drafts July and August.
	res := o.c.Do(http.MethodPost, invoicesBase+"/billing-runs", map[string]any{"issue": false, "source": "orders", "through": "2026-08-31"})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST billing-runs = %d %s", res.Status, res.Body)
	}
	var worker *invoices.BillingWorker
	for _, w := range o.h.Workers(o.h.Deps()) {
		if b, ok := w.(*invoices.BillingWorker); ok {
			worker = b
		}
	}
	if worker == nil {
		t.Fatal("no invoices-billing worker is composed")
	}
	if err := worker.RunCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	ids := modtestInvoiceIDs(t, o)
	if len(ids) != 1 {
		t.Fatalf("the run made %d drafts, want one", len(ids))
	}
	draft := o.doc(t, ids[0])
	if len(draft.Lines) != 2 || !strings.HasSuffix(draft.Lines[0].Description, "juli 2026") ||
		!strings.HasSuffix(draft.Lines[1].Description, "august 2026") || draft.Lines[0].UnitPrice != 310 || len(allocations(draft)) != 2 {
		t.Fatalf("the run's draft lines = %+v, want July and August at 310.00, each holding a period", draft.Lines)
	}
	if draft.DeliveryFrom == nil || draft.DeliveryFrom.Format(time.DateOnly) != "2026-07-01" ||
		draft.DeliveryTo == nil || draft.DeliveryTo.Format(time.DateOnly) != "2026-08-31" || draft.OrderReference != "" {
		t.Errorf("the draft's delivery = %v–%v, order reference %q; want July and August and none", draft.DeliveryFrom, draft.DeliveryTo, draft.OrderReference)
	}
	if got := o.duePeriods(t, sub.Id); !slices.Equal(got, []string{"2026-09-01"}) {
		t.Errorf("the due list while the draft holds two periods = %v, want September alone", got)
	}

	// The issue stamps both periods: billed through the end of August.
	inv := o.issue(t, draft.Id)
	got := o.subscription(t, sub.Id)
	if got.BilledThrough == nil || got.BilledThrough.Format(time.DateOnly) != "2026-08-31" {
		t.Errorf("billed through after the issue = %v, want 31 August", got.BilledThrough)
	}
	if got.Periods[0].State != ordgen.OrdersSubscriptionPeriodStateInvoiced || !slices.Equal(got.Periods[0].InvoiceNumbers, []int64{*inv.Number}) {
		t.Errorf("July = %+v, want invoiced by %d", got.Periods[0], *inv.Number)
	}

	// A full credit gives both back: they are due again, and billed through
	// falls back.
	var creditDraft invgen.InvoicesInvoiceResponse
	okJSON(t, o.c, http.MethodPost, invoiceAt(inv.Id)+"/credit", nil, &creditDraft)
	o.issue(t, creditDraft.Id)
	if got = o.subscription(t, sub.Id); got.BilledThrough != nil {
		t.Errorf("billed through after the credit = %v, want none", got.BilledThrough)
	}
	if got := o.duePeriods(t, sub.Id); !slices.Equal(got, []string{"2026-07-01", "2026-08-01", "2026-09-01"}) {
		t.Errorf("the due list after the credit = %v, want July to September again", got)
	}

	// The wizard bills them again through September, one per period.
	res = o.c.Do(http.MethodPost, fromOrdersPath, invgen.InvoicesFromOrdersRequest{Combine: false, Lines: []invgen.InvoicesFromOrdersLine{},
		Subscriptions: &[]invgen.InvoicesFromOrdersSubscription{{SubscriptionId: sub.Id, Through: openapi_types.Date{Time: time.Date(2026, time.September, 30, 0, 0, 0, 0, time.UTC)}}}})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST %s = %d %s", fromOrdersPath, res.Status, res.Body)
	}
	var out invgen.InvoicesFromOrdersResponse
	res.JSON(&out)
	if len(out.Invoices) != 1 || !slices.Equal(out.Invoices[0].SubscriptionNumbers, []int64{sub.Number}) || len(out.Invoices[0].OrderIds) != 0 {
		t.Fatalf("the wizard made %+v, want one draft of the subscription", out.Invoices)
	}
	if d := o.doc(t, out.Invoices[0].InvoiceId); len(d.Lines) != 3 {
		t.Errorf("the wizard's draft = %d lines, want July to September", len(d.Lines))
	}
}

// modtestInvoiceIDs is every invoice document's id, oldest first.
func modtestInvoiceIDs(t *testing.T, o ordersInstallation) []int64 {
	t.Helper()
	rows, err := o.h.Pool().Query(t.Context(), `SELECT id FROM invoices.invoices ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}
