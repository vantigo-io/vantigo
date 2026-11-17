package purchasing_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// The review of delivery B: receiving under purchasing:receive alone answers
// no price (A47), the receiving list, a replay after the order ended, the one
// rule for an order's end (A48), a person supplier's receipts, and the price
// a receipt line keeps (A49).

const receivingPath = "/api/v1/purchasing/receiving"

// priceFields are the keys that carry what was paid.
var priceFields = []string{`"unitPrice"`, `"discountPercent"`, `"lineNet"`, `"netTotal"`, `"currency"`}

func noPrice(t *testing.T, res *modtest.Response, what string) {
	t.Helper()
	for _, f := range priceFields {
		if strings.Contains(string(res.Body), f) {
			t.Errorf("%s answers %s: %s", what, f, res.Body)
		}
	}
}

// A caller with purchasing:receive and nothing else receives, reads the
// screen, looks a code up, lists what to receive, marks a line delivered and
// undoes a receipt, and none of the answers carries a price; a holder of
// purchasing:access as well gets the order with its prices.
func TestReceiveAlone_AnswersNoPrice(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	buyer := h.SignIn(t, receiver...)
	o := sentOrder(t, buyer, variantLine(varNut, "5"), textLine("Frakt", "1", "250"))
	nut, freight := o.Lines[0].Id, o.Lines[1].Id
	c := h.SignIn(t, "purchasing:receive")

	res := c.Do(http.MethodGet, receivingPath, nil)
	mustStatus(t, res, http.StatusOK, "the receiving list")
	noPrice(t, res, "the receiving list")
	res = c.Do(http.MethodGet, orderPath(o.Id, "receive"), nil)
	mustStatus(t, res, http.StatusOK, "the receive screen")
	noPrice(t, res, "the receive screen")
	res = c.Do(http.MethodGet, orderPath(o.Id, "receive", "code")+"?code=NUT-M8", nil)
	mustStatus(t, res, http.StatusOK, "a code")
	noPrice(t, res, "the code lookup")

	res = c.Do(http.MethodPost, receiptsPath(o.Id), receiptInput(rline(nut, "5", locShelfA)))
	mustStatus(t, res, http.StatusCreated, "receive")
	noPrice(t, res, "a receipt")
	var r gen.PurchasingReceiptResult
	res.JSON(&r)
	if r.Order != nil || r.Progress.Status != gen.PurchasingOrderProgressStatusPartiallyReceived || len(r.Progress.Lines) != 2 ||
		r.Progress.Lines[0].Received != "5" || r.Progress.Lines[0].Open != "0" || r.Progress.Lines[1].Open != "1" {
		t.Fatalf("the receipt's answer under receive alone = %+v", r)
	}

	res = c.Do(http.MethodPost, orderPath(o.Id, "lines", itoa(freight), "delivered"),
		gen.PurchasingDeliveredInput{ExpectedUpdatedAt: r.Progress.UpdatedAt, Delivered: true})
	mustStatus(t, res, http.StatusOK, "mark delivered")
	noPrice(t, res, "a delivered mark")
	var d gen.PurchasingDeliveredResult
	res.JSON(&d)
	if d.Order != nil || d.Progress.Status != gen.PurchasingOrderProgressStatusReceived || d.Progress.Lines[1].DeliveredAt == nil {
		t.Fatalf("the mark's answer under receive alone = %+v", d)
	}
	res = c.Do(http.MethodPost, orderPath(o.Id, "lines", itoa(freight), "delivered"),
		gen.PurchasingDeliveredInput{ExpectedUpdatedAt: d.Progress.UpdatedAt, Delivered: false})
	mustStatus(t, res, http.StatusOK, "take the mark off")
	noPrice(t, res, "taking the mark off")

	res = c.Do(http.MethodPost, receiptPath(r.Receipt.Id, "undo"), gen.PurchasingUndoInput{})
	mustStatus(t, res, http.StatusOK, "undo")
	noPrice(t, res, "an undo")
	res.JSON(&r)
	if r.Order != nil || r.Progress.Status != gen.PurchasingOrderProgressStatusSent {
		t.Fatalf("the undo's answer under receive alone = %+v", r)
	}

	// With purchasing:access the order comes with its prices.
	again := receive(t, buyer, o.Id, receiptInput(rline(nut, "1", locShelfA)))
	if again.Order == nil || again.Order.Lines[0].UnitPrice == "" || again.Progress.Lines[0].Received != "1" {
		t.Errorf("the answer to a holder of access = %+v", again)
	}
}

