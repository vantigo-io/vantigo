package purchasing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// Receipts (delivery B, P24–P30): a receive posts one receipt and one
// ledger receipt, the order's state follows its lines, and an undo takes it
// back. Inventory is the fake ledger here; internal/integration receives
// against the real one.

// receiver may do everything a receipt needs, and send orders to receive.
var receiver = []string{"purchasing:access", "purchasing:orders-manage", "purchasing:orders-send", "purchasing:receive"}

func receiptsPath(orderID int64) string { return orderPath(orderID, "receipts") }

func receiptPath(id int64, rest ...string) string {
	p := fmt.Sprintf("/api/v1/purchasing/receipts/%d", id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

// sentOrder is a draft for the Norwegian supplier at the main site with
// lines, marked as sent.
func sentOrder(t *testing.T, c *modtest.Client, lines ...gen.PurchasingLineInput) gen.PurchasingOrder {
	t.Helper()
	return markSent(t, c, createOrder(t, c, supNorsk, lines...))
}

// rline is a receipt line of an order line onto a location.
func rline(orderLine int64, qty string, loc int64) gen.PurchasingReceiptLineInput {
	return gen.PurchasingReceiptLineInput{OrderLineId: orderLine, Quantity: qty, LocationId: loc}
}

func receiptInput(lines ...gen.PurchasingReceiptLineInput) gen.PurchasingReceiptInput {
	return gen.PurchasingReceiptInput{ClientRequestId: uuid.New(), Lines: lines}
}

func receive(t *testing.T, c *modtest.Client, orderID int64, in gen.PurchasingReceiptInput) gen.PurchasingReceiptResult {
	t.Helper()
	res := c.Do(http.MethodPost, receiptsPath(orderID), in)
	mustStatus(t, res, http.StatusCreated, "receive")
	var r gen.PurchasingReceiptResult
	res.JSON(&r)
	return r
}

func received(t *testing.T, o gen.PurchasingOrder, lineID int64) string {
	t.Helper()
	for _, l := range o.Lines {
		if l.Id == lineID {
			return l.Received
		}
	}
	t.Fatalf("no line %d", lineID)
	return ""
}

func openOf(t *testing.T, h *harness, variant int32) string {
	t.Helper()
	rows, err := purchasing.Module().PurchaseSupply(h.Deps()).OpenByVariants(context.Background(), i64(siteMain), []int32{variant})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		return "0"
	}
	return rows[0].Quantity
}

// A partial receipt leaves the order partially received with its remainder
// on order; the next one receives it in full. Each is one ledger receipt
// with the receipt as its document and the order as its source line, a
// number of its own series, and an event without free text.
func TestReceive_PartialThenFull(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	bolts := variantLine(varBolt, "3") // 3 boxes of 10
	bolts.PackagingId = i32(packBox)
	o := sentOrder(t, c, bolts, variantLine(varNut, "5"))
	bolt, nut := o.Lines[0].Id, o.Lines[1].Id
	if got := openOf(t, h, varBolt); got != "30" {
		t.Fatalf("on order before = %s, want 30", got)
	}

	box := rline(bolt, "1", locShelfA)
	box.InPackaging = ptrTo(true)
	in := receiptInput(box, rline(nut, "5", locShelfB))
	in.DeliveryNote, in.Note = str("FS-1001"), str("Én eske manglet")
	r := receive(t, c, o.Id, in)
	if r.Receipt.Number != 1 || r.Receipt.Status != "posted" || r.Receipt.DeliveryNote != "FS-1001" || len(r.Receipt.Lines) != 2 {
		t.Fatalf("receipt = %+v", r.Receipt)
	}
	if l := r.Receipt.Lines[0]; l.Quantity != "10" || l.EnteredQuantity != "1" || !l.InPackaging || deref(l.PackagingName) != "Eske" || l.LocationCode != "HOVED-A1" {
		t.Errorf("box line = %+v", l)
	}
	if r.Order.Status != gen.PurchasingOrderStatusPartiallyReceived || received(t, *r.Order, bolt) != "10" || received(t, *r.Order, nut) != "5" {
		t.Fatalf("order after a partial receipt = %s, bolt %s", r.Order.Status, received(t, *r.Order, bolt))
	}
	n, led, src := h.ledger.last()
	if n != 1 || led.Document != (contracts.StockSource{Module: "purchasing", Kind: "receipt", ID: r.Receipt.Id}) ||
		src != (contracts.StockSource{Module: "purchasing", Kind: "order", ID: o.Id}) || led.SiteID != siteMain ||
		led.SupplierReference != "FS-1001" || led.SupplierName != "Norsk Stål AS" || len(led.Lines) != 2 ||
		led.Lines[0].Quantity != "10" || led.Lines[0].LineID != bolt || led.Lines[0].ToLocationID != locShelfA {
		t.Fatalf("ledger receipt = %d %+v from %+v", n, led, src)
	}
	if r.Receipt.MovementId == 0 {
		t.Error("the receipt names no movement")
	}
	if got := openOf(t, h, varBolt); got != "20" {
		t.Errorf("on order after the partial receipt = %s, want 20", got)
	}
	if got := openOf(t, h, varNut); got != "0" {
		t.Errorf("nuts on order = %s, want 0", got)
	}

	r2 := receive(t, c, o.Id, receiptInput(rline(bolt, "20", locShelfA)))
	if r2.Receipt.Number != 2 || r2.Order.Status != gen.PurchasingOrderStatusReceived || received(t, *r2.Order, bolt) != "30" {
		t.Fatalf("second receipt %d, order %s", r2.Receipt.Number, r2.Order.Status)
	}
	if got := openOf(t, h, varBolt); got != "0" {
		t.Errorf("on order after the full receipt = %s, want 0", got)
	}
	if n := eventsNamed(t, h, "purchasing.receipt.posted", o.Id); n != 2 {
		t.Errorf("posted events = %d, want 2", n)
	}
	var payload []byte
	if err := h.Pool().QueryRow(context.Background(), `SELECT payload FROM platform.events WHERE name = 'purchasing.receipt.posted' AND aggregate_id = $1 ORDER BY id LIMIT 1`,
		fmt.Sprint(o.Id)).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var ev contracts.PurchaseReceiptPosted
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.ReceiptID != r.Receipt.Id || ev.SiteID != siteMain || ev.MovementID != r.Receipt.MovementId || len(ev.Lines) != 2 || ev.Lines[0].Quantity != "10" {
		t.Errorf("event = %+v", ev)
	}
	for _, text := range []string{"FS-1001", "manglet", "Norsk"} {
		if strings.Contains(string(payload), text) {
			t.Errorf("the event carries free text %q: %s", text, payload)
		}
	}
	// The list and the receipt read; neither answers a price.
	res := c.Do(http.MethodGet, receiptsPath(o.Id), nil)
	mustStatus(t, res, http.StatusOK, "list receipts")
	var list gen.PurchasingReceiptList
	res.JSON(&list)
	if len(list.Items) != 2 || list.Items[0].Number != 2 {
		t.Errorf("receipts = %+v", list.Items)
	}
	if strings.Contains(strings.ToLower(string(res.Body)), "price") {
		t.Errorf("the receipts answer a price: %s", res.Body)
	}
	res = c.Do(http.MethodGet, receiptPath(r.Receipt.Id), nil)
	mustStatus(t, res, http.StatusOK, "get receipt")
	if res = c.Do(http.MethodGet, receiptPath(999999), nil); res.Status != http.StatusNotFound {
		t.Errorf("an unknown receipt = %d", res.Status)
	}
	// The change log names both receipts and the order received.
	if n := h.Count(t, `SELECT count(*) FROM purchasing.order_changes WHERE order_id = $1 AND actions::text LIKE '%receipt_posted%'`, o.Id); n != 2 {
		t.Errorf("change log receipts = %d", n)
	}
}

func ptrTo[T any](v T) *T { return &v }

// deliveredOf is the order a delivered mark answers to a holder of
// purchasing:access.
func deliveredOf(t *testing.T, res *modtest.Response) gen.PurchasingOrder {
	t.Helper()
	var d gen.PurchasingDeliveredResult
	res.JSON(&d)
	if d.Order == nil {
		t.Fatalf("no order in %s", res.Body)
	}
	return *d.Order
}

// More than the remainder is refused with over_receipt naming the line; the
// same request id with acceptOver then receives it, and the line holds more
// than ordered (P26).
func TestReceive_OverReceipt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"), variantLine(varBolt, "10"))
	nut := o.Lines[0].Id
	in := receiptInput(rline(o.Lines[1].Id, "2", locShelfA), rline(nut, "4", locShelfA), rline(nut, "3", locShelfB))
	p := expectRefusal(t, c.Do(http.MethodPost, receiptsPath(o.Id), in), http.StatusConflict, "over_receipt", "over")
	if p.Lines == nil || len(*p.Lines) != 2 || (*p.Lines)[0].Index != 1 || (*p.Lines)[1].Index != 2 {
		t.Fatalf("over_receipt lines = %+v", p.Lines)
	}
	if n, _, _ := h.ledger.last(); n != 0 || h.Count(t, `SELECT count(*) FROM purchasing.receipts`) != 0 {
		t.Fatal("a refused receipt left something behind")
	}
	in.AcceptOver = ptrTo(true)
	r := receive(t, c, o.Id, in)
	if received(t, *r.Order, nut) != "7" || r.Order.Status != gen.PurchasingOrderStatusPartiallyReceived {
		t.Fatalf("over-received = %s, order %s", received(t, *r.Order, nut), r.Order.Status)
	}
	if got := openOf(t, h, varNut); got != "0" {
		t.Errorf("nuts on order after an over-receipt = %s, want 0", got)
	}
}

