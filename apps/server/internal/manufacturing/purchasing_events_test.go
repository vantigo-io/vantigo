package manufacturing_test

import (
	"context"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
)

// Purchasing's sent order, posted receipt and undone receipt each queue one
// replenishment run for the order's site (purchasing design A4), none while
// a run covering it is pending, and the handler writes only this module's
// runs.
func TestPurchasingEvents_QueueASiteRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	runs := func(site int64) int {
		return h.Count(t, `SELECT count(*) FROM manufacturing.replenishment_runs WHERE kind = 'manual' AND site_id = $1 AND status = 'pending'
			AND requested_by_user_id IS NULL AND requested_by_display = 'Purchasing'`, site)
	}
	now := time.Now()
	publish(t, h, "purchasing", contracts.PurchaseOrderSent{OrderID: 1001, Number: 1, SupplierID: 5, SiteID: siteBranch, Currency: "NOK",
		NetTotal: "10.00", OrderDate: "2026-11-17", SendMethod: "manual", SentAt: now})
	if n := runs(siteBranch); n != 1 {
		t.Fatalf("runs after a sent order = %d, want 1", n)
	}
	// A receipt and its undo while that run is pending queue nothing more.
	publish(t, h, "purchasing",
		contracts.PurchaseReceiptPosted{ReceiptID: 1001, Number: 1, OrderID: 1001, SiteID: siteBranch, MovementID: 9, ReceivedOn: "2026-11-17", PostedAt: now},
		contracts.PurchaseReceiptUndone{ReceiptID: 1001, Number: 1, OrderID: 1001, SiteID: siteBranch, ReversalMovementID: 10, UndoneAt: now})
	if n := runs(siteBranch); n != 1 {
		t.Fatalf("runs after a receipt and its undo = %d, want still 1", n)
	}
	// Once that run is done, a receipt queues the next.
	h.Exec(t, `UPDATE manufacturing.replenishment_runs SET status = 'done' WHERE site_id = $1`, siteBranch)
	publish(t, h, "purchasing",
		contracts.PurchaseReceiptPosted{ReceiptID: 1002, Number: 2, OrderID: 1001, SiteID: siteBranch, MovementID: 11, ReceivedOn: "2026-11-17", PostedAt: now})
	if n := runs(siteBranch); n != 1 {
		t.Fatalf("runs after the next receipt = %d, want 1 pending", n)
	}
	// A pending run of every site covers this one too.
	h.Exec(t, `UPDATE manufacturing.replenishment_runs SET status = 'done'`)
	h.Exec(t, `INSERT INTO manufacturing.replenishment_runs (kind, requested_at) VALUES ('manual', now())`)
	publish(t, h, "purchasing",
		contracts.PurchaseReceiptUndone{ReceiptID: 1002, Number: 2, OrderID: 1001, SiteID: siteBranch, ReversalMovementID: 12, UndoneAt: now})
	if n := runs(siteBranch); n != 0 {
		t.Fatalf("runs with every site's run pending = %d, want 0", n)
	}
}

// With the module off, the handler does nothing.
func TestPurchasingEvents_ModuleOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Publish(ctx, tx, "purchasing", contracts.PurchaseOrderSent{OrderID: 1001, Number: 1, SiteID: siteBranch, SentAt: time.Now()}, h.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	d := h.Deps()
	cfg := *d.Config
	cfg.Modules = []string{"products", "inventory"}
	d.Config = &cfg
	if err := events.NewDispatcher(h.Pool(), manufacturing.Module().EventHandlers(d), d.Logger, h.Now).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.replenishment_runs`); n != 0 {
		t.Fatalf("runs with manufacturing off = %d, want 0", n)
	}
}