// The receiving list names every sent or partially received order with a
// goods line still to come, the earliest expected first, with how many lines
// are left; a draft, an order received in full, one whose goods are closed
// and one of text lines only are not on it. Only purchasing:receive reads it.
func TestReceivingList(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	expected := func(o gen.PurchasingOrder, day time.Time) gen.PurchasingOrder {
		t.Helper()
		u := updateOf(o)
		u.ExpectedOn = dateIn(day)
		res := c.Do(http.MethodPut, orderPath(o.Id), u)
		mustStatus(t, res, http.StatusOK, "expected day")
		return orderOf(t, res)
	}
	later := markSent(t, c, expected(createOrder(t, c, supNorsk, variantLine(varNut, "5"), variantLine(varBolt, "2")), h.Now().AddDate(0, 0, 9)))
	late := markSent(t, c, expected(createOrder(t, c, supNorsk, variantLine(varNut, "3")), h.Now().AddDate(0, 0, -2)))
	createOrder(t, c, supNorsk, variantLine(varNut, "1")) // a draft
	full := sentOrder(t, c, variantLine(varNut, "2"))
	receive(t, c, full.Id, receiptInput(rline(full.Lines[0].Id, "2", locShelfA)))
	closed := sentOrder(t, c, variantLine(varNut, "2"), textLine("Frakt", "1", "250"))
	mustStatus(t, c.Do(http.MethodPost, orderPath(closed.Id, "lines", itoa(closed.Lines[0].Id), "close"), version(closed)), http.StatusOK, "close the goods")
	sentOrder(t, c, textLine("Montering", "1", "900"))
	receive(t, c, later.Id, receiptInput(rline(later.Lines[0].Id, "5", locShelfA)))

	res := c.Do(http.MethodGet, receivingPath, nil)
	mustStatus(t, res, http.StatusOK, "the receiving list")
	var list gen.PurchasingReceivingList
	res.JSON(&list)
	if len(list.Items) != 2 {
		t.Fatalf("the receiving list = %+v, want two orders", list.Items)
	}
	first, second := list.Items[0], list.Items[1]
	if first.Id != late.Id || !first.Late || first.OpenLines != 1 || first.GoodsLines != 1 || first.SiteName != "Hovedlager" ||
		first.SupplierName != "Norsk Stål AS" || first.Status != gen.PurchasingReceivingOrderStatusSent || first.Number == nil {
		t.Errorf("first = %+v", first)
	}
	if second.Id != later.Id || second.Late || second.OpenLines != 1 || second.GoodsLines != 2 ||
		second.Status != gen.PurchasingReceivingOrderStatusPartiallyReceived || second.ExpectedOn == nil {
		t.Errorf("second = %+v", second)
	}

	for _, key := range []string{"purchasing:access", "purchasing:orders-manage", "purchasing:orders-send", "purchasing:receive",
		"purchasing:supplier-items-manage", "purchasing:invoices-match", "purchasing:settings"} {
		res := h.SignIn(t, key).Do(http.MethodGet, receivingPath, nil)
		if forbidden := res.Status == http.StatusForbidden; forbidden != (key != "purchasing:receive") {
			t.Errorf("the receiving list with %s alone = %d", key, res.Status)
		}
	}
}