// A replay of a clientRequestId answers the first receipt and receives
// nothing again; another body, or another order, under it is
// client_request_reused (A11).
func TestReceive_IdempotentReplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"))
	other := sentOrder(t, c, variantLine(varNut, "5"))
	in := receiptInput(rline(o.Lines[0].Id, "2", locShelfA))
	first := receive(t, c, o.Id, in)
	again := receive(t, c, o.Id, in)
	if again.Receipt.Id != first.Receipt.Id || received(t, *again.Order, o.Lines[0].Id) != "2" {
		t.Fatalf("replay = receipt %d, received %s", again.Receipt.Id, received(t, *again.Order, o.Lines[0].Id))
	}
	if n, _, _ := h.ledger.last(); n != 1 {
		t.Errorf("ledger receipts = %d, want 1", n)
	}
	changed := in
	changed.Lines = []gen.PurchasingReceiptLineInput{rline(o.Lines[0].Id, "3", locShelfA)}
	expectRefusal(t, c.Do(http.MethodPost, receiptsPath(o.Id), changed), http.StatusConflict, "client_request_reused", "another body")
	elsewhere := in
	elsewhere.Lines = []gen.PurchasingReceiptLineInput{rline(other.Lines[0].Id, "2", locShelfA)}
	expectRefusal(t, c.Do(http.MethodPost, receiptsPath(other.Id), elsewhere), http.StatusConflict, "client_request_reused", "another order")
}

