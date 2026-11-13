package integration_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/orders"
)

// TestOrdersUnreserved_RealLedger is contracts.SalesOrderLines.Unreserved
// (manufacturing phase 2 M107a) as Orders answers it over Inventory's own
// StockLedger: what a line holds is read with ReservationsForSources inside
// Orders' read-only transaction, so a reserved line is not demand, a line
// confirmed without reserving is demand whole, and a draft is none.
func TestOrdersUnreserved_RealLedger(t *testing.T) {
	t.Parallel()
	s := newStockInstallation(t)
	s.receive(t, s.shelf, 5)

	reserved := s.draftOrder(t, orderLine(s.shelf, 3, 0))
	if res := s.confirm(s.c, reserved, true); res.Status != http.StatusOK {
		t.Fatalf("confirm reserving = %d %s", res.Status, res.Body)
	}
	open := s.draftOrder(t, orderLine(s.shelf, 4, 0))
	if res := s.confirm(s.c, open, false); res.Status != http.StatusOK {
		t.Fatalf("confirm without reserving = %d %s", res.Status, res.Body)
	}
	s.draftOrder(t, orderLine(s.shelf, 7, 0))

	// Orders' provider over Inventory's real ledger, as Compose wires them.
	d := s.h.Deps()
	d.StockLedger = inventory.Module().StockLedger(d)
	lines := orders.Module().SalesOrderLines(d)
	got, err := lines.Unreserved(t.Context(), []int32{s.shelf}, s.site)
	if err != nil {
		t.Fatalf("Unreserved: %v", err)
	}
	if len(got) != 1 || got[0].OrderID != open.Id || got[0].VariantID != s.shelf || got[0].Quantity != "4" {
		t.Errorf("Unreserved = %+v, want the unreserved order's line of 4", got)
	}
}
