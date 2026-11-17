package inventory_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The ledger's receive (purchasing design P3, A5, A6, A13): another module's
// receiving document puts goods in stock on its own transaction, with the
// receipt endpoint's rules, and takes a receipt back. Each call runs on a
// pool of one: the ledger takes no connection of its own under the caller's.

// purchaseOrder is the receiving document, and receiptDoc the receipt record
// these tests post for it.
var (
	purchaseOrder = contracts.StockSource{Module: "purchasing", Kind: "order", ID: 5}
	receiptDoc    = contracts.StockSource{Module: "purchasing", Kind: "receipt", ID: 9}
)

// onePoolTx runs fn with a ledger built on a pool of one connection and a
// transaction holding that connection, marked as the caller's: a ledger call
// that took a connection of its own would wait for it until the deadline.
// It commits when fn answers nil and rolls back otherwise.
func onePoolTx(t *testing.T, h *harness, fn func(l contracts.StockLedger, tx pgx.Tx) error) error {
	t.Helper()
	cfg := h.Pool().Config()
	cfg.MaxConns, cfg.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("a pool of one: %v", err)
	}
	defer pool.Close()
	deps := h.Deps()
	deps.Pool = pool
	ctx, cancel := context.WithTimeout(inCallerTx(t.Context()), 30*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := fn(inventory.Module().StockLedger(deps), tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return nil
}

// receiveFor runs Receive on a pool of one and answers the movement.
func receiveFor(t *testing.T, h *harness, r contracts.StockReceipt) (int64, error) {
	t.Helper()
	var id int64
	err := onePoolTx(t, h, func(l contracts.StockLedger, tx pgx.Tx) error {
		posted, err := l.Receive(inCallerTx(t.Context()), tx, purchaseOrder, r)
		id = posted.MovementID
		return err
	})
	return id, err
}

// reverseFor runs ReverseReceipt the same way.
func reverseFor(t *testing.T, h *harness, receipt contracts.StockSource) (int64, error) {
	t.Helper()
	var id int64
	err := onePoolTx(t, h, func(l contracts.StockLedger, tx pgx.Tx) error {
		posted, err := l.ReverseReceipt(inCallerTx(t.Context()), tx, receipt, contracts.StockReversal{Note: "Failed inspection", PostedByDisplay: "Buyer"})
		id = posted.MovementID
		return err
	})
	return id, err
}

// movementOf reads a movement through the API.
func movementOf(t *testing.T, c *modtest.Client, id int64) gen.InventoryMovement {
	t.Helper()
	res := c.Do(http.MethodGet, movementPath(id), nil)
	mustStatus(t, res, http.StatusOK, "the movement")
	var m gen.InventoryMovement
	res.JSON(&m)
	return m
}

func TestStockLedger_ReceiveAndReverse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	site := s.site.Id
	r := contracts.StockReceipt{Document: receiptDoc, SiteID: site, Today: testToday, PostedByUserID: &user, PostedByDisplay: "Buyer",
		SupplierName: "Bolt AS", SupplierReference: "DN-1", Note: "Order 5",
		Lines: []contracts.ReceiveLine{
			{VariantID: varBolt, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "10"},
			// A Discontinued variant is received (A6).
			{VariantID: varOld, LineID: 2, ToLocationID: s.shelf2.Id, Quantity: "1.5"},
			{VariantID: varLot, LineID: 3, ToLocationID: s.shelf1.Id, Quantity: "4", Lot: &contracts.ReceiveLot{Number: "PO-1", SupplierLot: "HEAT 7"}},
			{VariantID: varSerial, LineID: 4, ToLocationID: s.shelf2.Id, Quantity: "2", Serials: []string{"SN1", "SN2"}},
		}}
	id, err := receiveFor(t, h, r)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	m := movementOf(t, c, id)
	if m.Kind != gen.Receipt || len(m.Lines) != 5 || m.Source == nil || m.Source.Module != "purchasing" || m.Source.Kind != "receipt" ||
		m.Source.Id != "9" || m.Source.LineId == nil || *m.Source.LineId != "5" || m.SupplierName == nil || *m.SupplierName != "Bolt AS" ||
		m.PostedBy.UserId == nil || *m.PostedBy.UserId != user {
		t.Errorf("the receipt = %+v", m)
	}
	if onHand(t, h, varBolt, s.shelf1.Id) != "10.000" || onHand(t, h, varOld, s.shelf2.Id) != "1.500" ||
		lotOnHand(t, h, lotID(t, h, varLot, "PO-1"), s.shelf1.Id) != "4.000" || lotOnHand(t, h, lotID(t, h, varSerial, "SN2"), s.shelf2.Id) != "1.000" {
		t.Errorf("balances after the receipt: bolts %q, old %q", onHand(t, h, varBolt, s.shelf1.Id), onHand(t, h, varOld, s.shelf2.Id))
	}

	// The same document again is the first movement, whatever its day and
	// poster; another body under it is refused.
	if again, err := receiveFor(t, h, r); err != nil || again != id {
		t.Errorf("a replay = %d, %v; want %d", again, err, id)
	}
	later := r
	later.Today, later.PostedByUserID, later.PostedByDisplay = testToday.AddDate(0, 0, 3), nil, "Someone else"
	if again, err := receiveFor(t, h, later); err != nil || again != id {
		t.Errorf("a replay on another day by another poster = %d, %v; want %d", again, err, id)
	}
	other := r
	other.Lines = r.Lines[:1]
	if _, err := receiveFor(t, h, other); refusalCode(err) != contracts.StockDocumentReused {
		t.Errorf("another body = %v, want document_reused", err)
	}

	// Reversed: every line back out, of its own kind and source.
	rev, err := reverseFor(t, h, receiptDoc)
	if err != nil {
		t.Fatalf("ReverseReceipt: %v", err)
	}
	m = movementOf(t, c, rev)
	if m.Kind != gen.ReceiptReversal || len(m.Lines) != 5 || m.Lines[0].From == nil || m.Lines[0].To != nil || m.Source == nil ||
		m.Source.Id != "9" || m.Source.LineId == nil || *m.Source.LineId != "5" {
		t.Errorf("the reversal = %+v", m)
	}
	if onHand(t, h, varBolt, s.shelf1.Id) != "" || onHand(t, h, varOld, s.shelf2.Id) != "" {
		t.Errorf("after the reversal: bolts %q, old %q", onHand(t, h, varBolt, s.shelf1.Id), onHand(t, h, varOld, s.shelf2.Id))
	}
	if again, err := reverseFor(t, h, receiptDoc); err != nil || again != rev {
		t.Errorf("a second reversal = %d, %v; want %d", again, err, rev)
	}
	// A retry with an edited note is the first reversal still.
	var edited int64
	if err := onePoolTx(t, h, func(l contracts.StockLedger, tx pgx.Tx) error {
		posted, err := l.ReverseReceipt(inCallerTx(t.Context()), tx, receiptDoc, contracts.StockReversal{Note: "Wrong goods, not the inspection"})
		edited = posted.MovementID
		return err
	}); err != nil || edited != rev {
		t.Errorf("a reversal with an edited note = %d, %v; want %d", edited, err, rev)
	}
	res := c.Do(http.MethodGet, movementsPath+"?kind=receipt_reversal", nil)
	mustStatus(t, res, http.StatusOK, "the reversals")
	var page gen.InventoryMovementPage
	res.JSON(&page)
	if len(page.Movements) != 1 || page.Movements[0].Id != rev {
		t.Errorf("reversals = %+v", page.Movements)
	}
	if _, err := reverseFor(t, h, contracts.StockSource{Module: "purchasing", Kind: "receipt", ID: 99}); refusalCode(err) != contracts.StockReceiptUnknown {
		t.Errorf("a reversal of no receipt = %v, want receipt_unknown", err)
	}
	assertLedgerConsistent(t, h)
}