// A deadlock is retried with the whole transaction, three tries in all
// (A7); after the third nothing is received and the answer is a 409.
func TestReceive_RetriedThreeTimes(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"))
	h.ledger.set(func(l *fakeLedger) { l.deadlocks = 2 })
	r := receive(t, c, o.Id, receiptInput(rline(o.Lines[0].Id, "1", locShelfA)))
	if r.Receipt.Number != 1 {
		t.Fatalf("after two deadlocks = receipt %d, want number 1 (the series rolled back)", r.Receipt.Number)
	}
	h.ledger.set(func(l *fakeLedger) { l.deadlocks = 3 })
	expectRefusal(t, c.Do(http.MethodPost, receiptsPath(o.Id), receiptInput(rline(o.Lines[0].Id, "1", locShelfA))),
		http.StatusConflict, "version_conflict", "three deadlocks")
	if n := h.Count(t, `SELECT count(*) FROM purchasing.receipts`); n != 1 {
		t.Errorf("receipts = %d, want 1", n)
	}
	if got := received(t, getOrder(t, c, o.Id), o.Lines[0].Id); got != "1" {
		t.Errorf("received = %s, want 1", got)
	}
}

// Two receipts of one order at once queue on the order's lock: the second
// sees the first's quantities, and a remainder gone is over_receipt.
func TestReceive_ConcurrentReceiptsOfOneOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"))
	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			res := c.Do(http.MethodPost, receiptsPath(o.Id), receiptInput(rline(o.Lines[0].Id, "4", locShelfA)))
			statuses[i] = res.Status
		})
	}
	wg.Wait()
	created, refused := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			refused++
		}
	}
	if created != 1 || refused != 1 {
		t.Fatalf("concurrent receipts = %v, want one 201 and one 409", statuses)
	}
	if got := received(t, getOrder(t, c, o.Id), o.Lines[0].Id); got != "4" {
		t.Errorf("received = %s, want 4", got)
	}
}

