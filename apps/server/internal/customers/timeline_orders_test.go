package customers_test

import (
	"context"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func publishOrdersEvents(t *testing.T, h *modtest.Harness, evs ...contracts.Event) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, ev := range evs {
		if err := events.Publish(ctx, tx, "orders", ev, h.Now()); err != nil {
			t.Fatalf("publish %s: %v", ev.EventName(), err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	handlers := customers.Module().EventHandlers(h.Deps())
	if err := events.NewDispatcher(h.Pool(), handlers, h.Deps().Logger, h.Now).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

type ordersEntryRow struct {
	EventType  string
	Summary    string
	Link       string
	Actor      string
	OccurredAt time.Time
}

func ordersEntries(t *testing.T, h *modtest.Harness, customerID int32) []ordersEntryRow {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(), `
		SELECT event_type, summary, payload_json->>'link', actor_kind, occurred_at
		FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND producer = 'customers.orders' ORDER BY id`, customerID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []ordersEntryRow
	for rows.Next() {
		var r ordersEntryRow
		if err := rows.Scan(&r.EventType, &r.Summary, &r.Link, &r.Actor, &r.OccurredAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestTimelineOrders_WritesAnEntryPerEvent(t *testing.T) {
	h := newHarness(t)
	id := insertCustomer(t, h, "Acme AS", "active")
	at := h.Now().Truncate(time.Second)
	publishOrdersEvents(t, h,
		contracts.QuoteSent{QuoteID: 5, Number: 1042, Revision: 2, CustomerID: id, Currency: "NOK", NetTotal: "100.00", SentAt: at},
		contracts.QuoteAccepted{QuoteID: 5, Number: 1042, Revision: 2, CustomerID: id, AcceptedIntoKind: "order", AcceptedIntoID: 9, AcceptedAt: at},
		contracts.SalesOrderConfirmed{OrderID: 9, Number: 2001, Version: 1, CustomerID: id, Currency: "NOK", NetTotal: "100.00", ConfirmedAt: at},
		contracts.SalesOrderCancelled{OrderID: 9, Number: 2001, CustomerID: id, CancelledAt: at},
	)
	want := []struct{ typ, link string }{
		{"orders.quote_sent", "/orders/quotes/5"},
		{"orders.quote_accepted", "/orders/quotes/5"},
		{"orders.order_confirmed", "/orders/orders/9"},
		{"orders.order_cancelled", "/orders/orders/9"},
	}
	got := ordersEntries(t, h, id)
	if len(got) != len(want) {
		t.Fatalf("entries = %+v, want %d", got, len(want))
	}
	// Deliveries are ordered per aggregate (quote 5, order 9), not across them.
	for _, w := range want {
		found := false
		for _, g := range got {
			if g.EventType == w.typ && g.Link == w.link && g.Actor == "system" && g.OccurredAt.Equal(at) {
				found = true
			}
		}
		if !found {
			t.Errorf("no %s entry linking %s by system at %v in %+v", w.typ, w.link, at, got)
		}
	}

	// A second dispatcher pass delivers nothing again.
	publishOrdersEvents(t, h)
	if again := ordersEntries(t, h, id); len(again) != len(want) {
		t.Errorf("after a second pass: %d entries, want %d", len(again), len(want))
	}
}

func TestTimelineOrders_SkipsAnUnknownCustomer(t *testing.T) {
	h := newHarness(t)
	publishOrdersEvents(t, h, contracts.QuoteSent{QuoteID: 6, Number: 1, Revision: 1, CustomerID: 999999, Currency: "NOK", NetTotal: "1", SentAt: h.Now()})
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE producer = 'customers.orders'`); n != 0 {
		t.Errorf("entries = %d, want none", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM platform.event_deliveries WHERE state <> 'done'`); n != 0 {
		t.Errorf("undone deliveries = %d, want 0 (skipped, not failed)", n)
	}
}

func TestTimelineOrders_FollowsAMergeAndSkipsAnonymised(t *testing.T) {
	h := newHarness(t)
	survivor := insertCustomer(t, h, "Survivor AS", "active")
	absorbed := insertCustomer(t, h, "Absorbed AS", "archived")
	h.Exec(t, `UPDATE customers.customers SET merged_into_customer_id = $1 WHERE id = $2`, survivor, absorbed)
	gone := insertCustomer(t, h, "Gone", "archived")
	h.Exec(t, `UPDATE customers.customers SET anonymised_at = now() WHERE id = $1`, gone)
	publishOrdersEvents(t, h,
		contracts.SalesOrderCancelled{OrderID: 1, Number: 1, CustomerID: absorbed, CancelledAt: h.Now()},
		contracts.SalesOrderCancelled{OrderID: 2, Number: 2, CustomerID: gone, CancelledAt: h.Now()},
	)
	if got := ordersEntries(t, h, survivor); len(got) != 1 {
		t.Errorf("survivor entries = %+v, want 1", got)
	}
	if got := ordersEntries(t, h, absorbed); len(got) != 0 {
		t.Errorf("absorbed entries = %+v, want none", got)
	}
	if got := ordersEntries(t, h, gone); len(got) != 0 {
		t.Errorf("anonymised entries = %+v, want none", got)
	}
}
