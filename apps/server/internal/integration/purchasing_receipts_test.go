package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	invgen "github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	purgen "github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// Purchasing's receipts (purchasing delivery B, P24–P30) against the real
// Inventory, Products and Accounting: a receive posts Inventory's receipt
// on Purchasing's transaction — on hand rises, the movement names the
// receipt and the order, and Purchasing's open supply falls —; lots,
// serials and an inspected variant's quarantine are Inventory's own rules,
// refused with Inventory's codes naming the line; a discontinued product's
// open line is received; an undo is Inventory's reversal, refused when the
// stock is gone; and Inventory's low-stock list counts what is on order.

const purOrdersPath = "/api/v1/purchasing/orders"

// purchasingPermissions are the keys a receiving buyer holds across the
// four modules, beyond installInvoices' own.
var purchasingPermissions = []string{
	"products:products-view", "products:products-manage", "products:variants-view", "products:variants-manage",
	"products:pricing-view", "products:pricing-manage", "products:categories-view",
	"products:tax-categories-view", "products:tax-categories-manage",
	"inventory:locations-view", "inventory:locations-manage", "inventory:stock-view", "inventory:stock-adjust",
	"inventory:receipts-manage", "inventory:lots-manage",
	"accounting:suppliers-manage",
	"purchasing:access", "purchasing:orders-manage", "purchasing:orders-send", "purchasing:receive",
}

// purchasingInstallation is customers, products, inventory, invoices,
// accounting and purchasing composed, with one warehouse and one shelf,
// one supplier in Accounting's register, and a tax category to make
// products under.
type purchasingInstallation struct {
	h         *modtest.Harness
	c         *modtest.Client
	site, loc int64
	supplier  int64
	tax       int32
}