// A lot line, a serial line and a lot by id reach the ledger as given, and
// the receipt shows them; the ledger's refusals keep Inventory's codes and
// name the request's line, 400 for a line's input and 409 for the stock.
func TestReceive_LotsSerialsAndTheLedgersRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"), variantLine(varBolt, "2"))
	nut, bolt := o.Lines[0].Id, o.Lines[1].Id
	lot := rline(nut, "3", locShelfA)
	exp := openapi_types.Date{Time: day(2027, 6, 30)}
	lot.Lot = &gen.PurchasingReceiptLotInput{Number: str("L-778"), SupplierLot: str("SUP-9"), Expiry: &exp}
	byID := rline(nut, "2", locShelfB)
	byID.LotId = i64(4242)
	serials := rline(bolt, "2", locShelfA)
	serials.Serials = &[]string{"SN-1", " SN-2 "}
	r := receive(t, c, o.Id, receiptInput(lot, byID, serials))
	_, led, _ := h.ledger.last()
	if l := led.Lines[0]; l.Lot == nil || l.Lot.Number != "L-778" || l.Lot.SupplierLot != "SUP-9" || l.Lot.Expiry == nil || !l.Lot.Expiry.Equal(day(2027, 6, 30)) {
		t.Errorf("lot line = %+v %+v", l, l.Lot)
	}
	if led.Lines[1].LotID != 4242 || len(led.Lines[2].Serials) != 2 || led.Lines[2].Serials[1] != "SN-2" {
		t.Errorf("lot by id / serials = %+v / %+v", led.Lines[1], led.Lines[2])
	}
	if l := r.Receipt.Lines[0]; deref(l.LotNumber) != "L-778" || l.Expiry == nil || l.Expiry.Day() != 30 {
		t.Errorf("receipt lot line = %+v", l)
	}
	if l := r.Receipt.Lines[2]; len(l.Serials) != 2 {
		t.Errorf("receipt serial line = %+v", l)
	}

	o2 := sentOrder(t, c, variantLine(varNut, "5"))
	h.ledger.set(func(l *fakeLedger) {
		l.refuse = func(contracts.StockReceipt) error {
			return &contracts.StockRefusal{Code: contracts.StockLotRequired, Detail: "a lot", Lines: []contracts.StockLineRefusal{
				{Code: contracts.StockLotRequired, Index: 1, Detail: "a lot"}}}
		}
	})
	p := expectRefusal(t, c.Do(http.MethodPost, receiptsPath(o2.Id), receiptInput(rline(o2.Lines[0].Id, "1", locShelfA), rline(o2.Lines[0].Id, "1", locShelfB))),
		http.StatusBadRequest, "lot_required", "lot required")
	if p.Lines == nil || (*p.Lines)[0].Index != 1 {
		t.Errorf("lines = %+v", p.Lines)
	}
	h.ledger.set(func(l *fakeLedger) {
		l.refuse = func(contracts.StockReceipt) error {
			return &contracts.StockRefusal{Code: contracts.StockLotBlocked, Lines: []contracts.StockLineRefusal{{Code: contracts.StockLotBlocked, Index: 0}}}
		}
	})
	expectRefusal(t, c.Do(http.MethodPost, receiptsPath(o2.Id), receiptInput(rline(o2.Lines[0].Id, "1", locShelfA))), http.StatusConflict, "lot_blocked", "lot blocked")
	if n := h.Count(t, `SELECT count(*) FROM purchasing.receipts WHERE order_id = $1`, o2.Id); n != 0 {
		t.Errorf("a refused receipt left %d receipts", n)
	}
}