// A replay answers the first receipt whatever the order became since (A42):
// received in full, or with the receipt undone — and when the first receipt
// commits between the replay's first check and its attempt, the attempt's
// refusal of an order no longer open gives way to the replay.
func TestReceive_ReplayAfterTheOrderEnded(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"))
	in := receiptInput(rline(o.Lines[0].Id, "5", locShelfA))
	first := receive(t, c, o.Id, in)
	if first.Order.Status != gen.PurchasingOrderStatusReceived {
		t.Fatalf("order = %s", first.Order.Status)
	}
	if again := receive(t, c, o.Id, in); again.Receipt.Id != first.Receipt.Id {
		t.Errorf("a replay after the order was received = receipt %d, want %d", again.Receipt.Id, first.Receipt.Id)
	}
	mustStatus(t, c.Do(http.MethodPost, receiptPath(first.Receipt.Id, "undo"), gen.PurchasingUndoInput{}), http.StatusOK, "undo")
	if again := receive(t, c, o.Id, in); again.Receipt.Id != first.Receipt.Id || again.Receipt.Status != "undone" {
		t.Errorf("a replay after the undo = receipt %d %s, want %d undone", again.Receipt.Id, again.Receipt.Status, first.Receipt.Id)
	}
	// A replay of an undo is a second undo, refused as such: it changes nothing.
	expectRefusal(t, c.Do(http.MethodPost, receiptPath(first.Receipt.Id, "undo"), gen.PurchasingUndoInput{}), http.StatusConflict, "receipt_undone", "an undo again")

	raced := sentOrder(t, c, variantLine(varNut, "5"))
	racedIn := receiptInput(rline(raced.Lines[0].Id, "5", locShelfA))
	var firstID int64
	var unset func()
	unset = purchasing.SetAfterReplayCheck(raced.Id, func() {
		unset()
		r := receive(t, c, raced.Id, racedIn)
		firstID = r.Receipt.Id
	})
	defer unset()
	res := c.Do(http.MethodPost, receiptsPath(raced.Id), racedIn)
	mustStatus(t, res, http.StatusCreated, "the replay that raced the first receipt")
	var r gen.PurchasingReceiptResult
	res.JSON(&r)
	if firstID == 0 || r.Receipt.Id != firstID || r.Progress.Status != gen.PurchasingOrderProgressStatusReceived {
		t.Errorf("the raced replay = receipt %d (first %d), order %s", r.Receipt.Id, firstID, r.Progress.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM purchasing.receipts WHERE order_id = $1`, raced.Id); n != 1 {
		t.Errorf("receipts of the raced order = %d, want 1", n)
	}
}

// One rule ends an order whichever action completes it (A48): received when
// every line came in full, closed — with its change-log row and
// purchasing.order.closed.v1 — when a closed line kept a remainder; Reopen
// counts delivered marks.
func TestOrderEnd_OneRule(t *testing.T) {
	t.Parallel()
	type step func(t *testing.T, h *harness, c *modtest.Client, o gen.PurchasingOrder) gen.PurchasingOrder
	fresh := func(t *testing.T, c *modtest.Client, id int64) gen.PurchasingOrder { return getOrder(t, c, id) }
	recv := func(line int, qty string) step {
		return func(t *testing.T, h *harness, c *modtest.Client, o gen.PurchasingOrder) gen.PurchasingOrder {
			receive(t, c, o.Id, receiptInput(rline(o.Lines[line].Id, qty, locShelfA)))
			return fresh(t, c, o.Id)
		}
	}
	closeLine := func(line int) step {
		return func(t *testing.T, h *harness, c *modtest.Client, o gen.PurchasingOrder) gen.PurchasingOrder {
			mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "lines", itoa(o.Lines[line].Id), "close"), version(o)), http.StatusOK, "close line")
			return fresh(t, c, o.Id)
		}
	}
	deliver := func(line int, on bool) step {
		return func(t *testing.T, h *harness, c *modtest.Client, o gen.PurchasingOrder) gen.PurchasingOrder {
			mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "lines", itoa(o.Lines[line].Id), "delivered"),
				gen.PurchasingDeliveredInput{ExpectedUpdatedAt: o.UpdatedAt, Delivered: on}), http.StatusOK, "delivered")
			return fresh(t, c, o.Id)
		}
	}
	closeOrder := func(t *testing.T, h *harness, c *modtest.Client, o gen.PurchasingOrder) gen.PurchasingOrder {
		mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "close"), version(o)), http.StatusOK, "close")
		return fresh(t, c, o.Id)
	}
	reopen := func(t *testing.T, h *harness, c *modtest.Client, o gen.PurchasingOrder) gen.PurchasingOrder {
		mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "reopen"), version(o)), http.StatusOK, "reopen")
		return fresh(t, c, o.Id)
	}
	undoLast := func(t *testing.T, h *harness, c *modtest.Client, o gen.PurchasingOrder) gen.PurchasingOrder {
		res := c.Do(http.MethodGet, receiptsPath(o.Id), nil)
		var list gen.PurchasingReceiptList
		res.JSON(&list)
		mustStatus(t, c.Do(http.MethodPost, receiptPath(list.Items[0].Id, "undo"), gen.PurchasingUndoInput{}), http.StatusOK, "undo")
		return fresh(t, c, o.Id)
	}
	nutAndBolt := []gen.PurchasingLineInput{variantLine(varNut, "5"), variantLine(varBolt, "2")}
	nutAndFreight := []gen.PurchasingLineInput{variantLine(varNut, "5"), textLine("Frakt", "1", "250")}
	for _, tc := range []struct {
		name    string
		lines   []gen.PurchasingLineInput
		steps   []step
		want    gen.PurchasingOrderStatus
		closed  int    // purchasing.order.closed.v1 events
		lastLog string // the newest change-log row's first action
	}{
		{"close A, then receive B in full: closed by the receipt", nutAndBolt, []step{closeLine(0), recv(1, "2")},
			gen.PurchasingOrderStatusClosed, 1, "closed"},
		{"receive B in full, then close A: closed by the line", nutAndBolt, []step{recv(1, "2"), closeLine(0)},
			gen.PurchasingOrderStatusClosed, 1, "closed"},
		{"close A, receive B, reopen: A is open again", nutAndBolt, []step{closeLine(0), recv(1, "2"), reopen},
			gen.PurchasingOrderStatusPartiallyReceived, 1, "reopened"},
		{"receive A in full, then close B unreceived: closed", nutAndBolt, []step{recv(0, "5"), closeLine(1)},
			gen.PurchasingOrderStatusClosed, 1, "closed"},
		{"receive both in full: received, no closed event", nutAndBolt, []step{recv(0, "5"), recv(1, "2")},
			gen.PurchasingOrderStatusReceived, 0, "received"},
		{"freight delivered, close, reopen: partially received", nutAndFreight, []step{deliver(1, true), closeOrder, reopen},
			gen.PurchasingOrderStatusPartiallyReceived, 1, "reopened"},
		{"freight delivered completes the order: received", nutAndFreight, []step{recv(0, "5"), deliver(1, true)},
			gen.PurchasingOrderStatusReceived, 0, "received"},
		{"freight closed undelivered, goods in full: closed", nutAndFreight, []step{closeLine(1), recv(0, "5")},
			gen.PurchasingOrderStatusClosed, 1, "closed"},
		{"received, mark off, undo: back to sent", nutAndFreight, []step{recv(0, "5"), deliver(1, true), deliver(1, false), undoLast},
			gen.PurchasingOrderStatusSent, 0, "receipt_undone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, harnessOptions{})
			c := h.SignIn(t, receiver...)
			o := sentOrder(t, c, tc.lines...)
			for _, s := range tc.steps {
				h.Advance(time.Second)
				o = s(t, h, c, o)
			}
			if o.Status != tc.want {
				t.Errorf("status = %s, want %s", o.Status, tc.want)
			}
			if tc.want == gen.PurchasingOrderStatusClosed && o.ClosedAt == nil {
				t.Error("a closed order without closedAt")
			}
			if n := eventsNamed(t, h, "purchasing.order.closed", o.Id); n != tc.closed {
				t.Errorf("closed events = %d, want %d", n, tc.closed)
			}
			if hist := historyOf(t, c, o.Id); hist.Changes[0].Actions[0].Action != tc.lastLog {
				t.Errorf("the newest change = %+v, want %s", hist.Changes[0].Actions, tc.lastLog)
			}
		})
	}
}

// A person supplier's receipts (P7): the movement names neither the supplier
// nor its delivery note; the export answers the receipts' notes and the erase
// blanks them.
func TestReceive_PersonSupplier(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := markSent(t, c, createOrder(t, c, supPerson, variantLine(varNut, "5")))
	in := receiptInput(rline(o.Lines[0].Id, "2", locShelfA))
	in.DeliveryNote, in.Note = str("FS-Ola-1"), str("Ola leverte selv")
	r := receive(t, c, o.Id, in)
	if _, led, _ := h.ledger.last(); led.SupplierName != "" || led.SupplierReference != "" {
		t.Errorf("the movement of a person's receipt names %q / %q", led.SupplierName, led.SupplierReference)
	}
	mustStatus(t, c.Do(http.MethodPost, receiptPath(r.Receipt.Id, "undo"), gen.PurchasingUndoInput{Note: str("Ola tok den med")}), http.StatusOK, "undo")
	// A company's movement keeps both.
	company := sentOrder(t, c, variantLine(varNut, "5"))
	cin := receiptInput(rline(company.Lines[0].Id, "1", locShelfA))
	cin.DeliveryNote = str("FS-9")
	receive(t, c, company.Id, cin)
	if _, led, _ := h.ledger.last(); led.SupplierName != "Norsk Stål AS" || led.SupplierReference != "FS-9" {
		t.Errorf("a company's movement = %q / %q", led.SupplierName, led.SupplierReference)
	}

	data := purchasing.Module().SupplierPersonalData(h.Deps())
	exported, err := data.ExportSupplierData(context.Background(), supPerson)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(exported)
	for _, want := range []string{"FS-Ola-1", "Ola leverte selv", "Ola tok den med"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the export lacks %q: %s", want, body)
		}
	}
	var erased []contracts.ErasedData
	inTx(t, h, true, func(tx pgx.Tx) {
		if erased, err = data.EraseSupplierData(context.Background(), tx, supPerson); err != nil {
			t.Fatal(err)
		}
	})
	counts := map[string]int64{}
	for _, e := range erased {
		counts[e.Kind] = e.Count
	}
	if counts["purchasing.receipts"] != 1 {
		t.Errorf("erased = %+v", erased)
	}
	if n := h.Count(t, `SELECT count(*) FROM purchasing.receipts WHERE order_id = $1 AND (delivery_note <> '' OR note <> '' OR undo_note IS NOT NULL)`, o.Id); n != 0 {
		t.Errorf("receipts still naming the person = %d", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM purchasing.receipts WHERE order_id = $1 AND delivery_note = 'FS-9'`, company.Id); n != 1 {
		t.Error("the erase reached another supplier's receipt")
	}
}

// What a receipt refuses and how it is judged a replay: a quantity whose
// received total would pass what the column holds is quantity_invalid even
// with acceptOver; acceptOver false is the same body as none.
func TestReceive_OverflowAndTheHash(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	o := sentOrder(t, c, variantLine(varNut, "5"))
	huge := receiptInput(rline(o.Lines[0].Id, "999999999", locShelfA), rline(o.Lines[0].Id, "999999999", locShelfB))
	huge.AcceptOver = ptrTo(true)
	p := expectRefusal(t, c.Do(http.MethodPost, receiptsPath(o.Id), huge), http.StatusBadRequest, "quantity_invalid", "past the column")
	if p.Lines == nil || len(*p.Lines) != 2 {
		t.Errorf("lines = %+v", p.Lines)
	}
	in := receiptInput(rline(o.Lines[0].Id, "1", locShelfA))
	in.AcceptOver = ptrTo(false)
	first := receive(t, c, o.Id, in)
	in.AcceptOver = nil
	if again := receive(t, c, o.Id, in); again.Receipt.Id != first.Receipt.Id {
		t.Errorf("acceptOver omitted after false = receipt %d, want the replay of %d", again.Receipt.Id, first.Receipt.Id)
	}
}

// A receipt line keeps the price per the variant's unit, net of the line's
// discount (P28, A49): 3 boxes of 10 at 250 a box less 10 % is 22.5 a piece.
func TestReceipt_KeepsTheUnitCost(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, receiver...)
	boxes := variantLine(varBolt, "3")
	boxes.PackagingId, boxes.UnitPrice, boxes.DiscountPercent = i32(packBox), str("250"), str("10")
	plain := variantLine(varNut, "4")
	plain.UnitPrice = str("12.3456")
	o := sentOrder(t, c, boxes, plain)
	r := receive(t, c, o.Id, receiptInput(rline(o.Lines[0].Id, "10", locShelfA), rline(o.Lines[1].Id, "4", locShelfA)))
	var bolt, nut string
	if err := h.Pool().QueryRow(context.Background(), `SELECT
		(SELECT unit_price::text FROM purchasing.receipt_lines WHERE receipt_id = $1 AND position = 1),
		(SELECT unit_price::text FROM purchasing.receipt_lines WHERE receipt_id = $1 AND position = 2)`, r.Receipt.Id).Scan(&bolt, &nut); err != nil {
		t.Fatal(err)
	}
	if bolt != "22.5000" || nut != "12.3456" {
		t.Errorf("unit costs = %s and %s, want 22.5000 and 12.3456", bolt, nut)
	}
}

// dateIn is a day as the API takes it.
func dateIn(d time.Time) *openapi_types.Date {
	return &openapi_types.Date{Time: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)}
}