// The receipt endpoint's refusals, every failing line named, and nothing
// written.
func TestStockLedger_ReceiveRefuses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := seedLots(t, h, c)
	site := s.site.Id
	doc := func(id int64) contracts.StockSource {
		return contracts.StockSource{Module: "purchasing", Kind: "receipt", ID: id}
	}
	cases := []struct {
		name string
		line contracts.ReceiveLine
		code string
	}{
		{"a service", contracts.ReceiveLine{VariantID: varService, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1"}, contracts.StockVariantNotStocked},
		{"an unknown variant", contracts.ReceiveLine{VariantID: 9999, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1"}, contracts.StockVariantNotStocked},
		{"another site's location", contracts.ReceiveLine{VariantID: varBolt, LineID: 1, ToLocationID: s.elsewhre.RootLocationId, Quantity: "1"}, contracts.StockLocationUnknown},
		{"a tracked line without a lot", contracts.ReceiveLine{VariantID: varLot, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1"}, contracts.StockLotRequired},
		{"an untracked line with a lot", contracts.ReceiveLine{VariantID: varBolt, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1", Lot: &contracts.ReceiveLot{Number: "X"}}, contracts.StockLotNotAllowed},
		{"a bad lot number", contracts.ReceiveLine{VariantID: varLot, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1", Lot: &contracts.ReceiveLot{Number: "A B"}}, contracts.StockLotNumberInvalid},
		{"serials not the quantity", contracts.ReceiveLine{VariantID: varSerial, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "3", Serials: []string{"S1", "S2"}}, contracts.StockSerialQuantity},
		{"a serial line of 2", contracts.ReceiveLine{VariantID: varSerial, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "2", Lot: &contracts.ReceiveLot{Number: "S9"}}, contracts.StockSerialQuantity},
		{"a bad quantity", contracts.ReceiveLine{VariantID: varBolt, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "0"}, contracts.StockQuantityInvalid},
		// The receipt endpoint's own reading (purchasing delivery 0).
		{"lot dates out of order", contracts.ReceiveLine{VariantID: varLot, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1",
			Lot: &contracts.ReceiveLot{Number: "D1", ProductionDate: ptr(testToday), Expiry: ptr(testToday.AddDate(0, 0, -1))}}, contracts.StockLotDatesInvalid},
		{"a serial named twice", contracts.ReceiveLine{VariantID: varSerial, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "2", Serials: []string{"S1", "s1"}}, contracts.StockSerialQuantity},
		{"a bad serial", contracts.ReceiveLine{VariantID: varSerial, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1", Serials: []string{"S 1"}}, contracts.StockLotNumberInvalid},
		{"serials and a lot number", contracts.ReceiveLine{VariantID: varSerial, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1",
			Serials: []string{"S1"}, Lot: &contracts.ReceiveLot{Number: "L1"}}, contracts.StockLineInvalid},
		{"a lot named two ways", contracts.ReceiveLine{VariantID: varLot, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1",
			LotID: 1, Lot: &contracts.ReceiveLot{Number: "L1"}}, contracts.StockLineInvalid},
		{"a supplier lot too long", contracts.ReceiveLine{VariantID: varLot, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1",
			Lot: &contracts.ReceiveLot{Number: "L2", SupplierLot: strings.Repeat("x", 41)}}, contracts.StockLineInvalid},
	}
	for i, tc := range cases {
		_, err := receiveFor(t, h, contracts.StockReceipt{Document: doc(int64(100 + i)), SiteID: site, Today: testToday,
			Lines: []contracts.ReceiveLine{{VariantID: varBolt, LineID: 7, ToLocationID: s.shelf1.Id, Quantity: "1"}, tc.line}})
		var r *contracts.StockRefusal
		if !errors.As(err, &r) || r.Code != tc.code || len(r.Lines) != 1 || r.Lines[0].Index != 1 || r.Lines[0].LineID != 1 {
			t.Errorf("%s: %+v, want %s on the second line", tc.name, err, tc.code)
		}
	}
	if _, err := receiveFor(t, h, contracts.StockReceipt{Document: doc(1), SiteID: 99999, Today: testToday,
		Lines: []contracts.ReceiveLine{{VariantID: varBolt, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1"}}}); refusalCode(err) != contracts.StockSiteUnknown {
		t.Errorf("an unknown site = %v", err)
	}
	if _, err := receiveFor(t, h, contracts.StockReceipt{Document: doc(2), SiteID: site, Today: testToday}); refusalCode(err) != contracts.StockNothingToPost {
		t.Errorf("no lines = %v", err)
	}
	// A line id below 1 is the line's refusal; a receipt with no document a
	// failure, not a refusal.
	_, err := receiveFor(t, h, contracts.StockReceipt{Document: doc(3), SiteID: site, Today: testToday,
		Lines: []contracts.ReceiveLine{{VariantID: varBolt, LineID: 0, ToLocationID: s.shelf1.Id, Quantity: "1"}}})
	if r := (*contracts.StockRefusal)(nil); !errors.As(err, &r) || r.Code != contracts.StockLineInvalid || len(r.Lines) != 1 || r.Lines[0].Index != 0 {
		t.Errorf("a line id of 0 = %+v, want line_invalid", err)
	}
	_, err = receiveFor(t, h, contracts.StockReceipt{SiteID: site, Today: testToday,
		Lines: []contracts.ReceiveLine{{VariantID: varBolt, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "1"}}})
	if r := (*contracts.StockRefusal)(nil); err == nil || errors.As(err, &r) {
		t.Errorf("a receipt with no document = %v, want a failure", err)
	}
	if got := h.Count(t, `SELECT count(*) FROM inventory.movements`); got != 0 {
		t.Errorf("movements after the refusals = %d, want none", got)
	}
}

// A receipt of an inspected variant quarantines its lot (M100); the
// reversal takes the lot back whatever its holds and releases the holds that
// receipt set, while another document's stay. Stock moved away first refuses
// it.
func TestStockLedger_ReverseReceiptTakesHeldLots(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	adjuster, _ := signIn(t, h, stockAdjuster...)
	s := seedLots(t, h, c)
	mustStatus(t, adjuster.Do(http.MethodPut, rulesPath(varLot), gen.InventoryVariantRulesInput{InspectOnReceipt: true}), http.StatusOK, "inspect on receipt")
	site := s.site.Id
	if _, err := receiveFor(t, h, contracts.StockReceipt{Document: receiptDoc, SiteID: site, Today: testToday,
		Lines: []contracts.ReceiveLine{{VariantID: varLot, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "6", Lot: &contracts.ReceiveLot{Number: "INS-9"}}}}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	lot := lotID(t, h, varLot, "INS-9")
	if l := getLot(t, c, lot); l.Status != gen.InventoryLotStatusQuarantine {
		t.Fatalf("the received lot = %s, want quarantine", l.Status)
	}
	deviation := contracts.StockSource{Module: "manufacturing", Kind: "deviation", ID: 3}
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return ledgerOf(h).HoldLots(inCallerTx(t.Context()), tx, deviation, []contracts.LotHold{{LotID: lot, Kind: "block", Cause: "deviation"}})
	}); err != nil {
		t.Fatalf("HoldLots: %v", err)
	}
	if _, err := reverseFor(t, h, receiptDoc); err != nil {
		t.Fatalf("ReverseReceipt of a held lot: %v", err)
	}
	if got := lotOnHand(t, h, lot, s.shelf1.Id); got != "" {
		t.Errorf("the lot after the reversal holds %q, want nothing", got)
	}
	l := getLot(t, c, lot)
	active := 0
	for _, hold := range l.Holds {
		if hold.ReleasedAt == nil {
			active++
			if hold.Cause != gen.InventoryLotHoldCauseDeviation {
				t.Errorf("an active hold after the reversal: %+v", hold)
			}
		}
	}
	if active != 1 {
		t.Errorf("active holds after the reversal = %d, want the deviation's alone", active)
	}

	// Stock no longer where the receipt put it.
	second := contracts.StockSource{Module: "purchasing", Kind: "receipt", ID: 10}
	if _, err := receiveFor(t, h, contracts.StockReceipt{Document: second, SiteID: site, Today: testToday,
		Lines: []contracts.ReceiveLine{{VariantID: varBolt, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "3"}}}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	mustStatus(t, move(c, site, gen.InventoryMoveLine{VariantId: varBolt, FromLocationId: s.shelf1.Id, ToLocationId: s.shelf2.Id, Quantity: qty(2)}),
		http.StatusCreated, "move two away")
	_, err := reverseFor(t, h, second)
	var r *contracts.StockRefusal
	if !errors.As(err, &r) || r.Code != contracts.StockInsufficientStock || len(r.Lines) != 1 || r.Lines[0].Index != 0 || r.Lines[0].VariantID != varBolt {
		t.Errorf("a reversal of stock moved away = %+v, want insufficient_stock", err)
	}
	assertLedgerConsistent(t, h)
}

// A reversal of a receipt into a lot that holds other stock takes the
// receipt's quantity and its hold alone (purchasing delivery 0); the lot's
// trace then ends a usage path at the reversal; the dashboard and the site
// card no longer count the receipt taken back.
func TestStockLedger_ReverseIntoAnExistingLot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	adjuster, _ := signIn(t, h, stockAdjuster...)
	s := seedLots(t, h, c)
	site := s.site.Id
	first := contracts.StockSource{Module: "purchasing", Kind: "receipt", ID: 21}
	if _, err := receiveFor(t, h, contracts.StockReceipt{Document: first, SiteID: site, Today: testToday, SupplierName: "Bolt AS",
		Lines: []contracts.ReceiveLine{{VariantID: varLot, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "4", Lot: &contracts.ReceiveLot{Number: "MIX-1"}}}}); err != nil {
		t.Fatalf("the first receipt: %v", err)
	}
	lot := lotID(t, h, varLot, "MIX-1")
	mustStatus(t, adjuster.Do(http.MethodPut, rulesPath(varLot), gen.InventoryVariantRulesInput{InspectOnReceipt: true}), http.StatusOK, "inspect on receipt")
	second := contracts.StockSource{Module: "purchasing", Kind: "receipt", ID: 22}
	if _, err := receiveFor(t, h, contracts.StockReceipt{Document: second, SiteID: site, Today: testToday, SupplierName: "Bolt AS",
		Lines: []contracts.ReceiveLine{{VariantID: varLot, LineID: 1, ToLocationID: s.shelf1.Id, Quantity: "3", LotID: lot}}}); err != nil {
		t.Fatalf("the second receipt: %v", err)
	}
	if got := lotOnHand(t, h, lot, s.shelf1.Id); got != "7.000" {
		t.Fatalf("the lot after two receipts = %q, want 7", got)
	}
	activeHolds := func() int {
		n := 0
		for _, hold := range getLot(t, c, lot).Holds {
			if hold.ReleasedAt == nil {
				n++
			}
		}
		return n
	}
	if n := activeHolds(); n != 1 {
		t.Fatalf("active holds after the inspected receipt = %d, want 1", n)
	}
	before := receiptsThisWeek(t, c)
	rev, err := reverseFor(t, h, second)
	if err != nil {
		t.Fatalf("ReverseReceipt: %v", err)
	}
	if got := lotOnHand(t, h, lot, s.shelf1.Id); got != "4.000" {
		t.Errorf("the lot after the reversal = %q, want the first receipt's 4", got)
	}
	if n := activeHolds(); n != 0 {
		t.Errorf("active holds after the reversal = %d, want the second receipt's released", n)
	}

	// The trace: where it went ends at the reversal, with the supplier.
	tr := getTrace(t, c, "lotId="+strconv.FormatInt(lot, 10)+"&direction=usage")
	found := false
	for _, n := range tr.Nodes {
		if n.Kind == gen.TraceReceiptReversal && n.Movement != nil && n.Movement.Id == rev && n.Quantity != nil && *n.Quantity == 3 &&
			n.Movement.SupplierName != nil && *n.Movement.SupplierName == "Bolt AS" {
			found = true
		}
	}
	if !found {
		t.Errorf("the usage trace = %v, want the reversal as an end", shape(tr))
	}
	res := c.Do(http.MethodGet, tracePath+".csv?lotId="+strconv.FormatInt(lot, 10)+"&direction=usage", nil)
	mustStatus(t, res, http.StatusOK, "the trace CSV")
	if !strings.Contains(string(res.Body), "Receipt reversal, movement "+strconv.FormatInt(rev, 10)) {
		t.Errorf("the trace CSV = %s, want the reversal named", res.Body)
	}

	// The dashboard counts the receipts this week but the one taken back.
	if after := receiptsThisWeek(t, c); after != before-1 {
		t.Errorf("receipts this week = %d after the reversal, want %d", after, before-1)
	}
	assertLedgerConsistent(t, h)
}

// receiptsThisWeek is the dashboard's count of the receipts this week.
func receiptsThisWeek(t *testing.T, c *modtest.Client) int32 {
	t.Helper()
	res := c.Do(http.MethodGet, statsPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /stats")
	var st gen.InventoryStats
	res.JSON(&st)
	return st.ReceiptsThisWeek
}