// A line of a product discontinued since the order was sent is received
// (A6): Purchasing does not judge the status; the ledger receives it.
func TestReceive_DiscontinuedVariant(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"))
	h.catalog.update(varNut, func(v *contracts.VariantEntry) { v.ProductStatus = "Discontinued" })
	r := receive(t, c, o.Id, receiptInput(rline(o.Lines[0].Id, "5", locShelfA)))
	if r.Order.Status != gen.PurchasingOrderStatusReceived {
		t.Errorf("order = %s", r.Order.Status)
	}
}

// What a receipt refuses of its own: an order not sent, a line that is not
// an open goods line of the order, a bad quantity, packagings on a line
// without one, a request without lines.
func TestReceive_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	draft := createOrder(t, c, supNorsk, variantLine(varNut, "5"))
	expectRefusal(t, c.Do(http.MethodPost, receiptsPath(draft.Id), receiptInput(rline(draft.Lines[0].Id, "1", locShelfA))),
		http.StatusConflict, "order_not_sent", "a draft")
	expectRefusal(t, c.Do(http.MethodPost, receiptsPath(999999), receiptInput(rline(1, "1", locShelfA))), http.StatusNotFound, "not_found", "unknown order")

	service := variantLine(varService, "2")
	o := sentOrder(t, c, variantLine(varNut, "5"), textLine("Frakt", "1", "250"), service, variantLine(varBolt, "5"))
	nut, text, srv, bolt := o.Lines[0].Id, o.Lines[1].Id, o.Lines[2].Id, o.Lines[3].Id
	res := c.Do(http.MethodPost, orderPath(o.Id, "lines", itoa(bolt), "close"), version(o))
	mustStatus(t, res, http.StatusOK, "close the bolts")
	p := expectRefusal(t, c.Do(http.MethodPost, receiptsPath(o.Id), receiptInput(rline(text, "1", locShelfA), rline(srv, "1", locShelfA),
		rline(bolt, "1", locShelfA), rline(draft.Lines[0].Id, "1", locShelfA), rline(nut, "0", locShelfA), rline(nut, "1.0001", locShelfA))),
		http.StatusBadRequest, "line_not_receivable", "not receivable")
	codes := []string{}
	for _, l := range *p.Lines {
		codes = append(codes, l.Code)
	}
	if strings.Join(codes, ",") != "line_not_receivable,line_not_receivable,line_not_receivable,line_not_receivable,quantity_invalid,quantity_invalid" {
		t.Errorf("codes = %v", codes)
	}
	pack := rline(nut, "1", locShelfA)
	pack.InPackaging = ptrTo(true)
	expectRefusal(t, c.Do(http.MethodPost, receiptsPath(o.Id), receiptInput(pack)), http.StatusBadRequest, "packaging_not_found", "no packaging")
	expectRefusal(t, c.Do(http.MethodPost, receiptsPath(o.Id), gen.PurchasingReceiptInput{ClientRequestId: uuid.New(), Lines: []gen.PurchasingReceiptLineInput{}}),
		http.StatusBadRequest, "", "no lines")
}