func newPurchasingInstallation(t *testing.T) purchasingInstallation {
	t.Helper()
	h, c, _, _ := installInvoices(t, []string{modCustomers, modProducts, modInventory, modInvoices, modAccounting, modPurchasing}, purchasingPermissions)
	p := purchasingInstallation{h: h, c: c}
	p.tax = taxCategory(t, c, "Standard", 0.25, 1)
	var site struct {
		Id             int64 `json:"id"`
		RootLocationId int64 `json:"rootLocationId"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/sites", map[string]any{"code": "OSL", "name": "Oslo", "kind": "warehouse"}, &site)
	var shelf struct {
		Id int64 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/locations", map[string]any{"parentId": site.RootLocationId, "kind": "shelf", "code": "A1"}, &shelf)
	p.site, p.loc = site.Id, shelf.Id
	// The register answers before the books are set up (purchasing design P2).
	var sup struct {
		Id int64 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/accounting/suppliers", map[string]any{
		"name": "Norsk Stål AS", "email": "ordre@stal.example", "addressLine": "Stålveien 1", "postalCode": "2000", "city": "Lillestrøm",
	}, &sup)
	p.supplier = sup.Id
	return p
}

// purProduct is a product made through Products' API and its one variant.
type purProduct struct {
	id      int32
	variant int32
	sku     string
}

// product creates an active goods product of one variant with the
// variant's fields (tracking, expiryDays) as given.
func (p purchasingInstallation) product(t *testing.T, name, sku string, variant map[string]any) purProduct {
	t.Helper()
	v := map[string]any{"sku": sku}
	for k, val := range variant {
		v[k] = val
	}
	var res struct {
		Id       int32 `json:"id"`
		Variants []struct {
			Id int32 `json:"id"`
		} `json:"variants"`
	}
	okJSON(t, p.c, http.MethodPost, "/api/v1/products", map[string]any{
		"name": name, "type": "Goods", "status": "Active", "taxCategoryId": p.tax,
		"variants": []map[string]any{v},
	}, &res)
	return purProduct{id: res.Id, variant: res.Variants[0].Id, sku: sku}
}

// sentOrder creates a draft for the supplier at the site of one line per
// (variant, quantity) and marks it sent.
func (p purchasingInstallation) sentOrder(t *testing.T, lines ...purgen.PurchasingLineInput) purgen.PurchasingOrder {
	t.Helper()
	var o purgen.PurchasingOrder
	okJSON(t, p.c, http.MethodPost, purOrdersPath, purgen.PurchasingOrderInput{SupplierId: p.supplier, SiteId: &p.site, Lines: &lines}, &o)
	var sent purgen.PurchasingSendResult
	okJSON(t, p.c, http.MethodPost, fmt.Sprintf("%s/%d/mark-sent", purOrdersPath, o.Id),
		purgen.PurchasingMarkSentInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt}, &sent)
	if sent.Order.Status != purgen.PurchasingOrderStatusSent {
		t.Fatalf("order after mark-sent = %s", sent.Order.Status)
	}
	return sent.Order
}

func purLine(variant int32, qty string) purgen.PurchasingLineInput {
	price := "10"
	return purgen.PurchasingLineInput{Kind: purgen.PurchasingLineInputKindVariant, VariantId: &variant, OrderQuantity: qty, UnitPrice: &price}
}

// receipt is a receipt input of lines onto the shelf.
func (p purchasingInstallation) receiptLine(orderLine int64, qty string) purgen.PurchasingReceiptLineInput {
	return purgen.PurchasingReceiptLineInput{OrderLineId: orderLine, Quantity: qty, LocationId: p.loc}
}

func (p purchasingInstallation) receiveRes(orderID int64, lines ...purgen.PurchasingReceiptLineInput) *modtest.Response {
	return p.c.Do(http.MethodPost, fmt.Sprintf("%s/%d/receipts", purOrdersPath, orderID),
		purgen.PurchasingReceiptInput{ClientRequestId: uuid.New(), Lines: lines})
}

func (p purchasingInstallation) receive(t *testing.T, orderID int64, lines ...purgen.PurchasingReceiptLineInput) purgen.PurchasingReceiptResult {
	t.Helper()
	res := p.receiveRes(orderID, lines...)
	if res.Status != http.StatusCreated {
		t.Fatalf("receive = %d %s, want 201", res.Status, res.Body)
	}
	var r purgen.PurchasingReceiptResult
	res.JSON(&r)
	return r
}

func (p purchasingInstallation) undo(receipt int64) *modtest.Response {
	return p.c.Do(http.MethodPost, fmt.Sprintf("/api/v1/purchasing/receipts/%d/undo", receipt), purgen.PurchasingUndoInput{})
}

func (p purchasingInstallation) order(t *testing.T, id int64) purgen.PurchasingOrder {
	t.Helper()
	var o purgen.PurchasingOrder
	okJSON(t, p.c, http.MethodGet, fmt.Sprintf("%s/%d", purOrdersPath, id), nil, &o)
	return o
}

// stockAt is Inventory's stock of the variant at the site: on hand, and on
// order as Inventory reads it from Purchasing.
func (p purchasingInstallation) stockAt(t *testing.T, variant int32) invgen.InventoryStockAtSite {
	t.Helper()
	var s invgen.InventoryStockAtSites
	okJSON(t, p.c, http.MethodGet, fmt.Sprintf("/api/v1/inventory/stock/sites?variantIds=%d&siteIds=%d", variant, p.site), nil, &s)
	for _, row := range s.Stock {
		if row.VariantId == variant && row.SiteId == p.site {
			return row
		}
	}
	return invgen.InventoryStockAtSite{VariantId: variant, SiteId: p.site}
}

// open is what Purchasing's real PurchaseSupply answers on order of the
// variant at the site, "" when nothing.
func (p purchasingInstallation) open(t *testing.T, variant int32) string {
	t.Helper()
	supply := p.h.Deps().PurchaseSupply
	if supply == nil {
		t.Fatal("Compose left Deps.PurchaseSupply empty with Purchasing composed")
	}
	rows, err := supply.OpenByVariants(context.Background(), &p.site, []int32{variant})
	if err != nil {
		t.Fatalf("OpenByVariants: %v", err)
	}
	if len(rows) == 0 {
		return ""
	}
	return rows[0].Quantity
}

// setOnHand corrects what the shelf holds of the variant (in the lot, when
// given) to qty.
func (p purchasingInstallation) setOnHand(t *testing.T, variant int32, lot *int64, qty float64) {
	t.Helper()
	line := map[string]any{"variantId": variant, "locationId": p.loc, "mode": "set", "quantity": qty}
	if lot != nil {
		line["lotId"] = *lot
	}
	okJSON(t, p.c, http.MethodPost, "/api/v1/inventory/movements/adjustments", map[string]any{
		"clientRequestId": uuid.New(), "siteId": p.site, "reason": "correction",
		"lines": []map[string]any{line},
	}, nil)
}

// lots is Inventory's lots of the variant.
func (p purchasingInstallation) lots(t *testing.T, variant int32) []invgen.InventoryLotSummary {
	t.Helper()
	var page invgen.InventoryLotPage
	okJSON(t, p.c, http.MethodGet, fmt.Sprintf("/api/v1/inventory/lots?variantId=%d", variant), nil, &page)
	return page.Lots
}

func (p purchasingInstallation) lot(t *testing.T, id int64) invgen.InventoryLot {
	t.Helper()
	var l invgen.InventoryLot
	okJSON(t, p.c, http.MethodGet, fmt.Sprintf("/api/v1/inventory/lots/%d", id), nil, &l)
	return l
}

// purRefused fails unless res is status with code, and answers its lines.
func purRefused(t *testing.T, res *modtest.Response, status int, code, what string) []purgen.PurchasingProblemLine {
	t.Helper()
	if res.Status != status || problemCode(res) != code {
		t.Fatalf("%s = %d %s, want %d %s", what, res.Status, res.Body, status, code)
	}
	var p purgen.PurchasingProblem
	res.JSON(&p)
	if p.Lines == nil {
		return nil
	}
	return *p.Lines
}

func purReceived(t *testing.T, o purgen.PurchasingOrder, line int64) string {
	t.Helper()
	for _, l := range o.Lines {
		if l.Id == line {
			return l.Received
		}
	}
	t.Fatalf("order %d has no line %d", o.Id, line)
	return ""
}

func purDate(y int, m time.Month, d int) *openapi_types.Date {
	return &openapi_types.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// A partial receipt of an untracked goods line puts its quantity on the
// shelf through Inventory's own ledger and leaves the rest on order; the
// second receives the rest. Inventory's movement is a receipt whose source
// is the Purchasing receipt and the order its line; Purchasing's real
// PurchaseSupply, and Inventory's on order read through it, fall with each.
func TestPurchasingReceipts_PartialThenFullAgainstTheRealLedger(t *testing.T) {
	t.Parallel()
	p := newPurchasingInstallation(t)
	bolt := p.product(t, "Bolt M8", "BOLT-M8", nil)
	o := p.sentOrder(t, purLine(bolt.variant, "30"))
	line := o.Lines[0].Id
	if got := p.open(t, bolt.variant); got != "30" {
		t.Fatalf("on order after the send = %q, want 30", got)
	}
	if s := p.stockAt(t, bolt.variant); s.OnHand != 0 || s.OnOrder != 30 {
		t.Fatalf("Inventory before the receipt: on hand %v, on order %v; want 0 and 30", s.OnHand, s.OnOrder)
	}

	r := p.receive(t, o.Id, p.receiptLine(line, "10"))
	if r.Order.Status != purgen.PurchasingOrderStatusPartiallyReceived || purReceived(t, r.Order, line) != "10" {
		t.Fatalf("order after the partial receipt = %s, received %s", r.Order.Status, purReceived(t, r.Order, line))
	}
	if s := p.stockAt(t, bolt.variant); s.OnHand != 10 || s.OnOrder != 20 {
		t.Errorf("Inventory after the partial receipt: on hand %v, on order %v; want 10 and 20", s.OnHand, s.OnOrder)
	}
	if got := p.open(t, bolt.variant); got != "20" {
		t.Errorf("PurchaseSupply after the partial receipt = %q, want 20", got)
	}
	var mv invgen.InventoryMovement
	okJSON(t, p.c, http.MethodGet, fmt.Sprintf("/api/v1/inventory/movements/%d", r.Receipt.MovementId), nil, &mv)
	if mv.Kind != invgen.Receipt || mv.Site.Id != p.site || len(mv.Lines) != 1 {
		t.Fatalf("Inventory's movement = kind %s site %d, %d lines", mv.Kind, mv.Site.Id, len(mv.Lines))
	}
	if src := mv.Source; src == nil || src.Module != "purchasing" || src.Kind != "receipt" || src.Id != fmt.Sprint(r.Receipt.Id) ||
		src.LineId == nil || *src.LineId != fmt.Sprint(o.Id) {
		t.Errorf("the movement's source = %+v, want purchasing/receipt/%d line %d", mv.Source, r.Receipt.Id, o.Id)
	}
	if mv.SupplierName == nil || *mv.SupplierName != "Norsk Stål AS" {
		t.Errorf("the movement's supplier = %v", mv.SupplierName)
	}

	r2 := p.receive(t, o.Id, p.receiptLine(line, "20"))
	if r2.Order.Status != purgen.PurchasingOrderStatusReceived || purReceived(t, r2.Order, line) != "30" {
		t.Fatalf("order after the full receipt = %s, received %s", r2.Order.Status, purReceived(t, r2.Order, line))
	}
	if s := p.stockAt(t, bolt.variant); s.OnHand != 30 || s.OnOrder != 0 {
		t.Errorf("Inventory after the full receipt: on hand %v, on order %v; want 30 and 0", s.OnHand, s.OnOrder)
	}
	if got := p.open(t, bolt.variant); got != "" {
		t.Errorf("PurchaseSupply after the full receipt = %q, want nothing", got)
	}
	if n := modtest.One[int](t, p.h, `SELECT count(*)::int FROM inventory.movements WHERE kind = 'receipt' AND source_module = 'purchasing' AND source_kind = 'receipt'`); n != 2 {
		t.Errorf("%d Inventory receipts from Purchasing, want 2", n)
	}
}

// A lot-tracked variant's receipt line with a lot number and expiry makes
// the lot in Inventory; one without a lot is Inventory's lot_required naming
// the request's line; a serial variant's two serials make two serial lots,
// and a serial count that is not the quantity is refused.
func TestPurchasingReceipts_LotsAndSerials(t *testing.T) {
	t.Parallel()
	p := newPurchasingInstallation(t)
	flour := p.product(t, "Mel", "MEL-1", map[string]any{"tracking": "lot", "expiryDays": 180})
	pump := p.product(t, "Pumpe", "PMP-1", map[string]any{"tracking": "serial"})
	plain := p.product(t, "Skive", "SKV-1", nil)
	o := p.sentOrder(t, purLine(plain.variant, "5"), purLine(flour.variant, "20"), purLine(pump.variant, "4"))
	plainLine, flourLine, pumpLine := o.Lines[0].Id, o.Lines[1].Id, o.Lines[2].Id

	// Without a lot: Inventory's code, the request's second line.
	lines := purRefused(t, p.receiveRes(o.Id, p.receiptLine(plainLine, "1"), p.receiptLine(flourLine, "5")),
		http.StatusBadRequest, "lot_required", "a tracked line without a lot")
	if len(lines) != 1 || lines[0].Index != 1 || lines[0].Code != "lot_required" {
		t.Errorf("lot_required lines = %+v, want index 1", lines)
	}
	if got := purReceived(t, p.order(t, o.Id), plainLine); got != "0" {
		t.Errorf("a refused receipt received %s of the plain line", got)
	}

	withLot := p.receiptLine(flourLine, "5")
	withLot.Lot = &purgen.PurchasingReceiptLotInput{Number: strPtr("L-778"), SupplierLot: strPtr("SUP-9"), Expiry: purDate(2031, time.June, 30)}
	r := p.receive(t, o.Id, withLot)
	if l := r.Receipt.Lines[0]; l.LotId == nil || l.LotNumber == nil || *l.LotNumber != "L-778" {
		t.Fatalf("the receipt's lot line = %+v", l)
	}
	ls := p.lots(t, flour.variant)
	if len(ls) != 1 || ls[0].Number != "L-778" || ls[0].OnHand != 5 || ls[0].Expiry == nil || ls[0].Expiry.Format("2006-01-02") != "2031-06-30" ||
		ls[0].SupplierLot == nil || *ls[0].SupplierLot != "SUP-9" || ls[0].Id != *r.Receipt.Lines[0].LotId {
		t.Fatalf("Inventory's lots of the flour = %+v", ls)
	}

	// Serials: two serials of 2 make two serial lots of one each.
	serials := p.receiptLine(pumpLine, "2")
	serials.Serials = &[]string{"SN-1", "SN-2"}
	p.receive(t, o.Id, serials)
	pumps := p.lots(t, pump.variant)
	if len(pumps) != 2 {
		t.Fatalf("serial lots = %+v, want two", pumps)
	}
	for _, l := range pumps {
		if !l.Serial || l.OnHand != 1 || (l.Number != "SN-1" && l.Number != "SN-2") {
			t.Errorf("serial lot = %+v", l)
		}
	}
	wrong := p.receiptLine(pumpLine, "2")
	wrong.Serials = &[]string{"SN-3"}
	res := p.receiveRes(o.Id, wrong)
	if res.Status != http.StatusBadRequest || problemCode(res) != "serial_quantity" {
		t.Errorf("one serial for two = %d %s, want 400 serial_quantity", res.Status, res.Body)
	}
	if got := purReceived(t, p.order(t, o.Id), pumpLine); got != "2" {
		t.Errorf("pumps received = %s, want 2", got)
	}
}

// An inspected variant's received lot is in quarantine under Inventory's
// inspection hold; the undo takes the stock back out of quarantine anyway
// and releases that hold with it.
func TestPurchasingReceipts_InspectedVariantQuarantineAndUndo(t *testing.T) {
	t.Parallel()
	p := newPurchasingInstallation(t)
	steel := p.product(t, "Stålplate", "STP-1", map[string]any{"tracking": "lot"})
	okJSON(t, p.c, http.MethodPut, fmt.Sprintf("/api/v1/inventory/variants/%d/rules", steel.variant),
		invgen.InventoryVariantRulesInput{InspectOnReceipt: true, Revision: 0}, nil)
	o := p.sentOrder(t, purLine(steel.variant, "8"))
	in := p.receiptLine(o.Lines[0].Id, "8")
	in.Lot = &purgen.PurchasingReceiptLotInput{Number: strPtr("H-1001")}
	r := p.receive(t, o.Id, in)
	var mv invgen.InventoryMovement
	okJSON(t, p.c, http.MethodGet, fmt.Sprintf("/api/v1/inventory/movements/%d", r.Receipt.MovementId), nil, &mv)
	if mv.InspectionLots == nil || len(*mv.InspectionLots) != 1 {
		t.Errorf("the movement's inspection lots = %v, want one", mv.InspectionLots)
	}
	lotID := *r.Receipt.Lines[0].LotId
	l := p.lot(t, lotID)
	if l.Status != "quarantine" || l.OnHand != 8 {
		t.Fatalf("the received lot = %s on hand %v, want quarantine and 8", l.Status, l.OnHand)
	}
	active := 0
	for _, h := range l.Holds {
		if h.ReleasedAt == nil && h.Cause == "inspection" {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("the lot's active inspection holds = %d (%+v), want 1", active, l.Holds)
	}

	res := p.undo(r.Receipt.Id)
	if res.Status != http.StatusOK {
		t.Fatalf("undo of a quarantined receipt = %d %s, want 200", res.Status, res.Body)
	}
	var u purgen.PurchasingReceiptResult
	res.JSON(&u)
	if u.Receipt.Status != "undone" || u.Order.Status != purgen.PurchasingOrderStatusSent || u.Receipt.ReversalMovementId == nil {
		t.Fatalf("after the undo: receipt %s, order %s", u.Receipt.Status, u.Order.Status)
	}
	var rev invgen.InventoryMovement
	okJSON(t, p.c, http.MethodGet, fmt.Sprintf("/api/v1/inventory/movements/%d", *u.Receipt.ReversalMovementId), nil, &rev)
	if rev.Kind != invgen.ReceiptReversal {
		t.Errorf("the reversal's kind = %s", rev.Kind)
	}
	l = p.lot(t, lotID)
	if l.OnHand != 0 {
		t.Errorf("the lot after the undo holds %v, want 0", l.OnHand)
	}
	for _, h := range l.Holds {
		if h.Cause == "inspection" && h.ReleasedAt == nil {
			t.Errorf("the inspection hold is still active after the undo: %+v", h)
		}
	}
	if l.Status != "available" {
		t.Errorf("the lot's status after the undo = %s, want available", l.Status)
	}
	if got := p.open(t, steel.variant); got != "8" {
		t.Errorf("on order after the undo = %q, want 8", got)
	}
}

// A product discontinued after its order was sent still has its open line
// received (A6): Purchasing does not judge the status, and Inventory's
// ledger takes a receipt of a discontinued variant.
func TestPurchasingReceipts_DiscontinuedProductIsReceived(t *testing.T) {
	t.Parallel()
	p := newPurchasingInstallation(t)
	old := p.product(t, "Gammel skrue", "OLD-1", nil)
	o := p.sentOrder(t, purLine(old.variant, "5"))
	var updated struct {
		Status string `json:"status"`
	}
	okJSON(t, p.c, http.MethodPut, fmt.Sprintf("/api/v1/products/%d", old.id), map[string]any{
		"name": "Gammel skrue", "type": "Goods", "status": "Discontinued", "taxCategoryId": p.tax,
	}, &updated)
	if updated.Status != "Discontinued" {
		t.Fatalf("product status = %s", updated.Status)
	}
	r := p.receive(t, o.Id, p.receiptLine(o.Lines[0].Id, "5"))
	if r.Order.Status != purgen.PurchasingOrderStatusReceived {
		t.Errorf("order = %s, want received", r.Order.Status)
	}
	if s := p.stockAt(t, old.variant); s.OnHand != 5 {
		t.Errorf("on hand of the discontinued variant = %v, want 5", s.OnHand)
	}
}

// An undo whose stock has left the shelf is Inventory's insufficient_stock
// and changes nothing; with the stock back it takes the receipt out of
// Inventory and puts the quantity back on order.
func TestPurchasingReceipts_UndoRefusedWhenTheStockIsGone(t *testing.T) {
	t.Parallel()
	p := newPurchasingInstallation(t)
	nut := p.product(t, "Mutter M8", "NUT-M8", nil)
	o := p.sentOrder(t, purLine(nut.variant, "5"))
	line := o.Lines[0].Id
	r := p.receive(t, o.Id, p.receiptLine(line, "5"))
	if p.open(t, nut.variant) != "" {
		t.Fatal("something left on order after the full receipt")
	}

	p.setOnHand(t, nut.variant, nil, 0)
	lines := purRefused(t, p.undo(r.Receipt.Id), http.StatusConflict, "insufficient_stock", "undo with the stock gone")
	if len(lines) != 1 || lines[0].Index != 0 {
		t.Errorf("insufficient_stock lines = %+v, want the receipt's first", lines)
	}
	if got := p.order(t, o.Id); got.Status != purgen.PurchasingOrderStatusReceived || purReceived(t, got, line) != "5" {
		t.Errorf("a refused undo changed the order: %s, received %s", got.Status, purReceived(t, got, line))
	}
	if n := modtest.One[int](t, p.h, `SELECT count(*)::int FROM inventory.movements WHERE kind = 'receipt_reversal'`); n != 0 {
		t.Errorf("%d reversals after a refused undo", n)
	}

	p.setOnHand(t, nut.variant, nil, 5)
	res := p.undo(r.Receipt.Id)
	if res.Status != http.StatusOK {
		t.Fatalf("undo with the stock there = %d %s", res.Status, res.Body)
	}
	var u purgen.PurchasingReceiptResult
	res.JSON(&u)
	if u.Order.Status != purgen.PurchasingOrderStatusSent || purReceived(t, u.Order, line) != "0" {
		t.Errorf("order after the undo = %s, received %s", u.Order.Status, purReceived(t, u.Order, line))
	}
	if s := p.stockAt(t, nut.variant); s.OnHand != 0 || s.OnOrder != 5 {
		t.Errorf("Inventory after the undo: on hand %v, on order %v; want 0 and 5", s.OnHand, s.OnOrder)
	}
	if got := p.open(t, nut.variant); got != "5" {
		t.Errorf("PurchaseSupply after the undo = %q, want 5", got)
	}
}

// Inventory's low-stock list counts what Purchasing has on order towards a
// placement's reorder point (purchasing design P4): a sent order lifts a
// low placement out of the list, a partial receipt moves on order into on
// hand, and a receipt in full leaves nothing on order.
func TestPurchasingReceipts_LowStockOnOrder(t *testing.T) {
	t.Parallel()
	p := newPurchasingInstallation(t)
	washer := p.product(t, "Skive M8", "WSH-M8", nil)
	reorder, maximum := 10.0, 40.0
	res := p.c.Do(http.MethodPut, fmt.Sprintf("/api/v1/inventory/variants/%d/placements/%d", washer.variant, p.site),
		invgen.InventoryPlacementInput{PrimaryLocationId: &p.loc, ReorderPoint: &reorder, Maximum: &maximum},
		modtest.Header("If-None-Match", "*"))
	if res.Status != http.StatusCreated {
		t.Fatalf("placement = %d %s", res.Status, res.Body)
	}
	low := func() *invgen.InventoryLowStockRow {
		t.Helper()
		var page invgen.InventoryLowStockPage
		okJSON(t, p.c, http.MethodGet, fmt.Sprintf("/api/v1/inventory/low-stock?siteId=%d", p.site), nil, &page)
		for i := range page.Rows {
			if page.Rows[i].Variant.Id == washer.variant {
				return &page.Rows[i]
			}
		}
		return nil
	}
	if row := low(); row == nil || row.OnOrder != 0 {
		t.Fatalf("low stock before the order = %+v, want the washer with nothing on order", row)
	}

	// 8 on order: available 0 plus incoming 8 is still at or below 10.
	o := p.sentOrder(t, purLine(washer.variant, "8"))
	row := low()
	if row == nil || row.OnOrder != 8 || row.Incoming != 8 || row.OnHand != 0 {
		t.Fatalf("low stock with 8 on order = %+v", row)
	}
	p.receive(t, o.Id, p.receiptLine(o.Lines[0].Id, "3"))
	row = low()
	if row == nil || row.OnOrder != 5 || row.OnHand != 3 {
		t.Fatalf("low stock after receiving 3 = %+v, want on order 5 and on hand 3", row)
	}
	p.receive(t, o.Id, p.receiptLine(o.Lines[0].Id, "5"))
	row = low()
	if row == nil || row.OnOrder != 0 || row.OnHand != 8 {
		t.Fatalf("low stock after the full receipt = %+v, want on order 0 and on hand 8", row)
	}
}
