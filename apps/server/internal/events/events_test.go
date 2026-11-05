package events_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

type confirmed struct {
	OrderID int64  `json:"orderId"`
	Total   string `json:"total"`
}

func (confirmed) EventName() string     { return "orders.sales_order.confirmed" }
func (confirmed) EventVersion() int     { return 1 }
func (e confirmed) AggregateID() string { return "7" }

// TestPublish_InsertsOnTheCallersTransaction pins Publish: the event is a row
// of platform.events on the caller's transaction, gone with its rollback.
func TestPublish_InsertsOnTheCallersTransaction(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	at := time.Date(2026, time.November, 5, 10, 0, 0, 0, time.UTC)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := events.Publish(ctx, tx, "orders", confirmed{OrderID: 7, Total: "125.00"}, at); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM platform.events").Scan(&n); err != nil || n != 0 {
		t.Fatalf("after rollback: %d events, %v; want none", n, err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := events.Publish(ctx, tx, "orders", confirmed{OrderID: 7, Total: "125.00"}, at); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var (
		name, module, aggregate, aggregateID string
		version                              int
		payload                              []byte
		occurred                             time.Time
	)
	if err := pool.QueryRow(ctx, "SELECT name, version, module, aggregate, aggregate_id, payload, occurred_at FROM platform.events").
		Scan(&name, &version, &module, &aggregate, &aggregateID, &payload, &occurred); err != nil {
		t.Fatalf("read the event: %v", err)
	}
	var got confirmed
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload %s: %v", payload, err)
	}
	if name != "orders.sales_order.confirmed" || version != 1 || module != "orders" || aggregate != "orders.sales_order" || aggregateID != "7" ||
		!occurred.Equal(at) || got != (confirmed{OrderID: 7, Total: "125.00"}) {
		t.Errorf("event = %s v%d by %s for %s %s at %v, %+v", name, version, module, aggregate, aggregateID, occurred, got)
	}
}

// badName is an event whose name is not "<module>.<aggregate>.<what>".
type badName struct{ name string }

func (e badName) EventName() string { return e.name }
func (badName) EventVersion() int   { return 1 }
func (badName) AggregateID() string { return "7" }

// TestPublish_RefusesAnIncompleteEvent pins the guard: no module, no row;
// a name of fewer than three segments, or an empty one, is refused before
// any statement.
func TestPublish_RefusesAnIncompleteEvent(t *testing.T) {
	t.Parallel()
	if err := events.Publish(context.Background(), nil, "", confirmed{}, time.Now()); err == nil {
		t.Error("Publish with no module = nil, want an error")
	}
	if err := events.Publish(context.Background(), nil, "orders", nil, time.Now()); err == nil {
		t.Error("Publish of a nil event = nil, want an error")
	}
	for _, name := range []string{"orders.confirmed", "orders..confirmed", "orders.sales_order.", "confirmed"} {
		if err := events.Publish(context.Background(), nil, "orders", badName{name}, time.Now()); err == nil {
			t.Errorf("Publish of %q = nil, want an error", name)
		}
	}
}