// An undo takes the receipt back through ReverseReceipt and lowers what is
// received; the order's state goes back with it (A16), the receipt is kept
// undone, and a second undo is receipt_undone.
func TestUndo_TakesTheReceiptBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"))
	nut := o.Lines[0].Id
	first := receive(t, c, o.Id, receiptInput(rline(nut, "2", locShelfA)))
	second := receive(t, c, o.Id, receiptInput(rline(nut, "3", locShelfA)))
	if second.Order.Status != gen.PurchasingOrderStatusReceived {
		t.Fatalf("order = %s", second.Order.Status)
	}
	res := c.Do(http.MethodPost, receiptPath(second.Receipt.Id, "undo"), gen.PurchasingUndoInput{Note: str("Feil vare")})
	mustStatus(t, res, http.StatusOK, "undo")
	var u gen.PurchasingReceiptResult
	res.JSON(&u)
	if u.Receipt.Status != "undone" || u.Receipt.UndoneAt == nil || deref(u.Receipt.UndoNote) != "Feil vare" || u.Receipt.ReversalMovementId == nil {
		t.Fatalf("undone receipt = %+v", u.Receipt)
	}
	if u.Order.Status != gen.PurchasingOrderStatusPartiallyReceived || received(t, *u.Order, nut) != "2" {
		t.Fatalf("order after the undo = %s, received %s", u.Order.Status, received(t, *u.Order, nut))
	}
	if got := openOf(t, h, varNut); got != "3" {
		t.Errorf("on order after the undo = %s, want 3", got)
	}
	expectRefusal(t, c.Do(http.MethodPost, receiptPath(second.Receipt.Id, "undo"), gen.PurchasingUndoInput{}), http.StatusConflict, "receipt_undone", "twice")
	res = c.Do(http.MethodPost, receiptPath(first.Receipt.Id, "undo"), gen.PurchasingUndoInput{})
	mustStatus(t, res, http.StatusOK, "undo the first")
	res.JSON(&u)
	if u.Order.Status != gen.PurchasingOrderStatusSent {
		t.Errorf("nothing left received = %s, want sent", u.Order.Status)
	}
	if h.ledger.reversalCount() != 2 || eventsNamed(t, h, "purchasing.receipt.undone", o.Id) != 2 {
		t.Errorf("reversals %d, events %d", h.ledger.reversalCount(), eventsNamed(t, h, "purchasing.receipt.undone", o.Id))
	}

	// A closed order stays closed (A16).
	o2 := sentOrder(t, c, variantLine(varNut, "5"))
	r := receive(t, c, o2.Id, receiptInput(rline(o2.Lines[0].Id, "2", locShelfA)))
	res = c.Do(http.MethodPost, orderPath(o2.Id, "close"), version(*r.Order))
	mustStatus(t, res, http.StatusOK, "close")
	res = c.Do(http.MethodPost, receiptPath(r.Receipt.Id, "undo"), gen.PurchasingUndoInput{})
	mustStatus(t, res, http.StatusOK, "undo on a closed order")
	res.JSON(&u)
	if u.Order.Status != gen.PurchasingOrderStatusClosed {
		t.Errorf("closed order after an undo = %s", u.Order.Status)
	}
}

// An undo is refused when Inventory refuses — the stock is gone, named by
// the receipt's line though a serial line was expanded —, and when a line
// would be left with less received than invoiced (A10).
func TestUndo_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varBolt, "2"), variantLine(varNut, "5"))
	serials := rline(o.Lines[0].Id, "2", locShelfA)
	serials.Serials = &[]string{"SN-1", "SN-2"}
	r := receive(t, c, o.Id, receiptInput(serials, rline(o.Lines[1].Id, "5", locShelfA)))
	h.ledger.set(func(l *fakeLedger) {
		l.refuseRev = func(contracts.StockSource) error {
			return &contracts.StockRefusal{Code: contracts.StockInsufficientStock, Lines: []contracts.StockLineRefusal{
				{Code: contracts.StockInsufficientStock, Index: 2}}} // the third movement line: the nuts
		}
	})
	p := expectRefusal(t, c.Do(http.MethodPost, receiptPath(r.Receipt.Id, "undo"), gen.PurchasingUndoInput{}), http.StatusConflict, "insufficient_stock", "stock gone")
	if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].Index != 1 {
		t.Errorf("lines = %+v, want the receipt's second line", p.Lines)
	}
	h.ledger.set(func(l *fakeLedger) { l.refuseRev = nil })
	h.Exec(t, `UPDATE purchasing.order_lines SET invoiced = 4 WHERE id = $1`, o.Lines[1].Id)
	p = expectRefusal(t, c.Do(http.MethodPost, receiptPath(r.Receipt.Id, "undo"), gen.PurchasingUndoInput{}), http.StatusConflict, "receipt_invoiced", "invoiced")
	if p.Lines == nil || (*p.Lines)[0].Index != 1 {
		t.Errorf("lines = %+v", p.Lines)
	}
	if got := received(t, getOrder(t, c, o.Id), o.Lines[1].Id); got != "5" || h.ledger.reversalCount() != 0 {
		t.Errorf("a refused undo changed something: received %s, reversals %d", got, h.ledger.reversalCount())
	}
	expectRefusal(t, c.Do(http.MethodPost, receiptPath(999999, "undo"), gen.PurchasingUndoInput{}), http.StatusNotFound, "not_found", "unknown")
}

