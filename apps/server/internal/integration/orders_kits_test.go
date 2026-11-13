package integration_test

import (
	"net/http"
	"testing"

	ordgen "github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// TestOrdersKits_RealModules is kits through Products, Inventory,
// Manufacturing, Orders and Invoices together (manufacturing phase 2 K,
// M144–M146), the slot wired by Compose itself: a kit BoM made through
// Manufacturing's API turns an order's kit line into child lines through
// contracts.KitStructure — a missing Deps.KitStructure in production would
// leave the kit a single line here. The children reserve and are delivered;
// the kit counts the kits delivered; the kit alone is offered to invoice,
// and its invoice is copied onto its children, leaving the order done.
func TestOrdersKits_RealModules(t *testing.T) {
	t.Parallel()
	s := newStockInstallationOf(t, []string{modManufacturing}, manufacturingPermissions)
	kits := taxCategory(t, s.c, "Sett", 0.25, 1)
	kit := pricedVariant(t, s.c, kits, "Verktøysett", "SETT-1", 500)
	okJSON(t, s.c, http.MethodPost, mfgBase+"/boms", map[string]any{
		"variantId": kit, "outputQuantity": "1", "kind": "kit", "operations": []any{},
		"lines": []map[string]any{{"variantId": s.shelf, "quantity": "2"}, {"variantId": s.screw, "quantity": "10"}},
	}, nil)
	s.receive(t, s.shelf, 10)
	s.receive(t, s.screw, 100)

	o := s.draftOrder(t, orderLine(kit, 3, 0))
	if len(o.Lines) != 3 || o.Lines[1].ParentLineId == nil || *o.Lines[1].ParentLineId != o.Lines[0].Id ||
		*o.Lines[1].VariantId != s.shelf || *o.Lines[1].Quantity != 6 || *o.Lines[2].Quantity != 30 || o.Lines[2].LineNet != 0 {
		t.Fatalf("the kit order's lines %+v, want the kit and its two children", o.Lines)
	}
	if res := s.confirm(s.c, o, true); res.Status != http.StatusOK {
		t.Fatalf("confirm = %d %s", res.Status, res.Body)
	}
	o = s.order(t, o.Id)
	if k, shelf := o.Lines[0], o.Lines[1]; *k.QtyReserved != 0 || *shelf.QtyReserved != 6 || k.Available == nil || *k.Available != 2 {
		t.Fatalf("reserved kit %v shelf %v, kit available %v; want 0, 6 and 2 (the shelves left make two kits)",
			*k.QtyReserved, *shelf.QtyReserved, k.Available)
	}

	d := deliveryFrom(t, s.newDelivery(o.Id), http.StatusCreated, "the kit's delivery")
	if len(d.Lines) != 2 {
		t.Fatalf("delivery lines %+v, want the two children", d.Lines)
	}
	if res := s.post(d); res.Status != http.StatusOK {
		t.Fatalf("post = %d %s", res.Status, res.Body)
	}
	o = s.order(t, o.Id)
	if o.Lines[0].QtyDelivered != 3 || o.DeliveryStatus != ordgen.OrdersOrderDeliveryStatusDelivered {
		t.Fatalf("kit delivered %v, order %s; want 3 and delivered", o.Lines[0].QtyDelivered, o.DeliveryStatus)
	}

	due := s.toInvoice(t, o.Id)
	if len(due) != 1 || due[o.Lines[0].Id] != 3 {
		t.Fatalf("to invoice %v, want the kit line's 3 alone", due)
	}
	drafts := s.fromOrders(t, false, pickLine(o.Id, o.Lines[0].Id, 3))
	if len(drafts) != 1 {
		t.Fatalf("drafts %+v", drafts)
	}
	if inv := s.issue(t, drafts[0].InvoiceId); len(inv.Lines) != 1 || inv.GrossTotal != 1875 {
		t.Errorf("invoice %d lines, gross %v; want the kit's line of 1500 + VAT", len(inv.Lines), inv.GrossTotal)
	}
	o = s.order(t, o.Id)
	if o.Lines[1].QtyInvoiced != 6 || o.Lines[2].QtyInvoiced != 30 || o.InvoiceStatus != ordgen.OrdersOrderInvoiceStatusInvoiced {
		t.Errorf("children invoiced %v and %v, order %s; want 6, 30 and invoiced", o.Lines[1].QtyInvoiced, o.Lines[2].QtyInvoiced,
			o.InvoiceStatus)
	}
	if !o.Done {
		t.Errorf("order %d is not done once delivered and invoiced", o.Number)
	}
}