// A text or service line is marked delivered by hand; with every goods line
// received that makes the order received, and taking the mark off opens it
// again. A goods line is received, not marked.
func TestDelivered_TextAndServiceLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"), textLine("Frakt", "1", "250"))
	nut, text := o.Lines[0].Id, o.Lines[1].Id
	r := receive(t, c, o.Id, receiptInput(rline(nut, "5", locShelfA)))
	if r.Order.Status != gen.PurchasingOrderStatusPartiallyReceived {
		t.Fatalf("order with the freight open = %s", r.Order.Status)
	}
	mark := func(o gen.PurchasingOrder, line int64, delivered bool) *modtest.Response {
		return c.Do(http.MethodPost, orderPath(o.Id, "lines", itoa(line), "delivered"),
			gen.PurchasingDeliveredInput{ExpectedUpdatedAt: o.UpdatedAt, Delivered: delivered})
	}
	h.Advance(time.Second) // so the mark moves the order's updatedAt
	res := mark(*r.Order, text, true)
	mustStatus(t, res, http.StatusOK, "mark delivered")
	done := deliveredOf(t, res)
	if done.Status != gen.PurchasingOrderStatusReceived || done.Lines[1].DeliveredAt == nil {
		t.Fatalf("after the mark = %s %v", done.Status, done.Lines[1].DeliveredAt)
	}
	// The mark that completes the order logs the end as a receipt does (A48).
	if hist := historyOf(t, c, o.Id); hist.Changes[0].Actions[0].Action != "received" || hist.Changes[1].Actions[0].Action != "line_delivered" {
		t.Errorf("the change log after the mark = %+v / %+v", hist.Changes[0].Actions, hist.Changes[1].Actions)
	}
	expectRefusal(t, mark(*r.Order, text, false), http.StatusConflict, "version_conflict", "stale")
	res = mark(done, text, false)
	mustStatus(t, res, http.StatusOK, "take the mark off")
	if back := deliveredOf(t, res); back.Status != gen.PurchasingOrderStatusPartiallyReceived || back.Lines[1].DeliveredAt != nil {
		t.Errorf("after taking it off = %s", back.Status)
	}
	expectRefusal(t, mark(getOrder(t, c, o.Id), nut, true), http.StatusConflict, "line_not_deliverable", "a goods line")
}

// The receive screen's read answers the open goods lines with their
// remainder, tracking and default location, and the site's active
// locations — no price; a scanned code names the line and what one scan
// adds.
func TestReceiveForm_AndScannedCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	h.catalog.update(varNut, func(v *contracts.VariantEntry) { v.Tracking = "lot"; v.ExpiryDays = i32(365) })
	h.stock.holding = []contracts.StockLocation{
		{VariantID: varBolt, LocationID: locShelfA, Quantity: "40"},
		{VariantID: varBolt, LocationID: locShelfB, Quantity: "2", Primary: true},
		{VariantID: varNut, LocationID: locShelfA, Quantity: "1"},
		{VariantID: varNut, LocationID: locShelfB, Quantity: "9"},
	}
	bolts := variantLine(varBolt, "3")
	bolts.PackagingId = i32(packBox)
	o := sentOrder(t, c, bolts, variantLine(varNut, "5"), textLine("Frakt", "1", "250"))
	receive(t, c, o.Id, receiptInput(rline(o.Lines[0].Id, "5", locShelfA)))
	res := c.Do(http.MethodGet, orderPath(o.Id, "receive"), nil)
	mustStatus(t, res, http.StatusOK, "receive form")
	if strings.Contains(strings.ToLower(string(res.Body)), "price") {
		t.Errorf("the receive screen answers a price: %s", res.Body)
	}
	var f gen.PurchasingReceiveForm
	res.JSON(&f)
	if len(f.Lines) != 2 || len(f.Locations) != 3 || f.SiteName != "Hovedlager" {
		t.Fatalf("form = %+v", f)
	}
	b, n := f.Lines[0], f.Lines[1]
	if b.Open != "25" || b.Received != "5" || deref(b.PackagingQuantity) != "10" || b.Tracking != "none" || b.DefaultLocationId == nil || *b.DefaultLocationId != locShelfB {
		t.Errorf("bolt line = %+v", b)
	}
	if n.Tracking != "lot" || n.ExpiryDays == nil || *n.ExpiryDays != 365 || n.DefaultLocationId == nil || *n.DefaultLocationId != locShelfB {
		t.Errorf("nut line = %+v", n)
	}
	code := func(q string) []gen.PurchasingReceiveCode {
		t.Helper()
		res := c.Do(http.MethodGet, orderPath(o.Id, "receive", "code")+"?code="+q, nil)
		mustStatus(t, res, http.StatusOK, "code "+q)
		var l gen.PurchasingReceiveCodeList
		res.JSON(&l)
		return l.Items
	}
	if m := code("7090000000077"); len(m) != 1 || m[0].OrderLineId != o.Lines[0].Id || m[0].Quantity != "10" || deref(m[0].PackagingName) != "Eske" {
		t.Errorf("box barcode = %+v", m)
	}
	if m := code("07090000000011"); len(m) != 1 || m[0].Quantity != "1" || m[0].PackagingId != nil {
		t.Errorf("a GTIN-14 of the bolt's EAN-13 = %+v", m)
	}
	if m := code("NOPE"); len(m) != 0 {
		t.Errorf("an unknown code = %+v", m)
	}
	expectRefusal(t, c.Do(http.MethodGet, orderPath(999999, "receive"), nil), http.StatusNotFound, "not_found", "unknown order")
	draft := createOrder(t, c, supNorsk)
	expectRefusal(t, c.Do(http.MethodGet, orderPath(draft.Id, "receive"), nil), http.StatusConflict, "order_not_sent", "draft form")
}

// Each new operation asks for its own key, and no other key alone admits it
// (§8): receiving, its screen, its code lookup, the undo and the delivered
// mark need purchasing:receive; the reads of receipts purchasing:access.
func TestReceipts_PermissionsUnderEachKeyAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"), textLine("Frakt", "1", "250"))
	r := receive(t, c, o.Id, receiptInput(rline(o.Lines[0].Id, "1", locShelfA)))
	keys := []string{"purchasing:access", "purchasing:orders-manage", "purchasing:orders-send", "purchasing:receive",
		"purchasing:supplier-items-manage", "purchasing:invoices-match", "purchasing:settings"}
	type op struct {
		method, path string
		body         func() any
		key          string
	}
	ops := []op{
		{http.MethodGet, orderPath(o.Id, "receive"), nil, "purchasing:receive"},
		{http.MethodGet, orderPath(o.Id, "receive", "code") + "?code=NUT-M8", nil, "purchasing:receive"},
		{http.MethodPost, receiptsPath(o.Id), func() any { return receiptInput(rline(o.Lines[0].Id, "1", locShelfA)) }, "purchasing:receive"},
		{http.MethodGet, receiptsPath(o.Id), nil, "purchasing:access"},
		{http.MethodGet, receiptPath(r.Receipt.Id), nil, "purchasing:access"},
		{http.MethodPost, receiptPath(999999, "undo"), func() any { return gen.PurchasingUndoInput{} }, "purchasing:receive"},
		{http.MethodPost, orderPath(o.Id, "lines", itoa(o.Lines[1].Id), "delivered"), func() any {
			return gen.PurchasingDeliveredInput{ExpectedUpdatedAt: o.UpdatedAt, Delivered: true}
		}, "purchasing:receive"},
	}
	for _, key := range keys {
		alone := h.SignIn(t, key)
		for _, op := range ops {
			var body any
			if op.body != nil {
				body = op.body()
			}
			res := alone.Do(op.method, op.path, body)
			if forbidden := res.Status == http.StatusForbidden; forbidden != (key != op.key) {
				t.Errorf("%s %s with %s alone = %d", op.method, op.path, key, res.Status)
			}
		}
	}
}
